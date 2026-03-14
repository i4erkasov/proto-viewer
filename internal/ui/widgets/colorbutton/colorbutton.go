package colorbutton

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Button is a lightweight button with a custom background color.
//
// Why not embed widget.Button?
// widget.Button's renderer paints its own themed background, which makes it
// unreliable to overlay a custom background color. We implement a minimal
// clickable widget ourselves so SetBackground always updates what's visible.
//
// It keeps default label styling (theme foreground color, bold when high importance).
// Icon is optional.
type Button struct {
	widget.BaseWidget

	Text       string
	Icon       fyne.Resource
	OnTapped   func()
	Importance widget.Importance

	bg       color.Color
	disabled bool
}

func New(label string, bg color.Color, tapped func()) *Button {
	b := &Button{Text: label, bg: bg, OnTapped: tapped, Importance: widget.HighImportance}
	b.ExtendBaseWidget(b)
	return b
}

func (b *Button) SetText(s string) {
	b.Text = s
	b.Refresh()
}

func (b *Button) SetIcon(res fyne.Resource) {
	b.Icon = res
	b.Refresh()
}

func (b *Button) SetBackground(c color.Color) {
	b.bg = c
	b.Refresh()
}

func (b *Button) Disable() {
	b.SetDisabled(true)
}

func (b *Button) Enable() {
	b.SetDisabled(false)
}

func (b *Button) Disabled() bool {
	return b.disabled
}

func (b *Button) SetDisabled(v bool) {
	if b.disabled == v {
		return
	}
	b.disabled = v
	b.Refresh()
}

func (b *Button) Tapped(*fyne.PointEvent) {
	if b.disabled {
		return
	}
	if b.OnTapped != nil {
		b.OnTapped()
	}
}

func (b *Button) TappedSecondary(*fyne.PointEvent) {}

// Desktop hover cursor.
func (b *Button) MouseIn(*desktop.MouseEvent)    { b.Refresh() }
func (b *Button) MouseOut()                      { b.Refresh() }
func (b *Button) MouseMoved(*desktop.MouseEvent) {}

func (b *Button) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(b.bg)
	bg.CornerRadius = theme.InputRadiusSize()

	lbl := canvas.NewText(b.Text, theme.ForegroundColor())
	lbl.Alignment = fyne.TextAlignCenter
	lbl.TextStyle = fyne.TextStyle{Bold: b.Importance == widget.HighImportance}

	ic := widget.NewIcon(b.Icon)
	ic.Hide()
	if b.Icon != nil {
		ic.Show()
	}

	// We place optional icon to the left of text.
	objs := []fyne.CanvasObject{bg, ic, lbl}
	return &renderer{b: b, bg: bg, icon: ic, label: lbl, objs: objs}
}

type renderer struct {
	b     *Button
	bg    *canvas.Rectangle
	icon  *widget.Icon
	label *canvas.Text
	objs  []fyne.CanvasObject
}

// Small height bump so the button looks less cramped compared to standard widgets.
// Increased a bit to make it visibly taller.
const minHeightBump float32 = 12

func (r *renderer) Layout(size fyne.Size) {
	r.bg.Resize(size)

	pad := theme.Padding()
	h := size.Height

	// icon size ~ h - 2*pad, but clamp.
	iconSize := h - 2*pad
	if iconSize < 0 {
		iconSize = 0
	}
	if r.b.Icon == nil {
		r.icon.Hide()
		r.label.Move(fyne.NewPos(pad, 0))
		r.label.Resize(fyne.NewSize(size.Width-2*pad, size.Height))
		return
	}

	r.icon.Show()
	r.icon.Resize(fyne.NewSize(iconSize, iconSize))
	r.icon.Move(fyne.NewPos(pad, pad))

	textX := pad + iconSize + pad
	r.label.Move(fyne.NewPos(textX, 0))
	r.label.Resize(fyne.NewSize(size.Width-textX-pad, size.Height))
}

func (r *renderer) MinSize() fyne.Size {
	pad := theme.Padding()
	text := canvas.NewText(r.b.Text, theme.ForegroundColor())
	text.TextStyle = fyne.TextStyle{Bold: r.b.Importance == widget.HighImportance}
	textSize := text.MinSize()

	// Base height: text + vertical padding.
	h := textSize.Height + 2*pad
	// Give it a bit more height than the default so it matches common button feel.
	h += minHeightBump

	w := textSize.Width + 2*pad
	if r.b.Icon != nil {
		w += (h - 2*pad) + pad // icon + spacing
	}
	return fyne.NewSize(w, h)
}

func (r *renderer) Refresh() {
	bg := r.b.bg
	alphaMul := uint8(0xff)
	if r.b.disabled {
		alphaMul = 0x99 // ~60% opacity
	}

	// Apply alpha multiplier only for NRGBA/RGBA-like colors we can access.
	// For other color types, just keep as-is.
	if c, ok := bg.(color.NRGBA); ok {
		c.A = uint8(uint16(c.A) * uint16(alphaMul) / 0xff)
		bg = c
	}
	if c, ok := bg.(color.RGBA); ok {
		c.A = uint8(uint16(c.A) * uint16(alphaMul) / 0xff)
		bg = c
	}

	r.bg.FillColor = bg
	r.bg.Refresh()

	r.label.Text = r.b.Text
	lblCol := theme.ForegroundColor()
	if c, ok := lblCol.(color.NRGBA); ok {
		if r.b.disabled {
			c.A = 0x99
		}
		lblCol = c
	}
	r.label.Color = lblCol
	r.label.TextStyle = fyne.TextStyle{Bold: r.b.Importance == widget.HighImportance}
	r.label.Refresh()

	r.icon.SetResource(r.b.Icon)
	if r.b.Icon == nil {
		r.icon.Hide()
	} else {
		r.icon.Show()
	}
	// Note: widget.Icon doesn't expose an Importance field in all Fyne versions.
	// Disabled styling is handled via background/label alpha.
	r.icon.Refresh()

	canvas.Refresh(r.b)
}

func (r *renderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *renderer) Destroy()                     {}
