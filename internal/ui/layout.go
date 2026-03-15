package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/jsonmarkdown"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/jsontree"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/protopicker"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/searchselect"

	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/protoutil"
	"github.com/i4erkasov/proto-viewer/internal/service/cache"
	"github.com/i4erkasov/proto-viewer/internal/ui/tab"
)

const prefLastProtoRoot = "lastProtoRoot"
const prefPresetIndex = "preset.index"
const prefPresetPrefix = "preset."

// overlayLayout positions the second object in the top-right corner without covering the whole area.
type overlayLayout struct{}

func (overlayLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	base := objects[0]
	base.Resize(size)
	base.Move(fyne.NewPos(0, 0))

	if len(objects) < 2 {
		return
	}
	overlay := objects[1]
	pad := theme.Padding()
	os := overlay.Size()
	if os.Width <= 0 || os.Height <= 0 {
		os = overlay.MinSize()
	}
	min := overlay.MinSize()
	if os.Width < min.Width {
		os.Width = min.Width
	}
	if os.Height < min.Height {
		os.Height = min.Height
	}
	maxW := size.Width - pad*2
	if maxW < 0 {
		maxW = 0
	}
	if os.Width > maxW {
		os.Width = maxW
	}
	// Keep overlay on the right but below the expand/collapse button.
	x := size.Width - os.Width - pad
	if x < 0 {
		x = 0
	}
	y := pad + theme.IconInlineSize() + pad
	overlay.Move(fyne.NewPos(x, y))
	overlay.Resize(os)
}

func (overlayLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	return objects[0].MinSize()
}

func build(w fyne.Window, deps Deps) fyne.CanvasObject {
	dec := deps.Decoder
	dc := deps.Cache
	prefs := fyne.CurrentApp().Preferences()

	// Workaround for macOS: track fullscreen transitions and restore window size.
	var preferredSize fyne.Size
	go func() {
		wasFS := w.FullScreen()
		for {
			time.Sleep(300 * time.Millisecond)
			isFS := w.FullScreen()
			if wasFS && !isFS && preferredSize.Width > 0 {
				fyne.Do(func() {
					w.Resize(preferredSize)
				})
			}
			if !isFS {
				fyne.Do(func() {
					cs := w.Canvas().Size()
					if cs.Width > 0 && cs.Height > 0 {
						preferredSize = cs
					}
				})
			}
			wasFS = isFS
		}
	}()

	// Status label (used by proto parsing + decoding)
	lblStatus := widget.NewLabel("Status: idle")
	lblStatus.TextStyle = fyne.TextStyle{Monospace: true}

	// Cache for parsed message types from .proto (path -> mtime/size -> options)
	type protoTypeCacheEntry struct {
		mtime int64
		size  int64
		opts  []string
	}
	var protoTypeCache struct {
		mu    sync.Mutex
		items map[string]protoTypeCacheEntry
	}
	protoTypeCache.items = make(map[string]protoTypeCacheEntry)

	// Helpers
	openFolder := func(p string) error {
		if p == "" {
			return fmt.Errorf("empty path")
		}
		dir := p
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			dir = filepath.Dir(p)
		}
		switch runtime.GOOS {
		case "darwin":
			return exec.Command("open", dir).Start()
		case "windows":
			return exec.Command("explorer", dir).Start()
		default:
			return exec.Command("xdg-open", dir).Start()
		}
	}

	revealFile := func(p string) error {
		if p == "" {
			return fmt.Errorf("empty path")
		}
		switch runtime.GOOS {
		case "darwin":
			return exec.Command("open", "-R", p).Start()
		case "windows":
			return exec.Command("explorer", "/select,", p).Start()
		default:
			return openFolder(p)
		}
	}

	// Keep full outputs in memory (UI shows preview for large payloads).
	var fullJSON string

	// Output widget (JSON tree + markdown)
	jsonTree := jsontree.NewSearchableJSONTree()
	jsonMarkdown := jsonmarkdown.NewJSONMarkdownView(w)

	_ = jsonTree.View()
	outMarkdown := jsonMarkdown.View()
	_ = jsonTree.SearchBar()
	searchJSONWrap := jsonMarkdown.SearchBar()

	// Рамка вокруг области вывода JSON.
	outputBorder := canvas.NewRectangle(color.Transparent)
	outputBorder.StrokeColor = theme.InputBorderColor()
	outputBorder.StrokeWidth = theme.InputBorderSize()
	outputBorder.CornerRadius = theme.InputRadiusSize()
	outputBody := container.NewMax(container.NewStack(outputBorder, outMarkdown))

	// Let header decide width; keep same behavior as before.
	jsonTree.SetSearchWidth(420)
	jsonMarkdown.SetSearchWidth(500)

	var resultPanel *fyne.Container
	var isOutputExpanded bool
	var savedSize fyne.Size // размер окна до expand

	outputContent := container.NewMax(container.New(overlayLayout{}, outputBody, searchJSONWrap))

	isTreeTab := func() bool {
		return false
	}

	isJSONTab := func() bool {
		return true
	}

	setSearchVisible := func(show bool) {
		if show && !isTreeTab() {
			return
		}
		jsonTree.SetSearchVisible(show)
		if resultPanel != nil {
			resultPanel.Refresh()
		}
		if show {
			w.Canvas().Focus(jsonTree.SearchEntry())
		}
	}

	setJSONSearchVisible := func(show bool) {
		if show && !isJSONTab() {
			return
		}
		jsonMarkdown.SetSearchVisible(show)
		if resultPanel != nil {
			resultPanel.Refresh()
		}
		if show {
			w.Canvas().Focus(jsonMarkdown.SearchEntry())
		}
	}

	var loadPresetDialog dialog.Dialog
	closeLoadPresetDialog := func() {
		if loadPresetDialog != nil {
			loadPresetDialog.Hide()
			loadPresetDialog = nil
		}
	}

	registerSearchShortcuts(w.Canvas(), setSearchVisible, func() bool { return jsonTree.SearchVisible() })
	registerSearchShortcuts(w.Canvas(), setJSONSearchVisible, func() bool { return jsonMarkdown.SearchVisible() })
	w.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(_ fyne.Shortcut) {
		if loadPresetDialog != nil {
			closeLoadPresetDialog()
		}
	})
	w.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(_ fyne.Shortcut) {
		if isTreeTab() {
			v := jsonTree.SelectedValueString()
			if strings.TrimSpace(v) == "" {
				return
			}
			w.Clipboard().SetContent(v)
			return
		}
		if isJSONTab() {
			v := jsonMarkdown.SelectedKeyValueString()
			if strings.TrimSpace(v) == "" {
				return
			}
			w.Clipboard().SetContent(v)
		}
	})
	setSearchVisible(false)
	setJSONSearchVisible(false)

	// Remember the initial window width so auto-resize never inflates it.
	var initialWidth float32

	setOutput := func(s string) {
		fullJSON = s
		jsonTree.SetJSON(s)
		jsonMarkdown.SetJSON(s)

		fyne.Do(func() {
			cs := w.Canvas().Size()
			if cs.Height <= 0 {
				return
			}
			if initialWidth <= 0 {
				initialWidth = cs.Width
			}
			targetH := cs.Height + 300
			if targetH > 940 {
				targetH = 940
			}
			if targetH > cs.Height {
				w.Resize(fyne.NewSize(initialWidth, targetH))
			}
		})
	}

	clearOutput := func() {
		fullJSON = ""
		jsonTree.SetJSON("")
		jsonMarkdown.SetJSON("")
	}

	normalizeLocalPath := func(s string) string {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "file://") {
			s = strings.TrimPrefix(s, "file://")
		}
		return s
	}

	// ---- Global settings (shared)
	protoRoot := widget.NewEntry()
	protoRoot.SetPlaceHolder("/path/to/protorepo")

	// restore last root
	if last := strings.TrimSpace(prefs.String(prefLastProtoRoot)); last != "" {
		protoRoot.SetText(last)
	}

	protoFile := widget.NewEntry()
	protoFile.SetPlaceHolder("/path/to/message.proto")
	protoFile.Disable()

	isInsideRoot := func(root, file string) bool {
		root = strings.TrimSpace(root)
		file = strings.TrimSpace(file)
		if root == "" || file == "" {
			return false
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return false
		}
		absRoot = filepath.Clean(absRoot)
		absFile, err := filepath.Abs(file)
		if err != nil {
			return false
		}
		absFile = filepath.Clean(absFile)
		rel, err := filepath.Rel(absRoot, absFile)
		if err != nil {
			return false
		}
		return !(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
	}

	// тип выбран из списка
	typeSelect := searchselect.NewSearchableSelect(w, "Select message type…", []string{}, false)

	// message type validation/error hint (english)
	typeErr := widget.NewLabel("")
	typeErr.TextStyle = fyne.TextStyle{Italic: true}
	typeErr.Hide()

	noteTypeError := func(msg string) {
		if strings.TrimSpace(msg) == "" {
			typeErr.Hide()
			typeErr.SetText("")
			typeErr.Refresh()
			return
		}
		typeErr.SetText(msg)
		typeErr.Show()
		typeErr.Refresh()
	}

	typeSelect.OnChangedSingle(func(v string) {
		noteTypeError("")
		_ = v
	})

	loadProtoTypesAndSelect := func(path string, desiredType string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		lblStatus.SetText("Status: parsing proto…")
		go func(protoPath string, desired string) {
			fi, err := os.Stat(protoPath)
			if err != nil {
				fyne.Do(func() {
					lblStatus.SetText("Status: error")
					dialog.ShowError(err, w)
				})
				return
			}

			protoTypeCache.mu.Lock()
			ce, ok := protoTypeCache.items[protoPath]
			protoTypeCache.mu.Unlock()
			if ok && ce.mtime == fi.ModTime().Unix() && ce.size == fi.Size() {
				fyne.Do(func() {
					lblStatus.SetText("Status: OK")
					typeSelect.SetOptions(ce.opts)
					typeSelect.SetSelected("")
					noteTypeError("")
					if strings.TrimSpace(desired) != "" {
						selected := false
						for _, opt := range ce.opts {
							if opt == desired {
								selected = true
								break
							}
						}
						if selected {
							typeSelect.SetSelected(desired)
						} else {
							dialog.ShowInformation("Message type", "Message type не найден в proto: "+desired, w)
						}
					}
				})
				return
			}

			b, err := os.ReadFile(protoPath)
			if err != nil {
				fyne.Do(func() {
					lblStatus.SetText("Status: error")
					dialog.ShowError(err, w)
				})
				return
			}
			pkg, msgs := protoutil.ParseProtoForTypes(b)
			opts := make([]string, 0, len(msgs))
			for _, m := range msgs {
				full := m
				if pkg != "" {
					full = pkg + "." + m
				}
				opts = append(opts, full)
			}

			protoTypeCache.mu.Lock()
			protoTypeCache.items[protoPath] = protoTypeCacheEntry{mtime: fi.ModTime().Unix(), size: fi.Size(), opts: opts}
			protoTypeCache.mu.Unlock()

			fyne.Do(func() {
				lblStatus.SetText("Status: OK")
				typeSelect.SetOptions(opts)
				typeSelect.SetSelected("")
				noteTypeError("")
				if strings.TrimSpace(desired) != "" {
					selected := false
					for _, opt := range opts {
						if opt == desired {
							selected = true
							break
						}
					}
					if selected {
						typeSelect.SetSelected(desired)
					} else {
						dialog.ShowInformation("Message type", "Message type не найден в proto: "+desired, w)
					}
				}
			})
		}(path, strings.TrimSpace(desiredType))
	}

	// ---- Source tabs (File/Redis/SQL)
	fileTab := tab.NewTabFile(w, deps.FileRepo)
	redisTab := tab.NewTabRedis(w, deps.RedisRepo)

	sourceTabs := container.NewAppTabs(
		container.NewTabItem(fileTab.Title(), container.NewBorder(fileTab.View(), nil, nil, nil, nil)),
		container.NewTabItem(redisTab.Title(), container.NewBorder(redisTab.View(), nil, nil, nil, nil)),
	)
	sourceTabs.SetTabLocation(container.TabLocationTop)

	sourceTabs.OnSelected = func(item *container.TabItem) {
		if item != nil && item.Text == redisTab.Title() {
			clearOutput()
		}
	}

	// --- buttons
	// Presets: save/load proto settings.
	type presetEntry struct {
		ProtoRoot   string `json:"proto_root"`
		ProtoFile   string `json:"proto_file"`
		MessageType string `json:"message_type"`
	}

	loadPresetIndex := func() []string {
		raw := strings.TrimSpace(prefs.String(prefPresetIndex))
		if raw == "" {
			return nil
		}
		var list []string
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return nil
		}
		return list
	}

	savePresetIndex := func(list []string) {
		b, err := json.Marshal(list)
		if err != nil {
			return
		}
		prefs.SetString(prefPresetIndex, string(b))
	}

	savePreset := func(name string, p presetEntry) {
		b, err := json.Marshal(p)
		if err != nil {
			return
		}
		prefs.SetString(prefPresetPrefix+name, string(b))
		list := loadPresetIndex()
		seen := make(map[string]struct{}, len(list))
		for _, v := range list {
			seen[v] = struct{}{}
		}
		if _, ok := seen[name]; !ok {
			list = append(list, name)
			sort.Strings(list)
			savePresetIndex(list)
		}
	}

	loadPreset := func(name string) (presetEntry, bool) {
		raw := strings.TrimSpace(prefs.String(prefPresetPrefix + name))
		if raw == "" {
			return presetEntry{}, false
		}
		var p presetEntry
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return presetEntry{}, false
		}
		return p, true
	}

	findPresetByMessageType := func(msgType string) (string, bool) {
		msgType = strings.TrimSpace(msgType)
		if msgType == "" {
			return "", false
		}
		for _, name := range loadPresetIndex() {
			p, ok := loadPreset(name)
			if !ok {
				continue
			}
			if strings.TrimSpace(p.MessageType) == msgType {
				return name, true
			}
		}
		return "", false
	}

	deletePreset := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		prefs.SetString(prefPresetPrefix+name, "")
		list := loadPresetIndex()
		if len(list) == 0 {
			return
		}
		out := make([]string, 0, len(list))
		for _, v := range list {
			if v != name {
				out = append(out, v)
			}
		}
		savePresetIndex(out)
	}

	btnSavePreset := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() {
		root := strings.TrimSpace(protoRoot.Text)
		pfile := strings.TrimSpace(protoFile.Text)
		mtype := strings.TrimSpace(typeSelect.Selected())
		if mtype == "" {
			dialog.ShowError(fmt.Errorf("message type is required to save"), w)
			return
		}
		nameEntry := widget.NewEntry()
		nameEntry.SetPlaceHolder("Preset name (default: message type)")
		form := dialog.NewForm("Save preset", "Save", "Cancel",
			[]*widget.FormItem{{Text: "Name", Widget: nameEntry}},
			func(ok bool) {
				if !ok {
					return
				}
				name := strings.TrimSpace(nameEntry.Text)
				if name == "" {
					name = mtype
				}
				if existing, ok := findPresetByMessageType(mtype); ok {
					display := strings.TrimSpace(existing)
					if display == "" {
						display = mtype
					}
					dialog.ShowInformation("Save preset", "Уже есть сохраненный message type: "+display, w)
					return
				}
				savePreset(name, presetEntry{ProtoRoot: root, ProtoFile: pfile, MessageType: mtype})
			},
			w,
		)
		form.Resize(fyne.NewSize(520, 160))
		form.Show()
	})

	btnLoadPreset := widget.NewButtonWithIcon("Load", theme.DownloadIcon(), func() {
		list := loadPresetIndex()
		if len(list) == 0 {
			dialog.ShowInformation("Load preset", "No saved presets", w)
			return
		}
		sel := searchselect.NewSearchableSelect(w, "Search preset…", list, false)
		sel.OnChangedSingle(func(name string) {
			_ = name
		})

		btnDelete := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			name := strings.TrimSpace(sel.Selected())
			if name == "" {
				return
			}
			dialog.ShowConfirm("Delete preset", "Delete preset '"+name+"'?", func(ok bool) {
				if !ok {
					return
				}
				deletePreset(name)
				updated := loadPresetIndex()
				sel.SetOptions(updated)
				sel.SetSelected("")
				sel.Refresh()
				if len(updated) == 0 {
					closeLoadPresetDialog()
				}
			}, w)
		})
		btnDelete.Importance = widget.LowImportance

		btnLoad := widget.NewButtonWithIcon("Load", theme.DownloadIcon(), func() {
			name := strings.TrimSpace(sel.Selected())
			if name == "" {
				return
			}
			p, ok := loadPreset(name)
			if !ok {
				dialog.ShowError(fmt.Errorf("failed to load preset"), w)
				return
			}
			protoRoot.SetText(p.ProtoRoot)
			protoFile.SetText(p.ProtoFile)
			loadProtoTypesAndSelect(p.ProtoFile, p.MessageType)
			closeLoadPresetDialog()
		})
		btnLoad.Importance = widget.HighImportance

		btnCancel := widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
			closeLoadPresetDialog()
		})
		btnCancel.Importance = widget.LowImportance

		selectRow := container.NewBorder(nil, nil, nil, btnDelete, sel)
		btnGap := container.NewGridWrap(fyne.NewSize(12, btnLoad.MinSize().Height), layout.NewSpacer())
		btnRow := container.NewHBox(layout.NewSpacer(), btnCancel, btnGap, btnLoad)

		content := container.NewBorder(
			nil,
			btnRow,
			nil,
			nil,
			container.NewVBox(
				widget.NewLabel("Select a saved preset:"),
				selectRow,
				widget.NewSeparator(),
			),
		)
		loadPresetDialog = dialog.NewCustomWithoutButtons("Load preset", content, w)
		loadPresetDialog.Resize(fyne.NewSize(560, 260))
		loadPresetDialog.Show()
	})

	// --- GZIP
	gzipCheck := widget.NewCheck("GZIP compressed", nil)
	gzipCheck.SetChecked(false)
	gzipHint := widget.NewLabel("")
	gzipHint.TextStyle = fyne.TextStyle{Italic: true}
	gzipHint.Hide()

	// --- Browse buttons
	btnBrowseRoot := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if u == nil {
				return
			}
			protoRoot.SetText(u.Path())
			prefs.SetString(prefLastProtoRoot, u.Path())
		}, w)

		if last := strings.TrimSpace(protoRoot.Text); last != "" {
			if uri, err := storage.ParseURI("file://" + last); err == nil {
				if lu, ok := uri.(fyne.ListableURI); ok {
					d.SetLocation(lu)
				}
			}
		}
		d.Show()
	})

	btnBrowseProto := widget.NewButtonWithIcon("", theme.ListIcon(), func() {
		root := strings.TrimSpace(protoRoot.Text)
		if root == "" {
			dialog.ShowError(fmt.Errorf("set Proto root first"), w)
			return
		}
		p, err := protopicker.New(w, root, func(absP string) {
			protoFile.SetText(absP)
			loadProtoTypesAndSelect(absP, "")
		})
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		p.Show()
	})

	btnW := btnBrowseRoot.MinSize().Width
	btnH := btnBrowseRoot.MinSize().Height
	if ms := btnBrowseProto.MinSize(); ms.Width > btnW {
		btnW = ms.Width
	}
	if ms := btnBrowseProto.MinSize(); ms.Height > btnH {
		btnH = ms.Height
	}
	btnBrowseRootWrap := container.NewGridWrap(fyne.NewSize(btnW, btnH), btnBrowseRoot)
	btnBrowseProtoWrap := container.NewGridWrap(fyne.NewSize(btnW, btnH), btnBrowseProto)

	lblProtoRoot := widget.NewLabel("Proto root:")
	lblProtoFile := widget.NewLabel("Proto file:")
	lblW := lblProtoRoot.MinSize().Width
	if lblProtoFile.MinSize().Width > lblW {
		lblW = lblProtoFile.MinSize().Width
	}
	lblH := lblProtoRoot.MinSize().Height
	lblProtoRootWrap := container.NewGridWrap(fyne.NewSize(lblW, lblH), lblProtoRoot)
	lblProtoFileWrap := container.NewGridWrap(fyne.NewSize(lblW, lblH), lblProtoFile)

	protoRootRow := container.NewBorder(nil, nil, lblProtoRootWrap, btnBrowseRootWrap, protoRoot)
	protoFileRow := container.NewBorder(nil, nil, lblProtoFileWrap, btnBrowseProtoWrap, protoFile)

	msgTypeRow := container.NewBorder(nil, nil,
		widget.NewLabel("Message type:"),
		container.NewHBox(btnSavePreset, btnLoadPreset),
		typeSelect,
	)

	globalBar := container.NewVBox(
		protoRootRow,
		protoFileRow,
		msgTypeRow,
		typeErr,
		widget.NewSeparator(),
	)

	// Output area: expand/collapse buttons (show/hide output)
	btnToggleOutput := widget.NewButtonWithIcon("", theme.ViewFullScreenIcon(), nil)
	btnToggleOutput.Importance = widget.LowImportance

	btnCollapse := widget.NewButtonWithIcon("", theme.ViewRestoreIcon(), nil)
	btnCollapse.Importance = widget.LowImportance
	btnCollapse.Hide()

	// Overlay buttons (same spot) so we don't reparent output tabs.
	overlayButtons := container.NewStack(btnToggleOutput, btnCollapse)
	btnOverlay := container.NewVBox(
		container.NewBorder(nil, nil, nil, overlayButtons, layout.NewSpacer()),
		layout.NewSpacer(),
	)
	outputStack := container.NewStack(outputContent, btnOverlay)

	// Header removed: search is now above output.

	// Decode button (wiring TODO - placeholder to keep layout stable)
	btnDecode := widget.NewButtonWithIcon("Decode", theme.ViewRefreshIcon(), nil)
	btnDecode.Importance = widget.MediumImportance
	btnDecode.OnTapped = func() {
		lblStatus.SetText("Status: decoding…")
		clearOutput()
		noteTypeError("")

		root := strings.TrimSpace(protoRoot.Text)
		protoAbs := strings.TrimSpace(protoFile.Text)
		typeName := strings.TrimSpace(typeSelect.Selected())
		if root == "" || protoAbs == "" || typeName == "" {
			lblStatus.SetText("Status: error")
			dialog.ShowError(fmt.Errorf("proto root, proto file and message type are required"), w)
			return
		}
		if !isInsideRoot(root, protoAbs) {
			lblStatus.SetText("Status: error")
			dialog.ShowError(fmt.Errorf("proto file must be inside proto root"), w)
			return
		}

		// file cache only for File tab and local path
		fileInputPath := ""
		if sourceTabs.SelectedIndex() == 0 {
			p := strings.TrimSpace(fileTab.InputPath())
			p = normalizeLocalPath(p)
			if p != "" {
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
					fileInputPath = p
				}
			}
		}

		if fileInputPath != "" {
			inFI, _ := os.Stat(fileInputPath)
			protoFI, _ := os.Stat(protoAbs)
			key := cache.FileKey(fileInputPath, protoAbs, typeName, gzipCheck.Checked, inFI, protoFI)
			if cached, ok, _ := dc.Read(key); ok {
				fullJSON = cached
				setOutput(fullJSON)
				lblStatus.SetText("Status: OK (from cache)")
				return
			}
		}

		// Choose source based on active tab.
		var src interface {
			Fetch(context.Context) ([]byte, error)
		}
		// Allow per-tab GZIP flag override (Redis row checkbox).
		effectiveGzip := gzipCheck.Checked
		switch sourceTabs.SelectedIndex() {
		case 0:
			src = fileTab
		case 1:
			src = redisTab
			if gz, ok := any(redisTab).(interface{ Gzip() bool }); ok {
				effectiveGzip = gz.Gzip()
			}
		default:
			lblStatus.SetText("Status: error")
			dialog.ShowError(fmt.Errorf("unknown source tab"), w)
			return
		}

		btnDecode.Disable()
		btnDecode.Refresh()

		go func() {
			defer fyne.Do(func() {
				btnDecode.Enable()
				btnDecode.Refresh()
			})

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			if sourceTabs.SelectedIndex() == 1 {
				fyne.Do(func() {
					lblStatus.SetText("Status: loading from Redis…")
				})
			}

			payload, err := src.Fetch(ctx)
			if err != nil {
				fyne.Do(func() {
					lblStatus.SetText("Status: error")
					dialog.ShowError(err, w)
				})
				return
			}

			// Redis cache based on key + field + content hash (before decode).
			if sourceTabs.SelectedIndex() == 1 {
				key := ""
				field := ""
				db := 0
				if rk, ok := any(redisTab).(interface{ SelectedKey() string }); ok {
					key = strings.TrimSpace(rk.SelectedKey())
				}
				if rf, ok := any(redisTab).(interface{ SelectedField() string }); ok {
					field = strings.TrimSpace(rf.SelectedField())
				}
				if rd, ok := any(redisTab).(interface{ SelectedDB() int }); ok {
					db = rd.SelectedDB()
				}

				if key != "" {
					protoAbs := strings.TrimSpace(protoFile.Text)
					msgType := strings.TrimSpace(typeSelect.Selected())
					keyHash := cache.RedisKey(db, key, field, protoAbs, msgType, gzipCheck.Checked, payload)
					if cached, ok, _ := dc.Read(keyHash); ok {
						fyne.Do(func() {
							fullJSON = cached
							setOutput(fullJSON)
							lblStatus.SetText("Status: OK (from cache)")
						})
						return
					}
				}
			}

			res, err := dec.Decode(ctx, domain.DecodeRequest{
				ProtoRoot: root,
				ProtoFile: protoAbs,
				FullType:  typeName,
				Gzip:      effectiveGzip,
				Format:    domain.OutputFormatJSON,
				Bytes:     payload,
			})
			if err != nil {
				fyne.Do(func() {
					lblStatus.SetText("Status: error")
					dialog.ShowError(err, w)
				})
				return
			}

			// For large JSON skip pretty printing (it can be slow)
			jsonText := res.Raw
			if len(jsonText) <= 512_000 {
				var v any
				if err := json.Unmarshal([]byte(jsonText), &v); err == nil {
					if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
						jsonText = string(pretty)
					}
				}
			}

			// Cache only for File tab
			cachedPath := ""
			if fileInputPath != "" {
				inFI, _ := os.Stat(fileInputPath)
				protoFI, _ := os.Stat(protoAbs)
				key := cache.FileKey(fileInputPath, protoAbs, typeName, gzipCheck.Checked, inFI, protoFI)
				meta := cache.Meta{InputPath: fileInputPath, ProtoFile: protoAbs, MessageType: typeName, Gzip: gzipCheck.Checked}
				if inFI != nil {
					meta.InputSize = inFI.Size()
					meta.InputMtime = inFI.ModTime().UnixNano()
				}
				if protoFI != nil {
					meta.ProtoSize = protoFI.Size()
					meta.ProtoMtime = protoFI.ModTime().UnixNano()
				}
				if p, err := dc.Write(key, meta, jsonText); err == nil {
					cachedPath = p
				}
			}

			// Cache for Redis using content hash.
			if sourceTabs.SelectedIndex() == 1 {
				key := ""
				field := ""
				db := 0
				if rk, ok := any(redisTab).(interface{ SelectedKey() string }); ok {
					key = strings.TrimSpace(rk.SelectedKey())
				}
				if rf, ok := any(redisTab).(interface{ SelectedField() string }); ok {
					field = strings.TrimSpace(rf.SelectedField())
				}
				if rd, ok := any(redisTab).(interface{ SelectedDB() int }); ok {
					db = rd.SelectedDB()
				}
				if key != "" {
					protoAbs := strings.TrimSpace(protoFile.Text)
					msgType := strings.TrimSpace(typeSelect.Selected())
					keyHash := cache.RedisKey(db, key, field, protoAbs, msgType, gzipCheck.Checked, payload)
					meta := cache.Meta{
						InputPath:   "redis://" + key,
						MessageType: msgType,
						ProtoFile:   protoAbs,
						Gzip:        gzipCheck.Checked,
					}
					_, _ = dc.Write(keyHash, meta, jsonText)
				}
			}

			// If it's big - offer: open folder (select file) or show in output.
			const bigJSON = 2_000_000
			if len(jsonText) >= bigJSON && cachedPath != "" {
				fyne.Do(func() {
					lblStatus.SetText("Status: OK (saved to file)")

					// Build dialog content with custom buttons so we can place icons.
					var dlg dialog.Dialog

					btnShow := widget.NewButtonWithIcon("Show in output", theme.VisibilityIcon(), func() {
						fullJSON = jsonText
						setOutput(fullJSON)
						lblStatus.SetText("Status: OK")
						if dlg != nil {
							dlg.Hide()
						}
					})

					// Open folder: single button widget (icon is part of the button).
					btnOpen := widget.NewButtonWithIcon("Open folder", theme.FolderOpenIcon(), func() {
						_ = revealFile(cachedPath)
						if dlg != nil {
							dlg.Hide()
						}
					})
					btnOpen.Importance = widget.HighImportance

					btnCancel := widget.NewButtonWithIcon("Close", theme.CancelIcon(), func() {
						if dlg != nil {
							dlg.Hide()
						}
					})
					btnCancel.Importance = widget.LowImportance

					content := container.NewVBox(
						widget.NewLabel("Output is large. You can show it in the app (may be slower) or open the folder with the saved JSON file."),
						widget.NewSeparator(),
						container.NewHBox(layout.NewSpacer(), btnCancel, btnShow, btnOpen),
					)

					dlg = dialog.NewCustomWithoutButtons("Large output", content, w)
					dlg.Resize(fyne.NewSize(620, 180))
					dlg.Show()
				})
				return
			}

			fyne.Do(func() {
				fullJSON = jsonText
				setOutput(fullJSON)
				lblStatus.SetText("Status: OK")
			})
		}()
	}

	btnCopy := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		w.Clipboard().SetContent(fullJSON)
		lblStatus.SetText("Status: copied")
	})

	btnOpenCache := widget.NewButtonWithIcon("Files", theme.FolderOpenIcon(), func() {
		// Ensure cache dirs exist so 'open folder' always works even before first decode.
		_ = dc.EnsureDirs()

		switch sourceTabs.SelectedIndex() {
		case 0:
			// File tab: open exact cached file if possible.
			p := strings.TrimSpace(fileTab.InputPath())
			p = normalizeLocalPath(p)
			if p == "" {
				_ = openFolder(dc.Dir())
				return
			}
			protoAbs := strings.TrimSpace(protoFile.Text)
			typeName := strings.TrimSpace(typeSelect.Selected())
			if protoAbs == "" || typeName == "" {
				_ = openFolder(dc.Dir())
				return
			}

			inFI, _ := os.Stat(p)
			protoFI, _ := os.Stat(protoAbs)
			key := cache.FileKey(p, protoAbs, typeName, gzipCheck.Checked, inFI, protoFI)
			jsonPath := dc.JSONPath(key)
			if _, err := os.Stat(jsonPath); err == nil {
				_ = revealFile(jsonPath)
				return
			}
			_ = openFolder(dc.Dir())
		case 1:
			// Redis tab: open cache folder; try to select last saved file for selected key.
			base := "redis"
			if rk, ok := any(redisTab).(interface{ SelectedKey() string }); ok {
				if v := strings.TrimSpace(rk.SelectedKey()); v != "" {
					base = v
				}
			}
			// sanitize like in saveLargeOutput
			repl := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
			base = repl.Replace(strings.TrimSpace(base))
			if base == "" {
				_ = openFolder(dc.Dir())
				return
			}
			if len(base) > 80 {
				base = base[:80]
			}
			matches, _ := filepath.Glob(filepath.Join(dc.Dir(), base+"-*.json"))
			if len(matches) == 0 {
				_ = openFolder(dc.Dir())
				return
			}
			// pick newest
			newest := ""
			var newestT time.Time
			for _, m := range matches {
				fi, err := os.Stat(m)
				if err != nil {
					continue
				}
				if newest == "" || fi.ModTime().After(newestT) {
					newest = m
					newestT = fi.ModTime()
				}
			}
			if newest != "" {
				_ = revealFile(newest)
				return
			}
			_ = openFolder(dc.Dir())
		default:
			// Any other tab/state: just open cache folder.
			_ = openFolder(dc.Dir())
		}
	})
	btnOpenCache.Importance = widget.LowImportance

	btnCopy.Importance = widget.LowImportance
	actions := container.NewHBox(lblStatus, layout.NewSpacer(), container.NewHBox(btnDecode, btnCopy, btnOpenCache))

	resultPanel = container.NewBorder(nil, actions, nil, nil, outputStack)

	// Output should take all available space.
	// (searchRow remains above output)

	// Layout: keep outputStack in a single container to avoid reparenting issues.
	contentArea := container.NewBorder(sourceTabs, nil, nil, nil, resultPanel)

	// Root
	normalContent := container.NewBorder(globalBar, nil, nil, nil, contentArea)

	btnToggleOutput.OnTapped = func() {
		isOutputExpanded = !isOutputExpanded
		if isOutputExpanded {
			savedSize = w.Canvas().Size()
			btnToggleOutput.Hide()
			btnCollapse.Show()
			globalBar.Hide()
			sourceTabs.Hide()
			actions.Hide()
			contentArea.Refresh()
			normalContent.Refresh()
		} else {
			btnCollapse.Hide()
			btnToggleOutput.Show()
			globalBar.Show()
			sourceTabs.Show()
			actions.Show()
			contentArea.Refresh()
			normalContent.Refresh()
			if savedSize.Width > 0 && savedSize.Height > 0 {
				w.Resize(savedSize)
			}
		}
	}
	btnCollapse.OnTapped = func() {
		isOutputExpanded = false
		btnCollapse.Hide()
		btnToggleOutput.Show()
		globalBar.Show()
		sourceTabs.Show()
		actions.Show()
		contentArea.Refresh()
		normalContent.Refresh()
		if savedSize.Width > 0 && savedSize.Height > 0 {
			w.Resize(savedSize)
		}
	}

	// Root that we can return
	rootC := normalContent

	// ---- Drag & Drop -> заполняем File tab
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if len(uris) == 0 {
			return
		}

		u := uris[0]
		if u == nil {
			return
		}

		p := u.Path()
		if p == "" {
			p = u.String()
		}
		if p == "" {
			return
		}

		sourceTabs.SelectIndex(0)
		fileTab.SetFilePath(p)
		fileTab.FlashDropHighlight()
	})

	return rootC
}

// (shortcut helpers moved to platform-specific files)
