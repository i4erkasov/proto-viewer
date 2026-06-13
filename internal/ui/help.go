package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/assets"
)

// showHelpWindow открывает отдельное окно со справкой (markdown -> RichText).
// Контент — в assets/help.md (встроен через пакет assets).
func showHelpWindow() {
	a := fyne.CurrentApp()
	if a == nil {
		return
	}
	win := a.NewWindow("Proto Viewer — Help")
	rt := widget.NewRichTextFromMarkdown(assets.Help)
	rt.Wrapping = fyne.TextWrapWord
	win.SetContent(container.NewVScroll(rt))
	win.Resize(fyne.NewSize(720, 560))
	win.Show()
}

// showAbout показывает простой диалог «О приложении».
func showAbout(parent fyne.Window) {
	content := container.NewVBox(
		widget.NewLabelWithStyle("Proto Viewer", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Просмотр и декодирование protobuf-сообщений."),
	)
	dialog.ShowCustom("About Proto Viewer", "Close", content, parent)
}
