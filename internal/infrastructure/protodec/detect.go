package protodec

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// Слой 1 авто-детекта: снимает транспортные/кодировочные обёртки
// (gzip/zlib/base64/hex) и классифицирует ядро payload'а. Чистые функции,
// не зависят от источника — одинаково работают для File и Redis.

const (
	encGzip     = "gzip"
	encZlib     = "zlib"
	encBase64   = "base64"
	encHex      = "hex"
	encProtobuf = "protobuf"
	encJSON     = "json"
	encText     = "text"
	encBinary   = "binary"
)

// detectEncoding итеративно «снимает» известные обёртки и возвращает развёрнутые
// байты и цепочку детекта, например ["base64","gzip","protobuf"].
//
// base64/hex снимаются только если ДЕКОДИРОВАННОЕ валидируется как что-то
// известное (gzip/zlib/protobuf/json) и текущие байты сами не являются валидным
// protobuf — это убирает ложные срабатывания на сырых payload'ах.
func detectEncoding(raw []byte) (inner []byte, chain []string) {
	cur := raw
	for i := 0; i < 8; i++ { // ограничение на число слоёв
		if looksLikeGzip(cur) {
			if d, err := gunzipBytes(cur); err == nil {
				cur, chain = d, append(chain, encGzip)
				continue
			}
		}
		if looksLikeZlib(cur) {
			if d, err := inflateZlib(cur); err == nil {
				cur, chain = d, append(chain, encZlib)
				continue
			}
		}
		// base64/hex — только если текущее не является само по себе protobuf.
		if !validProtoWire(cur) {
			if d, ok := tryBase64(cur); ok && looksDecodable(d) {
				cur, chain = d, append(chain, encBase64)
				continue
			}
			if d, ok := tryHex(cur); ok && looksDecodable(d) {
				cur, chain = d, append(chain, encHex)
				continue
			}
		}
		break
	}
	return cur, append(chain, classifyPayload(cur))
}

// looksDecodable — стоит ли доверять снятому слою: ядро похоже на известный формат.
func looksDecodable(b []byte) bool {
	return looksLikeGzip(b) || looksLikeZlib(b) || looksLikeProtobuf(b) || looksLikeJSON(b)
}

// classifyPayload определяет вид ядра (для строки статуса). Чисто косметика —
// на сам декод не влияет (байты уходят в decodeWithBytes как есть).
func classifyPayload(b []byte) string {
	if len(b) == 0 {
		return encBinary
	}
	if looksLikeJSON(b) {
		return encJSON
	}
	if looksLikeProtobuf(b) {
		return encProtobuf
	}
	if isProbablyText(b) {
		return encText
	}
	return encBinary
}

func looksLikeZlib(b []byte) bool {
	if len(b) < 2 || b[0]&0x0f != 0x08 { // CM=8 (deflate)
		return false
	}
	return (uint16(b[0])<<8|uint16(b[1]))%31 == 0 // контрольная сумма CMF/FLG
}

func inflateZlib(b []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

func looksLikeJSON(b []byte) bool {
	s := bytes.TrimSpace(b)
	if len(s) == 0 || (s[0] != '{' && s[0] != '[') {
		return false
	}
	return json.Valid(s)
}

// protoWireCoverage возвращает число ведущих байт b, образующих корректные
// protobuf-поля (останавливается на первом невалидном/усечённом поле).
func protoWireCoverage(b []byte) int {
	off, rest := 0, b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 || num < 1 || typ > 5 {
			break
		}
		m := protowire.ConsumeFieldValue(num, typ, rest[n:])
		if m < 0 {
			break
		}
		rest = rest[n+m:]
		off += n + m
	}
	return off
}

// validProtoWire — строго: все байты потреблены как валидный wire-format.
func validProtoWire(b []byte) bool {
	return len(b) > 0 && protoWireCoverage(b) == len(b)
}

// looksLikeProtobuf — валиден полностью ИЛИ почти полностью (усечённый хвост,
// например capped-дамп). Для коротких payload'ов требуем полной валидности.
func looksLikeProtobuf(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	cov := protoWireCoverage(b)
	if cov == len(b) {
		return true
	}
	return len(b) >= 64 && cov*100 >= len(b)*95
}

func tryBase64(b []byte) ([]byte, bool) {
	s := strings.TrimSpace(string(b))
	if len(s) < 8 || len(s)%4 != 0 {
		return nil, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=') {
			return nil, false
		}
	}
	d, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(d) == 0 {
		return nil, false
	}
	return d, true
}

func tryHex(b []byte) ([]byte, bool) {
	s := strings.TrimSpace(string(b))
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s) < 8 || len(s)%2 != 0 {
		return nil, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return nil, false
		}
	}
	d, err := hex.DecodeString(s)
	if err != nil || len(d) == 0 {
		return nil, false
	}
	return d, true
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
