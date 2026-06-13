//go:build darwin || linux || windows

package jsonviewer

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"github.com/bytedance/sonic"
)

// SetJSON resets content and renders the first viewport.
func (v *JSONView) SetJSON(s string) {
	v.mu.Lock()
	v.winStart = 0
	v.folded = map[int]bool{}
	v.matchLines = nil
	v.matchIndex = -1
	v.highlights = nil
	v.searchQuery = ""
	v.searchKeys = nil
	v.searchKeyIndex = nil
	v.searchAll = nil
	v.searchKeyRanges = nil
	v.searchKeyFold = nil
	v.searchMatchSet = nil
	v.trigramIndex = nil
	v.trigramEnabled = false
	v.trigramCapBytes = 0
	v.trigramUsedBytes = 0
	v.lineNumWidth = 0
	v.selectedKeyLine = -1
	v.selectedKeyRange = highlightRange{}
	v.selectedKeyValue = ""
	v.selectedValueLine = -1
	v.selectedValueRange = highlightRange{}
	v.selectedValueText = ""
	v.selActive = false
	v.selecting = false
	v.diffLines = nil
	v.mu.Unlock()
	v.SetSearchVisible(false)

	if strings.TrimSpace(s) == "" {
		v.mu.Lock()
		v.fullBuf = nil
		v.viewLines = v.viewLines[:0]
		v.contentW = 0
		v.mu.Unlock()
		v.resetScroll()
		v.updateWindow()
		v.setSearchKeys(nil)
		return
	}

	data := []byte(s)
	prettyBytes := data

	if sonic.Valid(data) {
		var buf bytes.Buffer
		if err := json.Indent(&buf, data, "", "  "); err == nil {
			prettyBytes = buf.Bytes()
		}
	}

	bundle := buildIndexBundleFromBytes(prettyBytes)

	v.mu.Lock()
	v.fullBuf = bundle.buf
	v.foldRanges = bundle.foldRanges
	v.folded = make(map[int]bool, len(bundle.foldRanges))
	for start := range bundle.foldRanges {
		if bundle.foldDepths[start] > 0 {
			v.folded[start] = true
		}
	}
	v.searchKeyIndex = bundle.searchIndex
	v.searchAll = bundle.searchAll
	v.searchKeyRanges = bundle.keyRanges
	v.searchKeyFold = bundle.keyFold
	v.lineNumWidth = bundle.lineNumWidth
	v.trigramIndex = bundle.trigramIndex
	v.trigramPostings = bundle.trigramPostings
	v.trigramEnabled = bundle.trigramEnabled
	v.trigramCapBytes = bundle.trigramCapBytes
	v.trigramUsedBytes = bundle.trigramUsedBytes
	v.rebuildViewLinesLocked()
	v.mu.Unlock()

	v.setSearchKeys(bundle.topKeys)
	v.resetScroll()
	v.updateWindow()
}

func (v *JSONView) applyKeyFilter(key string) {
	v.applyKeyFilterKeys(normalizeKeys([]string{key}))
}

func (v *JSONView) applyKeyFilterKeys(keys []string) {
	v.mu.Lock()
	if len(keys) == 0 {
		v.rebuildViewLinesLocked()
	} else {
		for _, key := range keys {
			if v.searchKeyFold != nil {
				if fs, ok := v.searchKeyFold[key]; ok {
					v.folded[fs] = false
				}
			}
			v.rebuildViewLinesForKeysLocked(keys)
		}
	}
	v.mu.Unlock()

	v.updateWindow()
}

func (v *JSONView) rebuildViewLinesForKeysLocked(keys []string) {
	v.viewLines = v.viewLines[:0]
	if len(keys) == 0 || v.fullLineCountLocked() == 0 || v.searchKeyRanges == nil {
		return
	}

	type keyBlock struct {
		start     int
		end       int
		foldStart int
	}
	blocks := make([]keyBlock, 0, len(keys))
	for _, key := range keys {
		rng, ok := v.searchKeyRanges[key]
		if !ok {
			continue
		}
		foldStart := -1
		if v.searchKeyFold != nil {
			if fs, ok := v.searchKeyFold[key]; ok {
				foldStart = fs
			}
		}
		blocks = append(blocks, keyBlock{start: rng.start, end: rng.end, foldStart: foldStart})
	}
	if len(blocks) == 0 {
		return
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].start < blocks[j].start })

	lineCount := v.fullLineCountLocked()
	for _, block := range blocks {
		start := block.start
		end := block.end
		if start < 0 {
			start = 0
		}
		if end >= lineCount {
			end = lineCount - 1
		}
		for i := start; i <= end; {
			if i < 0 || i >= lineCount {
				break
			}
			if i == start && block.foldStart == i+1 {
				if foldEnd, ok := v.foldRanges[block.foldStart]; ok && foldEnd > block.foldStart && v.folded[block.foldStart] {
					clampedEnd := foldEnd
					if clampedEnd > end {
						clampedEnd = end
					}
					v.viewLines = append(v.viewLines, i)
					v.viewLines = append(v.viewLines, foldMarker(block.foldStart))
					i = clampedEnd + 1
					continue
				}
			}
			if foldEnd, ok := v.foldRanges[i]; ok && foldEnd > i && v.folded[i] {
				clampedEnd := foldEnd
				if clampedEnd > end {
					clampedEnd = end
				}
				v.viewLines = append(v.viewLines, foldMarker(i))
				i = clampedEnd + 1
				continue
			}
			v.viewLines = append(v.viewLines, i)
			i++
		}
	}
}

func (v *JSONView) setSearchKeys(keys []string) {
	if keys == nil {
		keys = nil
	}
	opts := make([]string, 0, len(keys))
	opts = append(opts, keys...)
	fyne.Do(func() {
		if v.searchKeySelect == nil {
			return
		}
		v.searchKeySelect.SetOptions(opts)
		v.searchKeySelect.SetSelectedValues(nil)
		v.searchKeySelect.Refresh()
	})
}

func (v *JSONView) fullLineCountLocked() int {
	if v.fullBuf != nil {
		return v.fullBuf.LineCount()
	}
	return 0
}

func (v *JSONView) rebuildViewLinesLocked() {
	v.viewLines = v.viewLines[:0]
	if v.fullLineCountLocked() == 0 {
		return
	}
	for i := 0; i < v.fullLineCountLocked(); {
		if end, ok := v.foldRanges[i]; ok && end > i && v.folded[i] {
			v.viewLines = append(v.viewLines, foldMarker(i))
			i = end + 1
			continue
		}
		v.viewLines = append(v.viewLines, i)
		i++
	}
}

func (v *JSONView) fullLineBytes(i int) []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fullBuf == nil {
		return nil
	}
	return v.fullBuf.Line(i)
}

// --- Fold ranges & index

type indexBundle struct {
	buf              *JSONBuffer
	foldRanges       map[int]int
	foldDepths       map[int]int
	keyRanges        map[string]keyRange
	keyFold          map[string]int
	searchIndex      map[string][]int
	searchAll        []int
	trigramIndex     map[[3]byte]trigramRange
	trigramPostings  []int32
	trigramEnabled   bool
	trigramCapBytes  int
	trigramUsedBytes int
	lineNumWidth     int
	topKeys          []string
}

type trigramRange struct {
	offset int
	length int
}

func buildIndexBundleFromBytes(data []byte) indexBundle {
	bundle := indexBundle{}
	if len(data) == 0 {
		bundle.buf = &JSONBuffer{}
		return bundle
	}

	estLines := len(data) / 80
	if estLines < 1 {
		estLines = 1
	}
	lineOffsets := make([]int, 0, estLines+1)
	lineOffsets = append(lineOffsets, 0)
	foldRanges := make(map[int]int, estLines/2+1)
	foldDepths := make(map[int]int, estLines/2+1)
	allLines := make([]int, 0, estLines)
	keyRanges := make(map[string]keyRange, estLines/8+1)
	keyFold := make(map[string]int, estLines/8+1)
	keyStarts := make(map[string]int, estLines/8+1)
	keyOrder := make([]int, 0, estLines/8+1)
	keyNames := make([]string, 0, estLines/8+1)

	capBytes := trigramCapBytes(len(data))
	trigramEnabled := capBytes > 0
	trigramUsed := 0
	var trigramIndex map[[3]byte]trigramRange
	postingsCap := capBytes / 4
	if postingsCap < 1024 {
		postingsCap = 1024
	}
	if postingsCap > len(data) {
		postingsCap = len(data)
	}
	postings := make([]int32, 0, postingsCap)
	if trigramEnabled {
		trigramIndex = make(map[[3]byte]trigramRange, len(data)/64+1)
	}

	type foldEntry struct {
		line  int
		depth int
	}
	stack := make([]foldEntry, 0, 32)
	depth := 0
	inString := false
	esc := false

	lineStart := 0
	lineNum := 0

	finalizeLine := func(end int) {
		lineEnd := end
		if lineEnd > lineStart && data[lineEnd-1] == '\r' {
			lineEnd--
		}
		line := data[lineStart:lineEnd]
		allLines = append(allLines, lineNum)

		if lineIndentDepthBytes(line) == 1 {
			if key, ok := extractLineKeyBytes(line); ok {
				if _, exists := keyStarts[key]; !exists {
					keyStarts[key] = lineNum
					keyOrder = append(keyOrder, lineNum)
					keyNames = append(keyNames, key)
				}
			}
		}

		if trigramEnabled {
			if len(line) >= 3 {
				var seen map[[3]byte]struct{}
				if len(line) > 64 {
					seen = make(map[[3]byte]struct{}, 16)
				}
				for i := 0; i+2 < len(line); i++ {
					tri := [3]byte{toLowerASCII(line[i]), toLowerASCII(line[i+1]), toLowerASCII(line[i+2])}
					if seen != nil {
						if _, ok := seen[tri]; ok {
							continue
						}
						seen[tri] = struct{}{}
					}
					if r, ok := trigramIndex[tri]; ok {
						postings = append(postings, int32(lineNum))
						r.length++
						trigramIndex[tri] = r
						trigramUsed += 4
					} else {
						trigramIndex[tri] = trigramRange{offset: len(postings), length: 1}
						postings = append(postings, int32(lineNum))
						trigramUsed += 28
					}
					if trigramUsed > capBytes {
						trigramIndex = nil
						trigramEnabled = false
						postings = nil
						trigramUsed = 0
						break
					}
				}
			}
		}
	}

	for i := 0; i < len(data); i++ {
		b := data[i]

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
		} else {
			if b == '"' {
				inString = true
			} else {
				switch b {
				case '{', '[':
					stack = append(stack, foldEntry{line: lineNum, depth: depth})
					depth++
				case '}', ']':
					if len(stack) > 0 {
						depth--
						open := stack[len(stack)-1]
						stack = stack[:len(stack)-1]
						if open.line < lineNum {
							foldRanges[open.line] = lineNum
							foldDepths[open.line] = open.depth
						}
					}
				case '\n':
					finalizeLine(i)
					lineStart = i + 1
					lineNum++
					lineOffsets = append(lineOffsets, lineStart)
				}
			}
		}
	}
	finalizeLine(len(data))

	buf := &JSONBuffer{data: data, lineOffsets: lineOffsets}

	lineCount := buf.LineCount()
	for idx, start := range keyOrder {
		if idx >= len(keyNames) {
			break
		}
		key := keyNames[idx]
		end := lineCount - 1
		if idx+1 < len(keyOrder) {
			end = keyOrder[idx+1] - 1
		}
		if end < start {
			end = start
		}
		foldStart := -1
		if _, ok := foldRanges[start]; ok {
			foldStart = start
		} else if start+1 <= end {
			if _, ok := foldRanges[start+1]; ok {
				foldStart = start + 1
			}
		}
		if foldStart != -1 {
			if foldEnd, ok := foldRanges[foldStart]; ok && foldEnd > end {
				end = foldEnd
			}
			keyFold[key] = foldStart
		}
		keyRanges[key] = keyRange{start: start, end: end}
	}

	searchIndex := make(map[string][]int, len(keyRanges))
	for key, rng := range keyRanges {
		count := rng.end - rng.start + 1
		if count < 0 {
			continue
		}
		lines := make([]int, 0, count)
		for i := rng.start; i <= rng.end && i < lineCount; i++ {
			lines = append(lines, i)
		}
		searchIndex[key] = lines
	}

	bundle.buf = buf
	bundle.foldRanges = foldRanges
	bundle.foldDepths = foldDepths
	bundle.keyRanges = keyRanges
	bundle.keyFold = keyFold
	bundle.searchIndex = searchIndex
	bundle.searchAll = allLines
	bundle.trigramIndex = trigramIndex
	bundle.trigramPostings = postings
	bundle.trigramEnabled = trigramEnabled
	bundle.trigramCapBytes = capBytes
	bundle.trigramUsedBytes = trigramUsed
	bundle.lineNumWidth = len(strconv.Itoa(lineCount))
	bundle.topKeys = topKeysFromRanges(keyRanges)
	return bundle
}

// --- Helpers restored

func topKeysFromRanges(ranges map[string]keyRange) []string {
	if len(ranges) == 0 {
		return nil
	}
	keys := make([]string, 0, len(ranges))
	for k := range ranges {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func trigramCapBytes(dataSize int) int {
	if dataSize <= 0 {
		return 0
	}
	capBytes := dataSize / 10
	if capBytes < 8<<20 {
		capBytes = 8 << 20
	}
	if capBytes > 64<<20 {
		capBytes = 64 << 20
	}
	return capBytes
}

func toLowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
