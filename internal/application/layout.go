package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/internal/application/components/protopicker"
	"github.com/i4erkasov/proto-viewer/internal/application/tab"
	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/protodec"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/protoutil"
)

const prefLastProtoRoot = "lastProtoRoot"

func build(w fyne.Window) fyne.CanvasObject {
	dec := protodec.New()
	prefs := fyne.CurrentApp().Preferences()

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
	typeSelect := widget.NewSelect([]string{}, func(string) {})
	typeSelect.PlaceHolder = "Select message type…"

	selectedType := widget.NewLabel("Selected: (none)")

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
		typeSelect.Options = nil
		typeSelect.SetSelected("")
		typeSelect.Refresh()
		selectedType.SetText("Selected: (none)")
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
	sqlTab := tab.NewTabSQL(w)

	sourceTabs := container.NewAppTabs(
		container.NewTabItem(fileTab.Title(), container.NewBorder(fileTab.View(), nil, nil, nil, nil)),
		container.NewTabItem(redisTab.Title(), container.NewBorder(redisTab.View(), nil, nil, nil, nil)),
		container.NewTabItem(sqlTab.Title(), container.NewBorder(sqlTab.View(), nil, nil, nil, nil)),
	)
	sourceTabs.SetTabLocation(container.TabLocationTop)

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
		if len(typeSelect.Options) == 0 {
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
		case 2:
			src = sqlTab
		default:
			noteTypeError("Unknown source tab")
			return
		}
		if m, ok := src.(interface {
			FetchMany(context.Context) ([][]byte, error)
		}); ok {
			srcMany = m
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
		for _, fullType := range typeSelect.Options {
			allOK := true
			for _, p := range payloads {
				tryCtx, tryCancel := context.WithTimeout(ctx, 900*time.Millisecond)
				_, derr := dec.Decode(tryCtx, domain.DecodeRequest{
					ProtoRoot: root,
					ProtoFile: protoAbs,
					FullType:  fullType,
					Gzip:      gzipCheck.Checked,
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
			typeSelect.Refresh()
			selectedType.SetText("Selected: " + matches[0])
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
				typeSelect.Refresh()
				selectedType.SetText("Selected: " + s)
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
			b, err := os.ReadFile(absP)
			if err != nil {
				dialog.ShowError(err, w)
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
			typeSelect.Options = opts
			typeSelect.SetSelected("")
			typeSelect.Refresh()
			selectedType.SetText("Selected: (none)")
			noteTypeError("")
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
		selectedType,
		widget.NewSeparator(),
	)

	// keep selected label synced
	typeSelect.OnChanged = func(s string) {
		noteTypeError("")
		if strings.TrimSpace(s) == "" {
			selectedType.SetText("Selected: (none)")
			return
		}
		selectedType.SetText("Selected: " + s)
	}

	// ---- Result area
	status := widget.NewLabel("Status: idle")

	jsonOut := widget.NewMultiLineEntry()
	jsonOut.Wrapping = fyne.TextWrapWord
	jsonOut.TextStyle = fyne.TextStyle{Monospace: true}
	jsonOut.SetPlaceHolder("JSON output…")

	rawOut := widget.NewMultiLineEntry()
	rawOut.Wrapping = fyne.TextWrapWord
	rawOut.SetPlaceHolder("RAW output…")

	jsonScroll := container.NewScroll(jsonOut)
	rawScroll := container.NewScroll(rawOut)

	contentStack := container.NewStack(jsonScroll, rawScroll)
	showJSON := func() {
		jsonScroll.Show()
		rawScroll.Hide()
		contentStack.Refresh()
	}
	showRAW := func() {
		rawScroll.Show()
		jsonScroll.Hide()
		contentStack.Refresh()
	}
	showJSON()

	activeIsJSON := true

	btnTabJSON := widget.NewButton("JSON", nil)
	btnTabRAW := widget.NewButton("RAW", nil)
	btnTabJSON.Importance = widget.LowImportance
	btnTabRAW.Importance = widget.LowImportance

	ulJSON := canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	ulRAW := canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	ulH := theme.Size(theme.SizeNameSeparatorThickness)
	if ulH < 2 {
		ulH = 2
	}
	ulJSON.SetMinSize(fyne.NewSize(1, ulH))
	ulRAW.SetMinSize(fyne.NewSize(1, ulH))

	jsonTab := container.NewVBox(btnTabJSON, ulJSON)
	rawTab := container.NewVBox(btnTabRAW, ulRAW)

	setUnderline := func(isJSON bool) {
		if isJSON {
			ulJSON.Show()
			ulRAW.Hide()
		} else {
			ulRAW.Show()
			ulJSON.Hide()
		}
		ulJSON.Refresh()
		ulRAW.Refresh()
	}

	setActiveTab := func(isJSON bool) {
		activeIsJSON = isJSON
		setUnderline(isJSON)
		if isJSON {
			showJSON()
			return
		}
		showRAW()
	}

	btnTabJSON.OnTapped = func() { setActiveTab(true) }
	btnTabRAW.OnTapped = func() { setActiveTab(false) }
	setActiveTab(true)

	// --- Expand/collapse output
	var isOutputExpanded bool
	btnToggleOutput := widget.NewButtonWithIcon("", theme.ViewFullScreenIcon(), nil)
	btnToggleOutput.Importance = widget.LowImportance

	// Строка табов результата: табы слева, toggle справа (в одной линии)
	tabsHeader := container.NewBorder(nil, nil, container.NewHBox(jsonTab, rawTab), btnToggleOutput, nil)

	btnDecode := widget.NewButtonWithIcon("Decode", theme.ViewRefreshIcon(), func() {
		status.SetText("Status: decoding…")
		noteTypeError("")

		root := strings.TrimSpace(protoRoot.Text)
		protoAbs := strings.TrimSpace(protoFile.Text)
		typeName := strings.TrimSpace(typeSelect.Selected)
		if root == "" || protoAbs == "" || typeName == "" {
			status.SetText("Status: error")
			dialog.ShowError(fmt.Errorf("proto root, proto file and message type are required"), w)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var src interface {
			Fetch(context.Context) ([]byte, error)
		}
		switch sourceTabs.SelectedIndex() {
		case 0:
			src = fileTab
		case 1:
			src = redisTab
		case 2:
			src = sqlTab
		default:
			status.SetText("Status: error")
			dialog.ShowError(fmt.Errorf("unknown source tab"), w)
			return
		}

		// Support multi-payload sources.
		payloads := make([][]byte, 0, 1)
		if m, ok := src.(interface {
			FetchMany(context.Context) ([][]byte, error)
		}); ok {
			arr, err := m.FetchMany(ctx)
			if err != nil {
				status.SetText("Status: error")
				dialog.ShowError(err, w)
				return
			}
			payloads = arr
		} else {
			payload, err := src.Fetch(ctx)
			if err != nil {
				status.SetText("Status: error")
				dialog.ShowError(err, w)
				return
			}
			payloads = append(payloads, payload)
		}
		if len(payloads) == 0 {
			status.SetText("Status: error")
			dialog.ShowError(fmt.Errorf("no data"), w)
			return
		}

		// GZIP hint logic expects a single payload; keep it as before,
		// but use the first payload for file-tab heuristics.

		if sourceTabs.SelectedIndex() == 0 {
			if fileTab.LastHTTPWasGzipped() {
				gzipCheck.SetChecked(false)
				gzipCheck.Disable()
				gzipHint.SetText("HTTP gzip decoded")
				gzipHint.Show()
			} else {
				gzipCheck.Enable()
				gzipHint.Hide()
				if fileTab.LastInputLooksGzip() {
					gzipCheck.SetChecked(true)
				}
			}
		} else {
			gzipCheck.Enable()
			gzipHint.Hide()
		}
		gzipHint.Refresh()
		gzipCheck.Refresh()

		// Decode JSON for each payload.
		jsonItems := make([]json.RawMessage, 0, len(payloads))
		autoGzip := false
		for _, p := range payloads {
			jsonRes, err := dec.Decode(ctx, domain.DecodeRequest{
				ProtoRoot: root,
				ProtoFile: protoAbs,
				FullType:  typeName,
				Gzip:      gzipCheck.Checked,
				Format:    domain.OutputFormatJSON,
				Bytes:     p,
			})
			if err != nil {
				status.SetText("Status: error")
				dialog.ShowError(err, w)
				return
			}
			if jsonRes.AutoDetectedGzip {
				autoGzip = true
			}

			// Ensure each element is valid JSON before wrapping into an array.
			b := []byte(jsonRes.Raw)
			var tmp any
			if err := json.Unmarshal(b, &tmp); err != nil {
				status.SetText("Status: error")
				dialog.ShowError(fmt.Errorf("decoded JSON is invalid: %w", err), w)
				return
			}
			jsonItems = append(jsonItems, json.RawMessage(b))
		}

		if autoGzip {
			gzipCheck.SetChecked(true)
			gzipCheck.Refresh()
		}

		if len(jsonItems) == 1 {
			jsonOut.SetText(string(jsonItems[0]))
		} else {
			arrBytes, _ := json.MarshalIndent(jsonItems, "", "  ")
			jsonOut.SetText(string(arrBytes))
		}

		// RAW output: concatenate, separated.
		rawParts := make([]string, 0, len(payloads))
		autoGzip = false
		for i, p := range payloads {
			rawRes, err := dec.Decode(ctx, domain.DecodeRequest{
				ProtoRoot: root,
				ProtoFile: protoAbs,
				FullType:  typeName,
				Gzip:      gzipCheck.Checked,
				Format:    domain.OutputFormatRAW,
				Bytes:     p,
			})
			if err != nil {
				status.SetText("Status: error")
				dialog.ShowError(err, w)
				return
			}
			if rawRes.AutoDetectedGzip {
				autoGzip = true
			}
			if len(payloads) > 1 {
				rawParts = append(rawParts, fmt.Sprintf("#%d\n%s", i+1, rawRes.Raw))
			} else {
				rawParts = append(rawParts, rawRes.Raw)
			}
		}
		if autoGzip {
			gzipCheck.SetChecked(true)
			gzipCheck.Refresh()
		}
		rawOut.SetText(strings.Join(rawParts, "\n\n"))

		status.SetText("Status: OK")
	})

	btnCopy := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		if activeIsJSON {
			w.Clipboard().SetContent(jsonOut.Text)
		} else {
			w.Clipboard().SetContent(rawOut.Text)
		}
		status.SetText("Status: copied")
	})

	btnSave := widget.NewButtonWithIcon("Save…", theme.DocumentSaveIcon(), func() {
		content := jsonOut.Text
		if !activeIsJSON {
			content = rawOut.Text
		}
		if strings.TrimSpace(content) == "" {
			dialog.ShowInformation("Nothing to save", "Output is empty", w)
			return
		}

		d := dialog.NewFileSave(func(uc fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if uc == nil {
				return
			}
			defer func() { _ = uc.Close() }()
			if _, err := uc.Write([]byte(content)); err != nil {
				dialog.ShowError(err, w)
				status.SetText("Status: error")
				return
			}
			status.SetText("Status: saved")
		}, w)

		if activeIsJSON {
			d.SetFileName("output.json")
		} else {
			d.SetFileName("output.txt")
		}
		d.Show()
	})

	// Хедер результата: сначала GZIP сверху, потом табы слева и toggle справа (в одной линии)
	gzipRow := container.NewHBox(layout.NewSpacer(), gzipHint, gzipCheck)
	resultHeader := container.NewVBox(gzipRow, tabsHeader)

	// Bottom actions: status on the left, action buttons on the right.
	actionButtons := container.NewHBox(btnDecode, btnCopy, btnSave)
	actions := container.NewHBox(status, layout.NewSpacer(), actionButtons)

	resultPanel := container.NewBorder(resultHeader, actions, nil, nil, contentStack)

	// Normal layout: source tabs on top, output below
	normalMid := container.NewBorder(sourceTabs, nil, nil, nil, resultPanel)
	normalContent := container.NewBorder(globalBar, nil, nil, nil, normalMid)

	// Focus layout: output only (full window)
	// In expanded mode we still show JSON/RAW tabs; only hide global settings/source selectors.
	btnToggleMin := btnToggleOutput.MinSize()
	btnToggleWrap := container.NewGridWrap(btnToggleMin, btnToggleOutput)
	focusedHeader := container.NewHBox(
		container.NewHBox(jsonTab, rawTab),
		layout.NewSpacer(),
		btnToggleWrap,
	)
	focusedContent := container.NewBorder(focusedHeader, nil, nil, nil, contentStack)

	// Root that we can swap
	rootC := container.NewMax(normalContent)

	btnToggleOutput.OnTapped = func() {
		isOutputExpanded = !isOutputExpanded
		if isOutputExpanded {
			btnToggleOutput.SetIcon(theme.ViewRestoreIcon())
			btnToggleOutput.Refresh()
			rootC.Objects = []fyne.CanvasObject{focusedContent}
		} else {
			btnToggleOutput.SetIcon(theme.ViewFullScreenIcon())
			btnToggleOutput.Refresh()
			rootC.Objects = []fyne.CanvasObject{normalContent}
		}
		rootC.Refresh()
	}

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
			status.SetText("Status: drop ignored (empty path)")
			return
		}

		status.SetText("Status: dropped " + p)
		sourceTabs.SelectIndex(0)
		fileTab.SetFilePath(p)
		fileTab.FlashDropHighlight()
	})

	return rootC
}
