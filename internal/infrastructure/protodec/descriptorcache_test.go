package protodec

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"
)

// TestDescriptorCacheHitAndInvalidation проверяет, что набор кэшируется и
// переиспользуется, пока файлы не изменились, и инвалидируется при правке.
func TestDescriptorCacheHitAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.proto")
	if err := os.WriteFile(p, []byte(`syntax="proto3";`), 0o644); err != nil {
		t.Fatal(err)
	}

	name := "a.proto"
	fds := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{{Name: &name}},
	}
	key := descKey(dir, p)
	descCachePut(key, dir, fds)

	got, ok := descCacheGet(key)
	if !ok || got != fds {
		t.Fatal("expected cache hit returning the same FileDescriptorSet")
	}

	// Изменяем .proto -> кэш должен инвалидироваться.
	if err := os.WriteFile(p, []byte(`syntax="proto3"; message M { int32 a = 1; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := descCacheGet(key); ok {
		t.Fatal("expected cache miss after the .proto changed")
	}
}

// TestDescriptorCacheMissingFile проверяет инвалидацию при удалении файла.
func TestDescriptorCacheMissingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.proto")
	if err := os.WriteFile(p, []byte(`syntax="proto3";`), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "b.proto"
	fds := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{{Name: &name}},
	}
	key := descKey(dir, p)
	descCachePut(key, dir, fds)
	if _, ok := descCacheGet(key); !ok {
		t.Fatal("expected initial cache hit")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := descCacheGet(key); ok {
		t.Fatal("expected cache miss after the .proto was removed")
	}
}
