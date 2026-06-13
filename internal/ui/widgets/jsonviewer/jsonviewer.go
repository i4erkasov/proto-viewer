//go:build darwin || linux || windows

package jsonviewer

import (
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/searchselect"
)

const searchKeyPrompt = "Select key"

// JSONView renders JSON as a virtualized, syntax-highlighted grid.
type JSONView struct {
	mu        sync.Mutex
	viewLines []int

	// Виртуализация вьюпорта: рендерим только видимое окно строк.
	winStart int     // индекс первой строки текущего окна в viewLines
	lineH    float32 // высота одной строки (метрика TextGrid)
	cellW    float32 // ширина одного символа (моноширинный шрифт)
	contentW float32 // полная ширина контента для горизонтального скролла

	foldRanges map[int]int
	folded     map[int]bool

	tgrid   *widget.TextGrid
	overlay *tapOverlay
	scroll  *container.Scroll
	content *fyne.Container
	win     fyne.Window

	// OnScrolled вызывается при прокрутке (для синхронной прокрутки в Diff).
	OnScrolled func(fyne.Position)

	fullBuf *JSONBuffer

	searchEntry      *escEntry
	searchUp         *widget.Button
	searchDown       *widget.Button
	searchUpWrap     *fyne.Container
	searchDownWrap   *fyne.Container
	searchNavWrap    *fyne.Container
	searchEntryWrap  *fyne.Container
	searchWrap       *fyne.Container
	searchStructChk  *widget.Check
	searchStructWrap *fyne.Container
	hideOnlyMatches  bool
	searchWidth      float32
	searchQuery      string
	matchLines       []int
	matchIndex       int
	highlights       map[int][]highlightRange
	searchStructural bool

	selectedKeyLine    int
	selectedKeyRange   highlightRange
	selectedKeyValue   string
	selectedValueLine  int
	selectedValueRange highlightRange
	selectedValueText  string

	// Свободное выделение текста мышью (как в редакторе). Координаты —
	// абсолютные: строка в viewLines и рунный индекс в КОНТЕНТЕ строки (без
	// префикса с номером строки). anchor — точка нажатия, cur — текущий конец.
	selActive    bool
	selecting    bool
	selAnchorRow int
	selAnchorCol int
	selCurRow    int
	selCurCol    int

	// diffLines: фон по индексу исходной строки (режим Diff).
	diffLines map[int]color.Color

	searchKeySelect  *searchselect.SearchableSelect
	searchKeyWidth   float32
	searchKeys       []string
	searchKeyIndex   map[string][]int
	searchAll        []int
	searchKeyRanges  map[string]keyRange
	searchKeyFold    map[string]int
	searchSeq        uint64
	searchMatchSet   map[int]struct{}
	trigramIndex     map[[3]byte]trigramRange
	trigramPostings  []int32
	trigramEnabled   bool
	trigramCapBytes  int
	trigramUsedBytes int
	lineNumWidth     int
	debounceMu       sync.Mutex
	debounceTimer    *time.Timer
	debounceQuery    string
}

// highlightRange represents a range of text to be highlighted.
type highlightRange struct {
	start int
	end   int
}

type keyRange struct {
	start int
	end   int
}

// NewJSONMarkdownView creates a markdown view with lazy loading.
func NewJSONMarkdownView(win fyne.Window) *JSONView {
	v := &JSONView{}
	v.selectedKeyLine = -1
	v.selectedValueLine = -1
	v.win = win
	v.tgrid = widget.NewTextGrid()
	v.overlay = newTapOverlay(v.handleTap, v.handleSecondaryTap)
	v.overlay.onDragStart = v.beginTextSelection
	v.overlay.onDrag = v.updateTextSelection
	v.overlay.onDragEnd = v.endTextSelection
	v.probeMetrics()
	// Кастомный layout имитирует полную высоту контента, а в TextGrid лежит
	// только видимое окно строк (виртуализация вьюпорта).
	gridStack := container.NewStack(v.tgrid, v.overlay)
	v.content = container.New(&gridWindowLayout{v: v}, gridStack)
	v.scroll = container.NewScroll(v.content)
	v.scroll.OnScrolled = func(_ fyne.Position) {
		v.onScroll()
	}

	v.searchEntry = newEscEntry()
	v.searchEntry.SetPlaceHolder("Search output")
	v.searchEntry.OnChanged = v.onSearchChanged
	v.searchEntry.OnSubmitted = func(_ string) {
		v.navigateMatch(1)
	}
	v.searchEntry.SetOnEsc(func() {
		if v.SearchVisible() {
			v.SetSearchVisible(false)
		}
	})
	v.searchEntry.SetOnFind(func() {
		if v.SearchVisible() {
			v.SetSearchVisible(false)
		}
	})

	v.searchKeySelect = searchselect.NewSearchableSelect(win, searchKeyPrompt, nil, true)
	v.searchKeySelect.SetTextStyle(fyne.TextStyle{})
	v.searchKeySelect.SetMinWidth(200)
	v.searchKeySelect.OnChanged = func(keys []string) {
		selected := normalizeKeys(keys)
		v.mu.Lock()
		v.searchKeys = selected
		v.mu.Unlock()
		v.applyKeyFilterKeys(selected)
		v.applySearchAsync(v.searchEntry.Text)
	}
	v.searchKeySelect.SetSelectedValues(nil)
	v.searchKeyWidth = 200

	v.searchStructChk = widget.NewCheck("Only matches", func(checked bool) {
		v.SetSearchStructural(checked)
	})
	v.searchStructChk.SetChecked(false)
	v.searchStructural = false
	v.searchStructWrap = container.NewGridWrap(v.searchStructChk.MinSize(), v.searchStructChk)

	v.searchUp = widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
		v.navigateMatch(-1)
	})
	v.searchDown = widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
		v.navigateMatch(1)
	})
	v.searchUp.Importance = widget.LowImportance
	v.searchDown.Importance = widget.LowImportance
	btnH := v.searchEntry.MinSize().Height
	btnW := btnH
	if ms := v.searchUp.MinSize(); ms.Width > btnW {
		btnW = ms.Width
	}
	if ms := v.searchDown.MinSize(); ms.Width > btnW {
		btnW = ms.Width
	}
	v.searchUpWrap = container.NewGridWrap(fyne.NewSize(btnW, btnH), v.searchUp)
	v.searchDownWrap = container.NewGridWrap(fyne.NewSize(btnW, btnH), v.searchDown)
	v.searchUp.Disable()
	v.searchDown.Disable()

	navRow := container.NewGridWithColumns(2, v.searchUpWrap, v.searchDownWrap)
	v.searchNavWrap = container.NewGridWrap(fyne.NewSize(btnW*2, btnH), navRow)

	entryH := v.searchEntry.MinSize().Height
	entryPad := container.NewGridWrap(fyne.NewSize(btnW*2, entryH), layout.NewSpacer())
	entryLayer := container.NewBorder(nil, nil, nil, entryPad, v.searchEntry)
	v.searchEntryWrap = container.NewStack(entryLayer, container.NewBorder(nil, nil, nil, v.searchNavWrap, layout.NewSpacer()))

	v.searchWrap = container.NewGridWrap(
		fyne.NewSize(500, v.searchEntry.MinSize().Height),
		container.NewHBox(layout.NewSpacer(), v.searchKeySelect, v.searchEntryWrap),
	)
	v.searchWidth = 500
	v.searchWrap.Hide()
	v.SetSearchWidth(v.searchWidth)

	return v
}

// View returns the scrollable markdown view.
func (v *JSONView) View() fyne.CanvasObject {
	return v.scroll
}

// SearchBar returns the search UI container.
func (v *JSONView) SearchBar() fyne.CanvasObject {
	return v.searchWrap
}

// SearchEntry exposes the search input for focus management.
func (v *JSONView) SearchEntry() *widget.Entry {
	return &v.searchEntry.Entry
}

// SetSearchStructural toggles structural search mode (show only matched branches).
func (v *JSONView) SetSearchStructural(enabled bool) {
	v.mu.Lock()
	v.searchStructural = enabled
	query := v.searchQuery
	keys := append([]string(nil), v.searchKeys...)
	v.mu.Unlock()

	if enabled && strings.TrimSpace(query) != "" {
		v.applySearchAsync(query)
		return
	}
	v.applyKeyFilterKeys(keys)
}

// SetSearchWidth sets a fixed width for the search input wrapper.
func (v *JSONView) SetSearchWidth(w float32) {
	if w <= 0 {
		return
	}
	v.searchWidth = w
	btnW := theme.Padding() * 2
	if v.searchNavWrap != nil {
		btnW += v.searchNavWrap.MinSize().Width
	}
	avail := w
	keyW := v.searchKeyWidth
	if v.searchKeySelect != nil {
		if ms := v.searchKeySelect.MinSize(); ms.Width > 0 {
			keyW = ms.Width
		}
	}
	entryW := avail - keyW
	minEntryW := v.searchEntry.MinSize().Width
	if entryW < minEntryW {
		entryW = minEntryW
		keyW = avail - entryW
		if keyW < 80 {
			keyW = 80
		}
	}
	if entryW < 0 {
		entryW = minEntryW
	}

	keyWrap := container.NewGridWrap(
		fyne.NewSize(keyW, v.searchEntry.MinSize().Height),
		v.searchKeySelect,
	)

	entryH := v.searchEntry.MinSize().Height
	padW := float32(0)
	if v.searchNavWrap != nil {
		padW = v.searchNavWrap.MinSize().Width
	}
	entryPad := container.NewGridWrap(fyne.NewSize(padW, entryH), layout.NewSpacer())
	entryLayer := container.NewBorder(nil, nil, nil, entryPad, v.searchEntry)
	entryStack := container.NewStack(entryLayer, container.NewBorder(nil, nil, nil, v.searchNavWrap, layout.NewSpacer()))
	entryWrap := container.NewGridWrap(fyne.NewSize(entryW, entryH), entryStack)

	searchRow := container.NewHBox(layout.NewSpacer(), keyWrap, entryWrap)
	rowH := entryH
	searchRow.Resize(fyne.NewSize(w, rowH))

	if v.hideOnlyMatches {
		// Без чекбокса «Only matches» (например, в режиме Diff).
		v.searchWrap.Objects = []fyne.CanvasObject{searchRow}
		v.searchWrap.Resize(fyne.NewSize(w, rowH))
		v.searchWrap.Refresh()
		return
	}

	checkRow := container.NewHBox(layout.NewSpacer(), v.searchStructWrap)
	checkH := v.searchStructWrap.MinSize().Height
	if checkH < rowH {
		checkH = rowH
	}
	checkRow.Resize(fyne.NewSize(w, checkH))

	v.searchWrap.Objects = []fyne.CanvasObject{searchRow, checkRow}
	v.searchWrap.Resize(fyne.NewSize(w, rowH+checkH))
	v.searchWrap.Refresh()
}

// SetOnlyMatchesVisible управляет видимостью чекбокса «Only matches».
func (v *JSONView) SetOnlyMatchesVisible(show bool) {
	v.hideOnlyMatches = !show
	v.SetSearchWidth(v.searchWidth)
}

// SetSearchVisible shows or hides the search bar and clears query when hidden.
func (v *JSONView) SetSearchVisible(show bool) {
	if show {
		v.SetSearchWidth(v.searchWidth)
		v.searchWrap.Show()
		v.searchWrap.Refresh()
		return
	}
	v.searchEntry.SetText("")
	v.applySearchAsync("")
	v.mu.Lock()
	v.searchKeys = nil
	v.mu.Unlock()
	v.searchKeySelect.SetSelectedValues(nil)
	v.applyKeyFilterKeys(nil)
	v.searchWrap.Hide()
	v.searchWrap.Refresh()
}

// SearchVisible reports whether the search bar is visible.
func (v *JSONView) SearchVisible() bool {
	return v.searchWrap.Visible()
}
