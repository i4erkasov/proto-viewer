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
	"fyne.io/fyne/v2/canvas"
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
	mu        sync.Mutex
	fullLines []string
	viewLines []string
	lineMap   []int
	loaded    int
	chunk     int
	loading   bool

	foldRanges map[int]int
	folded     map[int]bool

	tgrid   *widget.TextGrid
	overlay *tapOverlay
	scroll  *container.Scroll
}

// NewJSONMarkdownView creates a markdown view with lazy loading.
func NewJSONMarkdownView() *JSONMarkdownView {
	v := &JSONMarkdownView{chunk: defaultChunkLines}
	v.tgrid = widget.NewTextGrid()
	v.overlay = newTapOverlay(v.handleTap)
	// styles applied per cell
	v.scroll = container.NewScroll(container.NewMax(v.tgrid, v.overlay))
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
	v.folded = map[int]bool{}
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

	lines := splitLines(pretty)
	foldRanges, foldDepths := buildFoldRangesWithDepth(lines)

	v.mu.Lock()
	v.fullLines = lines
	v.foldRanges = foldRanges
	v.folded = make(map[int]bool, len(foldRanges))
	for start := range foldRanges {
		if foldDepths[start] > 0 {
			v.folded[start] = true
		}
	}
	v.rebuildViewLinesLocked()
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
	if len(v.viewLines) == 0 {
		v.mu.Unlock()
		v.setGrid(nil)
		return
	}
	if v.loaded >= len(v.viewLines) {
		v.mu.Unlock()
		return
	}
	end := v.loaded + v.chunk
	if end > len(v.viewLines) {
		end = len(v.viewLines)
	}
	chunkLines := make([]string, end)
	copy(chunkLines, v.viewLines[:end])
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

// tapOverlay captures clicks over the TextGrid to toggle folds.
type tapOverlay struct {
	widget.BaseWidget
	onTap func(pos fyne.Position)
}

func newTapOverlay(onTap func(pos fyne.Position)) *tapOverlay {
	o := &tapOverlay{onTap: onTap}
	o.ExtendBaseWidget(o)
	return o
}

func (o *tapOverlay) Tapped(ev *fyne.PointEvent) {
	if o.onTap != nil {
		o.onTap(ev.Position)
	}
}

func (o *tapOverlay) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.Transparent)
	return widget.NewSimpleRenderer(rect)
}

func (v *JSONMarkdownView) rebuildViewLinesLocked() {
	v.viewLines = v.viewLines[:0]
	v.lineMap = v.lineMap[:0]
	if len(v.fullLines) == 0 {
		return
	}
	for i := 0; i < len(v.fullLines); {
		if end, ok := v.foldRanges[i]; ok && end > i && v.folded[i] {
			v.viewLines = append(v.viewLines, buildFoldPlaceholder(v.fullLines[i]))
			v.lineMap = append(v.lineMap, i)
			i = end + 1
			continue
		}
		v.viewLines = append(v.viewLines, v.fullLines[i])
		v.lineMap = append(v.lineMap, i)
		i++
	}
}

func (v *JSONMarkdownView) handleTap(pos fyne.Position) {
	if v.tgrid == nil {
		return
	}
	row, _ := v.tgrid.CursorLocationForPosition(pos)
	if row < 0 {
		return
	}

	v.mu.Lock()
	if row >= len(v.lineMap) {
		v.mu.Unlock()
		return
	}
	srcLine := v.lineMap[row]
	end, ok := v.foldRanges[srcLine]
	if !ok || end <= srcLine {
		v.mu.Unlock()
		return
	}
	v.folded[srcLine] = !v.folded[srcLine]
	v.rebuildViewLinesLocked()
	if v.loaded > len(v.viewLines) {
		v.loaded = len(v.viewLines)
	}
	if v.loaded == 0 && len(v.viewLines) > 0 {
		v.loaded = minInt(v.chunk, len(v.viewLines))
	}
	if !v.folded[srcLine] {
		if newRow := findViewRow(v.lineMap, srcLine); newRow >= 0 {
			target := newRow + 1 + v.chunk
			if target > v.loaded {
				v.loaded = minInt(target, len(v.viewLines))
			}
		}
	}
	lines := v.viewLines
	loaded := v.loaded
	offset := v.scroll.Offset
	v.mu.Unlock()

	v.setGrid(lines[:loaded])
	fyne.Do(func() {
		if v.scroll != nil {
			v.scroll.ScrollToOffset(offset)
		}
	})
}

func findViewRow(lineMap []int, srcLine int) int {
	for i, v := range lineMap {
		if v == srcLine {
			return i
		}
	}
	return -1
}

func buildFoldRangesWithDepth(lines []string) (map[int]int, map[int]int) {
	ranges := map[int]int{}
	depths := map[int]int{}
	type entry struct {
		line  int
		brace rune
		depth int
	}
	stack := make([]entry, 0, 32)
	depth := 0

	for i, line := range lines {
		inString := false
		esc := false
		for _, r := range line {
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
			switch r {
			case '{', '[':
				stack = append(stack, entry{line: i, brace: r, depth: depth})
				depth++
			case '}', ']':
				if len(stack) == 0 {
					continue
				}
				depth--
				open := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if open.line < i {
					ranges[open.line] = i
					depths[open.line] = open.depth
				}
			}
		}
	}
	return ranges, depths
}

func buildFoldRanges(lines []string) map[int]int {
	ranges, _ := buildFoldRangesWithDepth(lines)
	return ranges
}

func buildFoldPlaceholder(line string) string {
	idx, brace := findFoldToken(line)
	if idx == -1 {
		return line
	}
	prefix := line[:idx]
	if brace == '[' {
		return prefix + "[ ... ]"
	}
	return prefix + "{ ... }"
}

func findFoldToken(line string) (int, rune) {
	inString := false
	esc := false
	for i, r := range line {
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
		if r == '{' || r == '[' {
			return i, r
		}
	}
	return -1, 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
