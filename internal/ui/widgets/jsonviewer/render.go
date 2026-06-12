//go:build darwin || linux || windows

package jsonviewer

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"sync"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/internal/infrastructure/perf"
)

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

// styleKey identifies a unique cell style. color.Color values produced here
// are always comparable structs (color.NRGBA / resolved theme colors), so they
// are safe to use as map keys.
type styleKey struct {
	fg   color.Color
	bg   color.Color
	bold bool
}

var (
	styleCacheMu sync.Mutex
	styleCache   = make(map[styleKey]*widget.CustomTextGridStyle)
)

// cellStyle returns a shared *CustomTextGridStyle for the given attributes.
// The renderer treats styles as read-only, so sharing pointers across cells is
// safe and avoids one heap allocation per character.
func cellStyle(fg, bg color.Color, bold bool) *widget.CustomTextGridStyle {
	k := styleKey{fg: fg, bg: bg, bold: bold}
	styleCacheMu.Lock()
	s, ok := styleCache[k]
	if !ok {
		s = &widget.CustomTextGridStyle{FGColor: fg, BGColor: bg, TextStyle: fyne.TextStyle{Bold: bold}}
		styleCache[k] = s
	}
	styleCacheMu.Unlock()
	return s
}

func buildTextGridCells(line []byte, highlights []highlightRange, prefixLen int, selected highlightRange) []widget.TextGridCell {
	if len(line) == 0 {
		return nil
	}
	cells := make([]widget.TextGridCell, 0, len(line))
	pending := make([]byte, 0, 32)
	pendingColor := theme.ForegroundColor()
	// Resolve theme-dependent colors once per line instead of per byte.
	hlBg := highlightColor()
	lnBg := lineNumberBgColor()
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
		if len(pending) == 0 {
			return
		}
		for _, b := range pending {
			var bg color.Color
			bold := false
			if pos < prefixLen {
				bg = lnBg
			} else {
				if inHighlight(pos) {
					bg = hlBg
				}
				if inSelected(pos) {
					bold = true
				}
			}
			cells = append(cells, widget.TextGridCell{Rune: rune(b), Style: cellStyle(pendingColor, bg, bold)})
			pos++
		}
		pending = pending[:0]
	}
	setPending := func(text []byte, c color.Color) {
		if len(pending) > 0 && c != pendingColor {
			flush()
		}
		pendingColor = c
		pending = append(pending, text...)
	}

	isSpace := func(b byte) bool {
		switch b {
		case ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	}
	isNumberStartByte := func(b byte, next byte) bool {
		if b == '-' {
			return next >= '0' && next <= '9'
		}
		return b >= '0' && b <= '9'
	}
	isNumberCharByte := func(b byte) bool {
		return (b >= '0' && b <= '9') || b == '.' || b == 'e' || b == 'E' || b == '+' || b == '-'
	}

	for i := 0; i < len(line); {
		b := line[i]

		if i < prefixLen {
			j := i + 1
			for j < len(line) && j < prefixLen {
				j++
			}
			setPending(line[i:j], theme.ForegroundColor())
			i = j
			continue
		}

		if b == '"' {
			j := i + 1
			esc := false
			for j < len(line) {
				ch := line[j]
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
			lit := line[i:j]
			k := j
			for k < len(line) && isSpace(line[k]) {
				k++
			}
			if k < len(line) && line[k] == ':' {
				setPending(lit, jsonKeyColor())
			} else {
				setPending(lit, jsonStringColor())
			}
			i = j
			continue
		}

		if isSpace(b) {
			j := i + 1
			for j < len(line) && isSpace(line[j]) {
				j++
			}
			setPending(line[i:j], jsonPunctColor())
			i = j
			continue
		}

		if b == 't' && i+3 < len(line) && line[i+1] == 'r' && line[i+2] == 'u' && line[i+3] == 'e' {
			setPending(line[i:i+4], jsonBoolColor())
			i += 4
			continue
		}
		if b == 'f' && i+4 < len(line) && line[i+1] == 'a' && line[i+2] == 'l' && line[i+3] == 's' && line[i+4] == 'e' {
			setPending(line[i:i+5], jsonBoolColor())
			i += 5
			continue
		}
		if b == 'n' && i+3 < len(line) && line[i+1] == 'u' && line[i+2] == 'l' && line[i+3] == 'l' {
			setPending(line[i:i+4], jsonNullColor())
			i += 4
			continue
		}

		if isNumberStartByte(b, func() byte {
			if i+1 < len(line) {
				return line[i+1]
			}
			return 0
		}()) {
			j := i + 1
			for j < len(line) && isNumberCharByte(line[j]) {
				j++
			}
			setPending(line[i:j], jsonNumberColor())
			i = j
			continue
		}

		switch b {
		case '{', '}', '[', ']', ':', ',':
			setPending(line[i:i+1], jsonPunctColor())
			i++
			continue
		}

		setPending(line[i:i+1], theme.ForegroundColor())
		i++
	}
	flush()
	return cells
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

func trigramCandidatesFromIndex(idx map[[3]byte]trigramRange, postings []int32, queryLower []byte) []int {
	if idx == nil || len(queryLower) < 3 {
		return nil
	}
	trigrams := queryTrigrams(queryLower)
	if len(trigrams) == 0 {
		return nil
	}
	lists := make([][]int32, 0, len(trigrams))
	for _, tri := range trigrams {
		r, ok := idx[tri]
		if !ok || r.length == 0 {
			return []int{}
		}
		if r.offset < 0 || r.offset+r.length > len(postings) {
			return []int{}
		}
		lists = append(lists, postings[r.offset:r.offset+r.length])
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

// buildRowsForView builds TextGrid rows for the given view lines, applying the
// current search highlights and selection. Safe to call off the UI thread.
func (v *JSONView) buildRowsForView(viewLines []int) []widget.TextGridRow {
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
	return buildTextGridRows(lineBytes, srcLines, highlights, lineNumWidth, selectedLine, selectedRange)
}

// setGrid replaces the whole grid content with the given view lines.
func (v *JSONView) setGrid(viewLines []int) {
	fyne.Do(func() {
		if v.tgrid == nil {
			return
		}
		stopBuild := perf.Track(fmt.Sprintf("setGrid build (%d lines)", len(viewLines)))
		v.tgrid.Rows = v.buildRowsForView(viewLines)
		stopBuild()
		stopRefresh := perf.Track(fmt.Sprintf("setGrid refresh (%d rows)", len(v.tgrid.Rows)))
		v.tgrid.Refresh()
		v.scroll.Refresh()
		stopRefresh()
	})
}

// appendGrid appends rows for newly loaded view lines without rebuilding the
// rows for already-loaded lines. This keeps incremental scrolling O(new lines)
// on the Go side instead of O(all loaded lines).
func (v *JSONView) appendGrid(newViewLines []int) {
	if len(newViewLines) == 0 {
		return
	}
	fyne.Do(func() {
		if v.tgrid == nil {
			return
		}
		stopBuild := perf.Track(fmt.Sprintf("appendGrid build (%d lines)", len(newViewLines)))
		rows := v.buildRowsForView(newViewLines)
		v.tgrid.Rows = append(v.tgrid.Rows, rows...)
		stopBuild()
		stopRefresh := perf.Track(fmt.Sprintf("appendGrid refresh (%d rows total)", len(v.tgrid.Rows)))
		v.tgrid.Refresh()
		v.scroll.Refresh()
		stopRefresh()
	})
}
