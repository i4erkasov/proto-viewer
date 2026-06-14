package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/internal/domain"
)

// showTypeGuesses показывает модальный список кандидатов типа сообщения
// (ранжированных). Клик по строке передаёт в onSelect полное имя типа и путь
// его .proto-файла (относительно root) — чтобы подставить и тип, и Proto file.
func showTypeGuesses(parent fyne.Window, guesses []domain.TypeGuess, onSelect func(fullType, protoFile string)) {
	if len(guesses) == 0 {
		dialog.ShowInformation("Detect type",
			"Не удалось подобрать тип для этих данных.\nПроверь Proto root/file или формат payload.", parent)
		return
	}

	var dlg dialog.Dialog
	rows := container.NewVBox()
	for _, g := range guesses {
		mark := "~"
		if g.Unknown == 0 {
			mark = "✓" // схема объясняет все данные
		}
		text := fmt.Sprintf("%s  %s    —    поля: %d · unknown: %d б · глубина: %d",
			mark, g.FullType, g.Fields, g.Unknown, g.Depth)
		btn := widget.NewButton(text, func() {
			onSelect(g.FullType, g.ProtoFile)
			if dlg != nil {
				dlg.Hide()
			}
		})
		btn.Alignment = widget.ButtonAlignLeading
		rows.Add(btn)
	}

	header := widget.NewLabelWithStyle(
		"Кандидаты типа (✓ — схема объясняет все данные, ~ — частично):",
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	content := container.NewBorder(header, nil, nil, nil, container.NewVScroll(rows))

	dlg = dialog.NewCustom("Detect message type", "Close", content, parent)
	dlg.Resize(fyne.NewSize(560, 440))
	dlg.Show()
}
