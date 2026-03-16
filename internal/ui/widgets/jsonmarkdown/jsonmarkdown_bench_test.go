package jsonmarkdown

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

func makeLargeJSON(targetBytes int) string {
	if targetBytes < 1024 {
		targetBytes = 1024
	}
	var sb strings.Builder
	sb.Grow(targetBytes + 128)
	sb.WriteString("{\"items\":[")
	i := 0
	for sb.Len() < targetBytes-8 {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("{\"id\":")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(",\"name\":\"item_")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString("\",\"flags\":[true,false,null],\"value\":")
		sb.WriteString(strconv.Itoa(i % 1000))
		sb.WriteString("}")
		i++
	}
	sb.WriteString("]}")
	return sb.String()
}

func newTestView() *JSONMarkdownView {
	app := test.NewApp()
	win := test.NewWindow(nil)
	win.SetOnClosed(func() {})
	v := NewJSONMarkdownView(win)
	_ = app
	return v
}

func waitForSearch(v *JSONMarkdownView, query string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		v.mu.Lock()
		q := v.searchQuery
		ready := q == query && (v.searchMatchSet != nil)
		v.mu.Unlock()
		if ready || time.Now().After(deadline) {
			return
		}
		runtime.Gosched()
	}
}

func BenchmarkSetJSON_5MB(b *testing.B) {
	v := newTestView()
	json := makeLargeJSON(5 << 20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.SetJSON(json)
	}
}

func BenchmarkSetJSON_50MB(b *testing.B) {
	v := newTestView()
	json := makeLargeJSON(50 << 20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.SetJSON(json)
	}
}

func BenchmarkSearch_5MB(b *testing.B) {
	v := newTestView()
	json := makeLargeJSON(5 << 20)
	v.SetJSON(json)
	query := "item_1234"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.applySearchAsync(query)
		waitForSearch(v, query, 2*time.Second)
	}
}

func BenchmarkSetGrid_Chunk_5MB(b *testing.B) {
	v := newTestView()
	json := makeLargeJSON(5 << 20)
	v.SetJSON(json)
	v.mu.Lock()
	end := v.loaded
	if end == 0 {
		end = minInt(v.chunk, len(v.viewLines))
		v.loaded = end
	}
	lines := append([]int(nil), v.viewLines[:end]...)
	v.mu.Unlock()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.setGrid(lines)
	}
}

func BenchmarkFoldUnfold_5MB(b *testing.B) {
	v := newTestView()
	json := makeLargeJSON(5 << 20)
	v.SetJSON(json)

	v.mu.Lock()
	foldStart := -1
	for s := range v.foldRanges {
		foldStart = s
		break
	}
	v.mu.Unlock()
	if foldStart == -1 {
		b.Skip("no fold ranges")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.mu.Lock()
		v.folded[foldStart] = !v.folded[foldStart]
		v.rebuildViewLinesLocked()
		end := minInt(v.chunk, len(v.viewLines))
		lines := append([]int(nil), v.viewLines[:end]...)
		v.loaded = end
		v.mu.Unlock()
		v.setGrid(lines)
	}
}
