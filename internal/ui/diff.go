package ui

import (
	"encoding/json"
	"image/color"
	"strings"
)

// Цвета подсветки строк в режиме Diff (полупрозрачные поверх фона).
func diffColorRemoved() color.Color { return color.NRGBA{R: 0xB0, G: 0x33, B: 0x33, A: 0x66} } // слева (A)
func diffColorAdded() color.Color   { return color.NRGBA{R: 0x2E, G: 0x7D, B: 0x32, A: 0x66} } // справа (B)

// Усиленные цвета для внутристрочной подсветки (A2) — изменившаяся часть строки.
func diffStrongRemoved() color.Color { return color.NRGBA{R: 0xC0, G: 0x39, B: 0x39, A: 0xC0} }
func diffStrongAdded() color.Color   { return color.NRGBA{R: 0x35, G: 0x8E, B: 0x3A, A: 0xC0} }

// intraLineSpans возвращает рунные диапазоны изменившейся середины двух строк:
// отрезаем общий префикс и общий суффикс. Для пар «изменённых» строк это даёт
// точную часть, которую стоит подсветить (например, 88 → 91).
func intraLineSpans(a, b string) (aRange, bRange [2]int) {
	ra, rb := []rune(a), []rune(b)
	p := 0
	for p < len(ra) && p < len(rb) && ra[p] == rb[p] {
		p++
	}
	s := 0
	for s < len(ra)-p && s < len(rb)-p && ra[len(ra)-1-s] == rb[len(rb)-1-s] {
		s++
	}
	return [2]int{p, len(ra) - s}, [2]int{p, len(rb) - s}
}

// diffMaxCells ограничивает память LCS-таблицы (~16МБ при int32).
const diffMaxCells = 4_000_000

// diffHunk — участок различий: начальные строки в A/B (для навигации ◀/▶) и
// число различающихся строк с каждой стороны (для сводки changed/added/removed).
type diffHunk struct {
	aLine, bLine int
	aLen, bLen   int
}

// diffSummary классифицирует хунки: участок со строками с обеих сторон —
// «изменено», только в B — «добавлено», только в A — «удалено».
func diffSummary(hunks []diffHunk) (changed, added, removed int) {
	for _, h := range hunks {
		switch {
		case h.aLen > 0 && h.bLen > 0:
			changed++
		case h.bLen > 0:
			added++
		case h.aLen > 0:
			removed++
		}
	}
	return
}

// canonicalJSON приводит JSON к каноническому виду: рекурсивно сортирует ключи
// объектов (encoding/json маршалит map с сортировкой), снимая «шум» от разного
// порядка ключей в map. UseNumber сохраняет точность чисел (int64 не плывёт).
// При невалидном входе возвращает строку без изменений.
func canonicalJSON(s string) string {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return s
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(out)
}

// computeLineDiff возвращает индексы различающихся строк для каждой стороны и
// упорядоченный список «хунков» (стартов участков различий) для навигации.
// aDiff — строки A не из общей подпоследовательности (удалено/изменено),
// bDiff — то же для B (добавлено/изменено). Для огромных входов — дешёвый
// fallback по множеству строк (без учёта порядка/дублей).
func computeLineDiff(a, b []string) (aDiff, bDiff map[int]bool, hunks []diffHunk) {
	aDiff = make(map[int]bool)
	bDiff = make(map[int]bool)
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return
	}
	if n == 0 || m == 0 {
		for i := range a {
			aDiff[i] = true
		}
		for j := range b {
			bDiff[j] = true
		}
		if n > 0 || m > 0 {
			hunks = append(hunks, diffHunk{aLine: 0, bLine: 0, aLen: n, bLen: m})
		}
		return
	}

	if n*m > diffMaxCells {
		bset := make(map[string]struct{}, m)
		for _, l := range b {
			bset[l] = struct{}{}
		}
		aset := make(map[string]struct{}, n)
		for _, l := range a {
			aset[l] = struct{}{}
		}
		for i, l := range a {
			if _, ok := bset[l]; !ok {
				aDiff[i] = true
			}
		}
		for j, l := range b {
			if _, ok := aset[l]; !ok {
				bDiff[j] = true
			}
		}
		hunks = fallbackHunks(aDiff, bDiff, n, m)
		return
	}

	// LCS через DP (int32 ради памяти).
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	i, j := 0, 0
	cur := -1 // индекс текущего открытого хунка (-1 — вне хунка)
	for i < n && j < m {
		if a[i] == b[j] {
			i++
			j++
			cur = -1
			continue
		}
		if cur < 0 {
			hunks = append(hunks, diffHunk{aLine: i, bLine: j})
			cur = len(hunks) - 1
		}
		if dp[i+1][j] >= dp[i][j+1] {
			aDiff[i] = true
			hunks[cur].aLen++
			i++
		} else {
			bDiff[j] = true
			hunks[cur].bLen++
			j++
		}
	}
	if i < n || j < m {
		if cur < 0 {
			hunks = append(hunks, diffHunk{aLine: i, bLine: j})
			cur = len(hunks) - 1
		}
		for ; i < n; i++ {
			aDiff[i] = true
			hunks[cur].aLen++
		}
		for ; j < m; j++ {
			bDiff[j] = true
			hunks[cur].bLen++
		}
	}
	return
}

// fallbackHunks строит приблизительные хунки из множеств различий (для huge-режима).
func fallbackHunks(aDiff, bDiff map[int]bool, n, m int) []diffHunk {
	var hunks []diffHunk
	clamp := func(v, hi int) int {
		if hi <= 0 {
			return 0
		}
		if v >= hi {
			return hi - 1
		}
		return v
	}
	cur := -1
	for i := 0; i < n; i++ {
		if aDiff[i] {
			if cur < 0 {
				hunks = append(hunks, diffHunk{aLine: i, bLine: clamp(i, m)})
				cur = len(hunks) - 1
			}
			hunks[cur].aLen++
		} else {
			cur = -1
		}
	}
	cur = -1
	for j := 0; j < m; j++ {
		if bDiff[j] {
			if cur < 0 {
				hunks = append(hunks, diffHunk{aLine: clamp(j, n), bLine: j})
				cur = len(hunks) - 1
			}
			hunks[cur].bLen++
		} else {
			cur = -1
		}
	}
	return hunks
}
