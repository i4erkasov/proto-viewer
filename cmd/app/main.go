package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"github.com/i4erkasov/proto-viewer/internal/application"
)

func main() {
	// Уникальный ID нужен Fyne для Preferences API.
	a := app.NewWithID("com.i4erkasov.proto-viewer")
	w := a.NewWindow("Proto Inspector")
	w.Resize(fyne.NewSize(1400, 900))

	ui := application.New(w)
	w.SetContent(ui.Content())

	w.ShowAndRun()
}
