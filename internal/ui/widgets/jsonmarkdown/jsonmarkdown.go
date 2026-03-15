//go:build darwin || linux || windows

package jsonmarkdown

import (
	"bytes"
	"encoding/json"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	defaultChunkLines = 200
	scrollThreshold   = 12
)

// JSONMarkdownView renders JSON as markdown with lazy line loading.
type JSONMarkdownView struct {
	mu      sync.Mutex
	lines   []string
	loaded  int
	chunk   int
	loading bool

	tgrid  *widget.TextGrid
	scroll *container.Scroll
}

// NewJSONMarkdownView creates a markdown view with lazy loading.
func NewJSONMarkdownView() *JSONMarkdownView {
	v := &JSONMarkdownView{chunk: defaultChunkLines}
	v.tgrid = widget.NewTextGrid()
	// styles applied per cell
	v.scroll = container.NewScroll(v.tgrid)
	v.scroll.OnScrolled = func(_ fyne.Position) {
		v.tryLoadMore()
	}
	return v
}

// View returns the scrollable markdown view.
func (v *JSONMarkdownView) View() fyne.CanvasObject {
	return v.scroll
}

// SetJSON resets content and loads the first chunk.
func (v *JSONMarkdownView) SetJSON(s string) {
	v.mu.Lock()
	v.loaded = 0
	v.loading = false
	v.mu.Unlock()

	if strings.TrimSpace(s) == "" {
		v.setGrid(nil)
		return
	}

	pretty := s
	if json.Valid([]byte(s)) {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			if b, err := json.MarshalIndent(v, "", "  "); err == nil {
				pretty = string(b)
			}
		} else {
			var buf bytes.Buffer
			if err := json.Indent(&buf, []byte(s), "", "  "); err == nil {
				pretty = buf.String()
			}
		}
		if !strings.Contains(pretty, "\n") {
			var buf bytes.Buffer
			if err := json.Indent(&buf, []byte(s), "", "  "); err == nil {
				pretty = buf.String()
			}
		}
	}

	v.mu.Lock()
	v.lines = splitLines(pretty)
	v.mu.Unlock()

	v.loadMore()
}

func (v *JSONMarkdownView) tryLoadMore() {
	v.mu.Lock()
	if v.loading {
		v.mu.Unlock()
		return
	}
	v.loading = true
	v.mu.Unlock()

	fyne.Do(func() {
		v.mu.Lock()
		v.loading = false
		v.mu.Unlock()

		if v.scroll.Content == nil {
			return
		}
		if v.scroll.Offset.Y+v.scroll.Size().Height < v.scroll.Content.Size().Height-scrollThreshold {
			return
		}
		v.loadMore()
	})
}

func (v *JSONMarkdownView) loadMore() {
	v.mu.Lock()
	if len(v.lines) == 0 {
		v.mu.Unlock()
		v.setGrid(nil)
		return
	}
	if v.loaded >= len(v.lines) {
		v.mu.Unlock()
		return
	}
	end := v.loaded + v.chunk
	if end > len(v.lines) {
		end = len(v.lines)
	}
	chunkLines := make([]string, end)
	copy(chunkLines, v.lines[:end])
	v.loaded = end
	v.mu.Unlock()

	v.setGrid(chunkLines)
}

func (v *JSONMarkdownView) setGrid(lines []string) {
	fyne.Do(func() {
		if v.tgrid == nil {
			return
		}
		v.tgrid.Rows = buildTextGridRows(lines)
		v.tgrid.Refresh()
		v.scroll.Refresh()
	})
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// --- JSON color palette (matches tree colors)
func jsonKeyColor() color.Color {
	return color.NRGBA{R: 0x8B, G: 0xC4, B: 0xF9, A: 0xFF}
}

func jsonStringColor() color.Color {
	return color.NRGBA{R: 0x9E, G: 0xD9, B: 0x8A, A: 0xFF}
}

func jsonNumberColor() color.Color {
	return color.NRGBA{R: 0xF2, G: 0x9D, B: 0x50, A: 0xFF}
}

func jsonBoolColor() color.Color {
	return color.NRGBA{R: 0xB3, G: 0x8D, B: 0xF7, A: 0xFF}
}

func jsonNullColor() color.Color {
	return color.NRGBA{R: 0xA0, G: 0xA0, B: 0xA0, A: 0xFF}
}

func jsonPunctColor() color.Color {
	return color.NRGBA{R: 0xB0, G: 0xB0, B: 0xB0, A: 0xFF}
}

// --- JSON tokenizer -> colored segments
// (RichText segments removed.)

func hasWord(runes []rune, i int, word string) bool {
	w := []rune(word)
	if i+len(w) > len(runes) {
		return false
	}
	for k := range w {
		if runes[i+k] != w[k] {
			return false
		}
	}
	end := i + len(w)
	if end < len(runes) {
		if isWordChar(runes[end]) {
			return false
		}
	}
	if i > 0 {
		if isWordChar(runes[i-1]) {
			return false
		}
	}
	return true
}

func isWordChar(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isNumberStart(runes []rune, i int) bool {
	if i >= len(runes) {
		return false
	}
	r := runes[i]
	if r == '-' {
		return i+1 < len(runes) && unicode.IsDigit(runes[i+1])
	}
	return unicode.IsDigit(r)
}

func isNumberChar(r rune) bool {
	return unicode.IsDigit(r) || r == '.' || r == 'e' || r == 'E' || r == '+' || r == '-'
}

func buildTextGridRows(lines []string) []widget.TextGridRow {
	if len(lines) == 0 {
		return nil
	}
	rows := make([]widget.TextGridRow, 0, len(lines))
	for _, line := range lines {
		cells := buildTextGridCells(line)
		rows = append(rows, widget.TextGridRow{Cells: cells})
	}
	return rows
}

func buildTextGridCells(line string) []widget.TextGridCell {
	if line == "" {
		return nil
	}
	cells := make([]widget.TextGridCell, 0, len(line))
	pending := ""
	pendingColor := theme.ForegroundColor()
	flush := func() {
		if pending == "" {
			return
		}
		for _, r := range pending {
			cells = append(cells, widget.TextGridCell{
				Rune: r,
				Style: &widget.CustomTextGridStyle{
					FGColor: pendingColor,
				},
			})
		}
		pending = ""
	}
	setPending := func(text string, c color.Color) {
		if pending != "" && c != pendingColor {
			flush()
		}
		pendingColor = c
		pending += text
	}

	runes := []rune(line)
	i := 0
	for i < len(runes) {
		r := runes[i]

		// Strings
		if r == '"' {
			j := i + 1
			esc := false
			for j < len(runes) {
				ch := runes[j]
				if esc {
					esc = false
					j++
					continue
				}
				if ch == '\\' {
					esc = true
					j++
					continue
				}
				if ch == '"' {
					j++
					break
				}
				j++
			}
			lit := string(runes[i:j])
			k := j
			for k < len(runes) && unicode.IsSpace(runes[k]) {
				k++
			}
			if k < len(runes) && runes[k] == ':' {
				setPending(lit, jsonKeyColor())
			} else {
				setPending(lit, jsonStringColor())
			}
			i = j
			continue
		}

		// Whitespace
		if unicode.IsSpace(r) {
			j := i + 1
			for j < len(runes) && unicode.IsSpace(runes[j]) {
				j++
			}
			setPending(string(runes[i:j]), jsonPunctColor())
			i = j
			continue
		}

		// Booleans / null
		if hasWord(runes, i, "true") {
			setPending("true", jsonBoolColor())
			i += 4
			continue
		}
		if hasWord(runes, i, "false") {
			setPending("false", jsonBoolColor())
			i += 5
			continue
		}
		if hasWord(runes, i, "null") {
			setPending("null", jsonNullColor())
			i += 4
			continue
		}

		// Numbers
		if isNumberStart(runes, i) {
			j := i + 1
			for j < len(runes) && isNumberChar(runes[j]) {
				j++
			}
			num := string(runes[i:j])
			if _, err := strconv.ParseFloat(num, 64); err == nil {
				setPending(num, jsonNumberColor())
				i = j
				continue
			}
		}

		// Punctuation
		switch r {
		case '{', '}', '[', ']', ':', ',':
			setPending(string(r), jsonPunctColor())
			i++
			continue
		}

		setPending(string(r), theme.ForegroundColor())
		i++
	}
	flush()
	return cells
}
