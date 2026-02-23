package tab

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/internal/application/components"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/repository"
)

type FileTab struct {
	w fyne.Window

	repo *repository.FileRepo

	pathOrURL *widget.Entry
	browse    *widget.Button
	clear     *widget.Button
	dropZone  *components.DropZone

	root fyne.CanvasObject
}

func NewTabFile(w fyne.Window) *FileTab {
	t := &FileTab{w: w, repo: repository.NewFile()}

	t.pathOrURL = widget.NewEntry()
	t.pathOrURL.SetPlaceHolder("/path/to/file.bin OR https://host/path/file.bin")

	// Компактная кнопка очистки
	t.clear = widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		t.pathOrURL.SetText("")
	})

	// Компактная кнопка выбора файла (иконка вместо текста)
	t.browse = widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if r == nil {
				return
			}
			t.pathOrURL.SetText(r.URI().Path())
			_ = r.Close()
		}, w)
		d.Show()
	})

	// Делаем кнопки той же высоты, что и инпут.
	entryMin := t.pathOrURL.MinSize()

	btnBrowseMin := t.browse.MinSize()
	btnBrowseSize := fyne.NewSize(btnBrowseMin.Width, entryMin.Height)
	browseWrap := container.NewGridWrap(btnBrowseSize, t.browse)

	btnClearMin := t.clear.MinSize()
	btnClearSize := fyne.NewSize(btnClearMin.Width, entryMin.Height)
	clearWrap := container.NewGridWrap(btnClearSize, t.clear)

	rightButtons := container.NewHBox(clearWrap, browseWrap)

	drop := components.NewDropZone("Drop a .bin/.gz file here (or click the folder button)")
	t.dropZone = drop

	// layout: VBox как в других вкладках
	t.root = container.NewVBox(
		container.NewPadded(drop),
		container.NewBorder(nil, nil,
			widget.NewLabel("Path/URL:"),
			rightButtons,
			t.pathOrURL,
		),
	)

	return t
}

// FlashDropHighlight briefly highlights the drop zone.
func (t *FileTab) FlashDropHighlight() {
	if t.dropZone == nil {
		return
	}
	fyne.Do(func() {
		t.dropZone.SetHighlight(true)
	})
	time.AfterFunc(450*time.Millisecond, func() {
		fyne.Do(func() {
			t.dropZone.SetHighlight(false)
		})
	})
}

func (t *FileTab) Title() string           { return "File" }
func (t *FileTab) View() fyne.CanvasObject { return t.root }

func (t *FileTab) Fetch(ctx context.Context) ([]byte, error) {
	return t.repo.Fetch(ctx, t.pathOrURL.Text)
}

func (t *FileTab) LastHTTPWasGzipped() bool { return t.repo.LastHTTPWasGzipped() }
func (t *FileTab) LastInputLooksGzip() bool { return t.repo.LastInputLooksGzip() }
func (t *FileTab) SetFilePath(p string) {
	t.pathOrURL.SetText(p)
}
