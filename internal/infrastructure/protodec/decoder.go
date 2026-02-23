package protodec

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/protocbin"
)

type Decoder struct{}

func New() *Decoder { return &Decoder{} }

func relToRoot(protoRoot, abs string) (string, error) {
	rel, err := filepath.Rel(protoRoot, abs)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return "", fmt.Errorf("selected file is not under proto root (-I):\nroot: %s\nfile: %s", protoRoot, abs)
	}
	return rel, nil
}

func gunzipBytes(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

func looksLikeGzip(b []byte) bool {
	// gzip header: 1f 8b 08
	return len(b) >= 3 && b[0] == 0x1f && b[1] == 0x8b && b[2] == 0x08
}

func compileDescriptorSet(ctx context.Context, protoRoot, protoAbs string) (*descriptorpb.FileDescriptorSet, error) {
	relProto, err := relToRoot(protoRoot, protoAbs)
	if err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp("", "protoset-*.pb")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	protocPath, err := protocbin.Ensure()
	if err != nil {
		return nil, err
	}

	args := []string{
		"-I=" + protoRoot,
		"--include_imports",
		"--descriptor_set_out=" + tmpPath,
		relProto,
	}
	cmd := exec.CommandContext(ctx, protocPath, args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		if stderr == "" {
			stderr = err.Error()
		}
		return nil, fmt.Errorf("%s", stderr)
	}

	b, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, err
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(b, &fds); err != nil {
		return nil, err
	}
	return &fds, nil
}

func (d *Decoder) ValidateMessageType(ctx context.Context, protoRoot, fullType, protoAbs string) error {
	fds, err := compileDescriptorSet(ctx, protoRoot, protoAbs)
	if err != nil {
		return err
	}
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return err
	}
	desc, err := files.FindDescriptorByName(protoreflect.FullName(fullType))
	if err != nil {
		return fmt.Errorf("message type not found: %s", fullType)
	}
	if _, ok := desc.(protoreflect.MessageDescriptor); !ok {
		return fmt.Errorf("selected type is not a message: %s", fullType)
	}
	return nil
}

func decodeJSON(ctx context.Context, protoRoot, fullType, protoAbs string, binBytes []byte) (string, error) {
	fds, err := compileDescriptorSet(ctx, protoRoot, protoAbs)
	if err != nil {
		return "", err
	}

	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return "", err
	}

	desc, err := files.FindDescriptorByName(protoreflect.FullName(fullType))
	if err != nil {
		return "", err
	}
	md, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		return "", fmt.Errorf("descriptor is not a message: %s", fullType)
	}

	msg := dynamicpb.NewMessage(md)
	if err := proto.Unmarshal(binBytes, msg); err != nil {
		return "", err
	}

	b, err := protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		UseProtoNames:   true,
		EmitUnpopulated: false,
	}.Marshal(msg)
	if err != nil {
		return "", err
	}

	// Доп. форматирование: гарантируем валидный pretty JSON.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, b, "", "  "); err == nil {
		return pretty.String(), nil
	}

	return string(b), nil
}

func decodeRaw(ctx context.Context, binBytes []byte) (string, error) {
	protocPath, err := protocbin.Ensure()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, protocPath, "--decode_raw")
	cmd.Stdin = bytes.NewReader(binBytes)

	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		s := strings.TrimSpace(errBuf.String())
		if s == "" {
			s = err.Error()
		}
		return "", fmt.Errorf("%s", s)
	}
	return out.String(), nil
}

func (d *Decoder) Decode(ctx context.Context, req domain.DecodeRequest) (domain.DecodeResult, error) {
	// 1) Prepare bytes according to explicit UI flag.
	bin := req.Bytes
	if req.Gzip {
		unz, err := gunzipBytes(bin)
		if err != nil {
			// If user marked gzip but bytes aren't gzip, try decoding as-is.
			// This is especially useful for Redis where data may be plain.
			bin = req.Bytes
		} else {
			bin = unz
		}
	}

	// 2) Try normal decode.
	res, err := d.decodeWithBytes(ctx, req, bin)
	if err == nil {
		return res, nil
	}

	// 3) Auto-detect gzip when decode failed.
	// If bytes look like gzip -> try decompress + decode.
	// NOTE: we only try this when gzip wasn't already successfully applied.
	if !req.Gzip && looksLikeGzip(req.Bytes) {
		unz, gerr := gunzipBytes(req.Bytes)
		if gerr == nil {
			if res2, err2 := d.decodeWithBytes(ctx, req, unz); err2 == nil {
				res2.AutoDetectedGzip = true
				return res2, nil
			}
		}
	}

	return domain.DecodeResult{}, err
}

func (d *Decoder) decodeWithBytes(ctx context.Context, req domain.DecodeRequest, bin []byte) (domain.DecodeResult, error) {
	switch req.Format {
	case domain.OutputFormatJSON:
		raw, err := decodeJSON(ctx, req.ProtoRoot, req.FullType, req.ProtoFile, bin)
		if err != nil {
			return domain.DecodeResult{}, err
		}
		return domain.DecodeResult{Raw: raw, Pretty: raw}, nil
	case domain.OutputFormatRAW:
		raw, err := decodeRaw(ctx, bin)
		if err != nil {
			return domain.DecodeResult{}, err
		}
		return domain.DecodeResult{Raw: raw}, nil
	default:
		return domain.DecodeResult{}, fmt.Errorf("unsupported format: %s", req.Format)
	}
}
