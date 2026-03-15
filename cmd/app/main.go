package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	bootstrap "github.com/i4erkasov/proto-viewer/internal/app"
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

	u := bootstrap.New(w)
	w.SetContent(u.Content())

	// Size window to content, but keep it within reasonable bounds.
	minSize := u.Content().MinSize()
	cSize := fyne.NewSize(
		clamp(minSize.Width, 1024, 1480),
		clamp(minSize.Height, 648, 940),
	)
	w.Resize(cSize)

	w.ShowAndRun()
}
