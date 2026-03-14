package ui

import (
	"fyne.io/fyne/v2"

	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/repository"
	"github.com/i4erkasov/proto-viewer/internal/service/cache"
)

// Deps holds all external dependencies needed by the UI layer.
type Deps struct {
	Decoder   domain.Decoder
	FileRepo  *repository.FileRepo
	RedisRepo domain.RedisRepository
	Cache     *cache.Cache
}

type UI struct {
	w       fyne.Window
	content fyne.CanvasObject
}

func New(w fyne.Window, deps Deps) *UI {
	u := &UI{w: w}
	u.content = build(w, deps)
	return u
}

func (u *UI) Content() fyne.CanvasObject { return u.content }
