package jsonviewer

import (
	"strings"
	"testing"
)

// TestCollapseExpandAll проверяет, что CollapseAll сворачивает документ до
// компактного вида, а ExpandAll раскрывает его полностью.
func TestCollapseExpandAll(t *testing.T) {
	v := newTestView()
	v.SetJSON(makeLargeJSON(1 << 20))

	v.CollapseAll()
	v.mu.Lock()
	collapsed := len(v.viewLines)
	v.mu.Unlock()

	v.ExpandAll()
	v.mu.Lock()
	expanded := len(v.viewLines)
	v.mu.Unlock()

	if collapsed == 0 {
		t.Fatal("collapsed view is empty")
	}
	if expanded <= collapsed {
		t.Fatalf("expected expanded (%d) > collapsed (%d)", expanded, collapsed)
	}
}

// TestCopyExpandsFoldedContent проверяет, что копирование свёрнутого узла даёт
// полное содержимое блока, а не заглушку "{ ... }".
func TestCopyExpandsFoldedContent(t *testing.T) {
	v := newTestView()
	v.SetJSON(`{"a":{"b":{"c":"deep"}},"d":1}`)
	v.CollapseAll()
	v.SelectAll()

	got := v.SelectedText()
	if !strings.Contains(got, `"c": "deep"`) {
		t.Fatalf("collapsed copy lost inner content:\n%q", got)
	}

	v.mu.Lock()
	var full strings.Builder
	n := v.fullBuf.LineCount()
	for i := 0; i < n; i++ {
		full.WriteString(string(v.fullBuf.Line(i)))
		if i < n-1 {
			full.WriteByte('\n')
		}
	}
	v.mu.Unlock()
	if got != full.String() {
		t.Fatalf("collapsed SelectAll copy != full document:\n got  %q\n want %q", got, full.String())
	}
}

// TestSelectAllCoversDocument проверяет, что SelectAll выделяет весь видимый
// текст (число строк в выделении равно числу строк во viewLines).
func TestSelectAllCoversDocument(t *testing.T) {
	v := newTestView()
	v.SetJSON(`{"a":"x","b":{"c":"y"},"d":[1,2,3]}`)
	v.ExpandAll()

	v.mu.Lock()
	n := len(v.viewLines)
	v.mu.Unlock()

	v.SelectAll()
	got := v.SelectedText()
	if got == "" {
		t.Fatal("SelectAll produced empty text")
	}
	if lines := strings.Count(got, "\n") + 1; lines != n {
		t.Fatalf("selected %d lines, expected %d (viewLines)", lines, n)
	}
}
