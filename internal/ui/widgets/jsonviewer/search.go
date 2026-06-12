//go:build darwin || linux || windows

package jsonviewer

import (
	"bytes"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
)

// --- Search

func (v *JSONView) onSearchChanged(s string) {
	v.debounceMu.Lock()
	v.debounceQuery = s
	if v.debounceTimer == nil {
		v.debounceTimer = time.AfterFunc(300*time.Millisecond, v.fireSearchDebounce)
		v.debounceMu.Unlock()
		return
	}
	if !v.debounceTimer.Stop() {
		select {
		case <-v.debounceTimer.C:
		default:
		}
	}
	v.debounceTimer.Reset(300 * time.Millisecond)
	v.debounceMu.Unlock()
}

func (v *JSONView) fireSearchDebounce() {
	v.debounceMu.Lock()
	q := v.debounceQuery
	v.debounceMu.Unlock()
	v.applySearchAsync(q)
}

func (v *JSONView) applySearchAsync(q string) {
	query := strings.TrimSpace(q)
	seq := atomic.AddUint64(&v.searchSeq, 1)

	v.mu.Lock()
	keys := v.searchKeys
	index := v.searchKeyIndex
	allLines := v.searchAll
	keyRanges := v.searchKeyRanges
	trigramEnabled := v.trigramEnabled
	trigramIndex := v.trigramIndex
	trigramPostings := v.trigramPostings
	v.searchQuery = query
	if len(v.viewLines) > 0 {
		first := viewLineIndex(v.viewLines[0])
		last := viewLineIndex(v.viewLines[len(v.viewLines)-1])
		for _, k := range keys {
			if rng, ok := keyRanges[k]; ok {
				if first < rng.start || last > rng.end {
					v.rebuildViewLinesForKeysLocked(keys)
					break
				}
			}
		}
	}
	v.mu.Unlock()

	queryLower := asciiLowerBytes([]byte(query))
	if len(queryLower) == 0 {
		fyne.Do(func() {
			if seq != atomic.LoadUint64(&v.searchSeq) {
				return
			}
			v.mu.Lock()
			v.highlights = nil
			v.matchLines = nil
			v.searchMatchSet = nil
			v.matchIndex = -1
			v.mu.Unlock()
			v.updateNavButtons()
			v.updateWindow()
		})
		return
	}

	anchorByte := byte(0)
	useAnchor := len(queryLower) >= 4
	if useAnchor {
		anchorByte = queryLower[len(queryLower)/2]
	}

	var candidates []int
	if len(keys) == 0 {
		candidates = allLines
	} else {
		candidates = unionCandidateLines(index, keys)
	}

	if trigramEnabled && trigramIndex != nil && len(queryLower) >= 3 {
		triCandidates := trigramCandidatesFromIndex(trigramIndex, trigramPostings, queryLower)
		if triCandidates != nil {
			candidates = intersectSortedInts(candidates, triCandidates)
		}
	}

	go func(seq uint64, queryLower []byte, candidates []int) {
		matchLines := make([]int, 0)
		matchSet := make(map[int]struct{})

		for _, i := range candidates {
			lineBytes := v.fullLineBytes(i)
			if len(lineBytes) == 0 {
				continue
			}
			if useAnchor && bytes.IndexByte(lineBytes, anchorByte) < 0 {
				continue
			}
			if !containsFoldASCII(lineBytes, queryLower) {
				continue
			}
			matchSet[i] = struct{}{}
			matchLines = append(matchLines, i)
		}

		fyne.Do(func() {
			if seq != atomic.LoadUint64(&v.searchSeq) {
				return
			}
			v.mu.Lock()
			v.matchLines = matchLines
			v.searchMatchSet = matchSet
			if v.searchStructural {
				v.rebuildViewLinesForMatchesLocked(matchSet)
			}
			if len(matchLines) == 0 {
				v.matchIndex = -1
			} else if v.matchIndex < 0 || v.matchIndex >= len(matchLines) {
				v.matchIndex = 0
			}
			v.mu.Unlock()

			v.updateNavButtons()
			v.updateWindow()
		})
	}(seq, queryLower, candidates)
}

func (v *JSONView) applySearch(q string) {
	v.applySearchAsync(q)
}

func (v *JSONView) expandMatchesLocked() {
	if len(v.matchLines) == 0 {
		return
	}
	changed := false
	for _, line := range v.matchLines {
		if v.expandForLineLocked(line) {
			changed = true
		}
	}
	if changed {
		v.rebuildViewLinesLocked()
	}
}

func (v *JSONView) expandForLineLocked(line int) bool {
	changed := false
	for {
		opened := false
		for start, end := range v.foldRanges {
			if start < line && line <= end && v.folded[start] {
				v.folded[start] = false
				opened = true
				changed = true
			}
		}
		if !opened {
			break
		}
	}
	return changed
}

func (v *JSONView) navigateMatch(step int) {
	v.mu.Lock()
	if len(v.matchLines) == 0 {
		v.mu.Unlock()
		return
	}
	v.matchIndex += step
	if v.matchIndex < 0 {
		v.matchIndex = len(v.matchLines) - 1
	} else if v.matchIndex >= len(v.matchLines) {
		v.matchIndex = 0
	}
	line := v.matchLines[v.matchIndex]
	if v.expandForLineLocked(line) {
		v.rebuildViewLinesLocked()
	}
	if v.searchStructural && v.searchMatchSet != nil && len(v.searchMatchSet) > 0 {
		v.rebuildViewLinesForMatchesLocked(v.searchMatchSet)
	} else if len(v.searchKeys) > 0 {
		v.rebuildViewLinesForKeysLocked(v.searchKeys)
	}
	row := findViewRow(v.viewLines, line)
	v.mu.Unlock()

	v.scrollToViewRow(row)
}

func (v *JSONView) updateNavButtons() {
	if v.searchUp == nil || v.searchDown == nil {
		return
	}
	if len(v.matchLines) == 0 {
		v.searchUp.Disable()
		v.searchDown.Disable()
		return
	}
	v.searchUp.Enable()
	v.searchDown.Enable()
}

// --- Search helpers

func normalizeKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

func unionCandidateLines(index map[string][]int, keys []string) []int {
	if len(keys) == 0 || index == nil {
		return nil
	}
	seen := make(map[int]struct{})
	out := make([]int, 0)
	for _, k := range keys {
		for _, ln := range index[k] {
			if _, ok := seen[ln]; ok {
				continue
			}
			seen[ln] = struct{}{}
			out = append(out, ln)
		}
	}
	sort.Ints(out)
	return out
}
