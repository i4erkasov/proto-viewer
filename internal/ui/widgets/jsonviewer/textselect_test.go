package jsonviewer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSelectedTextPartialMultibyte проверяет, что выделение по рунным колонкам
// корректно вырезает многобайтовый текст (а не рвёт байты UTF-8).
func TestSelectedTextPartialMultibyte(t *testing.T) {
	v := newTestView()
	v.SetJSON(`{"a":"БОЛЬШЕ"}`)

	v.mu.Lock()
	row := 1 // строка с "a": "БОЛЬШЕ"
	content := viewLineString(v.fullBuf, v.viewLines[row])
	v.mu.Unlock()

	byteIdx := strings.Index(content, "БОЛЬШЕ")
	if byteIdx < 0 {
		t.Fatalf("value not found in view line %q", content)
	}
	rstart := utf8.RuneCountInString(content[:byteIdx])
	rend := rstart + utf8.RuneCountInString("БОЛЬШЕ")

	v.mu.Lock()
	v.selActive = true
	v.selAnchorRow, v.selAnchorCol = row, rstart
	v.selCurRow, v.selCurCol = row, rend
	v.mu.Unlock()

	if got := v.SelectedText(); got != "БОЛЬШЕ" {
		t.Fatalf("partial selection: got %q want %q", got, "БОЛЬШЕ")
	}
}

// TestSelectedTextMultiLine проверяет сшивку нескольких строк через \n и
// корректную работу при «обратном» выделении (cur выше anchor).
func TestSelectedTextMultiLine(t *testing.T) {
	v := newTestView()
	v.SetJSON(`{"a":"x","b":"y"}`)

	v.mu.Lock()
	if len(v.viewLines) < 3 {
		n := len(v.viewLines)
		v.mu.Unlock()
		t.Fatalf("expected at least 3 view lines, got %d", n)
	}
	l1 := viewLineString(v.fullBuf, v.viewLines[1])
	l2 := viewLineString(v.fullBuf, v.viewLines[2])
	// «Обратное» выделение: anchor на строке 2, cur на строке 1.
	v.selActive = true
	v.selAnchorRow, v.selAnchorCol = 2, utf8.RuneCountInString(l2)
	v.selCurRow, v.selCurCol = 1, 0
	v.mu.Unlock()

	want := l1 + "\n" + l2
	if got := v.SelectedText(); got != want {
		t.Fatalf("multiline selection:\n got  %q\n want %q", got, want)
	}
}

// TestClearSelectionEmptiesText проверяет, что снятие выделения обнуляет копию.
func TestClearSelectionEmptiesText(t *testing.T) {
	v := newTestView()
	v.SetJSON(`{"a":"x"}`)
	v.mu.Lock()
	v.selActive = true
	v.selAnchorRow, v.selAnchorCol = 1, 0
	v.selCurRow, v.selCurCol = 1, 3
	v.mu.Unlock()
	if v.SelectedText() == "" {
		t.Fatal("expected non-empty selection text")
	}
	v.clearTextSelection()
	if got := v.SelectedText(); got != "" {
		t.Fatalf("expected empty after clear, got %q", got)
	}
}
