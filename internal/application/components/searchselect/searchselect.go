package searchselect

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type focusLostEntry struct {
	widget.Entry
	onFocusLost func()
	onEscape    func()
}

func newFocusLostEntry(onFocusLost func()) *focusLostEntry {
	e := &focusLostEntry{onFocusLost: onFocusLost}
	e.ExtendBaseWidget(e)
	return e
}

func (e *focusLostEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocusLost != nil {
		e.onFocusLost()
	}
}

func (e *focusLostEntry) TypedKey(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyEscape {
		if e.onEscape != nil {
			e.onEscape()
			return
		}
	}
	e.Entry.TypedKey(ev)
}

// SearchableSelect is a Select-like widget with an inline, searchable dropdown.
//
// It works like a web "select2": click opens a popup containing a search entry + a filtered list.
//
// Keyboard:
//   - ↑ / ↓ : navigate list
//   - Enter : select
//   - Esc   : close
//
// API:
//   - NewSearchableSelect(win, options)
//   - OnChanged func(string)
//   - SetOptions([]string)
//   - Selected() string
//   - SetSelected(string)
//   - Enable()/Disable()
//
// Notes:
// We position the popup using the driver's AbsolutePositionForObject(). This is the most reliable
// way in Fyne v2 to map a widget into window canvas coordinates, regardless of nested containers.

type SearchableSelect struct {
	widget.BaseWidget

	win fyne.Window

	placeholder string

	options  []string
	filtered []string
	selected string

	open      bool
	highlight int // index in filtered, -1 means none
	disabled  bool

	fieldClick  *widget.Button
	label       *widget.Label
	arrowButton *widget.Button
	btnWrap     *fyne.Container

	popupBase *widget.PopUp
	search    *focusLostEntry
	list      *widget.List
	scroll    *container.Scroll
	root      *fyne.Container

	prevOnTypedKey func(*fyne.KeyEvent)

	// While popup is open, we poll popup visibility to detect outside-click close
	// and keep arrow icon state in sync across platforms.
	stopMonitor chan struct{}

	OnChanged func(string)
}

func NewSearchableSelect(win fyne.Window, placeholder string, options []string) *SearchableSelect {
	ss := &SearchableSelect{win: win, highlight: -1, placeholder: placeholder}

	ss.label = widget.NewLabel("")
	ss.label.Alignment = fyne.TextAlignLeading
	ss.label.Truncation = fyne.TextTruncateEllipsis

	ss.fieldClick = widget.NewButton("", func() { ss.Toggle() })
	ss.fieldClick.Alignment = widget.ButtonAlignLeading
	ss.fieldClick.Importance = widget.LowImportance
	ss.fieldClick.SetText(ss.label.Text)

	ss.arrowButton = widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), func() { ss.Toggle() })
	ss.arrowButton.Importance = widget.LowImportance

	ss.btnWrap = container.NewBorder(nil, nil, nil, ss.arrowButton, ss.fieldClick)

	ss.search = newFocusLostEntry(nil)
	ss.search.onEscape = func() { ss.HidePopup() }
	ss.search.onFocusLost = func() {
		// If popup got dismissed by outside click, keep our UI state in sync.
		ss.syncOpenState()
		if ss.open {
			ss.HidePopup()
		}
	}
	ss.search.SetPlaceHolder(placeholder)

	ss.search.OnChanged = func(q string) {
		ss.applyFilter(q)
		ss.refreshListSelection()
		ss.ensurePopupSizedAndPositioned()
	}
	ss.search.OnSubmitted = func(_ string) { ss.pickHighlightedOrSingle() }

	ss.list = widget.NewList(
		func() int {
			if len(ss.filtered) == 0 {
				return 1
			}
			return len(ss.filtered)
		},
		func() fyne.CanvasObject {
			b := widget.NewButton("", nil)
			b.Alignment = widget.ButtonAlignLeading
			b.Importance = widget.LowImportance
			return b
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			b := o.(*widget.Button)
			if len(ss.filtered) == 0 {
				b.SetText("Nothing found")
				b.Disable()
				b.OnTapped = nil
				return
			}

			b.Enable()
			if int(i) >= 0 && int(i) < len(ss.filtered) {
				val := ss.filtered[i]
				b.SetText(val)
				b.OnTapped = func() {
					ss.highlight = int(i)
					ss.list.Select(i)
					ss.pickByFilteredIndex(int(i))
				}
			} else {
				b.SetText("")
				b.OnTapped = nil
			}
		},
	)

	ss.list.OnSelected = func(id widget.ListItemID) {
		if len(ss.filtered) == 0 {
			ss.list.UnselectAll()
			return
		}
		ss.highlight = int(id)
	}

	ss.scroll = container.NewVScroll(ss.list)
	ss.scroll.SetMinSize(fyne.NewSize(10, 200))

	ss.root = container.NewVBox(
		container.NewPadded(ss.search),
		widget.NewSeparator(),
		container.NewPadded(ss.scroll),
	)

	ss.open = false
	ss.syncOpenState()

	ss.ExtendBaseWidget(ss)
	ss.SetOptions(options)
	ss.updateButtonLabel()
	return ss
}

func (ss *SearchableSelect) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(ss.btnWrap)
}

// TypedKey lets us handle Esc even when focus is inside nested widgets (e.g. the search Entry)
// because focused widgets consume key events before Window.Canvas().OnTypedKey.
func (ss *SearchableSelect) TypedKey(ev *fyne.KeyEvent) {
	ss.syncOpenState()
	if ss.open {
		switch ev.Name {
		case fyne.KeyEscape:
			ss.HidePopup()
			return
		case fyne.KeyDown:
			ss.moveHighlight(+1)
			return
		case fyne.KeyUp:
			ss.moveHighlight(-1)
			return
		case fyne.KeyReturn, fyne.KeyEnter:
			ss.pickHighlightedOrSingle()
			return
		}
	}
	// No-op for other keys: search Entry (when focused) handles text input,
	// and window canvas handler (installed while popup is open) covers the rest.
}

func (ss *SearchableSelect) syncOpenState() {
	// Prefer actual popup visibility if it exists.
	open := ss.open
	if ss.popupBase != nil {
		open = ss.popupBase.Visible()
	}
	ss.open = open
	if ss.open {
		ss.arrowButton.SetIcon(theme.MenuDropUpIcon())
	} else {
		ss.arrowButton.SetIcon(theme.MenuDropDownIcon())
	}
}

func (ss *SearchableSelect) Enable() {
	ss.disabled = false
	ss.fieldClick.Enable()
	ss.arrowButton.Enable()
	ss.Refresh()
}

func (ss *SearchableSelect) Disable() {
	ss.disabled = true
	ss.HidePopup()
	ss.fieldClick.Disable()
	ss.arrowButton.Disable()
	ss.Refresh()
}

func (ss *SearchableSelect) Disabled() bool { return ss.disabled }

func (ss *SearchableSelect) Toggle() {
	if ss.disabled {
		return
	}

	ss.syncOpenState()
	if ss.open {
		ss.HidePopup()
		return
	}
	ss.ShowPopup()
}

func (ss *SearchableSelect) ShowPopup() {
	if ss.disabled || ss.win == nil {
		return
	}
	ss.syncOpenState()
	if ss.open {
		return
	}

	ss.search.Text = ""
	ss.search.Refresh()
	ss.applyFilter("")
	ss.refreshListSelection()

	if ss.popupBase == nil {
		ss.popupBase = widget.NewPopUp(ss.root, ss.win.Canvas())
	}

	ss.popupBase.Show()
	ss.ensurePopupSizedAndPositioned()
	ss.installWindowHandlers()

	ss.win.Canvas().Focus(ss.search)
	ss.syncOpenState()
}

func (ss *SearchableSelect) HidePopup() {
	ss.uninstallWindowHandlers()
	if ss.popupBase != nil {
		ss.popupBase.Hide()
	}
	ss.open = false
	ss.syncOpenState()
}

func (ss *SearchableSelect) Selected() string { return ss.selected }

func (ss *SearchableSelect) SetSelected(v string) {
	ss.selected = strings.TrimSpace(v)
	ss.updateButtonLabel()
}

func (ss *SearchableSelect) SetOptions(opts []string) {
	ss.options = ss.options[:0]
	for _, s := range opts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ss.options = append(ss.options, s)
	}
	q := ""
	if ss.search != nil {
		q = ss.search.Text
	}
	ss.applyFilter(q)
	ss.refreshListSelection()
	ss.updateButtonLabel()
}

func (ss *SearchableSelect) updateButtonLabel() {
	if ss.selected == "" {
		ph := strings.TrimSpace(ss.placeholder)
		if ph == "" {
			ph = "Select..."
		}
		ss.label.SetText(ph)
		ss.fieldClick.SetText(ss.label.Text)
		return
	}
	ss.label.SetText(ss.selected)
	ss.fieldClick.SetText(ss.label.Text)
}

func (ss *SearchableSelect) applyFilter(q string) {
	q = strings.TrimSpace(q)
	qLower := strings.ToLower(q)
	ss.filtered = ss.filtered[:0]
	if q == "" {
		ss.filtered = append(ss.filtered, ss.options...)
	} else {
		for _, s := range ss.options {
			if strings.Contains(strings.ToLower(s), qLower) {
				ss.filtered = append(ss.filtered, s)
			}
		}
	}
	// No implicit highlight on filter change.
	ss.highlight = -1
	ss.list.UnselectAll()
	ss.list.Refresh()
}

func (ss *SearchableSelect) refreshListSelection() {
	ss.list.UnselectAll()
	if len(ss.filtered) == 0 {
		ss.highlight = -1
		ss.list.Refresh()
		return
	}

	// If we have a selected value, highlight it in the filtered list.
	if ss.selected != "" {
		for i, s := range ss.filtered {
			if s == ss.selected {
				ss.highlight = i
				ss.list.Select(widget.ListItemID(i))
				ss.list.ScrollTo(i)
				return
			}
		}
	}

	// Otherwise no explicit highlight.
	ss.highlight = -1
}

func (ss *SearchableSelect) moveHighlight(delta int) {
	if len(ss.filtered) == 0 {
		return
	}
	if ss.highlight < 0 {
		if delta > 0 {
			ss.highlight = 0
		} else {
			ss.highlight = len(ss.filtered) - 1
		}
	} else {
		ss.highlight += delta
		if ss.highlight < 0 {
			ss.highlight = 0
		}
		if ss.highlight >= len(ss.filtered) {
			ss.highlight = len(ss.filtered) - 1
		}
	}
	ss.list.Select(widget.ListItemID(ss.highlight))
	ss.list.ScrollTo(ss.highlight)
}

func (ss *SearchableSelect) pickHighlightedOrSingle() {
	if len(ss.filtered) == 0 {
		return
	}
	// We intentionally do NOT auto-pick единственный вариант по Enter,
	// если пользователь ничего не выделил.
	// Это поведение ближе к обычным dropdown.
	if ss.highlight >= 0 && ss.highlight < len(ss.filtered) {
		ss.pickByFilteredIndex(ss.highlight)
	}
}

func (ss *SearchableSelect) pickByFilteredIndex(idx int) {
	if idx < 0 || idx >= len(ss.filtered) {
		return
	}
	v := ss.filtered[idx]
	ss.SetSelected(v)
	if ss.OnChanged != nil {
		ss.OnChanged(v)
	}

	// Close the popup on selection (mouse or keyboard).
	ss.HidePopup()
}

func (ss *SearchableSelect) ensurePopupSizedAndPositioned() {
	if ss.popupBase == nil || ss.win == nil {
		return
	}

	// Width: match widget width.
	w := ss.Size().Width
	if w <= 0 {
		// During the first layout pass BaseWidget size can be 0; fall back to our actual renderer root.
		w = ss.btnWrap.Size().Width
	}
	if w <= 0 {
		w = ss.fieldClick.Size().Width
	}
	if w < 140 {
		w = 140
	}

	maxListH := float32(200)
	searchH := ss.search.MinSize().Height
	sepH := float32(theme.Padding())
	pad := float32(theme.Padding() * 2)
	height := pad + searchH + sepH + pad + maxListH

	ss.scroll.SetMinSize(fyne.NewSize(w-pad*2, maxListH))
	ss.popupBase.Resize(fyne.NewSize(w, height))

	// Absolute position on canvas.
	pos := fyne.NewPos(0, 0)
	if d := fyne.CurrentApp().Driver(); d != nil {
		pos = d.AbsolutePositionForObject(ss)
	}
	popupPos := fyne.NewPos(pos.X, pos.Y+ss.Size().Height)

	// Clamp into canvas.
	cSize := ss.win.Canvas().Size()
	if popupPos.X+w > cSize.Width {
		popupPos.X = cSize.Width - w - float32(theme.Padding())
		if popupPos.X < 0 {
			popupPos.X = 0
		}
	}
	if popupPos.Y+height > cSize.Height {
		above := pos.Y - height
		if above >= 0 {
			popupPos.Y = above
		} else {
			popupPos.Y = cSize.Height - height
			if popupPos.Y < 0 {
				popupPos.Y = 0
			}
		}
	}

	ss.popupBase.Move(popupPos)
}

// installWindowHandlers temporarily hooks window key handling while the popup is open.
// Fyne doesn't have a global shortcut for raw arrow keys, so we intercept Canvas.OnTypedKey.
func (ss *SearchableSelect) installWindowHandlers() {
	// Keep this as a fallback for cases when our widget doesn't receive keys.
	// But the primary path is TypedKey above.
	if ss.win == nil {
		return
	}
	c := ss.win.Canvas()
	if c == nil {
		return
	}

	if ss.prevOnTypedKey != nil {
		return
	}

	ss.prevOnTypedKey = c.OnTypedKey()
	c.SetOnTypedKey(func(ev *fyne.KeyEvent) {
		ss.syncOpenState()
		if ss.open {
			switch ev.Name {
			case fyne.KeyEscape:
				ss.HidePopup()
				return
			case fyne.KeyDown:
				ss.moveHighlight(+1)
				return
			case fyne.KeyUp:
				ss.moveHighlight(-1)
				return
			case fyne.KeyReturn, fyne.KeyEnter:
				ss.pickHighlightedOrSingle()
				return
			}
		}

		if ss.prevOnTypedKey != nil {
			ss.prevOnTypedKey(ev)
		}
	})
}

func (ss *SearchableSelect) uninstallWindowHandlers() {
	if ss.win == nil {
		return
	}
	c := ss.win.Canvas()
	if c == nil {
		return
	}

	if ss.prevOnTypedKey == nil {
		return
	}

	c.SetOnTypedKey(ss.prevOnTypedKey)
	ss.prevOnTypedKey = nil
}
