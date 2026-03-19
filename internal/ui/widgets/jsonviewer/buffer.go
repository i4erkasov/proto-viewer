//go:build darwin || linux || windows

package jsonviewer

import "strconv"

// --- JSON buffer and helpers

type JSONBuffer struct {
	data        []byte
	lineOffsets []int
}

func newJSONBuffer(s string) *JSONBuffer {
	return newJSONBufferFromBytes([]byte(s))
}

func newJSONBufferFromBytes(data []byte) *JSONBuffer {
	b := &JSONBuffer{}
	if len(data) == 0 {
		return b
	}
	b.data = data
	b.lineOffsets = buildLineOffsets(b.data)
	return b
}

func buildLineOffsets(data []byte) []int {
	if len(data) == 0 {
		return nil
	}
	offsets := make([]int, 0, 1024)
	offsets = append(offsets, 0)
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

func (b *JSONBuffer) LineCount() int {
	if b == nil {
		return 0
	}
	return len(b.lineOffsets)
}

func (b *JSONBuffer) Line(i int) []byte {
	if b == nil || i < 0 || i >= len(b.lineOffsets) {
		return nil
	}
	start := b.lineOffsets[i]
	if start >= len(b.data) {
		return nil
	}
	end := len(b.data)
	if i+1 < len(b.lineOffsets) {
		end = b.lineOffsets[i+1] - 1
		if end < start {
			end = start
		}
	}
	if end > len(b.data) {
		end = len(b.data)
	}
	return b.data[start:end]
}

func lineIndentDepthBytes(line []byte) int {
	count := 0
	for _, b := range line {
		if b != ' ' {
			break
		}
		count++
	}
	return count / 2
}

func extractLineKeyBytes(line []byte) (string, bool) {
	inString := false
	esc := false
	start := -1

	for i, b := range line {
		if inString {
			if esc {
				esc = false
				continue
			}
			if b == '\\' {
				esc = true
				continue
			}
			if b == '"' {
				keyBytes := line[start:i]
				j := i + 1
				for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
					j++
				}
				if j < len(line) && line[j] == ':' {
					key := string(keyBytes)
					if unq, err := strconv.Unquote("\"" + key + "\""); err == nil {
						return unq, true
					}
					return key, true
				}
				inString = false
				continue
			}
			continue
		}
		if b == '"' {
			inString = true
			start = i + 1
		}
	}
	return "", false
}
