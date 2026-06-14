package ui

import (
	"image"
	"image/color"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// diffMinimap — узкая вертикальная полоса (C1) с рисками в позициях изменённых
// строк. Клик по полосе прыгает к соответствующей строке через onJump.
type diffMinimap struct {
	widget.BaseWidget
	total   int
	changed []int // отсортированные индексы изменённых строк
	col     color.NRGBA
	onJump  func(line int)
}

func newDiffMinimap(col color.Color, onJump func(int)) *diffMinimap {
	m := &diffMinimap{col: color.NRGBAModel.Convert(col).(color.NRGBA), onJump: onJump}
	m.ExtendBaseWidget(m)
	return m
}

// SetData обновляет общее число строк и множество изменённых, перерисовывает.
func (m *diffMinimap) SetData(total int, changed map[int]bool) {
	lines := make([]int, 0, len(changed))
	for l := range changed {
		lines = append(lines, l)
	}
	sort.Ints(lines)
	m.total = total
	m.changed = lines
	m.Refresh()
}

func (m *diffMinimap) draw(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if m.total <= 0 || h == 0 || w == 0 {
		return img
	}
	for _, line := range m.changed {
		y := int(float64(line) / float64(m.total) * float64(h))
		for dy := 0; dy < 2 && y+dy < h; dy++ { // риска высотой 2px
			for x := 0; x < w; x++ {
				img.Set(x, y+dy, m.col)
			}
		}
	}
	return img
}

// Tapped — прыжок к строке по позиции клика.
func (m *diffMinimap) Tapped(e *fyne.PointEvent) {
	h := m.Size().Height
	if h <= 0 || m.total <= 0 || m.onJump == nil {
		return
	}
	line := int(float64(e.Position.Y) / float64(h) * float64(m.total))
	if line < 0 {
		line = 0
	}
	if line >= m.total {
		line = m.total - 1
	}
	m.onJump(line)
}

func (m *diffMinimap) CreateRenderer() fyne.WidgetRenderer {
	r := canvas.NewRaster(m.draw)
	return &minimapRenderer{raster: r}
}

type minimapRenderer struct {
	raster *canvas.Raster
}

func (r *minimapRenderer) Layout(s fyne.Size)           { r.raster.Resize(s) }
func (r *minimapRenderer) MinSize() fyne.Size           { return fyne.NewSize(12, 0) }
func (r *minimapRenderer) Refresh()                     { canvas.Refresh(r.raster) }
func (r *minimapRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.raster} }
func (r *minimapRenderer) Destroy()                     {}
