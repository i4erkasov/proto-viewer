//go:build darwin || linux || windows

package jsonviewer

import "sort"

func foldMarker(line int) int {
	return -line - 1
}

func viewLineIndex(v int) int {
	if v < 0 {
		return -v - 1
	}
	return v
}

func viewLineString(buf *JSONBuffer, v int) string {
	if buf == nil {
		return ""
	}
	idx := viewLineIndex(v)
	line := buf.Line(idx)
	if v < 0 {
		return string(buildFoldPlaceholderBytes(line))
	}
	return string(line)
}

func buildViewLineBytes(buf *JSONBuffer, viewLines []int) ([][]byte, []int, []bool) {
	if buf == nil || len(viewLines) == 0 {
		return nil, nil, nil
	}
	lines := make([][]byte, len(viewLines))
	srcLines := make([]int, len(viewLines))
	placeholders := make([]bool, len(viewLines))
	for i, v := range viewLines {
		idx := viewLineIndex(v)
		line := buf.Line(idx)
		if v < 0 {
			lines[i] = buildFoldPlaceholderBytes(line)
			placeholders[i] = true
		} else {
			lines[i] = line
		}
		srcLines[i] = idx
	}
	return lines, srcLines, placeholders
}

func buildFoldPlaceholderBytes(line []byte) []byte {
	idx, brace := findFoldTokenBytes(line)
	if idx == -1 {
		return append([]byte(nil), line...)
	}
	prefix := line[:idx]
	if brace == '[' {
		out := make([]byte, 0, len(prefix)+7)
		out = append(out, prefix...)
		out = append(out, '[', ' ', '.', '.', '.', ' ', ']')
		return out
	}
	out := make([]byte, 0, len(prefix)+7)
	out = append(out, prefix...)
	out = append(out, '{', ' ', '.', '.', '.', ' ', '}')
	return out
}

func findFoldTokenBytes(line []byte) (int, byte) {
	inString := false
	esc := false
	for i, b := range line {
		if inString {
			if esc {
				esc = false
				continue
			}
			if b == '\\' {
				esc = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			continue
		}
		switch b {
		case '{', '[':
			return i, b
		}
	}
	return -1, 0
}

// rebuildCurrentViewLocked перестраивает viewLines с учётом активного режима
// (структурный поиск / фильтр по ключам / обычный). Должна вызываться под v.mu.
func (v *JSONView) rebuildCurrentViewLocked() {
	if v.searchStructural && v.searchMatchSet != nil && len(v.searchMatchSet) > 0 {
		v.rebuildViewLinesForMatchesLocked(v.searchMatchSet)
	} else if len(v.searchKeys) > 0 {
		v.rebuildViewLinesForKeysLocked(v.searchKeys)
	} else {
		v.rebuildViewLinesLocked()
	}
}

// CollapseAll сворачивает все узлы документа.
func (v *JSONView) CollapseAll() {
	v.mu.Lock()
	if v.fullBuf == nil || len(v.foldRanges) == 0 {
		v.mu.Unlock()
		return
	}
	for start := range v.foldRanges {
		v.folded[start] = true
	}
	v.rebuildCurrentViewLocked()
	v.mu.Unlock()
	v.resetScroll()
	v.updateWindow()
}

// CollapseUnchanged сворачивает узлы, не содержащие ни одной изменённой строки
// (C2 — «только изменения»): пути к изменениям остаются раскрытыми, а нетронутые
// поддеревья сворачиваются. changed — множество индексов изменённых исходных строк.
func (v *JSONView) CollapseUnchanged(changed map[int]bool) {
	v.mu.Lock()
	if v.fullBuf == nil || len(v.foldRanges) == 0 {
		v.mu.Unlock()
		return
	}
	chg := make([]int, 0, len(changed))
	for l := range changed {
		chg = append(chg, l)
	}
	sort.Ints(chg)
	for start, end := range v.foldRanges {
		i := sort.SearchInts(chg, start)
		hasChange := i < len(chg) && chg[i] <= end // есть изменение в [start,end]
		v.folded[start] = !hasChange
	}
	v.rebuildCurrentViewLocked()
	v.mu.Unlock()
	v.resetScroll()
	v.updateWindow()
}

// ExpandAll разворачивает все узлы документа.
func (v *JSONView) ExpandAll() {
	v.mu.Lock()
	if v.fullBuf == nil || len(v.foldRanges) == 0 {
		v.mu.Unlock()
		return
	}
	for start := range v.foldRanges {
		v.folded[start] = false
	}
	v.rebuildCurrentViewLocked()
	v.mu.Unlock()
	v.updateWindow()
}

func findViewRow(viewLines []int, srcLine int) int {
	for i, v := range viewLines {
		if viewLineIndex(v) == srcLine {
			return i
		}
	}
	return -1
}

func isKeyLineWithoutBraceBytes(line []byte) bool {
	if _, _, ok := findKeyRange(string(line)); !ok {
		return false
	}
	idx, _ := findFoldTokenBytes(line)
	return idx == -1
}

func (v *JSONView) rebuildViewLinesForMatchesLocked(matchSet map[int]struct{}) {
	v.viewLines = v.viewLines[:0]
	if v.fullLineCountLocked() == 0 || len(matchSet) == 0 {
		return
	}

	ranges := make([]keyRange, 0, len(v.searchKeys))
	if len(v.searchKeys) > 0 {
		for _, r := range v.searchKeyRanges {
			ranges = append(ranges, r)
		}
	}
	allowed := func(line int) bool {
		if len(ranges) == 0 {
			return true
		}
		for _, r := range ranges {
			if line >= r.start && line <= r.end {
				return true
			}
		}
		return false
	}

	foldStarts := make([]int, 0, len(v.foldRanges))
	for s := range v.foldRanges {
		foldStarts = append(foldStarts, s)
	}
	sort.Ints(foldStarts)

	keepBlocks := make(map[int]struct{})
	keepLines := make(map[int]struct{})

	for line := range matchSet {
		if !allowed(line) {
			continue
		}
		keepLines[line] = struct{}{}
		for _, s := range foldStarts {
			end := v.foldRanges[s]
			if s <= line && line <= end {
				keepBlocks[s] = struct{}{}
			}
		}
	}

	for s := range keepBlocks {
		keepLines[s] = struct{}{}
		end := v.foldRanges[s]
		if allowed(end) {
			keepLines[end] = struct{}{}
		}
		prev := s - 1
		if prev >= 0 && allowed(prev) {
			if line := v.fullBuf.Line(prev); isKeyLineWithoutBraceBytes(line) {
				keepLines[prev] = struct{}{}
			}
		}
	}

	lineCount := v.fullLineCountLocked()
	for i := 0; i < lineCount; {
		if !allowed(i) {
			i++
			continue
		}
		if end, ok := v.foldRanges[i]; ok {
			if _, keep := keepBlocks[i]; !keep {
				i = end + 1
				continue
			}
		}
		if _, ok := keepLines[i]; ok {
			v.viewLines = append(v.viewLines, i)
		}
		i++
	}
}
