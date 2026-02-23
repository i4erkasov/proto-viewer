package protopicker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// fixedSizeLayout forces its children to take exactly `size`.
// This is useful in dialogs where some widgets may otherwise collapse to tiny sizes.
type fixedSizeLayout struct {
	size fyne.Size
}

func (l fixedSizeLayout) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	for _, o := range objects {
		o.Resize(l.size)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (l fixedSizeLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return l.size
}

// fixedTwoColLayout lays out exactly 2 objects side-by-side: left gets fixed width,
// right gets the remaining space.
type fixedTwoColLayout struct {
	leftWidth float32
}

func (l fixedTwoColLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	if len(objects) == 1 {
		objects[0].Move(fyne.NewPos(0, 0))
		objects[0].Resize(size)
		return
	}

	lw := l.leftWidth
	if lw < 0 {
		lw = 0
	}
	if lw > size.Width {
		lw = size.Width
	}

	left := objects[0]
	right := objects[1]

	left.Move(fyne.NewPos(0, 0))
	left.Resize(fyne.NewSize(lw, size.Height))

	right.Move(fyne.NewPos(lw, 0))
	right.Resize(fyne.NewSize(size.Width-lw, size.Height))
}

func (l fixedTwoColLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objects {
		ms := o.MinSize()
		if ms.Height > h {
			h = ms.Height
		}
	}
	return fyne.NewSize(l.leftWidth, h)
}

// entryErrorBorder draws a red border around an Entry when enabled.
// We can't rely on a raw Rectangle in a Stack because it may end up with a tiny size.
// This wrapper always resizes/moves the border to cover the wrapped entry.
type entryErrorBorder struct {
	widget.BaseWidget

	entry   *widget.Entry
	border  *canvas.Rectangle
	enabled bool
}

func newEntryErrorBorder(e *widget.Entry) *entryErrorBorder {
	col := theme.Color(theme.ColorNameError)
	b := canvas.NewRectangle(col)
	b.StrokeColor = col
	b.StrokeWidth = 2
	b.FillColor = nil
	b.Hide()

	w := &entryErrorBorder{entry: e, border: b}
	w.ExtendBaseWidget(w)
	return w
}

func (w *entryErrorBorder) SetError(on bool) {
	w.enabled = on
	if on {
		w.border.Show()
	} else {
		w.border.Hide()
	}
	w.Refresh()
}

func (w *entryErrorBorder) CreateRenderer() fyne.WidgetRenderer {
	objs := []fyne.CanvasObject{w.border, w.entry}
	return &entryErrorBorderRenderer{w: w, objects: objs}
}

type entryErrorBorderRenderer struct {
	w       *entryErrorBorder
	objects []fyne.CanvasObject
}

func (r *entryErrorBorderRenderer) Layout(size fyne.Size) {
	r.w.entry.Resize(size)
	r.w.entry.Move(fyne.NewPos(0, 0))

	// Border covers full entry area.
	r.w.border.Resize(size)
	r.w.border.Move(fyne.NewPos(0, 0))
}

func (r *entryErrorBorderRenderer) MinSize() fyne.Size { return r.w.entry.MinSize() }
func (r *entryErrorBorderRenderer) Refresh() {
	// Keep colors updated with theme.
	col := theme.Color(theme.ColorNameError)
	r.w.border.StrokeColor = col
	r.w.border.FillColor = nil
	canvas.Refresh(r.w.border)
	canvas.Refresh(r.w.entry)
}
func (r *entryErrorBorderRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *entryErrorBorderRenderer) Destroy()                     {}

type Picker struct {
	w    fyne.Window
	root string

	onPick func(absPath string)

	currentDir string
	dirs       []string
	files      []string

	dirList  *widget.List
	fileList *widget.List

	manualPath *widget.Entry
	folderFlt  *widget.Entry
	fileFlt    *widget.Entry

	selectedWrap *entryErrorBorder

	dlg dialog.Dialog
}

// New creates a picker that позволяет выбрать только .proto внутри root.
func New(w fyne.Window, root string, onPick func(absPath string)) (*Picker, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absRoot = filepath.Clean(absRoot)
	st, err := os.Stat(absRoot)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("proto root is not a directory: %s", absRoot)
	}

	p := &Picker{w: w, root: absRoot, onPick: onPick}
	p.currentDir = absRoot

	// Manual full path input (can be absolute or root-relative)
	p.manualPath = widget.NewEntry()
	p.manualPath.SetPlaceHolder("type full path (absolute or relative to Proto root), e.g. api/v1/foo.proto")
	p.manualPath.OnChanged = func(string) {
		// clear highlight while user is editing
		p.setSelectedError(false)
	}
	p.selectedWrap = newEntryErrorBorder(p.manualPath)

	// Folder filter (applies to the left list)
	p.folderFlt = widget.NewEntry()
	p.folderFlt.SetPlaceHolder("filter folders")
	p.folderFlt.Resize(fyne.NewSize(240, p.folderFlt.MinSize().Height))
	p.folderFlt.OnChanged = func(string) {
		p.refreshDirs()
	}

	// File filter (applies to the right list)
	p.fileFlt = widget.NewEntry()
	p.fileFlt.SetPlaceHolder("filter files")
	// comfortable minimum; actual width is controlled by right pane width
	p.fileFlt.Resize(fyne.NewSize(470, p.fileFlt.MinSize().Height))
	p.fileFlt.OnChanged = func(string) {
		p.refreshFiles()
	}

	// Directories list
	p.dirList = widget.NewList(
		func() int { return len(p.dirs) },
		func() fyne.CanvasObject { return widget.NewLabel("dir") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(p.dirs) {
				return
			}
			o.(*widget.Label).SetText(p.dirs[i])
		},
	)
	p.dirList.Resize(fyne.NewSize(320, 520))
	p.dirList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(p.dirs) {
			return
		}
		name := p.dirs[id]
		var next string
		if name == ".." {
			next = filepath.Dir(p.currentDir)
		} else {
			next = filepath.Join(p.currentDir, name)
		}
		next = filepath.Clean(next)
		if !p.isInsideRoot(next) {
			return
		}
		st, err := os.Stat(next)
		if err != nil || !st.IsDir() {
			return
		}
		p.currentDir = next
		p.refreshAll()
	}

	// Files list
	p.fileList = widget.NewList(
		func() int { return len(p.files) },
		func() fyne.CanvasObject { return widget.NewLabel("file") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(p.files) {
				return
			}
			o.(*widget.Label).SetText(p.files[i])
		},
	)
	p.fileList.Resize(fyne.NewSize(520, 520))
	p.fileList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(p.files) {
			return
		}
		abs := filepath.Join(p.currentDir, p.files[id])
		abs = filepath.Clean(abs)
		p.manualPath.SetText(abs)
	}

	// Controls
	btnUp := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		parent := filepath.Dir(p.currentDir)
		parent = filepath.Clean(parent)
		if !p.isInsideRoot(parent) {
			return
		}
		p.currentDir = parent
		p.refreshAll()
	})

	showPickError := func(msg string) {
		p.setSelectedError(true)
		dialog.ShowError(fmt.Errorf("%s", msg), w)
	}

	btnOpen := widget.NewButton("Open", func() {
		raw := strings.TrimSpace(p.manualPath.Text)
		if raw == "" {
			showPickError("Selected is empty. Please choose a .proto file.")
			return
		}

		abs := raw
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(p.root, abs)
		}
		abs = filepath.Clean(abs)

		if !strings.HasSuffix(strings.ToLower(abs), ".proto") {
			showPickError("Selected path must end with .proto")
			return
		}
		if !p.isInsideRoot(abs) {
			showPickError("Selected file must be inside Proto root")
			return
		}

		st, err := os.Stat(abs)
		if err != nil {
			showPickError("Selected file does not exist")
			return
		}
		if !st.Mode().IsRegular() {
			showPickError("Selected path is not a regular file")
			return
		}

		p.setSelectedError(false)
		if onPick != nil {
			onPick(abs)
		}
		if p.dlg != nil {
			p.dlg.Hide()
		}
	})

	btnCancel := widget.NewButton("Cancel", func() {
		if p.dlg != nil {
			p.dlg.Hide()
		}
	})

	const (
		dlgW = float32(920)
		dlgH = float32(740)
	)

	headerRootRow := container.New(fixedSizeLayout{size: fyne.NewSize(dlgW-40, widget.NewLabel("").MinSize().Height)},
		container.NewBorder(nil, nil,
			widget.NewLabelWithStyle("Proto root:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			nil,
			widget.NewLabel(p.root),
		),
	)

	header := container.NewBorder(nil, nil, btnUp, nil, headerRootRow)

	const (
		// keep list area width <= dialog width to avoid weird shrinking/empty gaps
		listAreaW = dlgW - 80
		listAreaH = float32(520)
		leftPaneW = float32(420)
		gapW      = float32(16)
	)

	// Force single-line headers by fixing their height/width.
	foldersHeader := container.New(fixedSizeLayout{size: fyne.NewSize(leftPaneW, p.folderFlt.MinSize().Height)},
		container.NewBorder(nil, nil, widget.NewLabel("Folders"), nil, container.NewStack(p.folderFlt)),
	)

	protoSuffix := widget.NewLabel(".proto")
	filesHeaderContent := container.NewBorder(nil, nil, nil, protoSuffix, container.NewStack(p.fileFlt))
	filesHeader := container.New(fixedSizeLayout{size: fyne.NewSize(listAreaW-leftPaneW-gapW, p.fileFlt.MinSize().Height)},
		filesHeaderContent,
	)

	// Add a small visual gap between columns so headers don't stick together.
	gap := container.New(fixedSizeLayout{size: fyne.NewSize(gapW, p.folderFlt.MinSize().Height)}, widget.NewLabel(""))

	// build panes explicitly
	foldersPane := container.NewBorder(foldersHeader, nil, nil, nil, container.NewStack(p.dirList))
	filesPane := container.NewBorder(filesHeader, nil, nil, nil, container.NewStack(p.fileList))

	// Use fixed 2-col layout so the right pane gets enough width.
	// Left width includes the visual gap.
	twoCols := container.New(fixedTwoColLayout{leftWidth: leftPaneW + gapW},
		container.NewHBox(foldersPane, gap),
		filesPane,
	)
	listsArea := container.New(fixedSizeLayout{size: fyne.NewSize(listAreaW, listAreaH)}, twoCols)

	// Selected row with optional red border highlight
	selectedField := container.NewPadded(p.selectedWrap)
	pathRow := container.NewBorder(nil, nil, widget.NewLabel("Selected:"), nil, selectedField)

	footer := container.NewVBox(
		pathRow,
		container.NewHBox(layout.NewSpacer(), btnCancel, btnOpen),
	)

	content := container.NewBorder(header, footer, nil, nil, listsArea)
	content.Resize(fyne.NewSize(dlgW, dlgH))
	p.dlg = dialog.NewCustomWithoutButtons("Select .proto inside Proto root", content, w)

	// Close on ESC while picker is visible.
	if c := w.Canvas(); c != nil {
		prev := c.OnTypedKey()
		c.SetOnTypedKey(func(ev *fyne.KeyEvent) {
			if ev != nil && ev.Name == fyne.KeyEscape {
				if p.dlg != nil {
					p.dlg.Hide()
					return
				}
			}
			if prev != nil {
				prev(ev)
			}
		})
	}

	p.refreshAll()
	return p, nil
}

func (p *Picker) Show() {
	if p.dlg != nil {
		p.dlg.Show()
	}
}

func (p *Picker) setSelectedError(on bool) {
	if p.selectedWrap == nil {
		return
	}
	p.selectedWrap.SetError(on)
}

func (p *Picker) refreshAll() {
	p.refreshDirs()
	p.refreshFiles()
	p.setSelectedError(false)
	p.manualPath.SetText("")
}

func (p *Picker) refreshDirs() {
	entries, err := os.ReadDir(p.currentDir)
	if err != nil {
		p.dirs = nil
		p.dirList.Refresh()
		return
	}
	flt := strings.ToLower(strings.TrimSpace(p.folderFlt.Text))

	dirs := make([]string, 0, 32)
	if p.currentDir != p.root {
		dirs = append(dirs, "..")
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// exclude hidden directories like .git, .idea
		if strings.HasPrefix(name, ".") {
			continue
		}
		if flt != "" && !strings.Contains(strings.ToLower(name), flt) {
			continue
		}

		abs := filepath.Join(p.currentDir, name)
		abs = filepath.Clean(abs)
		if !p.dirHasContent(abs) {
			continue
		}

		dirs = append(dirs, name)
	}

	sort.Strings(dirs[boolToInt(p.currentDir != p.root):])
	p.dirs = dirs
	p.dirList.UnselectAll()
	p.dirList.Refresh()
}

// dirHasContent returns true if `dir` contains at least one non-hidden subdirectory
// OR at least one .proto file. This helps hide "dead" directories.
func (p *Picker) dirHasContent(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			return true
		}
		if strings.HasSuffix(strings.ToLower(name), ".proto") {
			return true
		}
	}
	return false
}

func (p *Picker) refreshFiles() {
	entries, err := os.ReadDir(p.currentDir)
	if err != nil {
		p.files = nil
		p.fileList.Refresh()
		return
	}
	flt := strings.ToLower(strings.TrimSpace(p.fileFlt.Text))
	files := make([]string, 0, 64)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".proto") {
			continue
		}
		if flt != "" && !strings.Contains(strings.ToLower(name), flt) {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	p.files = files
	p.fileList.UnselectAll()
	p.fileList.Refresh()
}

func (p *Picker) isInsideRoot(path string) bool {
	path = filepath.Clean(path)
	rel, err := filepath.Rel(p.root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
