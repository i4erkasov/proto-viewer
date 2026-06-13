package jsonviewer

import (
	"runtime"
	"testing"
	"time"
)

func waitRows(v *JSONView, want func(int) bool, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		n := len(v.tgrid.Rows)
		if want(n) || time.Now().After(deadline) {
			return n
		}
		runtime.Gosched()
	}
}

// TestVirtualizationBoundsRows проверяет ключевой инвариант виртуализации:
// сколько бы строк ни было в документе, в TextGrid материализуется только
// окно (видимая область + overscan), а не весь документ. Именно это делает
// рендер независимым от качества OpenGL (важно для программного рендера Windows).
func TestVirtualizationBoundsRows(t *testing.T) {
	v := newTestView()
	v.SetJSON(makeLargeJSON(5 << 20))

	// Раскрываем все свёрнутые узлы, чтобы получить большой видимый набор строк
	// (по умолчанию глубокие узлы свёрнуты, и документ показывается кратко).
	v.mu.Lock()
	for k := range v.folded {
		v.folded[k] = false
	}
	v.rebuildViewLinesLocked()
	total := len(v.viewLines)
	lineH := v.lineH
	v.mu.Unlock()
	v.updateWindow()

	if total < 1000 {
		t.Fatalf("expected a large document, got %d view lines", total)
	}
	if lineH <= 0 {
		t.Fatalf("probeMetrics did not produce a positive line height: %v", lineH)
	}

	rows := waitRows(v, func(n int) bool { return n > 0 && n < total }, 2*time.Second)
	if rows == 0 {
		t.Fatal("grid is empty after unfolding")
	}
	// Окно не должно быть размером со весь документ.
	if rows >= total {
		t.Fatalf("grid materialized %d rows for a %d-line document; virtualization not active", rows, total)
	}
	// Разумная верхняя граница: видимая область при первой отрисовке плюс запас.
	const sane = 4 * (overscan + 1)
	if rows > sane {
		t.Fatalf("window too large: %d rows (sane upper bound %d)", rows, sane)
	}
}

// TestSetJSONEmptyClears проверяет, что пустой ввод очищает грид без паники.
func TestSetJSONEmptyClears(t *testing.T) {
	v := newTestView()
	v.SetJSON(makeLargeJSON(1 << 20))
	if len(v.tgrid.Rows) == 0 {
		t.Fatal("expected rows after loading content")
	}
	v.SetJSON("")
	if got := len(v.tgrid.Rows); got != 0 {
		t.Fatalf("expected empty grid after SetJSON(\"\"), got %d rows", got)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.viewLines) != 0 {
		t.Fatalf("expected no view lines after clear, got %d", len(v.viewLines))
	}
}
