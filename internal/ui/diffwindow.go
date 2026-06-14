package ui

import (
	"context"
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/assets"
	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/ui/tab"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/jsonviewer"
)

// diffDropTarget — пока открыт модальный выбор источника B, дроп файла в главное
// окно роутится сюда (а не в главный File-таб). nil — когда диалог закрыт.
var diffDropTarget func(path string)

// openDiffWindow показывает МОДАЛЬНЫЙ диалог выбора второго источника (B) в
// главном окне: вкладки File/Redis + Compare. После Compare декодирует B и
// открывает отдельное окно сравнения с jsonA.
func openDiffWindow(parent fyne.Window, deps Deps, jsonA string, getProto func() (string, string, string, bool)) {
	fileTab := tab.NewTabFile(parent, deps.FileRepo)
	redisTab := tab.NewTabRedis(parent, deps.RedisRepo)
	tabs := container.NewAppTabs(
		container.NewTabItem(fileTab.Title(), container.NewBorder(fileTab.View(), nil, nil, nil, nil)),
		container.NewTabItem(redisTab.Title(), container.NewBorder(redisTab.View(), nil, nil, nil, nil)),
	)

	status := widget.NewLabel("")
	var dlg dialog.Dialog
	compare := widget.NewButtonWithIcon("Compare", assets.CompareIcon, nil)
	compare.Importance = widget.HighImportance
	compare.OnTapped = func() {
		root, file, typ, gzip := getProto()
		if root == "" || file == "" || typ == "" {
			dialog.ShowError(fmt.Errorf("задай proto root/file/message type в главном окне"), parent)
			return
		}
		var src interface {
			Fetch(context.Context) ([]byte, error)
		}
		switch tabs.SelectedIndex() {
		case 1:
			src = redisTab
		default:
			src = fileTab
		}

		status.SetText("decoding…")
		compare.Disable()
		go func() {
			defer fyne.Do(func() { compare.Enable() })
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			payload, err := src.Fetch(ctx)
			if err != nil {
				fyne.Do(func() { status.SetText("error"); dialog.ShowError(err, parent) })
				return
			}
			res, err := deps.Decoder.Decode(ctx, domain.DecodeRequest{
				ProtoRoot: root, ProtoFile: file, FullType: typ,
				Gzip: gzip, Format: domain.OutputFormatJSON, Bytes: payload,
			})
			if err != nil {
				fyne.Do(func() { status.SetText("error"); dialog.ShowError(err, parent) })
				return
			}
			jsonB := strings.TrimSpace(res.Raw)
			fyne.Do(func() {
				dlg.Hide()
				openDiffResult(jsonA, jsonB)
			})
		}()
	}

	cancel := widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
		if dlg != nil {
			dlg.Hide()
		}
	})

	// Кнопки одинакового размера, в одну строку, по центру (без растягивания).
	bw := cancel.MinSize().Width
	if cw := compare.MinSize().Width; cw > bw {
		bw = cw
	}
	bsz := fyne.NewSize(bw, compare.MinSize().Height)
	buttons := container.NewHBox(
		layout.NewSpacer(),
		container.NewGridWrap(bsz, cancel),
		container.NewGridWrap(bsz, compare),
		layout.NewSpacer(),
	)

	content := container.NewBorder(nil, container.NewVBox(status, buttons), nil, nil, tabs)
	dlg = dialog.NewCustomWithoutButtons("Diff — choose second source (B)", content, parent)
	dlg.Resize(fyne.NewSize(580, 440))

	// Пока диалог открыт, дроп файла в главное окно идёт во вкладку File диалога.
	diffDropTarget = func(p string) {
		tabs.SelectIndex(0)
		fileTab.SetFilePath(p)
		fileTab.FlashDropHighlight()
	}
	dlg.SetOnClosed(func() { diffDropTarget = nil })
	dlg.Show()
}

// openDiffResult показывает два вьювера рядом (A | B) с подсветкой различий,
// навигацией по изменениям, разворотом/сворачиванием и общим скроллом.
func openDiffResult(jsonA, jsonB string) {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	win := app.NewWindow("Diff")

	vA := jsonviewer.NewJSONMarkdownView(win)
	vB := jsonviewer.NewJSONMarkdownView(win)
	vA.SetOnlyMatchesVisible(false)
	vB.SetOnlyMatchesVisible(false)
	vA.SetKeySelectVisible(false)
	vB.SetKeySelectVisible(false)
	vA.SetSearchWidth(356)
	vB.SetSearchWidth(356)

	// hunks/diffIdx обновляются applyTexts; используются навигацией и сводкой.
	var hunks []diffHunk
	diffIdx := -1
	expanded := true                    // текущее состояние: всё развёрнуто / свёрнуто
	var syncExpandIcon func()           // обновляет иконку кнопки expand/collapse
	var refreshSearch func()            // перезапуск поиска после перезагрузки текста
	var reapplyFold func()              // переприменить «Only changes» после перезагрузки
	var refreshMinimaps func()          // обновить маркеры изменений на полосах
	var aChanged, bChanged map[int]bool // изменённые строки A/B (для «Only changes»)
	var aTotal, bTotal int              // число строк A/B (для мини-карт)
	label := widget.NewLabel("")
	updateLabel := func() {
		if len(hunks) == 0 {
			label.SetText("no diffs")
			return
		}
		c, a, r := diffSummary(hunks)
		pos := ""
		if diffIdx >= 0 {
			pos = fmt.Sprintf("   [%d/%d]", diffIdx+1, len(hunks))
		}
		label.SetText(fmt.Sprintf("~%d  +%d  −%d%s", c, a, r, pos))
	}

	// applyTexts (пере)загружает оба вьювера и пересчитывает дифф. normalize —
	// канонизировать JSON (сортировка ключей), снимая «шум» от порядка map.
	applyTexts := func(normalize bool) {
		ta, tb := jsonA, jsonB
		if normalize {
			ta = canonicalJSON(jsonA)
			tb = canonicalJSON(jsonB)
		}
		vA.SetJSON(ta)
		vB.SetJSON(tb)

		// Подсветка различий: слева красным (удалено/изменено), справа зелёным.
		aLines, bLines := strings.Split(ta, "\n"), strings.Split(tb, "\n")
		aDiff, bDiff, hk := computeLineDiff(aLines, bLines)
		am := make(map[int]color.Color, len(aDiff))
		for i := range aDiff {
			am[i] = diffColorRemoved()
		}
		bm := make(map[int]color.Color, len(bDiff))
		for j := range bDiff {
			bm[j] = diffColorAdded()
		}
		vA.SetDiffLines(am)
		vB.SetDiffLines(bm)
		aChanged, bChanged = aDiff, bDiff
		aTotal, bTotal = len(aLines), len(bLines)

		// A2: внутристрочная подсветка — для пар изменённых строк в каждом хунке
		// красим только изменившуюся середину.
		aSpans, bSpans := map[int][2]int{}, map[int][2]int{}
		for _, h := range hk {
			n := h.aLen
			if h.bLen < n {
				n = h.bLen
			}
			for k := 0; k < n; k++ {
				ai, bi := h.aLine+k, h.bLine+k
				if ai >= len(aLines) || bi >= len(bLines) {
					continue
				}
				ar, br := intraLineSpans(aLines[ai], bLines[bi])
				if ar[1] > ar[0] {
					aSpans[ai] = ar
				}
				if br[1] > br[0] {
					bSpans[bi] = br
				}
			}
		}
		vA.SetDiffSpans(aSpans, diffStrongRemoved())
		vB.SetDiffSpans(bSpans, diffStrongAdded())
		hunks = hk
		diffIdx = -1
		updateLabel()
		// SetJSON сбрасывает свёртки — после перезагрузки всё развёрнуто.
		expanded = true
		if syncExpandIcon != nil {
			syncExpandIcon()
		}
		if refreshSearch != nil {
			refreshSearch()
		}
		if reapplyFold != nil {
			reapplyFold()
		}
		if refreshMinimaps != nil {
			refreshMinimaps()
		}
	}

	normalize := widget.NewCheck("Normalize", func(b bool) { applyTexts(b) })
	normalize.Checked = true // канонизация по умолчанию; снимает шум от порядка map
	applyTexts(true)

	// Одна кнопка-тумблер expand/collapse; иконка отражает текущее состояние:
	// «restore» когда развёрнуто (тап свернёт), «fullscreen» когда свёрнуто (тап развернёт).
	toggleExpand := widget.NewButtonWithIcon("", theme.ViewRestoreIcon(), nil)
	syncExpandIcon = func() {
		if expanded {
			toggleExpand.SetIcon(theme.ViewRestoreIcon())
		} else {
			toggleExpand.SetIcon(theme.ViewFullScreenIcon())
		}
	}
	toggleExpand.OnTapped = func() {
		if expanded {
			vA.CollapseAll()
			vB.CollapseAll()
		} else {
			vA.ExpandAll()
			vB.ExpandAll()
		}
		expanded = !expanded
		syncExpandIcon()
	}

	// Общий (синхронный) скролл — сравниваем построчно.
	syncing := false
	vA.OnScrolled = func(p fyne.Position) {
		if syncing {
			return
		}
		syncing = true
		vB.SetScrollOffset(p)
		syncing = false
	}
	vB.OnScrolled = func(p fyne.Position) {
		if syncing {
			return
		}
		syncing = true
		vA.SetScrollOffset(p)
		syncing = false
	}

	// Навигация по изменениям.
	nav := func(step int) {
		if len(hunks) == 0 {
			return
		}
		diffIdx = (diffIdx + step + len(hunks)) % len(hunks)
		h := hunks[diffIdx]
		// Двигаем стороны к своим строкам независимо (без взаимной синхронизации).
		syncing = true
		vA.ScrollToSourceLine(h.aLine)
		vB.ScrollToSourceLine(h.bLine)
		syncing = false
		updateLabel()
	}

	// «Only changes» (C2): сворачивает неизменённые JSON-узлы в обеих панелях.
	onlyChanges := widget.NewCheck("Only changes", func(on bool) {
		if on {
			vA.CollapseUnchanged(aChanged)
			vB.CollapseUnchanged(bChanged)
			expanded = false
		} else {
			vA.ExpandAll()
			vB.ExpandAll()
			expanded = true
		}
		syncExpandIcon()
	})
	reapplyFold = func() {
		if onlyChanges.Checked {
			vA.CollapseUnchanged(aChanged)
			vB.CollapseUnchanged(bChanged)
			expanded = false
			syncExpandIcon()
		}
	}

	// Навигация по изменениям (◀/▶ сверху).
	prev := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { nav(-1) })
	next := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() { nav(1) })
	prev.Importance = widget.LowImportance
	next.Importance = widget.LowImportance
	bar := container.NewHBox(toggleExpand, onlyChanges, normalize, layout.NewSpacer(), prev, next)
	// Сводка различий (~N +N −N) и позиция [i/n] — внизу окна.
	statusBar := container.NewHBox(layout.NewSpacer(), label, layout.NewSpacer())

	// --- Единый поиск по обеим панелям (B1+B2): одна строка, объединённая
	// навигация, синхронный переход (общий скролл подтягивает вторую панель).
	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder("Find in both panes…")
	searchCount := widget.NewLabel("")
	type smatch struct{ pane, line int }
	var (
		aMatches, bMatches []int
		combined           []smatch
		searchIdx          = -1
	)
	updateSearchCount := func() {
		switch {
		case strings.TrimSpace(searchEntry.Text) == "":
			searchCount.SetText("")
		case len(combined) == 0:
			searchCount.SetText("0")
		case searchIdx < 0:
			searchCount.SetText(fmt.Sprintf("%d", len(combined)))
		default:
			searchCount.SetText(fmt.Sprintf("%d/%d", searchIdx+1, len(combined)))
		}
	}
	rebuildSearch := func() {
		// Сливаем совпадения A и B по номеру строки (естественный порядок сверху
		// вниз); строку, совпавшую в обеих панелях, считаем один раз (панели
		// выровнены при Normalize, а синхро-скролл покажет обе).
		combined = combined[:0]
		seen := map[int]bool{}
		add := func(l, pane int) {
			if !seen[l] {
				seen[l] = true
				combined = append(combined, smatch{pane, l})
			}
		}
		for _, l := range aMatches {
			add(l, 0)
		}
		for _, l := range bMatches {
			add(l, 1)
		}
		sort.Slice(combined, func(i, j int) bool { return combined[i].line < combined[j].line })
		searchIdx = -1
		updateSearchCount()
	}
	vA.OnSearchResult = func(int) { aMatches = vA.MatchLines(); rebuildSearch() }
	vB.OnSearchResult = func(int) { bMatches = vB.MatchLines(); rebuildSearch() }

	searchNav := func(step int) {
		if len(combined) == 0 {
			return
		}
		searchIdx = (searchIdx + step + len(combined)) % len(combined)
		m := combined[searchIdx]
		if m.pane == 0 {
			vA.ScrollToSourceLine(m.line) // синхро-скролл подтянет вторую панель
		} else {
			vB.ScrollToSourceLine(m.line)
		}
		updateSearchCount()
	}

	var searchTimer *time.Timer
	searchEntry.OnChanged = func(q string) {
		if searchTimer != nil {
			searchTimer.Stop()
		}
		searchTimer = time.AfterFunc(250*time.Millisecond, func() {
			vA.Search(q)
			vB.Search(q)
		})
	}
	searchEntry.OnSubmitted = func(string) { searchNav(1) }

	sPrev := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { searchNav(-1) })
	sNext := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() { searchNav(1) })
	sClose := widget.NewButtonWithIcon("", theme.CancelIcon(), nil)
	sPrev.Importance = widget.LowImportance
	sNext.Importance = widget.LowImportance
	sClose.Importance = widget.LowImportance
	searchRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(searchCount, sPrev, sNext, sClose), searchEntry)
	searchRow.Hide()

	searchShown := false
	showSearch := func(show bool) {
		searchShown = show
		if show {
			searchRow.Show()
			win.Canvas().Focus(searchEntry)
		} else {
			searchRow.Hide()
			searchEntry.SetText("")
			vA.Search("")
			vB.Search("")
		}
	}
	sClose.OnTapped = func() { showSearch(false) }

	refreshSearch = func() {
		if q := strings.TrimSpace(searchEntry.Text); q != "" {
			vA.Search(q)
			vB.Search(q)
		}
	}

	// C1: мини-карты изменений на правом краю каждой панели; клик — прыжок
	// (синхро-скролл подтянет вторую панель).
	miniA := newDiffMinimap(diffColorRemoved(), func(line int) { vA.ScrollToSourceLine(line) })
	miniB := newDiffMinimap(diffColorAdded(), func(line int) { vB.ScrollToSourceLine(line) })
	refreshMinimaps = func() {
		miniA.SetData(aTotal, aChanged)
		miniB.SetData(bTotal, bChanged)
	}
	refreshMinimaps()

	paneA := container.NewBorder(nil, nil, nil, miniA, vA.View())
	paneB := container.NewBorder(nil, nil, nil, miniB, vB.View())
	split := container.NewHSplit(paneA, paneB)
	split.SetOffset(0.5)

	for _, mod := range []fyne.KeyModifier{fyne.KeyModifierShortcutDefault, fyne.KeyModifierControl, fyne.KeyModifierSuper} {
		win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: mod}, func(_ fyne.Shortcut) { showSearch(!searchShown) })
	}
	win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(_ fyne.Shortcut) {
		if searchShown {
			showSearch(false)
		}
	})

	top := container.NewVBox(bar, searchRow)
	win.SetContent(container.NewBorder(top, statusBar, nil, nil, split))
	win.Resize(fyne.NewSize(1000, 680))
	win.Show()
}
