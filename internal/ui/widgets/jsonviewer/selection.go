//go:build darwin || linux || windows

package jsonviewer

import (
	"strconv"
	"strings"
	"unicode"
)

func (v *JSONView) setSelectedKey(line int, rng highlightRange, key string) {
	v.mu.Lock()
	v.selectedKeyLine = line
	v.selectedKeyRange = rng
	v.selectedKeyValue = key
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.mu.Unlock()
}

func (v *JSONView) setSelectedValue(line int, rng highlightRange, val string) {
	v.mu.Lock()
	v.selectedValueLine = line
	v.selectedValueRange = rng
	v.selectedValueText = val
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.mu.Unlock()
}

func (v *JSONView) clearSelectedKey() {
	v.mu.Lock()
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.mu.Unlock()
}

func (v *JSONView) clearSelectedValue() {
	v.mu.Lock()
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.mu.Unlock()
}

func (v *JSONView) refreshSelection() {
	v.mu.Lock()
	lines := v.viewLines
	loaded := v.loaded
	v.mu.Unlock()
	if loaded > 0 {
		v.setGrid(lines[:loaded])
	} else {
		v.setGrid(nil)
	}
}

func (v *JSONView) SelectedKeyValueString() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.selectedValueLine >= 0 {
		if val, ok := v.fullValueForLineLocked(v.selectedValueLine); ok {
			return wrapCopyContent(strings.TrimSpace(val))
		}
	}
	if strings.TrimSpace(v.selectedValueText) != "" {
		return wrapCopyContent(strings.TrimSpace(v.selectedValueText))
	}
	return wrapCopyContent(quoteKeyIfNeeded(strings.TrimSpace(v.selectedKeyValue)))
}

func (v *JSONView) fullValueForLine(srcLine int) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fullValueForLineLocked(srcLine)
}

func (v *JSONView) fullValueForLineLocked(srcLine int) (string, bool) {
	if srcLine < 0 || srcLine >= v.fullLineCountLocked() {
		return "", false
	}
	line := string(v.fullBuf.Line(srcLine))
	val, rng, ok := findValueRange(line)
	if !ok {
		return "", false
	}
	val = strings.TrimSpace(val)
	if val != "{" && val != "[" {
		return "", false
	}
	braceIdx := rng.start
	brace := rune(val[0])
	closing := '}'
	if brace == '[' {
		closing = ']'
	}

	depth := 0
	inString := false
	esc := false
	out := make([]string, 0, 16)

	for i := srcLine; i < v.fullLineCountLocked(); i++ {
		ln := string(v.fullBuf.Line(i))
		runes := []rune(ln)
		startCol := 0
		if i == srcLine {
			startCol = braceIdx
		}
		var buf strings.Builder

		for j, r := range runes {
			if j >= startCol {
				buf.WriteRune(r)
			}

			if inString {
				if esc {
					esc = false
					continue
				}
				if r == '\\' {
					esc = true
					continue
				}
				if r == '"' {
					inString = false
				}
				continue
			}
			if r == '"' {
				inString = true
				continue
			}

			if r == brace {
				depth++
			} else if r == closing {
				depth--
				if depth == 0 {
					out = append(out, buf.String())
					return strings.Join(out, "\n"), true
				}
			}
		}

		if buf.Len() > 0 {
			out = append(out, buf.String())
		} else if i > srcLine {
			out = append(out, "")
		}
	}
	return "", false
}

// --- Selection helpers

func keyAtCol(line string, col int) (string, highlightRange, bool) {
	start, end, ok := findKeyRange(line)
	if !ok {
		return "", highlightRange{}, false
	}
	if col < start || col > end {
		return "", highlightRange{}, false
	}
	runes := []rune(line)
	if start+1 > end-1 || start < 0 || end >= len(runes) {
		return "", highlightRange{}, false
	}
	keyRunes := runes[start+1 : end]
	key := string(keyRunes)
	if unq, err := strconv.Unquote("\"" + key + "\""); err == nil {
		key = unq
	}
	return key, highlightRange{start: start, end: end}, true
}

func valueAtCol(line string, col int) (string, highlightRange, bool) {
	val, rng, ok := findValueRange(line)
	if !ok {
		return "", highlightRange{}, false
	}
	if col < rng.start || col > rng.end {
		return "", highlightRange{}, false
	}
	return val, rng, true
}

func extractKeyValue(line string) (string, string, bool) {
	keyStart, keyEnd, ok := findKeyRange(line)
	if !ok {
		return "", "", false
	}
	runes := []rune(line)
	if keyStart+1 > keyEnd-1 || keyStart < 0 || keyEnd >= len(runes) {
		return "", "", false
	}
	key := string(runes[keyStart+1 : keyEnd])
	if unq, err := strconv.Unquote("\"" + key + "\""); err == nil {
		key = unq
	}
	val, _, ok := findValueRange(line)
	if !ok {
		return key, "", false
	}
	return key, val, true
}

func isInteractiveCell(line string, col int) bool {
	if line == "" || col < 0 {
		return false
	}
	if start, end, ok := findTokenRange(line, "{ ... }"); ok {
		return col >= start && col <= end
	}
	if start, end, ok := findTokenRange(line, "[ ... ]"); ok {
		return col >= start && col <= end
	}
	if start, end, ok := findKeyRange(line); ok {
		return col >= start && col <= end
	}
	if idx, ok := singleBraceIndex(line); ok {
		return col == idx
	}
	return false
}

func findTokenRange(line, token string) (int, int, bool) {
	lineRunes := []rune(line)
	tokenRunes := []rune(token)
	if len(tokenRunes) == 0 || len(lineRunes) < len(tokenRunes) {
		return 0, 0, false
	}
	for i := 0; i+len(tokenRunes) <= len(lineRunes); i++ {
		match := true
		for j := range tokenRunes {
			if lineRunes[i+j] != tokenRunes[j] {
				match = false
				break
			}
		}
		if match {
			return i, i + len(tokenRunes) - 1, true
		}
	}
	return 0, 0, false
}

func findKeyRange(line string) (int, int, bool) {
	runes := []rune(line)
	inString := false
	esc := false
	start := -1
	for i, r := range runes {
		if inString {
			if esc {
				esc = false
				continue
			}
			if r == '\\' {
				esc = true
				continue
			}
			if r == '"' {
				j := i + 1
				for j < len(runes) && unicode.IsSpace(runes[j]) {
					j++
				}
				if j < len(runes) && runes[j] == ':' {
					return start, i, true
				}
				inString = false
				continue
			}
			continue
		}
		if r == '"' {
			inString = true
			start = i
		}
	}
	return 0, 0, false
}

func singleBraceIndex(line string) (int, bool) {
	runes := []rune(line)
	for i, r := range runes {
		if unicode.IsSpace(r) {
			continue
		}
		if r == '{' || r == '[' {
			return i, true
		}
		return 0, false
	}
	return 0, false
}

func findValueRange(line string) (string, highlightRange, bool) {
	_, keyEnd, ok := findKeyRange(line)
	if !ok {
		return "", highlightRange{}, false
	}
	runes := []rune(line)
	if keyEnd+1 >= len(runes) {
		return "", highlightRange{}, false
	}
	idx := keyEnd + 1
	for idx < len(runes) && runes[idx] != ':' {
		idx++
	}
	if idx >= len(runes) {
		return "", highlightRange{}, false
	}
	idx++
	for idx < len(runes) && unicode.IsSpace(runes[idx]) {
		idx++
	}
	if idx >= len(runes) {
		return "", highlightRange{}, false
	}
	start := idx
	if runes[idx] == '"' {
		idx++
		esc := false
		for idx < len(runes) {
			r := runes[idx]
			if esc {
				esc = false
				idx++
				continue
			}
			if r == '\\' {
				esc = true
				idx++
				continue
			}
			if r == '"' {
				idx++
				break
			}
			idx++
		}
		end := idx - 1
		val := string(runes[start+1 : end])
		if unq, err := strconv.Unquote("\"" + val + "\""); err == nil {
			val = unq
		}
		return val, highlightRange{start: start, end: end}, true
	}
	for idx < len(runes) {
		r := runes[idx]
		if r == ',' || r == '}' || r == ']' {
			break
		}
		if r == '\n' || r == '\r' {
			break
		}
		idx++
	}
	end := idx - 1
	val := strings.TrimSpace(string(runes[start:idx]))
	if strings.HasSuffix(val, ",") {
		val = strings.TrimSpace(strings.TrimSuffix(val, ","))
		end = start + len([]rune(val)) - 1
	}
	return val, highlightRange{start: start, end: end}, true
}

func wrapCopyContent(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return s
	}
	if strings.Contains(trimmed, "\n") {
		return "{\n" + trimmed + "\n}"
	}
	return "{" + trimmed + "}"
}

func quoteKeyIfNeeded(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "\"") && strings.HasSuffix(key, "\"") {
		return key
	}
	return "\"" + key + "\""
}
