package ui

import (
	"context"
	"fmt"
	"image/color"
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
	expanded := true          // текущее состояние: всё развёрнуто / свёрнуто
	var syncExpandIcon func() // обновляет иконку кнопки expand/collapse
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
		aDiff, bDiff, hk := computeLineDiff(strings.Split(ta, "\n"), strings.Split(tb, "\n"))
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
		hunks = hk
		diffIdx = -1
		updateLabel()
		// SetJSON сбрасывает свёртки — после перезагрузки всё развёрнуто.
		expanded = true
		if syncExpandIcon != nil {
			syncExpandIcon()
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

	prev := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() { nav(-1) })
	next := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() { nav(1) })
	prev.Importance = widget.LowImportance
	next.Importance = widget.LowImportance
	bar := container.NewHBox(
		toggleExpand,
		normalize,
		layout.NewSpacer(),
		prev, next,
	)
	// Сводка различий (~N +N −N) и позиция [i/n] — внизу окна.
	statusBar := container.NewHBox(layout.NewSpacer(), label, layout.NewSpacer())

	paneA := container.NewBorder(
		container.NewHBox(layout.NewSpacer(), vA.SearchBar()), nil, nil, nil, vA.View())
	paneB := container.NewBorder(
		container.NewHBox(layout.NewSpacer(), vB.SearchBar()), nil, nil, nil, vB.View())
	split := container.NewHSplit(paneA, paneB)
	split.SetOffset(0.5)

	// Поиск в обоих окнах: Cmd/Ctrl+F открыть, Esc закрыть.
	toggleFind := func() {
		show := !(vA.SearchVisible() || vB.SearchVisible())
		vA.SetSearchVisible(show)
		vB.SetSearchVisible(show)
		if show {
			win.Canvas().Focus(vA.SearchEntry())
		}
	}
	for _, mod := range []fyne.KeyModifier{fyne.KeyModifierShortcutDefault, fyne.KeyModifierControl, fyne.KeyModifierSuper} {
		win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: mod}, func(_ fyne.Shortcut) { toggleFind() })
	}
	win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(_ fyne.Shortcut) {
		if vA.SearchVisible() || vB.SearchVisible() {
			vA.SetSearchVisible(false)
			vB.SetSearchVisible(false)
		}
	})

	win.SetContent(container.NewBorder(bar, statusBar, nil, nil, split))
	win.Resize(fyne.NewSize(1000, 680))
	win.Show()
}
