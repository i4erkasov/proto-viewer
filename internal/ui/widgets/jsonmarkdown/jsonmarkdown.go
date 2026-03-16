//go:build darwin || linux || windows

package jsonmarkdown

import (
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
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/bytedance/sonic"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/searchselect"
)

const (
	defaultChunkLines = 200
	scrollThreshold   = 12
	searchKeyPrompt   = "Select key"
)

// JSONMarkdownView renders JSON as markdown with lazy line loading.
type JSONMarkdownView struct {
	mu        sync.Mutex
	viewLines []int
	loaded    int
	chunk     int
	loading   bool

	foldRanges map[int]int
	folded     map[int]bool

	tgrid   *widget.TextGrid
	overlay *tapOverlay
	scroll  *container.Scroll
	win     fyne.Window

	fullBuf *JSONBuffer

	searchEntry      *escEntry
	searchUp         *widget.Button
	searchDown       *widget.Button
	searchUpWrap     *fyne.Container
	searchDownWrap   *fyne.Container
	searchNavWrap    *fyne.Container
	searchEntryWrap  *fyne.Container
	searchWrap       *fyne.Container
	searchStructChk  *widget.Check
	searchStructWrap *fyne.Container
	searchWidth      float32
	searchQuery      string
	matchLines       []int
	matchIndex       int
	highlights       map[int][]highlightRange
	searchStructural bool

	selectedKeyLine    int
	selectedKeyRange   highlightRange
	selectedKeyValue   string
	selectedValueLine  int
	selectedValueRange highlightRange
	selectedValueText  string

	searchKeySelect  *searchselect.SearchableSelect
	searchKeyWidth   float32
	searchKeys       []string
	searchKeyIndex   map[string][]int
	searchAll        []int
	searchKeyRanges  map[string]keyRange
	searchKeyFold    map[string]int
	searchSeq        uint64
	searchMatchSet   map[int]struct{}
	trigramIndex     map[[3]byte][]int32
	trigramEnabled   bool
	trigramCapBytes  int
	trigramUsedBytes int
	lineNumWidth     int
	debounceMu       sync.Mutex
	debounceTimer    *time.Timer
	debounceQuery    string
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
func NewJSONMarkdownView(win fyne.Window) *JSONMarkdownView {
	v := &JSONMarkdownView{chunk: defaultChunkLines}
	v.selectedKeyLine = -1
	v.selectedValueLine = -1
	v.win = win
	v.tgrid = widget.NewTextGrid()
	v.overlay = newTapOverlay(v.handleTap, v.handleSecondaryTap)
	// styles applied per cell
	padTop := canvas.NewRectangle(color.Transparent)
	padTop.SetMinSize(fyne.NewSize(1, theme.Padding()))
	content := container.NewBorder(padTop, nil, nil, nil, container.NewMax(v.tgrid, v.overlay))
	v.scroll = container.NewScroll(content)
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

	v.searchKeySelect = searchselect.NewSearchableSelect(win, searchKeyPrompt, nil, true)
	v.searchKeySelect.SetTextStyle(fyne.TextStyle{})
	v.searchKeySelect.SetMinWidth(200)
	v.searchKeySelect.OnChanged = func(keys []string) {
		selected := normalizeKeys(keys)
		v.mu.Lock()
		v.searchKeys = selected
		v.mu.Unlock()
		v.applyKeyFilterKeys(selected)
		v.applySearchAsync(v.searchEntry.Text)
	}
	v.searchKeySelect.SetSelectedValues(nil)
	v.searchKeyWidth = 200

	v.searchStructChk = widget.NewCheck("Only matches", func(checked bool) {
		v.SetSearchStructural(checked)
	})
	v.searchStructChk.SetChecked(false)
	v.searchStructural = false
	v.searchStructWrap = container.NewGridWrap(v.searchStructChk.MinSize(), v.searchStructChk)

	v.searchUp = widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
		v.navigateMatch(-1)
	})
	v.searchDown = widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
		v.navigateMatch(1)
	})
	v.searchUp.Importance = widget.LowImportance
	v.searchDown.Importance = widget.LowImportance
	btnH := v.searchEntry.MinSize().Height
	btnW := btnH
	if ms := v.searchUp.MinSize(); ms.Width > btnW {
		btnW = ms.Width
	}
	if ms := v.searchDown.MinSize(); ms.Width > btnW {
		btnW = ms.Width
	}
	v.searchUpWrap = container.NewGridWrap(fyne.NewSize(btnW, btnH), v.searchUp)
	v.searchDownWrap = container.NewGridWrap(fyne.NewSize(btnW, btnH), v.searchDown)
	v.searchUp.Disable()
	v.searchDown.Disable()

	navRow := container.NewGridWithColumns(2, v.searchUpWrap, v.searchDownWrap)
	v.searchNavWrap = container.NewGridWrap(fyne.NewSize(btnW*2, btnH), navRow)

	entryH := v.searchEntry.MinSize().Height
	entryPad := container.NewGridWrap(fyne.NewSize(btnW*2, entryH), layout.NewSpacer())
	entryLayer := container.NewBorder(nil, nil, nil, entryPad, v.searchEntry)
	v.searchEntryWrap = container.NewStack(entryLayer, container.NewBorder(nil, nil, nil, v.searchNavWrap, layout.NewSpacer()))

	v.searchWrap = container.NewGridWrap(
		fyne.NewSize(500, v.searchEntry.MinSize().Height),
		container.NewHBox(layout.NewSpacer(), v.searchKeySelect, v.searchEntryWrap),
	)
	v.searchWidth = 500
	v.searchWrap.Hide()
	v.SetSearchWidth(v.searchWidth)

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

// SetSearchStructural toggles structural search mode (show only matched branches).
func (v *JSONMarkdownView) SetSearchStructural(enabled bool) {
	v.mu.Lock()
	v.searchStructural = enabled
	query := v.searchQuery
	keys := append([]string(nil), v.searchKeys...)
	v.mu.Unlock()

	if enabled && strings.TrimSpace(query) != "" {
		v.applySearchAsync(query)
		return
	}
	v.applyKeyFilterKeys(keys)
}

// SetSearchWidth sets a fixed width for the search input wrapper.
func (v *JSONMarkdownView) SetSearchWidth(w float32) {
	if w <= 0 {
		return
	}
	v.searchWidth = w
	btnW := theme.Padding() * 2
	if v.searchNavWrap != nil {
		btnW += v.searchNavWrap.MinSize().Width
	}
	avail := w
	keyW := v.searchKeyWidth
	if v.searchKeySelect != nil {
		if ms := v.searchKeySelect.MinSize(); ms.Width > 0 {
			keyW = ms.Width
		}
	}
	entryW := avail - keyW
	minEntryW := v.searchEntry.MinSize().Width
	if entryW < minEntryW {
		entryW = minEntryW
		keyW = avail - entryW
		if keyW < 80 {
			keyW = 80
		}
	}
	if entryW < 0 {
		entryW = minEntryW
	}

	keyWrap := container.NewGridWrap(
		fyne.NewSize(keyW, v.searchEntry.MinSize().Height),
		v.searchKeySelect,
	)

	entryH := v.searchEntry.MinSize().Height
	padW := float32(0)
	if v.searchNavWrap != nil {
		padW = v.searchNavWrap.MinSize().Width
	}
	entryPad := container.NewGridWrap(fyne.NewSize(padW, entryH), layout.NewSpacer())
	entryLayer := container.NewBorder(nil, nil, nil, entryPad, v.searchEntry)
	entryStack := container.NewStack(entryLayer, container.NewBorder(nil, nil, nil, v.searchNavWrap, layout.NewSpacer()))
	entryWrap := container.NewGridWrap(fyne.NewSize(entryW, entryH), entryStack)

	searchRow := container.NewHBox(layout.NewSpacer(), keyWrap, entryWrap)
	checkRow := container.NewHBox(layout.NewSpacer(), v.searchStructWrap)
	rowH := entryH
	checkH := v.searchStructWrap.MinSize().Height
	if checkH < rowH {
		checkH = rowH
	}
	searchRow.Resize(fyne.NewSize(w, rowH))
	checkRow.Resize(fyne.NewSize(w, checkH))

	v.searchWrap.Objects = []fyne.CanvasObject{searchRow, checkRow}
	v.searchWrap.Resize(fyne.NewSize(w, rowH+checkH))
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
	v.mu.Lock()
	v.searchKeys = nil
	v.mu.Unlock()
	v.searchKeySelect.SetSelectedValues(nil)
	v.applyKeyFilterKeys(nil)
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
	v.searchKeys = nil
	v.searchKeyIndex = nil
	v.searchAll = nil
	v.searchKeyRanges = nil
	v.searchKeyFold = nil
	v.searchMatchSet = nil
	v.trigramIndex = nil
	v.trigramEnabled = false
	v.trigramCapBytes = 0
	v.trigramUsedBytes = 0
	v.lineNumWidth = 0
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.mu.Unlock()
	v.SetSearchVisible(false)

	if strings.TrimSpace(s) == "" {
		v.setGrid(nil)
		v.setSearchKeys(nil)
		return
	}

	var parsed any
	pretty := s
	data := []byte(s)
	if sonic.Valid(data) {
		if err := sonic.Unmarshal(data, &parsed); err == nil {
			if b, err := sonic.MarshalIndent(parsed, "", "  "); err == nil {
				pretty = string(b)
			}
		}
		if !strings.Contains(pretty, "\n") && parsed != nil {
			if b, err := sonic.MarshalIndent(parsed, "", "  "); err == nil {
				pretty = string(b)
			}
		}
	}

	buf := newJSONBuffer(pretty)
	foldRanges, foldDepths := buildFoldRangesWithDepthBuffer(buf)
	topKeys := collectTopLevelKeys(parsed)
	index, keyRanges, allLines, keyFold := buildSearchIndexBuffer(buf, foldRanges)
	lineCount := buf.LineCount()
	lineNumWidth := len(strconv.Itoa(lineCount))

	v.mu.Lock()
	v.fullBuf = buf
	v.foldRanges = foldRanges
	v.folded = make(map[int]bool, len(foldRanges))
	for start := range foldRanges {
		if foldDepths[start] > 0 {
			v.folded[start] = true
		}
	}
	v.searchKeyIndex = index
	v.searchAll = allLines
	v.searchKeyRanges = keyRanges
	v.searchKeyFold = keyFold
	v.lineNumWidth = lineNumWidth
	v.buildTrigramIndexLocked(buf, len(pretty))
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
	chunkLines := make([]int, end)
	copy(chunkLines, v.viewLines[:end])
	v.loaded = end
	v.mu.Unlock()

	v.setGrid(chunkLines)
}

func (v *JSONMarkdownView) setGrid(viewLines []int) {
	fyne.Do(func() {
		if v.tgrid == nil {
			return
		}
		v.mu.Lock()
		query := strings.TrimSpace(v.searchQuery)
		matchSet := v.searchMatchSet
		lineNumWidth := v.lineNumWidth
		selectedLine := v.selectedKeyLine
		selectedRange := v.selectedKeyRange
		if v.selectedValueLine >= 0 {
			selectedLine = v.selectedValueLine
			selectedRange = v.selectedValueRange
		}
		buf := v.fullBuf
		v.mu.Unlock()

		lineBytes, srcLines, placeholders := buildViewLineBytes(buf, viewLines)
		var highlights map[int][]highlightRange
		if query != "" && matchSet != nil {
			queryLower := asciiLowerBytes([]byte(query))
			if len(queryLower) > 0 {
				highlights = buildVisibleHighlightsBytes(lineBytes, srcLines, placeholders, queryLower, matchSet)
			}
		}
		v.tgrid.Rows = buildTextGridRows(lineBytes, srcLines, highlights, lineNumWidth, selectedLine, selectedRange)
		v.tgrid.Refresh()
		v.scroll.Refresh()
	})
}

func (v *JSONMarkdownView) applyKeyFilter(key string) {
	v.applyKeyFilterKeys(normalizeKeys([]string{key}))
}

func (v *JSONMarkdownView) applyKeyFilterKeys(keys []string) {
	v.mu.Lock()
	if len(keys) == 0 {
		v.rebuildViewLinesLocked()
	} else {
		for _, key := range keys {
			if v.searchKeyFold != nil {
				if fs, ok := v.searchKeyFold[key]; ok {
					v.folded[fs] = false
				}
			}
			v.rebuildViewLinesForKeysLocked(keys)
		}
	}
	v.loaded = minInt(v.chunk, len(v.viewLines))
	lines := v.viewLines
	loaded := v.loaded
	v.mu.Unlock()

	if loaded > 0 {
		v.setGrid(lines[:loaded])
	} else {
		v.setGrid(nil)
	}
}

func (v *JSONMarkdownView) rebuildViewLinesForKeysLocked(keys []string) {
	v.viewLines = v.viewLines[:0]
	if len(keys) == 0 || v.fullLineCountLocked() == 0 || v.searchKeyRanges == nil {
		return
	}

	type keyBlock struct {
		start     int
		end       int
		foldStart int
	}
	blocks := make([]keyBlock, 0, len(keys))
	for _, key := range keys {
		rng, ok := v.searchKeyRanges[key]
		if !ok {
			continue
		}
		foldStart := -1
		if v.searchKeyFold != nil {
			if fs, ok := v.searchKeyFold[key]; ok {
				foldStart = fs
			}
		}
		blocks = append(blocks, keyBlock{start: rng.start, end: rng.end, foldStart: foldStart})
	}
	if len(blocks) == 0 {
		return
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].start < blocks[j].start })

	lineCount := v.fullLineCountLocked()
	for _, block := range blocks {
		start := block.start
		end := block.end
		if start < 0 {
			start = 0
		}
		if end >= lineCount {
			end = lineCount - 1
		}
		for i := start; i <= end; {
			if i < 0 || i >= lineCount {
				break
			}
			if i == start && block.foldStart == i+1 {
				if foldEnd, ok := v.foldRanges[block.foldStart]; ok && foldEnd > block.foldStart && v.folded[block.foldStart] {
					clampedEnd := foldEnd
					if clampedEnd > end {
						clampedEnd = end
					}
					v.viewLines = append(v.viewLines, i)
					v.viewLines = append(v.viewLines, foldMarker(block.foldStart))
					i = clampedEnd + 1
					continue
				}
			}
			if foldEnd, ok := v.foldRanges[i]; ok && foldEnd > i && v.folded[i] {
				clampedEnd := foldEnd
				if clampedEnd > end {
					clampedEnd = end
				}
				v.viewLines = append(v.viewLines, foldMarker(i))
				i = clampedEnd + 1
				continue
			}
			v.viewLines = append(v.viewLines, i)
			i++
		}
	}
}

func (v *JSONMarkdownView) setSearchKeys(keys []string) {
	if keys == nil {
		keys = nil
	}
	opts := make([]string, 0, len(keys))
	opts = append(opts, keys...)
	fyne.Do(func() {
		if v.searchKeySelect == nil {
			return
		}
		v.searchKeySelect.SetOptions(opts)
		v.searchKeySelect.SetSelectedValues(nil)
		v.searchKeySelect.Refresh()
	})
}

func buildVisibleHighlightsBytes(lines [][]byte, srcLines []int, placeholders []bool, queryLower []byte, matchSet map[int]struct{}) map[int][]highlightRange {
	if len(queryLower) == 0 || len(lines) == 0 || len(srcLines) == 0 || matchSet == nil {
		return nil
	}
	highlights := make(map[int][]highlightRange)
	for i := 0; i < len(lines) && i < len(srcLines); i++ {
		if placeholders != nil && placeholders[i] {
			continue
		}
		srcLine := srcLines[i]
		if _, ok := matchSet[srcLine]; !ok {
			continue
		}
		ranges := findHighlightRangesASCII(lines[i], queryLower)
		if len(ranges) == 0 {
			continue
		}
		highlights[srcLine] = ranges
	}
	return highlights
}

// --- UI

func (v *JSONMarkdownView) handleTap(pos fyne.Position) {
	if v.tgrid == nil {
		return
	}
	row, col := v.tgrid.CursorLocationForPosition(pos)
	if row < 0 {
		return
	}

	v.mu.Lock()
	if row >= len(v.viewLines) {
		v.mu.Unlock()
		return
	}
	srcLine := viewLineIndex(v.viewLines[row])
	lineNumWidth := v.lineNumWidth
	viewLine := viewLineString(v.fullBuf, v.viewLines[row])
	v.mu.Unlock()

	prefixLen := 0
	if lineNumWidth > 0 {
		prefixLen = lineNumWidth + 2
	}
	if col < prefixLen {
		v.clearSelectedKey()
		v.clearSelectedValue()
		v.refreshSelection()
		return
	}
	colAdj := col - prefixLen
	if key, rng, ok := keyAtCol(viewLine, colAdj); ok {
		v.setSelectedKey(srcLine, rng, key)
	} else if val, vrng, ok := valueAtCol(viewLine, colAdj); ok {
		v.setSelectedValue(srcLine, vrng, val)
	} else {
		v.clearSelectedKey()
		v.clearSelectedValue()
	}
	if !isInteractiveCell(viewLine, colAdj) {
		v.refreshSelection()
		return
	}

	v.mu.Lock()
	end, ok := v.foldRanges[srcLine]
	if !ok || end <= srcLine {
		v.mu.Unlock()
		v.refreshSelection()
		return
	}
	v.folded[srcLine] = !v.folded[srcLine]
	if v.searchStructural && v.searchMatchSet != nil && len(v.searchMatchSet) > 0 {
		v.rebuildViewLinesForMatchesLocked(v.searchMatchSet)
	} else if len(v.searchKeys) > 0 {
		v.rebuildViewLinesForKeysLocked(v.searchKeys)
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
			if endRow := findViewRow(v.viewLines, foldEnd); endRow >= 0 {
				v.ensureLoadedForRowLocked(endRow)
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

func (v *JSONMarkdownView) handleSecondaryTap(pos fyne.Position) {
	if v.tgrid == nil || v.win == nil {
		return
	}
	row, col := v.tgrid.CursorLocationForPosition(pos)
	if row < 0 {
		return
	}

	v.mu.Lock()
	if row >= len(v.viewLines) {
		v.mu.Unlock()
		return
	}
	lineNumWidth := v.lineNumWidth
	viewLine := viewLineString(v.fullBuf, v.viewLines[row])
	srcLine := viewLineIndex(v.viewLines[row])
	v.mu.Unlock()

	prefixLen := 0
	if lineNumWidth > 0 {
		prefixLen = lineNumWidth + 2
	}
	if col < prefixLen {
		return
	}
	colAdj := col - prefixLen

	keyText, keyRange, keyOk := keyAtCol(viewLine, colAdj)
	valText, valRange, valOk := valueAtCol(viewLine, colAdj)
	fullKey, fullVal, kvOk := extractKeyValue(viewLine)
	fullBlockVal, blockOk := v.fullValueForLine(srcLine)

	if keyOk {
		v.setSelectedKey(srcLine, keyRange, keyText)
	} else if valOk {
		v.setSelectedValue(srcLine, valRange, valText)
	} else {
		return
	}
	v.refreshSelection()

	if blockOk {
		fullVal = fullBlockVal
	}
	if !kvOk {
		fullKey = keyText
		if !blockOk {
			fullVal = ""
		}
	}
	if strings.TrimSpace(fullKey) == "" && strings.TrimSpace(keyText) != "" {
		fullKey = keyText
	}
	if strings.TrimSpace(fullVal) == "" && strings.TrimSpace(valText) != "" {
		fullVal = valText
	}

	keyItem := fyne.NewMenuItem("Copy", func() {
		keyOut := quoteKeyIfNeeded(fullKey)
		if keyOut != "" && fullVal != "" {
			v.win.Clipboard().SetContent(wrapCopyContent(keyOut + ": " + fullVal))
			return
		}
		if keyOut != "" {
			v.win.Clipboard().SetContent(wrapCopyContent(keyOut))
			return
		}
		if fullVal != "" {
			v.win.Clipboard().SetContent(wrapCopyContent(fullVal))
		}
	})
	if strings.TrimSpace(fullKey) == "" && strings.TrimSpace(fullVal) == "" {
		keyItem.Disabled = true
	}
	valItem := fyne.NewMenuItem("Copy value", func() {
		if fullVal != "" {
			v.win.Clipboard().SetContent(fullVal)
		}
	})
	if strings.TrimSpace(fullVal) == "" {
		valItem.Disabled = true
	}

	menu := fyne.NewMenu("", keyItem, valItem)

	absPos := pos
	if d := fyne.CurrentApp().Driver(); d != nil {
		base := d.AbsolutePositionForObject(v.overlay)
		absPos = fyne.NewPos(base.X+pos.X, base.Y+pos.Y)
	}
	widget.ShowPopUpMenuAtPosition(menu, v.win.Canvas(), absPos)
}

func (v *JSONMarkdownView) setSelectedKey(line int, rng highlightRange, key string) {
	v.mu.Lock()
	v.selectedKeyLine = line
	v.selectedKeyRange = rng
	v.selectedKeyValue = key
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.mu.Unlock()
}

func (v *JSONMarkdownView) setSelectedValue(line int, rng highlightRange, val string) {
	v.mu.Lock()
	v.selectedValueLine = line
	v.selectedValueRange = rng
	v.selectedValueText = val
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.mu.Unlock()
}

func (v *JSONMarkdownView) clearSelectedKey() {
	v.mu.Lock()
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.mu.Unlock()
}

func (v *JSONMarkdownView) clearSelectedValue() {
	v.mu.Lock()
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.mu.Unlock()
}

func (v *JSONMarkdownView) refreshSelection() {
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

func (v *JSONMarkdownView) SelectedKeyValueString() string {
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

func (v *JSONMarkdownView) fullLineCountLocked() int {
	if v.fullBuf != nil {
		return v.fullBuf.LineCount()
	}
	return 0
}

func (v *JSONMarkdownView) rebuildViewLinesLocked() {
	v.viewLines = v.viewLines[:0]
	if v.fullLineCountLocked() == 0 {
		return
	}
	for i := 0; i < v.fullLineCountLocked(); {
		if end, ok := v.foldRanges[i]; ok && end > i && v.folded[i] {
			v.viewLines = append(v.viewLines, foldMarker(i))
			i = end + 1
			continue
		}
		v.viewLines = append(v.viewLines, i)
		i++
	}
}

func (v *JSONMarkdownView) fullLineBytes(i int) []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fullBuf == nil {
		return nil
	}
	return v.fullBuf.Line(i)
}

func (v *JSONMarkdownView) fullValueForLine(srcLine int) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fullValueForLineLocked(srcLine)
}

func (v *JSONMarkdownView) fullValueForLineLocked(srcLine int) (string, bool) {
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

// --- Search

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
	seq := atomic.AddUint64(&v.searchSeq, 1)

	v.mu.Lock()
	keys := v.searchKeys
	index := v.searchKeyIndex
	allLines := v.searchAll
	keyRanges := v.searchKeyRanges
	trigramEnabled := v.trigramEnabled
	trigramIndex := v.trigramIndex
	v.searchQuery = query
	if len(v.viewLines) > 0 {
		first := viewLineIndex(v.viewLines[0])
		last := viewLineIndex(v.viewLines[len(v.viewLines)-1])
		for _, k := range keys {
			if rng, ok := keyRanges[k]; ok {
				if first < rng.start || last > rng.end {
					v.rebuildViewLinesForKeysLocked(keys)
					break
				}
			}
		}
	}
	v.mu.Unlock()

	queryLower := asciiLowerBytes([]byte(query))
	if len(queryLower) == 0 {
		fyne.Do(func() {
			if seq != atomic.LoadUint64(&v.searchSeq) {
				return
			}
			v.mu.Lock()
			v.highlights = nil
			v.matchLines = nil
			v.searchMatchSet = nil
			v.matchIndex = -1
			lines := v.viewLines
			loaded := v.loaded
			v.mu.Unlock()
			v.updateNavButtons()
			if loaded > 0 {
				v.setGrid(lines[:loaded])
			} else {
				v.setGrid(nil)
			}
		})
		return
	}

	var candidates []int
	if len(keys) == 0 {
		candidates = allLines
	} else {
		candidates = unionCandidateLines(index, keys)
	}

	if trigramEnabled && trigramIndex != nil && len(queryLower) >= 3 {
		triCandidates := trigramCandidatesFromIndex(trigramIndex, queryLower)
		if triCandidates != nil {
			candidates = intersectSortedInts(candidates, triCandidates)
		}
	}

	go func(seq uint64, queryLower []byte, candidates []int) {
		matchLines := make([]int, 0)
		matchSet := make(map[int]struct{})

		for _, i := range candidates {
			lineBytes := v.fullLineBytes(i)
			if len(lineBytes) == 0 {
				continue
			}
			if !containsFoldASCII(lineBytes, queryLower) {
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
			v.matchLines = matchLines
			v.searchMatchSet = matchSet
			if v.searchStructural {
				v.rebuildViewLinesForMatchesLocked(matchSet)
			}
			if len(matchLines) == 0 {
				v.matchIndex = -1
			} else if v.matchIndex < 0 || v.matchIndex >= len(matchLines) {
				v.matchIndex = 0
			}
			v.loaded = minInt(v.chunk, len(v.viewLines))
			lines := v.viewLines
			loaded := v.loaded
			v.mu.Unlock()

			v.updateNavButtons()
			if loaded > 0 {
				v.setGrid(lines[:loaded])
			} else {
				v.setGrid(nil)
			}
		})
	}(seq, queryLower, candidates)
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
	if v.searchStructural && v.searchMatchSet != nil && len(v.searchMatchSet) > 0 {
		v.rebuildViewLinesForMatchesLocked(v.searchMatchSet)
	} else if len(v.searchKeys) > 0 {
		v.rebuildViewLinesForKeysLocked(v.searchKeys)
	}
	row := findViewRow(v.viewLines, line)
	v.ensureLoadedForRowLocked(row)
	lines := v.viewLines
	loaded := v.loaded
	v.mu.Unlock()

	v.setGrid(lines[:loaded])
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

func (v *JSONMarkdownView) rebuildViewLinesForMatchesLocked(matchSet map[int]struct{}) {
	v.viewLines = v.viewLines[:0]
	if v.fullLineCountLocked() == 0 || len(matchSet) == 0 {
		return
	}

	ranges := make([]keyRange, 0, len(v.searchKeys))
	if len(v.searchKeys) > 0 {
		for _, r := range v.searchKeyRanges {
			ranges = append(ranges, r)
		}
	}
	allowed := func(line int) bool {
		if len(ranges) == 0 {
			return true
		}
		for _, r := range ranges {
			if line >= r.start && line <= r.end {
				return true
			}
		}
		return false
	}

	foldStarts := make([]int, 0, len(v.foldRanges))
	for s := range v.foldRanges {
		foldStarts = append(foldStarts, s)
	}
	sort.Ints(foldStarts)

	keepBlocks := make(map[int]struct{})
	keepLines := make(map[int]struct{})

	for line := range matchSet {
		if !allowed(line) {
			continue
		}
		keepLines[line] = struct{}{}
		for _, s := range foldStarts {
			end := v.foldRanges[s]
			if s <= line && line <= end {
				keepBlocks[s] = struct{}{}
			}
		}
	}

	for s := range keepBlocks {
		keepLines[s] = struct{}{}
		end := v.foldRanges[s]
		if allowed(end) {
			keepLines[end] = struct{}{}
		}
		prev := s - 1
		if prev >= 0 && allowed(prev) {
			if line := v.fullBuf.Line(prev); isKeyLineWithoutBraceBytes(line) {
				keepLines[prev] = struct{}{}
			}
		}
	}

	lineCount := v.fullLineCountLocked()
	for i := 0; i < lineCount; {
		if !allowed(i) {
			i++
			continue
		}
		if end, ok := v.foldRanges[i]; ok {
			if _, keep := keepBlocks[i]; !keep {
				i = end + 1
				continue
			}
		}
		if _, ok := keepLines[i]; ok {
			v.viewLines = append(v.viewLines, i)
		}
		i++
	}
}

// --- Line access helpers

func foldMarker(line int) int {
	return -line - 1
}

func viewLineIndex(v int) int {
	if v < 0 {
		return -v - 1
	}
	return v
}

func viewLineString(buf *JSONBuffer, v int) string {
	if buf == nil {
		return ""
	}
	idx := viewLineIndex(v)
	line := buf.Line(idx)
	if v < 0 {
		return string(buildFoldPlaceholderBytes(line))
	}
	return string(line)
}

func buildViewLineBytes(buf *JSONBuffer, viewLines []int) ([][]byte, []int, []bool) {
	if buf == nil || len(viewLines) == 0 {
		return nil, nil, nil
	}
	lines := make([][]byte, len(viewLines))
	srcLines := make([]int, len(viewLines))
	placeholders := make([]bool, len(viewLines))
	for i, v := range viewLines {
		idx := viewLineIndex(v)
		line := buf.Line(idx)
		if v < 0 {
			lines[i] = buildFoldPlaceholderBytes(line)
			placeholders[i] = true
		} else {
			lines[i] = line
		}
		srcLines[i] = idx
	}
	return lines, srcLines, placeholders
}

func buildFoldPlaceholderBytes(line []byte) []byte {
	idx, brace := findFoldTokenBytes(line)
	if idx == -1 {
		return append([]byte(nil), line...)
	}
	prefix := line[:idx]
	if brace == '[' {
		out := make([]byte, 0, len(prefix)+7)
		out = append(out, prefix...)
		out = append(out, '[', ' ', '.', '.', '.', ' ', ']')
		return out
	}
	out := make([]byte, 0, len(prefix)+7)
	out = append(out, prefix...)
	out = append(out, '{', ' ', '.', '.', '.', ' ', '}')
	return out
}

func findFoldTokenBytes(line []byte) (int, byte) {
	inString := false
	esc := false
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
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			continue
		}
		switch b {
		case '{', '[':
			return i, b
		}
	}
	return -1, 0
}

func findViewRow(viewLines []int, srcLine int) int {
	for i, v := range viewLines {
		if viewLineIndex(v) == srcLine {
			return i
		}
	}
	return -1
}

func isKeyLineWithoutBraceBytes(line []byte) bool {
	if _, _, ok := findKeyRange(string(line)); !ok {
		return false
	}
	idx, _ := findFoldTokenBytes(line)
	return idx == -1
}

// --- JSON buffer and helpers

type JSONBuffer struct {
	data        []byte
	lineOffsets []int
}

func newJSONBuffer(s string) *JSONBuffer {
	b := &JSONBuffer{}
	if s == "" {
		return b
	}
	b.data = []byte(s)
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

func buildTextGridRows(lines [][]byte, srcLines []int, highlights map[int][]highlightRange, lineNumWidth int, selectedLine int, selectedRange highlightRange) []widget.TextGridRow {
	if len(lines) == 0 {
		return nil
	}
	rows := make([]widget.TextGridRow, 0, len(lines))
	for i, line := range lines {
		var hl []highlightRange
		var sel highlightRange
		prefix := ""
		if srcLines != nil && i < len(srcLines) {
			lineNum := srcLines[i] + 1
			prefix = fmt.Sprintf("%*d  ", lineNumWidth, lineNum)
			if highlights != nil {
				hl = highlights[srcLines[i]]
			}
			if srcLines[i] == selectedLine {
				sel = selectedRange
			}
		}
		fullLine := make([]byte, 0, len(prefix)+len(line))
		fullLine = append(fullLine, prefix...)
		fullLine = append(fullLine, line...)
		cells := buildTextGridCells(fullLine, hl, len(prefix), sel)
		rows = append(rows, widget.TextGridRow{Cells: cells})
	}
	return rows
}

func buildTextGridCells(line []byte, highlights []highlightRange, prefixLen int, selected highlightRange) []widget.TextGridCell {
	if len(line) == 0 {
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
	inSelected := func(i int) bool {
		if selected.start == 0 && selected.end == 0 {
			return false
		}
		if i < prefixLen {
			return false
		}
		adj := i - prefixLen
		return adj >= selected.start && adj < selected.end
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
			if inSelected(pos) {
				style.TextStyle = fyne.TextStyle{Bold: true}
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

	runes := []rune(string(line))
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

		if unicode.IsSpace(r) {
			j := i + 1
			for j < len(runes) && unicode.IsSpace(runes[j]) {
				j++
			}
			setPending(string(runes[i:j]), jsonPunctColor())
			i = j
			continue
		}

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

// --- Tap overlay

type tapOverlay struct {
	widget.BaseWidget
	onTap       func(pos fyne.Position)
	onSecondary func(pos fyne.Position)
}

func newTapOverlay(onTap func(pos fyne.Position), onSecondary func(pos fyne.Position)) *tapOverlay {
	o := &tapOverlay{onTap: onTap, onSecondary: onSecondary}
	o.ExtendBaseWidget(o)
	return o
}

func (o *tapOverlay) Tapped(ev *fyne.PointEvent) {
	if o.onTap != nil {
		o.onTap(ev.Position)
	}
}

func (o *tapOverlay) TappedSecondary(ev *fyne.PointEvent) {
	if o.onSecondary != nil {
		o.onSecondary(ev.Position)
	}
}

func (o *tapOverlay) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.Transparent)
	return widget.NewSimpleRenderer(rect)
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

// --- Search helpers

func normalizeKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

func unionCandidateLines(index map[string][]int, keys []string) []int {
	if len(keys) == 0 || index == nil {
		return nil
	}
	seen := make(map[int]struct{})
	out := make([]int, 0)
	for _, k := range keys {
		for _, ln := range index[k] {
			if _, ok := seen[ln]; ok {
				continue
			}
			seen[ln] = struct{}{}
			out = append(out, ln)
		}
	}
	sort.Ints(out)
	return out
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

func findHighlightRangesASCII(line []byte, queryLower []byte) []highlightRange {
	if len(line) == 0 || len(queryLower) == 0 {
		return nil
	}
	var ranges []highlightRange
	start := 0
	for {
		idx := indexFoldASCII(line[start:], queryLower)
		if idx < 0 {
			break
		}
		from := start + idx
		to := from + len(queryLower)
		ranges = append(ranges, highlightRange{start: from, end: to})
		start = to
		if start >= len(line) {
			break
		}
	}
	return ranges
}

func asciiLowerBytes(in []byte) []byte {
	out := make([]byte, len(in))
	for i, b := range in {
		if b >= 'A' && b <= 'Z' {
			out[i] = b + ('a' - 'A')
			continue
		}
		out[i] = b
	}
	return out
}

func indexFoldASCII(haystack []byte, needleLower []byte) int {
	if len(needleLower) == 0 {
		return 0
	}
	if len(needleLower) > len(haystack) {
		return -1
	}
	limit := len(haystack) - len(needleLower)
	for i := 0; i <= limit; i++ {
		match := true
		for j := 0; j < len(needleLower); j++ {
			b := haystack[i+j]
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if b != needleLower[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func containsFoldASCII(haystack []byte, needleLower []byte) bool {
	return indexFoldASCII(haystack, needleLower) >= 0
}

// --- Fold ranges & index

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

func buildSearchIndexBuffer(buf *JSONBuffer, foldRanges map[int]int) (map[string][]int, map[string]keyRange, []int, map[string]int) {
	if buf == nil {
		return nil, nil, nil, nil
	}
	index := make(map[string][]int)
	keyStarts := make(map[string]int)
	keyOrder := make([]int, 0)
	keyRanges := make(map[string]keyRange)
	keyFold := make(map[string]int)
	lineCount := buf.LineCount()
	allLines := make([]int, 0, lineCount)

	for i := 0; i < lineCount; i++ {
		line := buf.Line(i)
		allLines = append(allLines, i)
		if lineIndentDepthBytes(line) != 1 {
			continue
		}
		if key, ok := extractLineKeyBytes(line); ok {
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
		end := lineCount - 1
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
		for i := start; i <= end && i < lineCount; i++ {
			index[key] = append(index[key], i)
		}
	}
	return index, keyRanges, allLines, keyFold
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

func buildFoldRangesWithDepthBuffer(buf *JSONBuffer) (map[int]int, map[int]int) {
	ranges := map[int]int{}
	depths := map[int]int{}
	if buf == nil {
		return ranges, depths
	}
	type entry struct {
		line  int
		brace rune
		depth int
	}
	stack := make([]entry, 0, 32)
	depth := 0
	lineCount := buf.LineCount()

	for i := 0; i < lineCount; i++ {
		line := buf.Line(i)
		inString := false
		esc := false
		for _, b := range line {
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
					inString = false
				}
				continue
			}
			if b == '"' {
				inString = true
				continue
			}
			switch b {
			case '{', '[':
				stack = append(stack, entry{line: i, brace: rune(b), depth: depth})
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

// --- Trigram index

func (v *JSONMarkdownView) buildTrigramIndexLocked(buf *JSONBuffer, dataSize int) {
	v.trigramIndex = nil
	v.trigramEnabled = false
	v.trigramUsedBytes = 0
	v.trigramCapBytes = trigramCapBytes(dataSize)
	if v.trigramCapBytes <= 0 || buf == nil {
		return
	}

	idx := make(map[[3]byte][]int32)
	used := 0
	capBytes := v.trigramCapBytes
	lineCount := buf.LineCount()

	for lineIdx := 0; lineIdx < lineCount; lineIdx++ {
		line := buf.Line(lineIdx)
		if len(line) < 3 {
			continue
		}
		var seen map[[3]byte]struct{}
		if len(line) > 64 {
			seen = make(map[[3]byte]struct{}, 16)
		}
		for i := 0; i+2 < len(line); i++ {
			tri := [3]byte{toLowerASCII(line[i]), toLowerASCII(line[i+1]), toLowerASCII(line[i+2])}
			if seen != nil {
				if _, ok := seen[tri]; ok {
					continue
				}
				seen[tri] = struct{}{}
			}
			lst, ok := idx[tri]
			if !ok {
				lst = make([]int32, 0, 4)
				idx[tri] = lst
				used += 24
			}
			idx[tri] = append(lst, int32(lineIdx))
			used += 4
			if used > capBytes {
				v.trigramIndex = nil
				v.trigramEnabled = false
				v.trigramUsedBytes = 0
				return
			}
		}
	}

	v.trigramIndex = idx
	v.trigramEnabled = true
	v.trigramUsedBytes = used
}

func trigramCapBytes(dataSize int) int {
	if dataSize <= 0 {
		return 0
	}
	capBytes := dataSize / 10
	if capBytes < 8<<20 {
		capBytes = 8 << 20
	}
	if capBytes > 64<<20 {
		capBytes = 64 << 20
	}
	return capBytes
}

func trigramCandidatesFromIndex(idx map[[3]byte][]int32, queryLower []byte) []int {
	if idx == nil || len(queryLower) < 3 {
		return nil
	}
	trigrams := queryTrigrams(queryLower)
	if len(trigrams) == 0 {
		return nil
	}
	lists := make([][]int32, 0, len(trigrams))
	for _, tri := range trigrams {
		lst, ok := idx[tri]
		if !ok || len(lst) == 0 {
			return []int{}
		}
		lists = append(lists, lst)
	}
	sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
	base := append([]int32(nil), lists[0]...)
	for i := 1; i < len(lists); i++ {
		base = intersectSortedInt32(base, lists[i])
		if len(base) == 0 {
			return []int{}
		}
	}
	out := make([]int, len(base))
	for i, v := range base {
		out[i] = int(v)
	}
	return out
}

func queryTrigrams(queryLower []byte) [][3]byte {
	if len(queryLower) < 3 {
		return nil
	}
	out := make([][3]byte, 0, len(queryLower)-2)
	seen := make(map[[3]byte]struct{}, 8)
	for i := 0; i+2 < len(queryLower); i++ {
		tri := [3]byte{queryLower[i], queryLower[i+1], queryLower[i+2]}
		if _, ok := seen[tri]; ok {
			continue
		}
		seen[tri] = struct{}{}
		out = append(out, tri)
	}
	return out
}

func intersectSortedInt32(a, b []int32) []int32 {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make([]int32, 0, minInt(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		av := a[i]
		bv := b[j]
		switch {
		case av == bv:
			out = append(out, av)
			i++
			j++
		case av < bv:
			i++
		default:
			j++
		}
	}
	return out
}

func intersectSortedInts(a, b []int) []int {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make([]int, 0, minInt(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		av := a[i]
		bv := b[j]
		switch {
		case av == bv:
			out = append(out, av)
			i++
			j++
		case av < bv:
			i++
		default:
			j++
		}
	}
	return out
}

func toLowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// --- Misc

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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
