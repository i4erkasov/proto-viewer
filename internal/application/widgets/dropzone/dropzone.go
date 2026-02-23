package dropzone

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// DropZone is a simple decorative widget that visually marks a place
// where the user can drag & drop a file.
//
// It does not handle drop events itself (window-level drop handler is used).
type DropZone struct {
	widget.BaseWidget

	Text string

	highlight bool
}

func New(text string) *DropZone {
	d := &DropZone{Text: text}
	d.ExtendBaseWidget(d)
	return d
}

// SetHighlight changes DropZone border color.
//
// Note: Fyne doesn't provide a built-in "drag enter/leave" callback for DnD,
// so this is a helper API that can be wired later if needed.
func (d *DropZone) SetHighlight(on bool) {
	if d.highlight == on {
		return
	}
	d.highlight = on
	d.Refresh()
}

func (d *DropZone) CreateRenderer() fyne.WidgetRenderer {
	disabled := theme.Color(theme.ColorNameDisabled)

	bg := canvas.NewRectangle(color.Transparent)
	bg.StrokeWidth = 2
	bg.StrokeColor = disabled

	icon := widget.NewIcon(theme.UploadIcon())
	label := canvas.NewText(d.Text, disabled)
	label.Alignment = fyne.TextAlignCenter
	label.TextStyle = fyne.TextStyle{Bold: true}

	objects := []fyne.CanvasObject{bg, icon, label}
	return &renderer{d: d, bg: bg, icon: icon, label: label, objects: objects}
}

type renderer struct {
	d       *DropZone
	bg      *canvas.Rectangle
	icon    *widget.Icon
	label   *canvas.Text
	objects []fyne.CanvasObject
}

func (r *renderer) Layout(size fyne.Size) {
	pad := float32(12)

	r.bg.Resize(size)

	contentW := size.Width - pad*2
	contentH := size.Height - pad*2
	if contentW < 0 {
		contentW = 0
	}
	if contentH < 0 {
		contentH = 0
	}

	iconSize := float32(48)
	r.icon.Resize(fyne.NewSize(iconSize, iconSize))

	labelMin := r.label.MinSize()
	gap := float32(10)
	totalH := iconSize + gap + labelMin.Height

	startY := pad + (contentH-totalH)/2
	if startY < pad {
		startY = pad
	}

	r.icon.Move(fyne.NewPos(pad+(contentW-iconSize)/2, startY))
	r.label.Move(fyne.NewPos(pad, startY+iconSize+gap))
	r.label.Resize(fyne.NewSize(contentW, labelMin.Height))
}

func (r *renderer) MinSize() fyne.Size {
	return fyne.NewSize(320, 160)
}

func (r *renderer) Refresh() {
	r.label.Text = r.d.Text

	if r.d.highlight {
		r.bg.StrokeColor = color.NRGBA{R: 80, G: 160, B: 255, A: 220}
	} else {
		r.bg.StrokeColor = theme.Color(theme.ColorNameDisabled)
	}

	r.label.Refresh()
	r.icon.Refresh()
	r.bg.Refresh()
}

func (r *renderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *renderer) Destroy()                     {}
