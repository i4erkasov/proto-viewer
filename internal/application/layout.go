package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/i4erkasov/proto-viewer/internal/application/widgets/jsonmarkdown"
	"github.com/i4erkasov/proto-viewer/internal/application/widgets/jsontree"
	"github.com/i4erkasov/proto-viewer/internal/application/widgets/protopicker"
	"github.com/i4erkasov/proto-viewer/internal/application/widgets/searchselect"

	"github.com/i4erkasov/proto-viewer/internal/application/tab"
	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/protodec"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/protoutil"
)

const prefLastProtoRoot = "lastProtoRoot"

func build(w fyne.Window) fyne.CanvasObject {
	dec := protodec.New()
	prefs := fyne.CurrentApp().Preferences()

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

	// Keep full outputs in memory (UI shows preview for large payloads).
	var fullJSON string

	// Output widget (JSON tree + markdown)
	jsonTree := jsontree.NewSearchableJSONTree()
	jsonMarkdown := jsonmarkdown.NewJSONMarkdownView()

	outTree := jsonTree.View()
	outMarkdown := jsonMarkdown.View()
	searchWrap := jsonTree.SearchBar()

	// Let header decide width; keep same behavior as before.
	jsonTree.SetSearchWidth(420)

	var resultPanel *fyne.Container
	var isOutputExpanded bool

	outputTabs := container.NewAppTabs(
		container.NewTabItem("JSON", outMarkdown),
		container.NewTabItem("Tree", outTree),
	)
	outputTabs.SetTabLocation(container.TabLocationTop)

	isTreeTab := func() bool {
		return outputTabs.SelectedIndex() == 1
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

	registerSearchShortcuts(w.Canvas(), setSearchVisible, func() bool { return jsonTree.SearchVisible() })
	setSearchVisible(false)

	setOutput := func(s string) {
		fullJSON = s
		jsonTree.SetJSON(s)
		jsonMarkdown.SetJSON(s)

		// If we have output to show, grow window height a bit (once per setOutput call)
		// so users can see more lines without manual resize.
		fyne.Do(func() {
			cs := w.Canvas().Size()
			if cs.Height <= 0 {
				return
			}
			targetH := cs.Height + 300
			if targetH > 940 {
				targetH = 940
			}
			if targetH > cs.Height {
				w.Resize(fyne.NewSize(cs.Width, targetH))
			}
		})
	}

	clearOutput := func() {
		fullJSON = ""
		jsonTree.SetJSON("")
		jsonMarkdown.SetJSON("")
	}

	// ---- Decode output cache (File tab / local files only)
	type decodeCacheMeta struct {
		InputPath   string `json:"inputPath"`
		InputSize   int64  `json:"inputSize"`
		InputMtime  int64  `json:"inputMtime"`
		ProtoFile   string `json:"protoFile"`
		ProtoSize   int64  `json:"protoSize"`
		ProtoMtime  int64  `json:"protoMtime"`
		MessageType string `json:"messageType"`
		Gzip        bool   `json:"gzip"`
		Key         string `json:"key"`
		CreatedAt   int64  `json:"createdAt"`
	}

	normalizeLocalPath := func(s string) string {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "file://") {
			s = strings.TrimPrefix(s, "file://")
		}
		return s
	}

	workDir := func() string {
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

	cacheDir := func() string {
		return filepath.Join(workDir(), "decode")
	}
	cacheMetaDir := func() string {
		return filepath.Join(cacheDir(), "meta")
	}

	cachePaths := func(key string) (jsonPath, metaPath string) {
		jsonPath = filepath.Join(cacheDir(), key+".json")
		metaPath = filepath.Join(cacheMetaDir(), key+".meta.json")
		return
	}

	ensureDir := func(dir string) error {
		if dir == "" {
			return fmt.Errorf("empty dir")
		}
		return os.MkdirAll(dir, 0o755)
	}

	readCacheIfFresh := func(key string) (jsonText string, ok bool, _ error) {
		jsonPath, metaPath := cachePaths(key)
		mb, err := os.ReadFile(metaPath)
		if err != nil {
			return "", false, nil
		}
		var meta decodeCacheMeta
		if err := json.Unmarshal(mb, &meta); err != nil {
			return "", false, nil
		}
		if meta.Key != key {
			return "", false, nil
		}
		jb, err := os.ReadFile(jsonPath)
		if err != nil {
			return "", false, nil
		}
		return string(jb), true, nil
	}

	writeCache := func(key string, meta decodeCacheMeta, jsonText string) (jsonPath string, _ error) {
		if err := ensureDir(cacheDir()); err != nil {
			return "", err
		}
		if err := ensureDir(cacheMetaDir()); err != nil {
			return "", err
		}
		jsonPath, metaPath := cachePaths(key)
		meta.Key = key
		meta.CreatedAt = time.Now().Unix()
		metaBytes, _ := json.MarshalIndent(meta, "", "  ")

		writeOne := func(path string, content []byte) error {
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, content, 0o644); err != nil {
				return err
			}
			return os.Rename(tmp, path)
		}
		if err := writeOne(jsonPath, []byte(jsonText)); err != nil {
			return "", err
		}
		if err := writeOne(metaPath, metaBytes); err != nil {
			return "", err
		}
		return jsonPath, nil
	}

	// Try to reveal a file in OS file manager (select the file).
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
			// Most linux file managers don't have stable select flag; open folder.
			return openFolder(p)
		}
	}

	cacheKey := func(inputPath, protoFileAbs, msgType string, gzip bool, inFI, protoFI os.FileInfo) string {
		h := sha256.New()
		_, _ = h.Write([]byte(inputPath))
		_, _ = h.Write([]byte("\n"))
		_, _ = h.Write([]byte(protoFileAbs))
		_, _ = h.Write([]byte("\n"))
		_, _ = h.Write([]byte(msgType))
		_, _ = h.Write([]byte("\n"))
		if gzip {
			_, _ = h.Write([]byte("gzip=1\n"))
		} else {
			_, _ = h.Write([]byte("gzip=0\n"))
		}
		if inFI != nil {
			_, _ = h.Write([]byte(fmt.Sprintf("in_mtime=%d in_size=%d\n", inFI.ModTime().UnixNano(), inFI.Size())))
		}
		if protoFI != nil {
			_, _ = h.Write([]byte(fmt.Sprintf("proto_mtime=%d proto_size=%d\n", protoFI.ModTime().UnixNano(), protoFI.Size())))
		}
		sum := h.Sum(nil)
		return fmt.Sprintf("%x", sum)
	}

	redisCacheKey := func(db int, key, field, protoFileAbs, msgType string, gzip bool, payload []byte) string {
		h := sha256.New()
		_, _ = h.Write([]byte(fmt.Sprintf("redis-db=%d\n", db)))
		_, _ = h.Write([]byte("redis-key=" + key + "\n"))
		_, _ = h.Write([]byte("redis-field=" + field + "\n"))
		_, _ = h.Write([]byte("proto-file=" + protoFileAbs + "\n"))
		_, _ = h.Write([]byte("msg-type=" + msgType + "\n"))
		if gzip {
			_, _ = h.Write([]byte("gzip=1\n"))
		} else {
			_, _ = h.Write([]byte("gzip=0\n"))
		}
		if len(payload) > 0 {
			sum := sha256.Sum256(payload)
			_, _ = h.Write([]byte(fmt.Sprintf("payload=%x\n", sum)))
		}
		return fmt.Sprintf("%x", h.Sum(nil))
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
	typeSelect := searchselect.NewSearchableSelect(w, "Select message type…", []string{})

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

	resetProtoSelection := func() {
		protoFile.SetText("")
		typeSelect.SetOptions(nil)
		typeSelect.SetSelected("")
		noteTypeError("")
	}

	protoRoot.OnChanged = func(s string) {
		prefs.SetString(prefLastProtoRoot, strings.TrimSpace(s))
		if strings.TrimSpace(protoFile.Text) == "" {
			return
		}
		if !isInsideRoot(s, protoFile.Text) {
			resetProtoSelection()
		}
	}

	// --- GZIP
	gzipCheck := widget.NewCheck("GZIP compressed", nil)
	gzipCheck.SetChecked(false)
	gzipHint := widget.NewLabel("")
	gzipHint.TextStyle = fyne.TextStyle{Italic: true}
	gzipHint.Hide()

	// ---- Source tabs (File/Redis/SQL)
	fileTab := tab.NewTabFile(w)
	redisTab := tab.NewTabRedis(w)

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
	btnAutoDetect := widget.NewButton("Auto-detect type", func() {
		noteTypeError("")

		root := strings.TrimSpace(protoRoot.Text)
		protoAbs := strings.TrimSpace(protoFile.Text)
		if root == "" {
			noteTypeError("Proto root is not set")
			return
		}
		if protoAbs == "" {
			noteTypeError("Proto file is not selected")
			return
		}
		if len(typeSelect.Options()) == 0 {
			noteTypeError("No message types found in the selected .proto")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Fetch bytes from active source, supporting multi-payload sources.
		var src interface {
			Fetch(context.Context) ([]byte, error)
		}
		var srcMany interface {
			FetchMany(context.Context) ([][]byte, error)
		}

		switch sourceTabs.SelectedIndex() {
		case 0:
			src = fileTab
		case 1:
			src = redisTab
		default:
			noteTypeError("Unknown source tab")
			return
		}
		// Effective gzip can be overridden by the active tab (Redis).
		effectiveGzip := gzipCheck.Checked
		if sourceTabs.SelectedIndex() == 1 {
			if gz, ok := any(redisTab).(interface{ Gzip() bool }); ok {
				effectiveGzip = gz.Gzip()
			}
		}

		payloads := make([][]byte, 0, 1)
		if srcMany != nil {
			arr, err := srcMany.FetchMany(ctx)
			if err != nil {
				noteTypeError("Failed to read input bytes: " + err.Error())
				return
			}
			payloads = arr
		} else {
			payload, err := src.Fetch(ctx)
			if err != nil {
				noteTypeError("Failed to read input bytes: " + err.Error())
				return
			}
			payloads = append(payloads, payload)
		}
		if len(payloads) == 0 {
			noteTypeError("No data")
			return
		}

		// Try each type and collect all successful decodes (all payloads must decode).
		matches := make([]string, 0, 8)
		for _, fullType := range typeSelect.Options() {
			allOK := true
			for _, p := range payloads {
				tryCtx, tryCancel := context.WithTimeout(ctx, 900*time.Millisecond)
				_, derr := dec.Decode(tryCtx, domain.DecodeRequest{
					ProtoRoot: root,
					ProtoFile: protoAbs,
					FullType:  fullType,
					Gzip:      effectiveGzip,
					Format:    domain.OutputFormatJSON,
					Bytes:     p,
				})
				tryCancel()
				if derr != nil {
					allOK = false
					break
				}
			}
			if allOK {
				matches = append(matches, fullType)
			}
		}

		if len(matches) == 0 {
			noteTypeError("Couldn't auto-detect message type (no type could decode the payload)")
			return
		}

		// If there is exactly one match, apply it directly.
		if len(matches) == 1 {
			typeSelect.SetSelected(matches[0])
			return
		}

		// Show modal with all candidates so user can choose the correct one.
		// Use a Select widget for a simple list experience.
		sel := widget.NewSelect(matches, func(string) {})
		sel.PlaceHolder = "Choose a detected type…"

		dlg := dialog.NewCustomConfirm(
			"Auto-detect: choose type",
			"Use",
			"Cancel",
			container.NewVBox(
				widget.NewLabel(fmt.Sprintf("Found %d matching message types:", len(matches))),
				sel,
			),
			func(ok bool) {
				if !ok {
					return
				}
				s := strings.TrimSpace(sel.Selected)
				if s == "" {
					noteTypeError("Please select one of the detected message types")
					return
				}
				typeSelect.SetSelected(s)
			},
			w,
		)
		dlg.Resize(fyne.NewSize(520, 220))
		dlg.Show()
	})

	lblProtoRoot := widget.NewLabel("Proto root:")
	lblProtoFile := widget.NewLabel("Proto file:")
	lblW := lblProtoRoot.MinSize().Width
	for _, l := range []*widget.Label{lblProtoFile} {
		if l.MinSize().Width > lblW {
			lblW = l.MinSize().Width
		}
	}
	lblH := lblProtoRoot.MinSize().Height
	lblProtoRootWrap := container.NewGridWrap(fyne.NewSize(lblW, lblH), lblProtoRoot)
	lblProtoFileWrap := container.NewGridWrap(fyne.NewSize(lblW, lblH), lblProtoFile)

	// Browse buttons
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

		// try open at last root
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

			// Parse types in background (big proto files can be slow).
			lblStatus.SetText("Status: parsing proto…")

			go func(path string) {
				fi, err := os.Stat(path)
				if err != nil {
					fyne.Do(func() {
						lblStatus.SetText("Status: error")
						dialog.ShowError(err, w)
					})
					return
				}

				// cache lookup
				protoTypeCache.mu.Lock()
				ce, ok := protoTypeCache.items[path]
				protoTypeCache.mu.Unlock()
				if ok && ce.mtime == fi.ModTime().Unix() && ce.size == fi.Size() {
					fyne.Do(func() {
						lblStatus.SetText("Status: OK")
						typeSelect.SetOptions(ce.opts)
						typeSelect.SetSelected("")
						noteTypeError("")
					})
					return
				}

				b, err := os.ReadFile(path)
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
				protoTypeCache.items[path] = protoTypeCacheEntry{mtime: fi.ModTime().Unix(), size: fi.Size(), opts: opts}
				protoTypeCache.mu.Unlock()

				fyne.Do(func() {
					lblStatus.SetText("Status: OK")
					typeSelect.SetOptions(opts)
					typeSelect.SetSelected("")
					noteTypeError("")
				})
			}(absP)
		})
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		p.Show()
	})

	// Wrap icon buttons into a fixed size so they don't stretch/shrink oddly in Border layout.
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

	protoRootRow := container.NewBorder(nil, nil, lblProtoRootWrap, btnBrowseRootWrap, protoRoot)
	protoFileRow := container.NewBorder(nil, nil, lblProtoFileWrap, btnBrowseProtoWrap, protoFile)

	// Message type row: label + select + button all in one line.
	msgTypeRow := container.NewBorder(nil, nil,
		widget.NewLabel("Message type:"),
		btnAutoDetect,
		typeSelect,
	)

	globalBar := container.NewVBox(
		widget.NewLabel("Proto settings"),
		protoRootRow,
		protoFileRow,
		msgTypeRow,
		typeErr,
		widget.NewSeparator(),
	)

	// ---- Result area
	// remove unused local status label (we use lblStatus already)

	// Expand/collapse output
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
	searchOverlay := container.NewVBox(
		container.NewPadded(searchWrap),
		layout.NewSpacer(),
	)
	outputStack := container.NewStack(outputTabs, btnOverlay, searchOverlay)

	// Header removed: search is now overlaid on output.

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
			key := cacheKey(fileInputPath, protoAbs, typeName, gzipCheck.Checked, inFI, protoFI)
			if cached, ok, _ := readCacheIfFresh(key); ok {
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
					keyHash := redisCacheKey(db, key, field, protoAbs, msgType, gzipCheck.Checked, payload)
					if cached, ok, _ := readCacheIfFresh(keyHash); ok {
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
				key := cacheKey(fileInputPath, protoAbs, typeName, gzipCheck.Checked, inFI, protoFI)
				meta := decodeCacheMeta{InputPath: fileInputPath, ProtoFile: protoAbs, MessageType: typeName, Gzip: gzipCheck.Checked}
				if inFI != nil {
					meta.InputSize = inFI.Size()
					meta.InputMtime = inFI.ModTime().UnixNano()
				}
				if protoFI != nil {
					meta.ProtoSize = protoFI.Size()
					meta.ProtoMtime = protoFI.ModTime().UnixNano()
				}
				if p, err := writeCache(key, meta, jsonText); err == nil {
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
					keyHash := redisCacheKey(db, key, field, protoAbs, msgType, gzipCheck.Checked, payload)
					meta := decodeCacheMeta{
						InputPath:   "redis://" + key,
						MessageType: msgType,
						ProtoFile:   protoAbs,
						Gzip:        gzipCheck.Checked,
					}
					_, _ = writeCache(keyHash, meta, jsonText)
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
		_ = ensureDir(cacheDir())
		_ = ensureDir(cacheMetaDir())

		switch sourceTabs.SelectedIndex() {
		case 0:
			// File tab: open exact cached file if possible.
			p := strings.TrimSpace(fileTab.InputPath())
			p = normalizeLocalPath(p)
			if p == "" {
				_ = openFolder(cacheDir())
				return
			}
			protoAbs := strings.TrimSpace(protoFile.Text)
			typeName := strings.TrimSpace(typeSelect.Selected())
			if protoAbs == "" || typeName == "" {
				_ = openFolder(cacheDir())
				return
			}

			inFI, _ := os.Stat(p)
			protoFI, _ := os.Stat(protoAbs)
			key := cacheKey(p, protoAbs, typeName, gzipCheck.Checked, inFI, protoFI)
			jsonPath, _ := cachePaths(key)
			if _, err := os.Stat(jsonPath); err == nil {
				_ = revealFile(jsonPath)
				return
			}
			_ = openFolder(cacheDir())
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
				_ = openFolder(cacheDir())
				return
			}
			if len(base) > 80 {
				base = base[:80]
			}
			matches, _ := filepath.Glob(filepath.Join(cacheDir(), base+"-*.json"))
			if len(matches) == 0 {
				_ = openFolder(cacheDir())
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
			_ = openFolder(cacheDir())
		default:
			// Any other tab/state: just open cache folder.
			_ = openFolder(cacheDir())
		}
	})
	btnOpenCache.Importance = widget.LowImportance

	actions := container.NewHBox(lblStatus, layout.NewSpacer(), container.NewHBox(btnDecode, btnCopy, btnOpenCache))

	// Output should take all available space.
	resultPanel = container.NewBorder(nil, actions, nil, nil, outputStack)

	outputTabs.OnSelected = func(_ *container.TabItem) {
		if !isTreeTab() {
			setSearchVisible(false)
		}
	}

	// Layout: keep outputStack in a single container to avoid reparenting issues.
	contentArea := container.NewBorder(sourceTabs, nil, nil, nil, resultPanel)

	// Root
	normalContent := container.NewBorder(globalBar, nil, nil, nil, contentArea)

	btnToggleOutput.OnTapped = func() {
		isOutputExpanded = !isOutputExpanded
		if isOutputExpanded {
			setSearchVisible(false)
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
