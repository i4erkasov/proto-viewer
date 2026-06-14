package protodec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// descByName компилирует .proto и возвращает дескриптор сообщения.
func descByName(t *testing.T, root, abs, name string) protoreflect.MessageDescriptor {
	t.Helper()
	fds, err := compileDescriptorSet(context.Background(), root, abs)
	if err != nil {
		t.Fatalf("compileDescriptorSet: %v", err)
	}
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		t.Fatalf("NewFiles: %v", err)
	}
	d, err := files.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		t.Fatalf("find %s: %v", name, err)
	}
	md, ok := d.(protoreflect.MessageDescriptor)
	if !ok {
		t.Fatalf("%s is not a message", name)
	}
	return md
}

// TestCompileAndDecodeJSON проверяет in-process компиляцию .proto (protocompile)
// и декод сообщения в JSON — без внешнего protoc.
func TestCompileAndDecodeJSON(t *testing.T) {
	dir := t.TempDir()
	src := `syntax = "proto3";
message M {
  string name = 1;
  int32 age = 2;
}`
	p := filepath.Join(dir, "m.proto")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	md := descByName(t, dir, p, "M")
	msg := dynamicpb.NewMessage(md)
	msg.Set(md.Fields().ByName("name"), protoreflect.ValueOfString("Bob"))
	msg.Set(md.Fields().ByName("age"), protoreflect.ValueOfInt32(7))
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	js, err := decodeJSON(ctx, dir, "M", p, raw)
	if err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if !strings.Contains(js, `"name": "Bob"`) || !strings.Contains(js, `"age": 7`) {
		t.Fatalf("unexpected json:\n%s", js)
	}
}

// TestCompileWithImport проверяет, что импорты резолвятся (include_imports).
func TestCompileWithImport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "common.proto"), []byte(`syntax = "proto3";
message Inner { string v = 1; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "outer.proto")
	if err := os.WriteFile(p, []byte(`syntax = "proto3";
import "common.proto";
message Outer { Inner inner = 1; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = descByName(t, dir, p, "Outer")
}

// TestDecodeAnyUnpacked проверяет, что google.protobuf.Any распаковывается
// (через резолвер из скомпилированного набора), а не показывается как base64.
func TestDecodeAnyUnpacked(t *testing.T) {
	dir := t.TempDir()
	src := `syntax = "proto3";
import "google/protobuf/any.proto";
message Inner { string v = 1; }
message Outer { google.protobuf.Any data = 1; }`
	p := filepath.Join(dir, "a.proto")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Собираем байты вручную: Outer{ data = Any{ type_url, value=Inner{v:"hi"} } }.
	inner := protowire.AppendTag(nil, 1, protowire.BytesType)
	inner = protowire.AppendBytes(inner, []byte("hi"))
	var any []byte
	any = protowire.AppendTag(any, 1, protowire.BytesType)
	any = protowire.AppendBytes(any, []byte("type.googleapis.com/Inner"))
	any = protowire.AppendTag(any, 2, protowire.BytesType)
	any = protowire.AppendBytes(any, inner)
	var outer []byte
	outer = protowire.AppendTag(outer, 1, protowire.BytesType)
	outer = protowire.AppendBytes(outer, any)

	js, err := decodeJSON(context.Background(), dir, "Outer", p, outer)
	if err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if !strings.Contains(js, "@type") || !strings.Contains(js, `"hi"`) {
		t.Fatalf("Any не распаковался:\n%s", js)
	}
}

func TestDecodeRaw(t *testing.T) {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, 150)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendBytes(b, []byte("hi"))

	out, err := decodeRaw(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1: 150") || !strings.Contains(out, `2: "hi"`) {
		t.Fatalf("raw dump:\n%s", out)
	}
}
