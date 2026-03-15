package searchselect

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
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

	fieldClick  *tapArea
	label       *widget.Label
	arrowButton *widget.Button
	btnWrap     *fyne.Container
	labelWrap   *fyne.Container

	popupBase *widget.PopUp
	search    *focusLostEntry
	list      *widget.List
	scroll    *container.Scroll
	root      *fyne.Container

	prevOnTypedKey func(*fyne.KeyEvent)

	OnChanged func(string)

	frameWrap *fyne.Container
	bg        *canvas.Rectangle
	border    *canvas.Rectangle
	arrowWrap *fyne.Container

	textStyle fyne.TextStyle
	minWidth  float32
}

func NewSearchableSelect(win fyne.Window, placeholder string, options []string) *SearchableSelect {
	ss := &SearchableSelect{win: win, highlight: -1, placeholder: placeholder}
	ss.minWidth = 220

	ss.label = widget.NewLabel("")
	ss.label.Alignment = fyne.TextAlignLeading
	ss.label.Truncation = fyne.TextTruncateEllipsis

	ss.fieldClick = newTapArea(func() { ss.Toggle() })

	ss.arrowButton = widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), func() { ss.Toggle() })
	ss.arrowButton.Importance = widget.LowImportance

	// Arrow button size may change when swapping icons (down/up) which can cause parent layouts to reflow.
	// Keep it stable by reserving max size of both icons.
	upProbe := widget.NewButtonWithIcon("", theme.MenuDropUpIcon(), func() {})
	upProbe.Importance = widget.LowImportance
	arrowSize := ss.arrowButton.MinSize()
	if ms := upProbe.MinSize(); ms.Width > arrowSize.Width {
		arrowSize.Width = ms.Width
	}
	if ms := upProbe.MinSize(); ms.Height > arrowSize.Height {
		arrowSize.Height = ms.Height
	}
	ss.arrowWrap = container.NewGridWrap(arrowSize, ss.arrowButton)

	ss.labelWrap = container.NewStack(ss.label, ss.fieldClick)
	ss.btnWrap = container.NewBorder(nil, nil, nil, ss.arrowWrap, ss.labelWrap)

	// Rounded border frame (native-like)
	ss.bg = canvas.NewRectangle(theme.InputBackgroundColor())
	ss.bg.CornerRadius = theme.InputRadiusSize()

	ss.border = canvas.NewRectangle(color.Transparent)
	ss.border.StrokeColor = theme.InputBorderColor()
	ss.border.StrokeWidth = theme.InputBorderSize()
	ss.border.CornerRadius = theme.InputRadiusSize()

	ss.frameWrap = container.NewStack(ss.bg, ss.border, ss.btnWrap)

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
			lbl := widget.NewLabel("")
			lbl.Alignment = fyne.TextAlignLeading
			btn := newTapArea(nil)
			row := container.NewStack(lbl, btn)
			return row
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*fyne.Container)
			lbl := row.Objects[0].(*widget.Label)
			btn := row.Objects[1].(*tapArea)
			lbl.TextStyle = ss.textStyle
			lbl.Refresh()
			if len(ss.filtered) == 0 {
				lbl.SetText("Nothing found")
				btn.Disable()
				btn.onTapped = nil
				return
			}

			btn.Enable()
			if int(i) >= 0 && int(i) < len(ss.filtered) {
				val := ss.filtered[i]
				lbl.SetText(val)
				btn.onTapped = func() {
					ss.highlight = int(i)
					ss.list.Select(i)
					ss.pickByFilteredIndex(int(i))
				}
			} else {
				lbl.SetText("")
				btn.onTapped = nil
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
	ss.applyTextStyle()
	return ss
}

func (ss *SearchableSelect) CreateRenderer() fyne.WidgetRenderer {
	return &searchableSelectRenderer{ss: ss, obj: ss.frameWrap}
}

type searchableSelectRenderer struct {
	ss  *SearchableSelect
	obj fyne.CanvasObject
}

func (r *searchableSelectRenderer) Destroy() {}

func (r *searchableSelectRenderer) Layout(s fyne.Size) {
	r.obj.Resize(s)
	// Keep primitives in sync.
	if r.ss.bg != nil {
		r.ss.bg.Resize(s)
	}
	if r.ss.border != nil {
		r.ss.border.Resize(s)
	}
}

func (r *searchableSelectRenderer) MinSize() fyne.Size {
	// Make MinSize stable so parent containers (and potentially window) don't reflow on state changes.
	// Height: like Entry.
	eh := widget.NewEntry().MinSize().Height
	btnH := r.ss.label.MinSize().Height
	minH := eh
	if btnH > minH {
		minH = btnH
	}

	// Width: label/button + arrow + padding.

	arrowW := float32(0)
	if r.ss.arrowWrap != nil {
		arrowW = r.ss.arrowWrap.MinSize().Width
	}
	btnW := r.ss.labelWrap.MinSize().Width
	minW := btnW + arrowW
	if minW < r.ss.minWidth {
		minW = r.ss.minWidth
	}
	return fyne.NewSize(minW, minH)
}

func (r *searchableSelectRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.obj} }

func (r *searchableSelectRenderer) Refresh() {
	// Update colors from theme + state.
	r.ss.syncOpenState()
	canvas.Refresh(r.obj)
}

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

	// Update disabled visuals.
	if ss.bg != nil {
		if ss.disabled {
			// Slightly dimmed background for disabled state.
			ss.bg.FillColor = theme.DisabledButtonColor()
		} else {
			ss.bg.FillColor = theme.InputBackgroundColor()
		}
		ss.bg.Refresh()
	}
	if ss.border != nil {
		if ss.disabled {
			ss.border.StrokeColor = theme.DisabledColor()
		} else {
			ss.border.StrokeColor = theme.InputBorderColor()
		}
		ss.border.Refresh()
	}
}

func (ss *SearchableSelect) Enable() {
	ss.disabled = false
	if ss.fieldClick != nil {
		ss.fieldClick.Enable()
	}
	ss.arrowButton.Enable()
	ss.Refresh()
}

func (ss *SearchableSelect) Disable() {
	ss.disabled = true
	ss.HidePopup()
	if ss.fieldClick != nil {
		ss.fieldClick.Disable()
	}
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

func (ss *SearchableSelect) SetTextStyle(style fyne.TextStyle) {
	ss.textStyle = style
	ss.applyTextStyle()
}

func (ss *SearchableSelect) SetMinWidth(w float32) {
	if w <= 0 {
		return
	}
	ss.minWidth = w
	ss.Refresh()
}

func (ss *SearchableSelect) applyTextStyle() {
	if ss.label != nil {
		ss.label.TextStyle = ss.textStyle
		ss.label.Refresh()
	}
	if ss.list != nil {
		ss.list.Refresh()
	}
}

func (ss *SearchableSelect) updateButtonLabel() {
	if ss.selected == "" {
		ph := strings.TrimSpace(ss.placeholder)
		if ph == "" {
			ph = "Select..."
		}
		ss.label.SetText(ph)
		ss.applyTextStyle()
		return
	}
	ss.label.SetText(ss.selected)
	ss.applyTextStyle()
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
		w = ss.labelWrap.Size().Width
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

// Options returns the full options list.
func (ss *SearchableSelect) Options() []string {
	out := make([]string, len(ss.options))
	copy(out, ss.options)
	return out
}

// Clear resets selection and search text.
func (ss *SearchableSelect) Clear() {
	ss.SetSelected("")
	if ss.search != nil {
		ss.search.SetText("")
	}
}

// colorTransparent returns a fully transparent color.
// (unused)

type tapArea struct {
	widget.BaseWidget
	onTapped func()
	disabled bool
}

func newTapArea(onTapped func()) *tapArea {
	t := &tapArea{onTapped: onTapped}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tapArea) Tapped(_ *fyne.PointEvent) {
	if t.disabled {
		return
	}
	if t.onTapped != nil {
		t.onTapped()
	}
}

func (t *tapArea) Enable() {
	t.disabled = false
	t.Refresh()
}

func (t *tapArea) Disable() {
	t.disabled = true
	t.Refresh()
}

func (t *tapArea) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.Transparent)
	return widget.NewSimpleRenderer(rect)
}
