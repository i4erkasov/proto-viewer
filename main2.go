package main

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// Layout-only skeleton: tabs (File/Redis/SQL) + global proto settings + result area (Raw/Pretty).
// No decoding logic here — wire your services later.

func buildUI(w fyne.Window) fyne.CanvasObject {
	// -----------------------------
	// Global / shared controls
	// -----------------------------
	protoRoot := widget.NewEntry()
	protoRoot.SetPlaceHolder("/path/to/protorepo")

	btnBrowseRoot := widget.NewButton("Browse", func() {
		d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if u == nil {
				return
			}
			protoRoot.SetText(u.Path())
		}, w)
		d.Show()
	})

	typeSearch := widget.NewEntry()
	typeSearch.SetPlaceHolder("Search message type…")

	selectedType := widget.NewLabel("Selected: (none)")
	btnPickType := widget.NewButton("Tree…", func() {
		dialog.ShowInformation("TODO", "Open Type Picker dialog (tree + search).", w)
	})

	outputFormat := widget.NewRadioGroup([]string{"Text", "JSON", "RAW"}, func(string) {})
	outputFormat.SetSelected("Text")
	outputFormat.Horizontal = true // ✅ в одну строку

	gzipCheck := widget.NewCheck("GZIP compressed", nil)
	gzipCheck.SetChecked(false) // или true по умолчанию

	btnAutoDetect := widget.NewButton("Auto-detect type", func() {
		dialog.ShowInformation("TODO", "Auto-detect message type from bytes.", w)
	})

	globalBar := container.NewVBox(
		widget.NewLabel("Proto settings"),
		container.NewGridWithColumns(3,
			widget.NewLabel("Proto root:"),
			protoRoot,
			btnBrowseRoot,
		),
		container.NewGridWithColumns(3,
			widget.NewLabel("Message type:"),
			typeSearch,
			btnPickType,
		),
		container.NewHBox(selectedType, layoutSpacer(), btnAutoDetect),
		container.NewHBox(widget.NewLabel("Output:"), outputFormat, layout.NewSpacer(), gzipCheck),

		widget.NewSeparator(),
	)

	// -----------------------------
	// File Tab
	// -----------------------------
	fileMode := widget.NewRadioGroup([]string{"Local file", "URL"}, func(string) {})
	fileMode.SetSelected("Local file")

	filePath := widget.NewEntry()
	filePath.SetPlaceHolder("/path/to/file.bin")

	btnBrowseFile := widget.NewButton("Browse", func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if r == nil {
				return
			}
			filePath.SetText(r.URI().Path())
			_ = r.Close()
		}, w)
		d.Show()
	})

	fileURL := widget.NewEntry()
	fileURL.SetPlaceHolder("https://host/path/file.bin")

	fileTab := container.NewVBox(
		widget.NewLabel("File source"),
		container.NewHBox(widget.NewLabel("Mode:"), fileMode),
		widget.NewSeparator(),
		widget.NewLabel("Local file"),
		container.NewGridWithColumns(3, widget.NewLabel("Path:"), filePath, btnBrowseFile),
		widget.NewSeparator(),
		widget.NewLabel("URL"),
		container.NewGridWithColumns(2, widget.NewLabel("Link:"), fileURL),
		container.NewHBox(widget.NewButton("Download (optional)", func() {
			dialog.ShowInformation("TODO", "Download URL to memory/file.", w)
		})),
		widget.NewSeparator(),
	)

	// -----------------------------
	// Redis Tab
	// -----------------------------
	redisHost := widget.NewEntry()
	redisHost.SetText("127.0.0.1")
	redisPort := widget.NewEntry()
	redisPort.SetText("6379")
	redisDB := widget.NewEntry()
	redisDB.SetText("0")
	redisPass := widget.NewPasswordEntry()
	redisPass.SetPlaceHolder("(optional)")

	redisKey := widget.NewEntry()
	redisKey.SetPlaceHolder("my:protobuf:key")

	redisTab := container.NewVBox(
		widget.NewLabel("Redis source"),
		widget.NewSeparator(),
		widget.NewLabel("Connection"),
		container.NewGridWithColumns(6,
			widget.NewLabel("Host:"), redisHost,
			widget.NewLabel("Port:"), redisPort,
			widget.NewLabel("DB:"), redisDB,
		),
		container.NewGridWithColumns(2, widget.NewLabel("Password:"), redisPass),
		container.NewHBox(widget.NewButton("Test connection", func() {
			dialog.ShowInformation("TODO", "Test Redis connection.", w)
		})),
		widget.NewSeparator(),
		widget.NewLabel("Payload"),
		container.NewGridWithColumns(2, widget.NewLabel("Key:"), redisKey),
	)

	// -----------------------------
	// SQL Tab
	// -----------------------------
	sqlDriver := widget.NewRadioGroup([]string{"Postgres", "MySQL"}, func(string) {})
	sqlDriver.SetSelected("Postgres")

	sqlHost := widget.NewEntry()
	sqlHost.SetText("localhost")
	sqlPort := widget.NewEntry()
	sqlPort.SetText("5432")
	sqlDB := widget.NewEntry()
	sqlDB.SetPlaceHolder("database")
	sqlUser := widget.NewEntry()
	sqlUser.SetPlaceHolder("user")
	sqlPass := widget.NewPasswordEntry()
	sqlPass.SetPlaceHolder("password")

	sqlTable := widget.NewSelect([]string{}, func(string) {})
	sqlTable.PlaceHolder = "Select table…"

	sqlColumn := widget.NewSelect([]string{}, func(string) {})
	sqlColumn.PlaceHolder = "Select payload column…"

	whereField := widget.NewEntry()
	whereField.SetPlaceHolder("id")
	whereOp := widget.NewSelect([]string{"=", "!=", ">", ">=", "<", "<=", "LIKE"}, func(string) {})
	whereOp.SetSelected("=")
	whereVal := widget.NewEntry()
	whereVal.SetPlaceHolder("123")

	sqlTab := container.NewVBox(
		widget.NewLabel("SQL source"),
		widget.NewSeparator(),
		container.NewHBox(widget.NewLabel("Driver:"), sqlDriver),
		widget.NewSeparator(),
		widget.NewLabel("Connection"),
		container.NewGridWithColumns(6,
			widget.NewLabel("Host:"), sqlHost,
			widget.NewLabel("Port:"), sqlPort,
			widget.NewLabel("DB:"), sqlDB,
		),
		container.NewGridWithColumns(4,
			widget.NewLabel("User:"), sqlUser,
			widget.NewLabel("Password:"), sqlPass,
		),
		container.NewHBox(widget.NewButton("Test connection", func() {
			dialog.ShowInformation("TODO", "Test DB connection.", w)
		})),
		widget.NewSeparator(),
		widget.NewLabel("Select"),
		container.NewGridWithColumns(4,
			widget.NewLabel("Table:"), sqlTable,
			widget.NewLabel("Column:"), sqlColumn,
		),
		widget.NewSeparator(),
		widget.NewLabel("Filter (WHERE)"),
		container.NewGridWithColumns(6,
			widget.NewLabel("Field:"), whereField,
			widget.NewLabel("Op:"), whereOp,
			widget.NewLabel("Value:"), whereVal,
		),
		container.NewHBox(
			widget.NewButton("+ Add filter (AND)", func() {
				dialog.ShowInformation("TODO", "Add multiple WHERE filters.", w)
			}),
		),
		widget.NewSeparator(),
	)

	// -----------------------------
	// Tabs container
	// -----------------------------
	sourceTabs := container.NewAppTabs(
		container.NewTabItem("File", container.NewScroll(fileTab)),
		container.NewTabItem("Redis", container.NewScroll(redisTab)),
		container.NewTabItem("SQL", container.NewScroll(sqlTab)),
	)

	// -----------------------------
	// Result area: Raw / Pretty
	// -----------------------------
	rawOut := widget.NewMultiLineEntry()
	rawOut.Wrapping = fyne.TextWrapWord
	rawOut.SetPlaceHolder("Raw output… (copy-friendly)")

	prettyOut := widget.NewRichTextFromMarkdown("```json\n\n```")
	prettyOut.Wrapping = fyne.TextWrapWord

	resultTabs := container.NewAppTabs(
		container.NewTabItem("Raw", container.NewScroll(rawOut)),
		container.NewTabItem("Pretty", container.NewScroll(prettyOut)),
	)

	// Bottom action bar
	status := widget.NewLabel("Status: idle")
	btnDecode := widget.NewButton("Decode", func() {
		dialog.ShowInformation("TODO", "Fetch bytes from active tab, decode, fill Raw/Pretty.", w)
	})
	btnCopy := widget.NewButton("Copy", func() {
		w.Clipboard().SetContent(rawOut.Text)
		status.SetText("Status: copied")
	})
	btnSave := widget.NewButton("Save…", func() {
		dialog.ShowInformation("TODO", "Save current output to file.", w)
	})

	actions := container.NewHBox(btnDecode, btnCopy, btnSave, layout.NewSpacer(), status)

	// tabs сверху
	sourceTabs.SetTabLocation(container.TabLocationTop)

	// Result panel (tabs + actions)
	resultPanel := container.NewBorder(
		nil,
		actions,
		nil,
		nil,
		resultTabs,
	)

	// ✅ ВАЖНО: сплитим только "вкладки источника" и "результат"
	midSplit := container.NewVSplit(sourceTabs, resultPanel)
	midSplit.Offset = 0.60 // больше => больше места вкладкам, меньше => больше месту результата

	// ✅ Proto settings — фиксированно сверху, не участвует в сплите
	content := container.NewBorder(
		globalBar, // top
		nil,       // bottom
		nil,       // left
		nil,       // right
		midSplit,  // center
	)

	w.SetContent(content)
	w.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		if len(uris) == 0 {
			return
		}

		u := uris[0]
		path := u.Path()
		if path == "" {
			return
		}

		// Проверяем что активна вкладка File
		if sourceTabs.Selected() != nil && sourceTabs.Selected().Text != "File" {
			return
		}

		// Переключаем режим
		fileMode.SetSelected("Local file")
		filePath.SetText(path)

		// Если файл .gz — включаем gzip
		if strings.HasSuffix(strings.ToLower(path), ".gz") {
			gzipCheck.SetChecked(true)
		}
	})

	return content
}

// Simple spacer without importing fyne/layout explicitly in this snippet.
func layoutSpacer() fyne.CanvasObject { return widget.NewLabel("") }

func main() {
	a := app.New()
	w := a.NewWindow("Proto Inspector")
	w.Resize(fyne.NewSize(1100, 780))
	w.SetContent(buildUI(w))
	w.ShowAndRun()

}
