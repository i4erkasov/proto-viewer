package main

import (
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/searchselect"
)

func main() {
	a := app.New()
	w := a.NewWindow("SearchableSelect example")
	w.Resize(fyne.NewSize(520, 360))

	options := []string{
		"Абаза", "Абакан", "Абдулино", "Абинск", "Агидель", "Агрыз",
		"Адлер", "Азов", "Аксай", "Алапаевск", "Алдан", "Александров",
		"Алексин", "Альметьевск", "Анадырь", "Анапа", "Ангарск",
	}

	label := widget.NewLabel("Selected: (none)")
	ss := searchselect.NewSearchableSelect(w, "Search...", options)
	ss.OnChanged = func(v string) {
		label.SetText("Selected: " + v)
		log.Println("selected:", v)
	}

	content := container.NewVBox(
		widget.NewLabel("Pick a city"),
		ss,
		label,
		widget.NewSeparator(),
		widget.NewLabel("Tip: type to filter, use ↑/↓, Enter to select, Esc to close"),
	)

	w.SetContent(container.NewPadded(content))
	w.ShowAndRun()
}
