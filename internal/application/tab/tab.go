package tab

import (
	"context"

	"fyne.io/fyne/v2"
)

type Tab interface {
	Title() string
	View() fyne.CanvasObject
	Fetch(ctx context.Context) ([]byte, error)
}
