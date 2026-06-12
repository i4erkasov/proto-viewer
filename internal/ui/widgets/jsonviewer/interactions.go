//go:build darwin || linux || windows

package jsonviewer

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// --- UI

func (v *JSONView) handleTap(pos fyne.Position) {
	if v.tgrid == nil {
		return
	}
	// Одиночный клик снимает свободное выделение (Fyne не зовёт Tapped после
	// протяжки, так что собственное выделение это не сбрасывает).
	v.clearTextSelection()
	row, col := v.tgrid.CursorLocationForPosition(pos)
	if row < 0 {
		return
	}

	v.mu.Lock()
	// TextGrid содержит только видимое окно, поэтому строка тапа — локальная;
	// добавляем смещение окна, чтобы получить индекс в полном viewLines.
	row += v.winStart
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
	v.mu.Unlock()

	// Виртуализация: общая высота контента пересчитывается из нового viewLines,
	// текущая позиция прокрутки сохраняется, перерисовываем видимое окно.
	v.updateWindow()
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
	row += v.winStart
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

	// Если активно свободное выделение текста, правый клик не трогает его —
	// просто предложим скопировать.
	rawSel := v.SelectedText()
	hasTextSel := strings.TrimSpace(rawSel) != ""

	if keyOk {
		if !hasTextSel {
			v.setSelectedKey(srcLine, keyRange, keyText)
		}
	} else if valOk {
		if !hasTextSel {
			v.setSelectedValue(srcLine, valRange, valText)
		}
	} else if !hasTextSel {
		return
	}
	if !hasTextSel {
		v.refreshSelection()
	}

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

	var items []*fyne.MenuItem
	if hasTextSel {
		sel := rawSel
		items = append(items, fyne.NewMenuItem("Copy selection", func() {
			if v.win != nil {
				v.win.Clipboard().SetContent(sel)
			}
		}))
	}
	items = append(items, keyItem, valItem)
	menu := fyne.NewMenu("", items...)

	absPos := pos
	if d := fyne.CurrentApp().Driver(); d != nil {
		base := d.AbsolutePositionForObject(v.overlay)
		absPos = fyne.NewPos(base.X+pos.X, base.Y+pos.Y)
	}
	widget.ShowPopUpMenuAtPosition(menu, v.win.Canvas(), absPos)
}

// --- Tap overlay
//
// Overlay перехватывает мышь поверх TextGrid: одиночный тап (выбор токена /
// разворот фолда), правый клик (контекст-меню) и протяжку для выделения текста
// как в редакторе. Реализует Focusable+Shortcutable, чтобы ловить Cmd/Ctrl+C.

type tapOverlay struct {
	widget.BaseWidget
	onTap       func(pos fyne.Position)
	onSecondary func(pos fyne.Position)
	onDragStart func(pos fyne.Position)
	onDrag      func(pos fyne.Position)
	onDragEnd   func()
	onCopy      func()

	downPos  fyne.Position
	dragging bool
	focused  bool
}

func newTapOverlay(onTap func(pos fyne.Position), onSecondary func(pos fyne.Position)) *tapOverlay {
	o := &tapOverlay{onTap: onTap, onSecondary: onSecondary}
	o.ExtendBaseWidget(o)
	return o
}

func (o *tapOverlay) Tapped(ev *fyne.PointEvent) {
	// Fyne не вызывает Tapped после протяжки, так что это всегда «чистый» клик.
	if o.onTap != nil {
		o.onTap(ev.Position)
	}
}

func (o *tapOverlay) TappedSecondary(ev *fyne.PointEvent) {
	if o.onSecondary != nil {
		o.onSecondary(ev.Position)
	}
}

// desktop.Mouseable: фиксируем точку нажатия как якорь выделения.
func (o *tapOverlay) MouseDown(ev *desktop.MouseEvent) {
	o.downPos = ev.Position
	o.dragging = false
}

func (o *tapOverlay) MouseUp(_ *desktop.MouseEvent) {}

// fyne.Draggable: протяжка мышью = выделение текста.
func (o *tapOverlay) Dragged(ev *fyne.DragEvent) {
	if !o.dragging {
		o.dragging = true
		if o.onDragStart != nil {
			o.onDragStart(o.downPos)
		}
	}
	if o.onDrag != nil {
		o.onDrag(ev.Position)
	}
}

func (o *tapOverlay) DragEnd() {
	o.dragging = false
	if o.onDragEnd != nil {
		o.onDragEnd()
	}
}

// fyne.Focusable
func (o *tapOverlay) FocusGained()              { o.focused = true }
func (o *tapOverlay) FocusLost()                { o.focused = false }
func (o *tapOverlay) TypedRune(_ rune)          {}
func (o *tapOverlay) TypedKey(_ *fyne.KeyEvent) {}

// fyne.Shortcutable: Cmd/Ctrl+C копирует выделение.
func (o *tapOverlay) TypedShortcut(s fyne.Shortcut) {
	if _, ok := s.(*fyne.ShortcutCopy); ok && o.onCopy != nil {
		o.onCopy()
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
