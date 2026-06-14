package protodec

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	"google.golang.org/protobuf/reflect/protoregistry"
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

// allRootKey — отдельный ключ кэша для набора «все .proto под root».
const allRootKey = "\x00ALL"

// compileAllUnderRoot компилирует ВСЕ .proto под protoRoot в единый набор.
// Используется авто-детектом типа, когда конкретный Proto file не выбран.
func compileAllUnderRoot(ctx context.Context, protoRoot string) (*descriptorpb.FileDescriptorSet, error) {
	key := descKey(protoRoot, allRootKey)
	if fds, ok := descCacheGet(key); ok {
		perf.Log("descriptor cache hit (all under %s)", protoRoot)
		return fds, nil
	}

	var rels []string
	err := filepath.WalkDir(protoRoot, func(path string, dEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if dEntry.IsDir() || !strings.HasSuffix(strings.ToLower(dEntry.Name()), ".proto") {
			return nil
		}
		rel, rerr := relToRoot(protoRoot, path)
		if rerr != nil {
			return rerr
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("no .proto files found under %s", protoRoot)
	}

	// Быстрый путь: компилируем всё разом.
	if fds, err := runProtocDescriptorSet(ctx, protoRoot, rels...); err == nil {
		descCachePut(key, protoRoot, fds)
		return fds, nil
	}

	// Устойчивый путь: какой-то файл не компилируется (например, импортирует
	// внешнюю buf-зависимость). Компилируем по одному, пропуская проблемные,
	// и объединяем успешные наборы.
	// Порядок важен: protodesc.NewFiles ждёт зависимости раньше зависимых.
	// Внутри каждого набора импорты идут перед файлом — сохраняем этот порядок,
	// дедуплицируя по имени.
	fds := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var skipped int
	var firstErr error
	for _, rel := range rels {
		sub, err := runProtocDescriptorSet(ctx, protoRoot, rel)
		if err != nil {
			skipped++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, f := range sub.GetFile() {
			if !seen[f.GetName()] {
				seen[f.GetName()] = true
				fds.File = append(fds.File, f)
			}
		}
	}
	if skipped > 0 {
		perf.Log("compile root: skipped %d/%d .proto with unresolved imports (e.g. %v)", skipped, len(rels), firstErr)
	}
	if len(fds.File) == 0 {
		return nil, firstErr
	}

	descCachePut(key, protoRoot, fds)
	return fds, nil
}

// runProtocDescriptorSet компилирует .proto в FileDescriptorSet прямо в процессе
// через protocompile (без внешнего protoc). Возвращает набор со всеми импортами
// (аналог protoc --include_imports).
func runProtocDescriptorSet(ctx context.Context, protoRoot string, rels ...string) (*descriptorpb.FileDescriptorSet, error) {
	stop := perf.Track("compile descriptor_set (" + strings.Join(rels, ",") + ")")
	defer stop()

	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{protoRoot},
		}),
		SourceInfoMode: protocompile.SourceInfoNone,
	}
	files, err := compiler.Compile(ctx, rels...)
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
		// Резолвер из скомпилированного набора — чтобы распаковывать
		// google.protobuf.Any (вложенный тип ищется здесь).
		Resolver: buildTypeResolver(files),
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
// buildTypeResolver собирает реестр типов из скомпилированного набора файлов,
// чтобы protojson мог распаковывать google.protobuf.Any (динамические типы).
func buildTypeResolver(files *protoregistry.Files) *protoregistry.Types {
	types := new(protoregistry.Types)
	var register func(mds protoreflect.MessageDescriptors)
	register = func(mds protoreflect.MessageDescriptors) {
		for i := 0; i < mds.Len(); i++ {
			md := mds.Get(i)
			_ = types.RegisterMessage(dynamicpb.NewMessageType(md))
			register(md.Messages()) // вложенные
		}
	}
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		register(fd.Messages())
		return true
	})
	return types
}

func decodeRaw(_ context.Context, binBytes []byte) (string, error) {
	var sb strings.Builder
	// tolerant=true: на усечённом хвосте показываем распарсенное + пометку,
	// а не падаем целиком (полезно для capped-дампов вроде meta.bin).
	if err := dumpRawFields(&sb, binBytes, 0, true); err != nil {
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

func dumpRawFields(sb *strings.Builder, b []byte, depth int, tolerant bool) error {
	if depth > 100 {
		return fmt.Errorf("nesting too deep")
	}
	pad := strings.Repeat("  ", depth)
	// fail обрабатывает невалидное/усечённое поле: при tolerant — печатает
	// пометку и сворачивает разбор (return nil), иначе возвращает ошибку.
	fail := func(remaining int) error {
		if tolerant {
			fmt.Fprintf(sb, "%s… truncated (%d bytes unparsed)\n", pad, remaining)
			return nil
		}
		return fmt.Errorf("malformed protobuf")
	}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 || num < 1 || typ > 5 {
			return fail(len(b))
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return fail(len(b))
			}
			b = b[n:]
			fmt.Fprintf(sb, "%s%d: %d\n", pad, num, v)
		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return fail(len(b))
			}
			b = b[n:]
			fmt.Fprintf(sb, "%s%d: 0x%08x\n", pad, num, v)
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return fail(len(b))
			}
			b = b[n:]
			fmt.Fprintf(sb, "%s%d: 0x%016x\n", pad, num, v)
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return fail(len(b))
			}
			b = b[n:]
			var nested strings.Builder
			switch {
			case isProbablyText(v):
				fmt.Fprintf(sb, "%s%d: %q\n", pad, num, string(v))
			case len(v) > 0 && dumpRawFields(&nested, v, depth+1, false) == nil:
				fmt.Fprintf(sb, "%s%d: {\n%s%s}\n", pad, num, nested.String(), pad)
			default:
				fmt.Fprintf(sb, "%s%d: %x\n", pad, num, v)
			}
		case protowire.StartGroupType:
			v, n := protowire.ConsumeGroup(num, b)
			if n < 0 {
				return fail(len(b))
			}
			b = b[n:]
			var nested strings.Builder
			_ = dumpRawFields(&nested, v, depth+1, false)
			fmt.Fprintf(sb, "%s%d: {\n%s%s}\n", pad, num, nested.String(), pad)
		default:
			return fail(len(b))
		}
	}
	return nil
}

func (d *Decoder) Decode(ctx context.Context, req domain.DecodeRequest) (domain.DecodeResult, error) {
	// Слой-1 авто-детект: снимаем gzip/zlib/base64/hex (одинаково для File и Redis).
	// Явный флаг req.Gzip теперь избыточен (gzip определяется по magic-байтам) и
	// сохранён лишь для обратной совместимости / ключей кэша.
	unwrapped, chain := detectEncoding(req.Bytes)

	res, err := d.decodeWithBytes(ctx, req, unwrapped)
	if err == nil {
		res.DetectedChain = chain
		res.AutoDetectedGzip = containsStr(chain, encGzip)
		return res, nil
	}

	// Если развёртка изменила байты, но декод не удался — пробуем исходные.
	if !bytes.Equal(unwrapped, req.Bytes) {
		if res2, err2 := d.decodeWithBytes(ctx, req, req.Bytes); err2 == nil {
			res2.DetectedChain = []string{classifyPayload(req.Bytes)}
			return res2, nil
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
