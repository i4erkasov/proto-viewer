package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/i4erkasov/proto-viewer/internal/application"
)

func clamp(v, min, max float32) float32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func main() {
	// Уникальный ID нужен Fyne для Preferences API.
	a := app.NewWithID("com.i4erkasov.proto-viewer")
	w := a.NewWindow("Proto Inspector")

	ui := application.New(w)
	w.SetContent(ui.Content())

	// Size window to content, but keep it within reasonable bounds.
	min := ui.Content().MinSize()
	csize := fyne.NewSize(
		clamp(min.Width+0, 940, 1480),
		clamp(min.Height+0, 580, 940),
	)
	w.Resize(csize)

	w.ShowAndRun()
}
