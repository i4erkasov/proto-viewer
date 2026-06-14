package protodec

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// samplePB — валидный protobuf wire: {1: varint 150, 2: "hello"}.
func samplePB() []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, 150)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendBytes(b, []byte("hello"))
	return b
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func zl(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func TestDetectEncoding(t *testing.T) {
	pb := samplePB()

	cases := []struct {
		name      string
		in        []byte
		wantChain []string
		wantInner []byte
	}{
		{"raw protobuf", pb, []string{encProtobuf}, pb},
		{"gzip", gz(pb), []string{encGzip, encProtobuf}, pb},
		{"zlib", zl(pb), []string{encZlib, encProtobuf}, pb},
		{"base64", []byte(base64.StdEncoding.EncodeToString(pb)), []string{encBase64, encProtobuf}, pb},
		{"base64+gzip", []byte(base64.StdEncoding.EncodeToString(gz(pb))), []string{encBase64, encGzip, encProtobuf}, pb},
		{"hex", []byte(hex.EncodeToString(pb)), []string{encHex, encProtobuf}, pb},
		{"json", []byte(`{"a":1,"b":"x"}`), []string{encJSON}, []byte(`{"a":1,"b":"x"}`)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inner, chain := detectEncoding(c.in)
			if !reflect.DeepEqual(chain, c.wantChain) {
				t.Fatalf("chain = %v, want %v", chain, c.wantChain)
			}
			if !bytes.Equal(inner, c.wantInner) {
				t.Fatalf("inner = %x, want %x", inner, c.wantInner)
			}
		})
	}
}

// TestTruncatedProtobuf — protobuf с обрезанным хвостом (capped-дамп):
// детект всё равно признаёт его protobuf, а RAW показывает распарсенное + пометку.
func TestTruncatedProtobuf(t *testing.T) {
	var pb []byte
	for i := 0; i < 20; i++ { // достаточно большой валидный префикс (>64 байт)
		pb = protowire.AppendTag(pb, 1, protowire.BytesType)
		pb = protowire.AppendBytes(pb, []byte("player-name"))
	}
	valid := len(pb)
	// Обрезанное поле: tag + длина 100, но данных нет.
	pb = protowire.AppendTag(pb, 2, protowire.BytesType)
	pb = protowire.AppendVarint(pb, 100)

	if !looksLikeProtobuf(pb) {
		t.Fatalf("truncated protobuf not recognized (cov=%d len=%d)", protoWireCoverage(pb), len(pb))
	}
	if validProtoWire(pb) {
		t.Fatal("strict validProtoWire must reject truncated input")
	}
	if protoWireCoverage(pb) != valid {
		t.Fatalf("coverage = %d, want %d", protoWireCoverage(pb), valid)
	}

	raw, err := decodeRaw(context.Background(), pb)
	if err != nil {
		t.Fatalf("tolerant decodeRaw must not fail: %v", err)
	}
	if !strings.Contains(raw, "player-name") || !strings.Contains(raw, "truncated") {
		t.Fatalf("raw dump missing data or truncation marker:\n%s", raw)
	}
}

// TestDetectNoFalsePeel — обычный текст с пробелами/пунктуацией не должен
// сниматься как base64/hex (вне их алфавита) и не плодить лишние слои.
func TestDetectNoFalsePeel(t *testing.T) {
	in := []byte("Hello, this is not encoded.")
	inner, chain := detectEncoding(in)
	if len(chain) != 1 {
		t.Fatalf("expected single classification, got chain %v", chain)
	}
	if !bytes.Equal(inner, in) {
		t.Fatalf("text was modified: %q", inner)
	}
}
