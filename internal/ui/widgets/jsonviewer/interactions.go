//go:build darwin || linux || windows

package jsonviewer

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// --- UI

func (v *JSONView) handleTap(pos fyne.Position) {
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

func (v *JSONView) handleSecondaryTap(pos fyne.Position) {
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
