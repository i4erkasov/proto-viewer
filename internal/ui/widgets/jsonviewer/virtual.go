//go:build darwin || linux || windows

package jsonviewer

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/internal/infrastructure/perf"
)

// Виртуализация вьюпорта.
//
// Вместо того чтобы держать в одном TextGrid все строки JSON (по объекту
// canvas.Text на КАЖДЫЙ символ — это и есть причина тормозов на программном
// OpenGL Windows), мы рендерим только строки, попадающие в видимую область
// плюс небольшой запас сверху/снизу. Полная высота контента имитируется
// кастомным layout'ом, поэтому скроллбар и горизонтальная прокрутка работают
// как раньше, а число реальных объектов на экране остаётся константно малым.

// overscan — сколько дополнительных строк рендерить за пределами вьюпорта
// сверху и снизу, чтобы быстрый скролл не оголял пустые края.
const overscan = 24

// gridWindowLayout сообщает Scroll'у полный виртуальный размер контента
// (ширина самой длинной строки × число строк) и позиционирует окно строк на
// его реальном месте по вертикали.
type gridWindowLayout struct {
	v *JSONView
}

func (l *gridWindowLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	v := l.v
	v.mu.Lock()
	n := len(v.viewLines)
	lineH := v.lineH
	contentW := v.contentW
	v.mu.Unlock()
	return fyne.NewSize(contentW, lineH*float32(n))
}

func (l *gridWindowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	v := l.v
	v.mu.Lock()
	start := v.winStart
	lineH := v.lineH
	winRows := len(v.tgrid.Rows)
	contentW := v.contentW
	v.mu.Unlock()

	w := size.Width
	if contentW > w {
		w = contentW
	}
	objects[0].Move(fyne.NewPos(0, lineH*float32(start)))
	objects[0].Resize(fyne.NewSize(w, lineH*float32(winRows)))
}

// probeMetrics измеряет высоту строки и ширину символа тем же типом виджета
// (TextGrid), которым потом рисуем, — так метрики окна точно совпадают с
// реальным рендером и не возникает дрейфа позиционирования.
func (v *JSONView) probeMetrics() {
	probe := widget.NewTextGrid()
	probe.SetText("X")
	ms := probe.MinSize()
	v.lineH = ms.Height
	v.cellW = ms.Width
}

// updateWindow перестраивает строки, попадающие в текущий вьюпорт, и
// репозиционирует окно. Безопасно вызывать из любого потока.
func (v *JSONView) updateWindow() {
	fyne.Do(func() {
		if v.tgrid == nil || v.scroll == nil || v.content == nil {
			return
		}
		v.mu.Lock()
		n := len(v.viewLines)
		lineH := v.lineH
		v.mu.Unlock()

		if n == 0 || lineH <= 0 {
			v.tgrid.Rows = nil
			v.tgrid.Refresh()
			v.content.Refresh()
			return
		}

		offY := v.scroll.Offset.Y
		viewH := v.scroll.Size().Height
		if viewH <= 0 {
			// Ещё не выложено — отрисуем первый экран целиком с запасом.
			viewH = lineH * float32(2*overscan)
		}

		first := int(offY/lineH) - overscan
		if first < 0 {
			first = 0
		}
		count := int(viewH/lineH) + 2*overscan + 1
		last := first + count
		if last > n {
			last = n
		}

		window := make([]int, last-first)
		v.mu.Lock()
		copy(window, v.viewLines[first:last])
		v.winStart = first
		v.mu.Unlock()

		stopBuild := perf.Track(fmt.Sprintf("window build (%d lines, %d total)", len(window), n))
		rows := v.buildRowsForView(window)
		stopBuild()

		// Ширина контента считается по самой длинной строке В ОКНЕ, а не во всём
		// документе: горизонтальный скролл появляется только когда видимая строка
		// реально шире вьюпорта.
		maxCells := 0
		for i := range rows {
			if c := len(rows[i].Cells); c > maxCells {
				maxCells = c
			}
		}
		v.mu.Lock()
		v.contentW = float32(maxCells) * v.cellW
		v.mu.Unlock()

		v.tgrid.Rows = rows
		stopRefresh := perf.Track(fmt.Sprintf("window refresh (%d rows)", len(rows)))
		v.tgrid.Refresh()
		v.content.Refresh()
		stopRefresh()
	})
}

// onScroll перерисовывает окно при прокрутке и уведомляет внешнего слушателя
// (используется для синхронной прокрутки двух вьюверов в режиме Diff).
func (v *JSONView) onScroll() {
	v.updateWindow()
	if v.OnScrolled != nil && v.scroll != nil {
		v.OnScrolled(v.scroll.Offset)
	}
}

// ScrollOffset возвращает текущее смещение прокрутки.
func (v *JSONView) ScrollOffset() fyne.Position {
	if v.scroll == nil {
		return fyne.Position{}
	}
	return v.scroll.Offset
}

// SetScrollOffset задаёт смещение прокрутки (для синхронной прокрутки).
// Не уведомляет OnScrolled (чтобы не зациклить синхронизацию).
func (v *JSONView) SetScrollOffset(p fyne.Position) {
	if v.scroll == nil {
		return
	}
	v.scroll.ScrollToOffset(p)
	v.updateWindow()
}

// resetScroll возвращает прокрутку в начало (например, при загрузке нового JSON).
func (v *JSONView) resetScroll() {
	v.mu.Lock()
	v.winStart = 0
	v.mu.Unlock()
	fyne.Do(func() {
		if v.scroll != nil {
			v.scroll.ScrollToOffset(fyne.NewPos(0, 0))
		}
	})
}

// scrollToViewRow прокручивает так, чтобы указанная строка вьюпорта стала видна.
func (v *JSONView) scrollToViewRow(row int) {
	if v.scroll == nil || row < 0 {
		return
	}
	v.mu.Lock()
	lineH := v.lineH
	v.mu.Unlock()
	if lineH <= 0 {
		return
	}
	v.scroll.ScrollToOffset(fyne.NewPos(v.scroll.Offset.X, lineH*float32(row)))
	v.updateWindow()
	// Уведомляем слушателя (синхронная прокрутка соседнего окна в Diff),
	// т.к. программный ScrollToOffset не всегда вызывает scroll.OnScrolled.
	if v.OnScrolled != nil {
		v.OnScrolled(v.scroll.Offset)
	}
}
