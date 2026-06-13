package ui

import "testing"

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
