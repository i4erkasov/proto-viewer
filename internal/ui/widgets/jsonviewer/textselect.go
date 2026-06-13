//go:build darwin || linux || windows

package jsonviewer

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// selectionColor — фон выделенного текста (полупрозрачный, поверх подсветки).
func selectionColor() color.Color {
	bg := theme.BackgroundColor()
	if isDarkColor(bg) {
		return color.NRGBA{R: 0x33, G: 0x5A, B: 0x9E, A: 0xAA}
	}
	return color.NRGBA{R: 0x33, G: 0x66, B: 0xCC, A: 0x55}
}

// posToRowCol переводит позицию мыши в (строка viewLines, рунный столбец
// контента). Столбец отсчитывается после префикса с номером строки.
func (v *JSONView) posToRowCol(pos fyne.Position) (int, int, bool) {
	if v.tgrid == nil {
		return 0, 0, false
	}
	row, col := v.tgrid.CursorLocationForPosition(pos)
	if row < 0 {
		return 0, 0, false
	}
	v.mu.Lock()
	row += v.winStart
	n := len(v.viewLines)
	prefix := 0
	if v.lineNumWidth > 0 {
		prefix = v.lineNumWidth + 2
	}
	v.mu.Unlock()
	if n == 0 {
		return 0, 0, false
	}
	if row >= n {
		row = n - 1
	}
	cc := col - prefix
	if cc < 0 {
		cc = 0
	}
	return row, cc, true
}

func (v *JSONView) beginTextSelection(pos fyne.Position) {
	row, cc, ok := v.posToRowCol(pos)
	if !ok {
		return
	}
	v.mu.Lock()
	v.selActive = true
	v.selecting = true
	v.selAnchorRow, v.selAnchorCol = row, cc
	v.selCurRow, v.selCurCol = row, cc
	// Свободное выделение и подсветка токена взаимоисключающи — снимаем токен.
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.mu.Unlock()

	if v.win != nil && v.overlay != nil {
		v.win.Canvas().Focus(v.overlay)
	}
	v.updateWindow()
}

func (v *JSONView) updateTextSelection(pos fyne.Position) {
	row, cc, ok := v.posToRowCol(pos)
	if !ok {
		return
	}
	v.mu.Lock()
	if !v.selActive {
		v.mu.Unlock()
		return
	}
	changed := v.selCurRow != row || v.selCurCol != cc
	v.selCurRow, v.selCurCol = row, cc
	v.mu.Unlock()
	if changed {
		v.updateWindow()
	}
}

func (v *JSONView) endTextSelection() {
	v.mu.Lock()
	v.selecting = false
	v.mu.Unlock()
}

// clearTextSelection снимает выделение (например, по одиночному клику).
func (v *JSONView) clearTextSelection() {
	v.mu.Lock()
	had := v.selActive
	v.selActive = false
	v.selecting = false
	v.mu.Unlock()
	if had {
		v.updateWindow()
	}
}

// normSelLocked возвращает нормализованные границы выделения так, что
// (sr,sc) <= (er,ec). Должна вызываться под v.mu.
func (v *JSONView) normSelLocked() (sr, sc, er, ec int) {
	sr, sc = v.selAnchorRow, v.selAnchorCol
	er, ec = v.selCurRow, v.selCurCol
	if er < sr || (er == sr && ec < sc) {
		sr, sc, er, ec = er, ec, sr, sc
	}
	return
}

// selSpanForRowLocked возвращает рунный диапазон [start,end) контента для строки
// row, попадающий в выделение. end>start означает, что есть что подсвечивать.
// lineRuneLen — длина контента строки в рунах. Должна вызываться под v.mu.
func (v *JSONView) selSpanForRowLocked(row, lineRuneLen int) highlightRange {
	if !v.selActive {
		return highlightRange{}
	}
	sr, sc, er, ec := v.normSelLocked()
	if row < sr || row > er {
		return highlightRange{}
	}
	start := 0
	end := lineRuneLen
	if row == sr {
		start = sc
	}
	if row == er {
		end = ec
	}
	if start < 0 {
		start = 0
	}
	if end > lineRuneLen {
		end = lineRuneLen
	}
	if end < start {
		end = start
	}
	return highlightRange{start: start, end: end}
}

// SelectedText возвращает текст текущего выделения (строки разделены \n).
func (v *JSONView) SelectedText() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.selActive {
		return ""
	}
	sr, sc, er, ec := v.normSelLocked()
	if sr < 0 || sr >= len(v.viewLines) {
		return ""
	}
	var b strings.Builder
	for r := sr; r <= er && r < len(v.viewLines); r++ {
		vl := v.viewLines[r]
		if vl < 0 {
			// Свёрнутый узел: копируем ПОЛНОЕ содержимое блока из исходного
			// буфера, а не заглушку "{ ... }".
			start := viewLineIndex(vl)
			end := start
			if fe, ok := v.foldRanges[start]; ok && fe > end {
				end = fe
			}
			total := v.fullLineCountLocked()
			for s := start; s <= end && s < total; s++ {
				b.WriteString(string(v.fullBuf.Line(s)))
				if s < end {
					b.WriteByte('\n')
				}
			}
			if r < er {
				b.WriteByte('\n')
			}
			continue
		}
		content := []rune(viewLineString(v.fullBuf, vl))
		start := 0
		end := len(content)
		if r == sr {
			start = sc
		}
		if r == er {
			end = ec
		}
		if start < 0 {
			start = 0
		}
		if end > len(content) {
			end = len(content)
		}
		if end < start {
			end = start
		}
		b.WriteString(string(content[start:end]))
		if r < er {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// SelectAll выделяет весь видимый текст (от первой строки до конца последней).
func (v *JSONView) SelectAll() {
	v.mu.Lock()
	n := len(v.viewLines)
	if n == 0 {
		v.mu.Unlock()
		return
	}
	last := n - 1
	lastLen := len([]rune(viewLineString(v.fullBuf, v.viewLines[last])))
	v.selActive = true
	v.selecting = false
	v.selAnchorRow, v.selAnchorCol = 0, 0
	v.selCurRow, v.selCurCol = last, lastLen
	// Свободное выделение взаимоисключающе с подсветкой токена.
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.mu.Unlock()
	v.updateWindow()
}

// CopyToClipboard кладёт в буфер свободное выделение, либо (если его нет) —
// выбранный токен ключ/значение.
func (v *JSONView) CopyToClipboard() {
	if v.win == nil {
		return
	}
	txt := v.SelectedText()
	if strings.TrimSpace(txt) == "" {
		txt = v.SelectedKeyValueString()
	}
	if strings.TrimSpace(txt) == "" {
		return
	}
	v.win.Clipboard().SetContent(txt)
}
