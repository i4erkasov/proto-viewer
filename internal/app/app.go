package app

import (
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"

	"github.com/i4erkasov/proto-viewer/internal/infrastructure/protodec"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/repository"
	"github.com/i4erkasov/proto-viewer/internal/service/cache"
	"github.com/i4erkasov/proto-viewer/internal/ui"
)

func workDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	d := filepath.Dir(exe)
	if d == "" {
		return "."
	}
	return d
}

// New wires all dependencies and returns a ready-to-use UI.
func New(w fyne.Window) *ui.UI {
	decoder := protodec.New()
	fileRepo := repository.NewFile()
	redisRepo := repository.NewRedis()
	decodeCache := cache.New(filepath.Join(workDir(), "decode"))

	return ui.New(w, ui.Deps{
		Decoder:   decoder,
		FileRepo:  fileRepo,
		RedisRepo: redisRepo,
		Cache:     decodeCache,
	})
}
