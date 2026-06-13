package protodec

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/perf"
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

// --- Descriptor-set cache
//
// Компиляция .proto в FileDescriptorSet требует запуска protoc подпроцессом
// (а на Windows это дорого: создание процесса + антивирус + первая распаковка).
// Один и тот же .proto обычно используется для множества разных payload'ов
// (разные Redis-ключи / .bin), поэтому кэшируем результат в памяти и
// перекомпилируем только если изменился сам .proto или любой из его импортов.

type fileStamp struct {
	size  int64
	mtime int64
}

type descCacheEntry struct {
	fds   *descriptorpb.FileDescriptorSet
	files map[string]fileStamp // абсолютный путь -> отпечаток
}

var (
	descCacheMu sync.Mutex
	descCache   = map[string]*descCacheEntry{}
)

func descKey(protoRoot, protoAbs string) string {
	return protoRoot + "\x00" + protoAbs
}

func statStamp(path string) (fileStamp, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return fileStamp{}, false
	}
	return fileStamp{size: fi.Size(), mtime: fi.ModTime().UnixNano()}, true
}

// descCacheGet возвращает закэшированный набор, если все его файлы не изменились.
func descCacheGet(key string) (*descriptorpb.FileDescriptorSet, bool) {
	descCacheMu.Lock()
	defer descCacheMu.Unlock()
	entry, ok := descCache[key]
	if !ok {
		return nil, false
	}
	for path, want := range entry.files {
		got, ok := statStamp(path)
		if !ok || got != want {
			delete(descCache, key) // устарел — выкинуть
			return nil, false
		}
	}
	return entry.fds, true
}

// descCachePut сохраняет набор и отпечаток всех его файлов, найденных под root.
func descCachePut(key, protoRoot string, fds *descriptorpb.FileDescriptorSet) {
	files := make(map[string]fileStamp, len(fds.GetFile()))
	for _, f := range fds.GetFile() {
		// Имена в наборе — пути относительно -I root. Well-known типы
		// (google/protobuf/*) под root не лежат — их пропускаем (они стабильны).
		p := filepath.Join(protoRoot, filepath.FromSlash(f.GetName()))
		if stamp, ok := statStamp(p); ok {
			files[p] = stamp
		}
	}
	descCacheMu.Lock()
	descCache[key] = &descCacheEntry{fds: fds, files: files}
	descCacheMu.Unlock()
}

func compileDescriptorSet(ctx context.Context, protoRoot, protoAbs string) (*descriptorpb.FileDescriptorSet, error) {
	key := descKey(protoRoot, protoAbs)
	if fds, ok := descCacheGet(key); ok {
		perf.Log("descriptor cache hit (%s)", protoAbs)
		return fds, nil
	}
	relProto, err := relToRoot(protoRoot, protoAbs)
	if err != nil {
		return nil, err
	}
	fds, err := runProtocDescriptorSet(ctx, protoRoot, relProto)
	if err != nil {
		return nil, err
	}
	descCachePut(key, protoRoot, fds)
	return fds, nil
}

// runProtocDescriptorSet компилирует .proto в FileDescriptorSet прямо в процессе
// через protocompile (без внешнего protoc). Возвращает набор со всеми импортами
// (аналог protoc --include_imports).
func runProtocDescriptorSet(ctx context.Context, protoRoot, relProto string) (*descriptorpb.FileDescriptorSet, error) {
	stop := perf.Track("compile descriptor_set (" + relProto + ")")
	defer stop()

	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{protoRoot},
		}),
		SourceInfoMode: protocompile.SourceInfoNone,
	}
	files, err := compiler.Compile(ctx, relProto)
	if err != nil {
		return nil, err
	}

	fds := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]bool)
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imps := fd.Imports()
		for i := 0; i < imps.Len(); i++ {
			add(imps.Get(i).FileDescriptor)
		}
		fds.File = append(fds.File, protodesc.ToFileDescriptorProto(fd))
	}
	for _, f := range files {
		add(f)
	}
	return fds, nil
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

// decodeRaw разбирает сырые protobuf-байты без схемы (аналог protoc --decode_raw),
// прямо в процессе через protowire.
func decodeRaw(_ context.Context, binBytes []byte) (string, error) {
	var sb strings.Builder
	if err := dumpRawFields(&sb, binBytes, 0); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// isProbablyText сообщает, выглядят ли байты как печатный UTF-8 текст
// (для RAW предпочитаем показать строку, а не пытаться разобрать как сообщение).
func isProbablyText(v []byte) bool {
	if len(v) == 0 {
		return true
	}
	if !utf8.Valid(v) {
		return false
	}
	for _, r := range string(v) {
		if r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func dumpRawFields(sb *strings.Builder, b []byte, depth int) error {
	if depth > 100 {
		return fmt.Errorf("nesting too deep")
	}
	pad := strings.Repeat("  ", depth)
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
			fmt.Fprintf(sb, "%s%d: %d\n", pad, num, v)
		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
			fmt.Fprintf(sb, "%s%d: 0x%08x\n", pad, num, v)
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
			fmt.Fprintf(sb, "%s%d: 0x%016x\n", pad, num, v)
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
			var nested strings.Builder
			switch {
			case isProbablyText(v):
				fmt.Fprintf(sb, "%s%d: %q\n", pad, num, string(v))
			case len(v) > 0 && dumpRawFields(&nested, v, depth+1) == nil:
				fmt.Fprintf(sb, "%s%d: {\n%s%s}\n", pad, num, nested.String(), pad)
			default:
				fmt.Fprintf(sb, "%s%d: %x\n", pad, num, v)
			}
		case protowire.StartGroupType:
			v, n := protowire.ConsumeGroup(num, b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
			var nested strings.Builder
			_ = dumpRawFields(&nested, v, depth+1)
			fmt.Fprintf(sb, "%s%d: {\n%s%s}\n", pad, num, nested.String(), pad)
		default:
			return fmt.Errorf("unknown wire type %d", typ)
		}
	}
	return nil
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
