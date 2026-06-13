package ui

import "image/color"

// Цвета подсветки строк в режиме Diff (полупрозрачные поверх фона).
func diffColorRemoved() color.Color { return color.NRGBA{R: 0xB0, G: 0x33, B: 0x33, A: 0x66} } // слева (A)
func diffColorAdded() color.Color   { return color.NRGBA{R: 0x2E, G: 0x7D, B: 0x32, A: 0x66} } // справа (B)

// diffMaxCells ограничивает память LCS-таблицы (~16МБ при int32).
const diffMaxCells = 4_000_000

// diffHunk — начало участка различий: строка в A и соответствующая в B.
// Для навигации (◀/▶) прокручиваем A к aLine, B к bLine.
type diffHunk struct {
	aLine, bLine int
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
			hunks = append(hunks, diffHunk{0, 0})
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
	inHunk := false
	for i < n && j < m {
		if a[i] == b[j] {
			i++
			j++
			inHunk = false
			continue
		}
		if !inHunk {
			hunks = append(hunks, diffHunk{i, j})
			inHunk = true
		}
		if dp[i+1][j] >= dp[i][j+1] {
			aDiff[i] = true
			i++
		} else {
			bDiff[j] = true
			j++
		}
	}
	if i < n || j < m {
		if !inHunk {
			hunks = append(hunks, diffHunk{i, j})
		}
		for ; i < n; i++ {
			aDiff[i] = true
		}
		for ; j < m; j++ {
			bDiff[j] = true
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
	prev := false
	for i := 0; i < n; i++ {
		if aDiff[i] {
			if !prev {
				hunks = append(hunks, diffHunk{i, clamp(i, m)})
			}
			prev = true
		} else {
			prev = false
		}
	}
	prev = false
	for j := 0; j < m; j++ {
		if bDiff[j] {
			if !prev {
				hunks = append(hunks, diffHunk{clamp(j, n), j})
			}
			prev = true
		} else {
			prev = false
		}
	}
	return hunks
}
