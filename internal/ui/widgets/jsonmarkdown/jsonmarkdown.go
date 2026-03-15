//go:build darwin || linux || windows

package jsonmarkdown

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	searchKeyPrompt   = "Select key"
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

	searchEntry *escEntry
	searchUp    *widget.Button
	searchDown  *widget.Button
	searchWrap  *fyne.Container
	searchWidth float32
	searchQuery string
	matchLines  []int
	matchIndex  int
	highlights  map[int][]highlightRange

	searchKeySelect *widget.Select
	searchKeyWidth  float32
	searchKey       string
	searchKeys      []string
	searchKeyIndex  map[string][]int
	searchLower     []string
	searchAll       []int
	searchKeyRanges map[string]keyRange
	searchKeyFold   map[string]int
	searchSeq       uint64
	searchMatchSet  map[int]struct{}
	lineNumWidth    int
	debounceMu      sync.Mutex
	debounceTimer   *time.Timer
	debounceQuery   string
}

// highlightRange represents a range of text to be highlighted.
type highlightRange struct {
	start int
	end   int
}

type keyRange struct {
	start int
	end   int
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

	v.searchEntry = newEscEntry()
	v.searchEntry.SetPlaceHolder("Search output")
	v.searchEntry.OnChanged = v.onSearchChanged
	v.searchEntry.OnSubmitted = func(_ string) {
		v.navigateMatch(1)
	}
	v.searchEntry.SetOnEsc(func() {
		if v.SearchVisible() {
			v.SetSearchVisible(false)
		}
	})

	v.searchKeySelect = widget.NewSelect([]string{searchKeyPrompt}, func(s string) {
		v.mu.Lock()
		if s == searchKeyPrompt {
			v.searchKey = ""
		} else {
			v.searchKey = s
		}
		key := v.searchKey
		v.mu.Unlock()
		v.applyKeyFilter(key)
		v.applySearchAsync(v.searchEntry.Text)
	})
	v.searchKeySelect.SetSelected(searchKeyPrompt)
	v.searchKeyWidth = 180

	v.searchUp = widget.NewButton("▲", func() {
		v.navigateMatch(-1)
	})
	v.searchDown = widget.NewButton("▼", func() {
		v.navigateMatch(1)
	})
	v.searchUp.Disable()
	v.searchDown.Disable()

	v.searchWrap = container.NewGridWrap(
		fyne.NewSize(420, v.searchEntry.MinSize().Height),
		container.NewHBox(v.searchKeySelect, v.searchEntry, v.searchUp, v.searchDown),
	)
	v.searchWidth = 420
	v.searchWrap.Hide()

	return v
}

// View returns the scrollable markdown view.
func (v *JSONMarkdownView) View() fyne.CanvasObject {
	return v.scroll
}

// SearchBar returns the search UI container.
func (v *JSONMarkdownView) SearchBar() fyne.CanvasObject {
	return v.searchWrap
}

// SearchEntry exposes the search input for focus management.
func (v *JSONMarkdownView) SearchEntry() *widget.Entry {
	return &v.searchEntry.Entry
}

// SetSearchWidth sets a fixed width for the search input wrapper.
func (v *JSONMarkdownView) SetSearchWidth(w float32) {
	if w <= 0 {
		return
	}
	v.searchWidth = w
	btnW := v.searchUp.MinSize().Width + v.searchDown.MinSize().Width + theme.Padding()*2
	avail := w - btnW
	keyW := v.searchKeyWidth
	entryW := avail - keyW
	minEntryW := v.searchEntry.MinSize().Width
	if entryW < minEntryW {
		entryW = minEntryW
		keyW = avail - entryW
		if keyW < 80 {
			keyW = 80
		}
	}

	keyWrap := container.NewGridWrap(
		fyne.NewSize(keyW, v.searchEntry.MinSize().Height),
		v.searchKeySelect,
	)
	entryWrap := container.NewGridWrap(
		fyne.NewSize(entryW, v.searchEntry.MinSize().Height),
		v.searchEntry,
	)
	row := container.NewHBox(keyWrap, entryWrap, v.searchUp, v.searchDown)
	row.Resize(fyne.NewSize(w, v.searchEntry.MinSize().Height))
	row.Refresh()

	v.searchWrap.Objects = []fyne.CanvasObject{row}
	v.searchWrap.Resize(fyne.NewSize(w, v.searchEntry.MinSize().Height))
	v.searchWrap.Refresh()
}

// SetSearchVisible shows or hides the search bar and clears query when hidden.
func (v *JSONMarkdownView) SetSearchVisible(show bool) {
	if show {
		v.SetSearchWidth(v.searchWidth)
		v.searchWrap.Show()
		v.searchWrap.Refresh()
		return
	}
	v.searchEntry.SetText("")
	v.applySearchAsync("")
	v.searchWrap.Hide()
	v.searchWrap.Refresh()
}

// SearchVisible reports whether the search bar is visible.
func (v *JSONMarkdownView) SearchVisible() bool {
	return v.searchWrap.Visible()
}

// SetJSON resets content and loads the first chunk.
func (v *JSONMarkdownView) SetJSON(s string) {
	v.mu.Lock()
	v.loaded = 0
	v.loading = false
	v.folded = map[int]bool{}
	v.matchLines = nil
	v.matchIndex = -1
	v.highlights = nil
	v.searchQuery = ""
	v.searchKey = ""
	v.searchKeys = nil
	v.searchKeyIndex = nil
	v.searchLower = nil
	v.searchAll = nil
	v.searchKeyRanges = nil
	v.searchKeyFold = nil
	v.searchMatchSet = nil
	v.lineNumWidth = 0
	v.mu.Unlock()
	v.SetSearchVisible(false)

	if strings.TrimSpace(s) == "" {
		v.setGrid(nil, nil)
		v.setSearchKeys(nil)
		return
	}

	var parsed any
	pretty := s
	if json.Valid([]byte(s)) {
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			if b, err := json.MarshalIndent(parsed, "", "  "); err == nil {
				pretty = string(b)
			}
		} else {
			parsed = nil
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
	topKeys := collectTopLevelKeys(parsed)
	index, lower, keyRanges, allLines, keyFold := buildSearchIndex(lines, foldRanges)
	lineNumWidth := len(strconv.Itoa(len(lines)))

	v.mu.Lock()
	v.fullLines = lines
	v.foldRanges = foldRanges
	v.folded = make(map[int]bool, len(foldRanges))
	for start := range foldRanges {
		if foldDepths[start] > 0 {
			v.folded[start] = true
		}
	}
	v.searchKeyIndex = index
	v.searchLower = lower
	v.searchAll = allLines
	v.searchKeyRanges = keyRanges
	v.searchKeyFold = keyFold
	v.lineNumWidth = lineNumWidth
	v.rebuildViewLinesLocked()
	v.mu.Unlock()

	v.setSearchKeys(topKeys)
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
		v.setGrid(nil, nil)
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
	chunkMap := make([]int, end)
	copy(chunkMap, v.lineMap[:end])
	v.loaded = end
	v.mu.Unlock()

	v.setGrid(chunkLines, chunkMap)
}

func (v *JSONMarkdownView) setGrid(lines []string, lineMap []int) {
	fyne.Do(func() {
		if v.tgrid == nil {
			return
		}
		v.mu.Lock()
		query := strings.TrimSpace(v.searchQuery)
		matchSet := v.searchMatchSet
		lineNumWidth := v.lineNumWidth
		v.mu.Unlock()
		highlights := v.highlights
		if query != "" && matchSet != nil {
			highlights = buildVisibleHighlights(lines, lineMap, query, matchSet)
		}
		v.tgrid.Rows = buildTextGridRows(lines, lineMap, highlights, lineNumWidth)
		v.tgrid.Refresh()
		v.scroll.Refresh()
	})
}

func (v *JSONMarkdownView) applyKeyFilter(key string) {
	v.mu.Lock()
	if key == "" {
		v.rebuildViewLinesLocked()
	} else {
		if v.searchKeyFold != nil {
			if fs, ok := v.searchKeyFold[key]; ok {
				v.folded[fs] = false
			}
		}
		v.rebuildViewLinesForKeyLocked(key)
	}
	v.loaded = minInt(v.chunk, len(v.viewLines))
	lines := v.viewLines
	lineMap := v.lineMap
	loaded := v.loaded
	v.mu.Unlock()

	if loaded > 0 {
		v.setGrid(lines[:loaded], lineMap[:loaded])
	} else {
		v.setGrid(nil, nil)
	}
}

func (v *JSONMarkdownView) rebuildViewLinesForKeyLocked(key string) {
	v.viewLines = v.viewLines[:0]
	v.lineMap = v.lineMap[:0]
	if key == "" || len(v.fullLines) == 0 || v.searchKeyRanges == nil {
		return
	}
	rng, ok := v.searchKeyRanges[key]
	if !ok {
		return
	}
	foldStart := -1
	if v.searchKeyFold != nil {
		if fs, ok := v.searchKeyFold[key]; ok {
			foldStart = fs
		}
	}
	start := rng.start
	end := rng.end
	if start < 0 {
		start = 0
	}
	if end >= len(v.fullLines) {
		end = len(v.fullLines) - 1
	}
	for i := start; i <= end; {
		if i < 0 || i >= len(v.fullLines) {
			break
		}
		if i == start && foldStart == i+1 {
			if foldEnd, ok := v.foldRanges[foldStart]; ok && foldEnd > foldStart && v.folded[foldStart] {
				clampedEnd := foldEnd
				if clampedEnd > end {
					clampedEnd = end
				}
				// Keep the key line visible and fold the brace line below it.
				v.viewLines = append(v.viewLines, v.fullLines[i])
				v.lineMap = append(v.lineMap, i)
				v.viewLines = append(v.viewLines, buildFoldPlaceholder(v.fullLines[foldStart]))
				v.lineMap = append(v.lineMap, foldStart)
				i = clampedEnd + 1
				continue
			}
		}
		if foldEnd, ok := v.foldRanges[i]; ok && foldEnd > i && v.folded[i] {
			clampedEnd := foldEnd
			if clampedEnd > end {
				clampedEnd = end
			}
			v.viewLines = append(v.viewLines, buildFoldPlaceholder(v.fullLines[i]))
			v.lineMap = append(v.lineMap, i)
			i = clampedEnd + 1
			continue
		}
		v.viewLines = append(v.viewLines, v.fullLines[i])
		v.lineMap = append(v.lineMap, i)
		i++
	}
}

func (v *JSONMarkdownView) setSearchKeys(keys []string) {
	if keys == nil {
		keys = nil
	}
	opts := make([]string, 0, len(keys)+1)
	opts = append(opts, searchKeyPrompt)
	opts = append(opts, keys...)
	fyne.Do(func() {
		if v.searchKeySelect == nil {
			return
		}
		v.searchKeySelect.Options = opts
		v.searchKeySelect.SetSelected(searchKeyPrompt)
		v.searchKeySelect.Refresh()
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

func buildTextGridRows(lines []string, lineMap []int, highlights map[int][]highlightRange, lineNumWidth int) []widget.TextGridRow {
	if len(lines) == 0 {
		return nil
	}
	rows := make([]widget.TextGridRow, 0, len(lines))
	for i, line := range lines {
		var hl []highlightRange
		var prefix string
		if lineMap != nil && i < len(lineMap) {
			lineNum := lineMap[i] + 1
			prefix = fmt.Sprintf("%*d  ", lineNumWidth, lineNum)
			if highlights != nil {
				hl = highlights[lineMap[i]]
			}
		}
		cells := buildTextGridCells(prefix+line, hl, len(prefix))
		rows = append(rows, widget.TextGridRow{Cells: cells})
	}
	return rows
}

func buildTextGridCells(line string, highlights []highlightRange, prefixLen int) []widget.TextGridCell {
	if line == "" {
		return nil
	}
	cells := make([]widget.TextGridCell, 0, len(line))
	pending := ""
	pendingColor := theme.ForegroundColor()
	rangeIndex := 0
	pos := 0

	inHighlight := func(i int) bool {
		if i < prefixLen {
			return false
		}
		adj := i - prefixLen
		for rangeIndex < len(highlights) && adj >= highlights[rangeIndex].end {
			rangeIndex++
		}
		if rangeIndex >= len(highlights) {
			return false
		}
		return adj >= highlights[rangeIndex].start && adj < highlights[rangeIndex].end
	}
	flush := func() {
		if pending == "" {
			return
		}
		for _, r := range pending {
			style := &widget.CustomTextGridStyle{FGColor: pendingColor}
			if inHighlight(pos) {
				style.BGColor = highlightColor()
			}
			if pos < prefixLen {
				style.BGColor = lineNumberBgColor()
			}
			cells = append(cells, widget.TextGridCell{Rune: r, Style: style})
			pos++
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

		if i < prefixLen {
			j := i + 1
			for j < len(runes) && j < prefixLen {
				j++
			}
			setPending(string(runes[i:j]), theme.ForegroundColor())
			i = j
			continue
		}

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
	if v.searchKey != "" {
		v.rebuildViewLinesForKeyLocked(v.searchKey)
	} else {
		v.rebuildViewLinesLocked()
	}
	if v.loaded > len(v.viewLines) {
		v.loaded = len(v.viewLines)
	}
	if v.loaded == 0 && len(v.viewLines) > 0 {
		v.loaded = minInt(v.chunk, len(v.viewLines))
	}
	if !v.folded[srcLine] {
		if foldEnd, ok := v.foldRanges[srcLine]; ok && foldEnd > srcLine {
			if endRow := findViewRow(v.lineMap, foldEnd); endRow >= 0 {
				v.ensureLoadedForRowLocked(endRow)
			}
		}
	}
	lines := v.viewLines
	lineMap := v.lineMap
	loaded := v.loaded
	offset := v.scroll.Offset
	v.mu.Unlock()

	v.setGrid(lines[:loaded], lineMap[:loaded])
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

func highlightColor() color.Color {
	bg := theme.BackgroundColor()
	if isDarkColor(bg) {
		return color.NRGBA{R: 0xFF, G: 0xB3, B: 0x4D, A: 0x7F}
	}
	return color.NRGBA{R: 0xFF, G: 0xE0, B: 0x59, A: 0x99}
}

func lineNumberBgColor() color.Color {
	bg := theme.BackgroundColor()
	if isDarkColor(bg) {
		return color.NRGBA{R: 0x24, G: 0x24, B: 0x24, A: 0xFF}
	}
	return color.NRGBA{R: 0xF1, G: 0xF1, B: 0xF1, A: 0xFF}
}

func isDarkColor(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	rl := float64(r) / 65535.0
	gl := float64(g) / 65535.0
	bl := float64(b) / 65535.0
	lum := 0.2126*rl + 0.7152*gl + 0.0722*bl
	return lum < 0.5
}

// --- end JSON color palette

func (v *JSONMarkdownView) onSearchChanged(s string) {
	v.debounceMu.Lock()
	v.debounceQuery = s
	if v.debounceTimer == nil {
		v.debounceTimer = time.AfterFunc(300*time.Millisecond, v.fireSearchDebounce)
		v.debounceMu.Unlock()
		return
	}
	if !v.debounceTimer.Stop() {
		select {
		case <-v.debounceTimer.C:
		default:
		}
	}
	v.debounceTimer.Reset(300 * time.Millisecond)
	v.debounceMu.Unlock()
}

func (v *JSONMarkdownView) fireSearchDebounce() {
	v.debounceMu.Lock()
	q := v.debounceQuery
	v.debounceMu.Unlock()
	v.applySearchAsync(q)
}

func (v *JSONMarkdownView) applySearchAsync(q string) {
	query := strings.TrimSpace(q)
	lower := strings.ToLower(query)
	seq := atomic.AddUint64(&v.searchSeq, 1)

	v.mu.Lock()
	key := v.searchKey
	index := v.searchKeyIndex
	allLines := v.searchAll
	keyRanges := v.searchKeyRanges
	lowerLines := v.searchLower
	fullLines := v.fullLines
	if key != "" && keyRanges != nil {
		if rng, ok := keyRanges[key]; ok {
			if len(v.lineMap) == 0 || v.lineMap[0] < rng.start || v.lineMap[len(v.lineMap)-1] > rng.end {
				v.rebuildViewLinesForKeyLocked(key)
			}
		}
	}
	v.mu.Unlock()

	if lower == "" || len(fullLines) == 0 {
		fyne.Do(func() {
			if seq != atomic.LoadUint64(&v.searchSeq) {
				return
			}
			v.mu.Lock()
			v.searchQuery = query
			v.highlights = nil
			v.matchLines = nil
			v.searchMatchSet = nil
			v.matchIndex = -1
			lines := v.viewLines
			lineMap := v.lineMap
			loaded := v.loaded
			v.mu.Unlock()
			v.updateNavButtons()
			if loaded > 0 {
				v.setGrid(lines[:loaded], lineMap[:loaded])
			} else {
				v.setGrid(nil, nil)
			}
		})
		return
	}

	var candidates []int
	if key == "" {
		candidates = allLines
	} else {
		candidates = index[key]
	}

	go func(seq uint64, lower string, candidates []int, lowerLines, fullLines []string) {
		matchLines := make([]int, 0)
		matchSet := make(map[int]struct{})

		for _, i := range candidates {
			if i < 0 || i >= len(fullLines) || i >= len(lowerLines) {
				continue
			}
			if !strings.Contains(lowerLines[i], lower) {
				continue
			}
			matchSet[i] = struct{}{}
			matchLines = append(matchLines, i)
		}

		fyne.Do(func() {
			if seq != atomic.LoadUint64(&v.searchSeq) {
				return
			}
			v.mu.Lock()
			v.searchQuery = query
			v.matchLines = matchLines
			v.searchMatchSet = matchSet
			if len(matchLines) == 0 {
				v.matchIndex = -1
			} else if v.matchIndex < 0 || v.matchIndex >= len(matchLines) {
				v.matchIndex = 0
			}
			// Keep initial render cheap; load more on scroll.
			v.loaded = minInt(v.chunk, len(v.viewLines))
			lines := v.viewLines
			lineMap := v.lineMap
			loaded := v.loaded
			v.mu.Unlock()

			v.updateNavButtons()
			if loaded > 0 {
				v.setGrid(lines[:loaded], lineMap[:loaded])
			} else {
				v.setGrid(nil, nil)
			}
		})
	}(seq, lower, candidates, lowerLines, fullLines)
}

func (v *JSONMarkdownView) applySearch(q string) {
	v.applySearchAsync(q)
}

func (v *JSONMarkdownView) expandMatchesLocked() {
	if len(v.matchLines) == 0 {
		return
	}
	changed := false
	for _, line := range v.matchLines {
		if v.expandForLineLocked(line) {
			changed = true
		}
	}
	if changed {
		v.rebuildViewLinesLocked()
	}
}

func (v *JSONMarkdownView) expandForLineLocked(line int) bool {
	changed := false
	for {
		opened := false
		for start, end := range v.foldRanges {
			if start < line && line <= end && v.folded[start] {
				v.folded[start] = false
				opened = true
				changed = true
			}
		}
		if !opened {
			break
		}
	}
	return changed
}

func (v *JSONMarkdownView) ensureLoadedForRowLocked(row int) {
	if row < 0 {
		return
	}
	target := row + 1 + v.chunk
	if target > v.loaded {
		v.loaded = minInt(target, len(v.viewLines))
	}
}

func (v *JSONMarkdownView) navigateMatch(step int) {
	v.mu.Lock()
	if len(v.matchLines) == 0 {
		v.mu.Unlock()
		return
	}
	v.matchIndex += step
	if v.matchIndex < 0 {
		v.matchIndex = len(v.matchLines) - 1
	} else if v.matchIndex >= len(v.matchLines) {
		v.matchIndex = 0
	}
	line := v.matchLines[v.matchIndex]
	if v.expandForLineLocked(line) {
		v.rebuildViewLinesLocked()
	}
	if v.searchKey != "" {
		if rng, ok := v.searchKeyRanges[v.searchKey]; ok {
			if len(v.lineMap) == 0 || v.lineMap[0] < rng.start || v.lineMap[len(v.lineMap)-1] > rng.end {
				v.rebuildViewLinesForKeyLocked(v.searchKey)
			}
		}
	}
	row := findViewRow(v.lineMap, line)
	v.ensureLoadedForRowLocked(row)
	lines := v.viewLines
	lineMap := v.lineMap
	loaded := v.loaded
	v.mu.Unlock()

	v.setGrid(lines[:loaded], lineMap[:loaded])
	v.scrollToRow(row)
}

func (v *JSONMarkdownView) updateNavButtons() {
	if v.searchUp == nil || v.searchDown == nil {
		return
	}
	if len(v.matchLines) == 0 {
		v.searchUp.Disable()
		v.searchDown.Disable()
		return
	}
	v.searchUp.Enable()
	v.searchDown.Enable()
}

func (v *JSONMarkdownView) scrollToRow(row int) {
	if v.scroll == nil || v.tgrid == nil || row < 0 {
		return
	}
	rows := len(v.tgrid.Rows)
	if rows == 0 {
		return
	}
	rowH := v.tgrid.MinSize().Height / float32(rows)
	v.scroll.ScrollToOffset(fyne.NewPos(0, rowH*float32(row)))
}

// escEntry provides a small helper to close the search on Escape.
type escEntry struct {
	widget.Entry
	onEsc func()
}

func newEscEntry() *escEntry {
	e := &escEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *escEntry) SetOnEsc(fn func()) {
	e.onEsc = fn
}

func (e *escEntry) TypedKey(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyEscape {
		if e.onEsc != nil {
			e.onEsc()
			return
		}
	}
	e.Entry.TypedKey(ev)
}

func extractLineKey(line string) (string, bool) {
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
				key := string(runes[start:i])
				j := i + 1
				for j < len(runes) && unicode.IsSpace(runes[j]) {
					j++
				}
				if j < len(runes) && runes[j] == ':' {
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
		if r == '"' {
			inString = true
			start = i + 1
		}
	}
	return "", false
}

func collectJSONKeys(v any) []string {
	if v == nil {
		return nil
	}
	set := make(map[string]struct{})
	collectKeysRecursive(v, set)
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func collectKeysRecursive(v any, set map[string]struct{}) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			set[k] = struct{}{}
			collectKeysRecursive(child, set)
		}
	case []any:
		for _, child := range t {
			collectKeysRecursive(child, set)
		}
	}
}

func collectTopLevelKeys(v any) []string {
	root, ok := v.(map[string]any)
	if !ok || len(root) == 0 {
		return nil
	}
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildSearchIndex(lines []string, foldRanges map[int]int) (map[string][]int, []string, map[string]keyRange, []int, map[string]int) {
	index := make(map[string][]int)
	lower := make([]string, len(lines))
	keyStarts := make(map[string]int)
	keyOrder := make([]int, 0)
	keyRanges := make(map[string]keyRange)
	keyFold := make(map[string]int)
	allLines := make([]int, 0, len(lines))

	for i, line := range lines {
		lower[i] = strings.ToLower(line)
		allLines = append(allLines, i)
		if lineIndentDepth(line) != 1 {
			continue
		}
		if key, ok := extractLineKey(line); ok {
			if _, exists := keyStarts[key]; !exists {
				keyStarts[key] = i
				keyOrder = append(keyOrder, i)
			}
		}
	}
	sort.Ints(keyOrder)

	for idx, start := range keyOrder {
		key := ""
		for k, v := range keyStarts {
			if v == start {
				key = k
				break
			}
		}
		if key == "" {
			continue
		}
		end := len(lines) - 1
		if idx+1 < len(keyOrder) {
			end = keyOrder[idx+1] - 1
		}
		if end < start {
			end = start
		}
		foldStart := -1
		if _, ok := foldRanges[start]; ok {
			foldStart = start
		} else if start+1 <= end {
			if _, ok := foldRanges[start+1]; ok {
				foldStart = start + 1
			}
		}
		if foldStart != -1 {
			if foldEnd, ok := foldRanges[foldStart]; ok && foldEnd > end {
				end = foldEnd
			}
			keyFold[key] = foldStart
		}
		keyRanges[key] = keyRange{start: start, end: end}
		for i := start; i <= end && i < len(lines); i++ {
			index[key] = append(index[key], i)
		}
	}
	return index, lower, keyRanges, allLines, keyFold
}

func lineIndentDepth(line string) int {
	count := 0
	for _, r := range line {
		if r != ' ' {
			break
		}
		count++
	}
	return count / 2
}

func buildVisibleHighlights(lines []string, lineMap []int, query string, matchSet map[int]struct{}) map[int][]highlightRange {
	if query == "" || len(lines) == 0 || len(lineMap) == 0 || matchSet == nil {
		return nil
	}
	lower := strings.ToLower(query)
	highlights := make(map[int][]highlightRange)
	for i := 0; i < len(lines) && i < len(lineMap); i++ {
		srcLine := lineMap[i]
		if _, ok := matchSet[srcLine]; !ok {
			continue
		}
		ranges := findHighlightRanges(lines[i], lower)
		if len(ranges) == 0 {
			continue
		}
		highlights[srcLine] = ranges
	}
	return highlights
}

func findHighlightRanges(line string, query string) []highlightRange {
	if query == "" || line == "" {
		return nil
	}
	lineRunes := []rune(strings.ToLower(line))
	queryRunes := []rune(strings.ToLower(query))
	if len(queryRunes) == 0 || len(queryRunes) > len(lineRunes) {
		return nil
	}
	var ranges []highlightRange
	for i := 0; i+len(queryRunes) <= len(lineRunes); i++ {
		match := true
		for j := range queryRunes {
			if lineRunes[i+j] != queryRunes[j] {
				match = false
				break
			}
		}
		if match {
			ranges = append(ranges, highlightRange{start: i, end: i + len(queryRunes)})
			i += len(queryRunes) - 1
		}
	}
	return ranges
}
