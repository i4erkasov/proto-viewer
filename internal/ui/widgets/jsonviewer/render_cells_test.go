package jsonviewer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBuildCellsMultibyte проверяет, что ячейки строятся по рунам, а не по
// байтам: иначе многобайтовый UTF-8 (кириллица, CJK, арабский, диакритика)
// отображается крокозябрами, хотя копирование берёт исходную строку и работает.
func TestBuildCellsMultibyte(t *testing.T) {
	cases := []string{
		`  "uk": "БІЛЬШЕ ІГОР."`,
		`  "ar": "العب أكثر."`,
		`  "zh_hans": "畅玩不停！"`,
		`  "es": "JUEGA MÁS."`,
		`  "ko": "더 경기하고."`,
	}
	for _, line := range cases {
		cells := buildTextGridCells([]byte(line), nil, 0, highlightRange{}, highlightRange{}, nil)
		var sb strings.Builder
		for _, c := range cells {
			sb.WriteRune(c.Rune)
		}
		if got := sb.String(); got != line {
			t.Fatalf("rune round-trip mismatch:\n got  %q\n want %q", got, line)
		}
		if want := utf8.RuneCountInString(line); len(cells) != want {
			t.Fatalf("cell count %d != rune count %d for %q", len(cells), want, line)
		}
	}
}

// TestBuildCellsLineNumberPrefix проверяет корректность с ASCII-префиксом
// (номера строк) перед многобайтовым контентом.
func TestBuildCellsLineNumberPrefix(t *testing.T) {
	prefix := " 12  "
	content := `"ru": "БОЛЬШЕ"`
	full := prefix + content
	cells := buildTextGridCells([]byte(full), nil, len(prefix), highlightRange{}, highlightRange{}, nil)

	var sb strings.Builder
	for _, c := range cells {
		sb.WriteRune(c.Rune)
	}
	if got := sb.String(); got != full {
		t.Fatalf("round-trip mismatch:\n got  %q\n want %q", got, full)
	}
}
