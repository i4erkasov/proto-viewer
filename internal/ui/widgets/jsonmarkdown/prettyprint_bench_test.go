package jsonmarkdown

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
)

func prettyViaSonicUnmarshalMarshalIndent(data []byte) ([]byte, error) {
	var parsed any
	if err := sonic.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	return sonic.MarshalIndent(parsed, "", "  ")
}

func prettyViaJSONIndent(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func BenchmarkPrettyPrint_50MB_SonicUnmarshal(b *testing.B) {
	data := []byte(makeLargeJSON(50 << 20))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := prettyViaSonicUnmarshalMarshalIndent(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPrettyPrint_50MB_JSONIndent(b *testing.B) {
	data := []byte(makeLargeJSON(50 << 20))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := prettyViaJSONIndent(data); err != nil {
			b.Fatal(err)
		}
	}
}
