package protodec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// TestGuessType проверяет, что правильный тип сообщения ранжируется первым.
func TestGuessType(t *testing.T) {
	dir := t.TempDir()
	src := `syntax = "proto3";
message Player { string name = 1; int32 age = 2; Team team = 3; }
message Team   { string title = 1; }
message Other  { bool flag = 1; }`
	p := filepath.Join(dir, "p.proto")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Player{ name:"Bob", age:7, team:{title:"X"} } вручную в wire-формате.
	var team []byte
	team = protowire.AppendTag(team, 1, protowire.BytesType)
	team = protowire.AppendBytes(team, []byte("X"))
	var pb []byte
	pb = protowire.AppendTag(pb, 1, protowire.BytesType)
	pb = protowire.AppendBytes(pb, []byte("Bob"))
	pb = protowire.AppendTag(pb, 2, protowire.VarintType)
	pb = protowire.AppendVarint(pb, 7)
	pb = protowire.AppendTag(pb, 3, protowire.BytesType)
	pb = protowire.AppendBytes(pb, team)

	guesses, err := New().GuessType(context.Background(), dir, p, pb)
	if err != nil {
		t.Fatalf("GuessType: %v", err)
	}
	if len(guesses) == 0 {
		t.Fatal("no candidates")
	}
	top := guesses[0]
	if top.FullType != "Player" {
		t.Fatalf("top candidate = %q, want Player (all: %+v)", top.FullType, guesses)
	}
	if top.Unknown != 0 {
		t.Fatalf("Player should explain all data (unknown=%d)", top.Unknown)
	}
	if top.Fields < 3 {
		t.Fatalf("Player should have >=3 populated fields, got %d", top.Fields)
	}
}

// TestGuessTypeAllRoot — без указания Proto file детект ищет по всем .proto под root.
func TestGuessTypeAllRoot(t *testing.T) {
	dir := t.TempDir()
	// Два файла в корне; нужный тип — в b.proto, который мы НЕ указываем.
	if err := os.WriteFile(filepath.Join(dir, "a.proto"),
		[]byte("syntax = \"proto3\";\nmessage Alpha { bool x = 1; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.proto"),
		[]byte("syntax = \"proto3\";\nmessage Beta { string name = 1; int32 n = 2; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	var pb []byte
	pb = protowire.AppendTag(pb, 1, protowire.BytesType)
	pb = protowire.AppendBytes(pb, []byte("hello"))
	pb = protowire.AppendTag(pb, 2, protowire.VarintType)
	pb = protowire.AppendVarint(pb, 42)

	// protoAbs пуст → поиск по всему root.
	guesses, err := New().GuessType(context.Background(), dir, "", pb)
	if err != nil {
		t.Fatalf("GuessType: %v", err)
	}
	if len(guesses) == 0 || guesses[0].FullType != "Beta" {
		t.Fatalf("want Beta first, got %+v", guesses)
	}
}

// TestGuessTypeAllRootTolerant — файл с неразрешённым импортом не должен ломать
// детект по остальным файлам под root.
func TestGuessTypeAllRootTolerant(t *testing.T) {
	dir := t.TempDir()
	// Битый файл: импортирует отсутствующую внешнюю зависимость.
	if err := os.WriteFile(filepath.Join(dir, "broken.proto"),
		[]byte("syntax = \"proto3\";\nimport \"buf/validate/validate.proto\";\nmessage Broken { string s = 1; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Нормальный файл.
	if err := os.WriteFile(filepath.Join(dir, "ok.proto"),
		[]byte("syntax = \"proto3\";\nmessage Good { string name = 1; int32 n = 2; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	var pb []byte
	pb = protowire.AppendTag(pb, 1, protowire.BytesType)
	pb = protowire.AppendBytes(pb, []byte("x"))
	pb = protowire.AppendTag(pb, 2, protowire.VarintType)
	pb = protowire.AppendVarint(pb, 5)

	guesses, err := New().GuessType(context.Background(), dir, "", pb)
	if err != nil {
		t.Fatalf("GuessType должен пережить битый файл: %v", err)
	}
	if len(guesses) == 0 || guesses[0].FullType != "Good" {
		t.Fatalf("want Good first despite broken.proto, got %+v", guesses)
	}
}

// TestGuessTypeEmpty — пустой payload даёт пустой результат (нечего детектить).
func TestGuessTypeEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "e.proto")
	_ = os.WriteFile(p, []byte("syntax = \"proto3\";\nmessage M { string s = 1; }"), 0o644)
	g, err := New().GuessType(context.Background(), dir, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 0 {
		t.Fatalf("empty payload must yield no guesses, got %v", g)
	}
}
