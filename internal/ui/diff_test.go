package ui

import (
	"strings"
	"testing"
)

func TestComputeLineDiffChanged(t *testing.T) {
	a := []string{"{", `  "id": 42,`, `  "name": "SALAH",`, `  "rating": 88`, "}"}
	b := []string{"{", `  "id": 42,`, `  "name": "Mohamed Salah",`, `  "rating": 91`, "}"}

	aDiff, bDiff, _ := computeLineDiff(a, b)

	for _, i := range []int{2, 3} {
		if !aDiff[i] {
			t.Errorf("A line %d should be marked", i)
		}
		if !bDiff[i] {
			t.Errorf("B line %d should be marked", i)
		}
	}
	for _, i := range []int{0, 1, 4} {
		if aDiff[i] {
			t.Errorf("A line %d should be common", i)
		}
		if bDiff[i] {
			t.Errorf("B line %d should be common", i)
		}
	}
}

func TestComputeLineDiffAddedRemoved(t *testing.T) {
	a := []string{"x", "y"}
	b := []string{"x", "new", "y"}
	aDiff, bDiff, _ := computeLineDiff(a, b)
	if len(aDiff) != 0 {
		t.Fatalf("nothing should be removed from A, got %v", aDiff)
	}
	if !bDiff[1] || len(bDiff) != 1 {
		t.Fatalf("only B line 1 should be added, got %v", bDiff)
	}
}

func TestComputeLineDiffIdentical(t *testing.T) {
	a := []string{"a", "b", "c"}
	aDiff, bDiff, hunks := computeLineDiff(a, a)
	if len(aDiff) != 0 || len(bDiff) != 0 {
		t.Fatalf("identical inputs must have no diff, got %v %v", aDiff, bDiff)
	}
	if len(hunks) != 0 {
		t.Fatalf("identical inputs must have no hunks, got %v", hunks)
	}
}

func TestComputeLineDiffHunks(t *testing.T) {
	a := []string{"a", "b", "c", "d", "e"}
	b := []string{"a", "B", "c", "d", "E"}
	_, _, hunks := computeLineDiff(a, b)
	// Два отдельных участка различий: вокруг индекса 1 и индекса 4.
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d: %v", len(hunks), hunks)
	}
	if hunks[0].aLine != 1 || hunks[0].bLine != 1 {
		t.Fatalf("hunk0 = %+v, want {1,1}", hunks[0])
	}
	if hunks[1].aLine != 4 || hunks[1].bLine != 4 {
		t.Fatalf("hunk1 = %+v, want {4,4}", hunks[1])
	}
}

// TestIntraLineSpans проверяет вычисление изменившейся середины строки (A2).
func TestIntraLineSpans(t *testing.T) {
	// Меняется значение в конце: подсвечиваем "88" → "91".
	ar, br := intraLineSpans(`  "rating": 88`, `  "rating": 91`)
	if ar != [2]int{12, 14} || br != [2]int{12, 14} {
		t.Fatalf("rating spans = %v / %v, want [12 14] / [12 14]", ar, br)
	}
	// Разная длина середины: "XYZ" → "Q".
	ar, br = intraLineSpans("abcXYZdef", "abcQdef")
	if ar != [2]int{3, 6} || br != [2]int{3, 4} {
		t.Fatalf("mid spans = %v / %v, want [3 6] / [3 4]", ar, br)
	}
	// Идентичные строки → пустые диапазоны.
	ar, br = intraLineSpans("same", "same")
	if ar[1] > ar[0] || br[1] > br[0] {
		t.Fatalf("identical lines must yield empty spans, got %v / %v", ar, br)
	}
}

// TestDiffSummary проверяет классификацию хунков: изменено/добавлено/удалено.
func TestDiffSummary(t *testing.T) {
	// Три участка, разделённые общими строками: изменение, удаление, добавление.
	a := []string{"common1", "CHG_A", "common2", "RM", "common3"}
	b := []string{"common1", "CHG_B", "common2", "common3", "ADD"}
	_, _, hunks := computeLineDiff(a, b)
	changed, added, removed := diffSummary(hunks)
	if changed != 1 || added != 1 || removed != 1 {
		t.Fatalf("summary = ~%d +%d -%d, want ~1 +1 -1 (hunks=%+v)", changed, added, removed, hunks)
	}
}

// TestCanonicalJSON проверяет сортировку ключей и сохранение больших int64.
func TestCanonicalJSON(t *testing.T) {
	out := canonicalJSON(`{"b":1,"a":{"y":2,"x":9223372036854775807}}`)
	ia, ib := strings.Index(out, `"a"`), strings.Index(out, `"b"`)
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("keys not sorted:\n%s", out)
	}
	if !strings.Contains(out, "9223372036854775807") {
		t.Fatalf("int64 lost precision:\n%s", out)
	}
	// Невалидный JSON возвращается как есть.
	if got := canonicalJSON("not json"); got != "not json" {
		t.Fatalf("invalid input mangled: %q", got)
	}
}
