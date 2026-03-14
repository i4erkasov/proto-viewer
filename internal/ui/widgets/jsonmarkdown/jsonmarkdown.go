//go:build darwin || linux || windows

package jsonmarkdown

import (
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const (
	defaultChunkLines = 200
	scrollThreshold   = 12
)

// JSONMarkdownView renders JSON as markdown with lazy line loading.
type JSONMarkdownView struct {
	mu      sync.Mutex
	lines   []string
	loaded  int
	chunk   int
	loading bool

	rich   *widget.RichText
	scroll *container.Scroll
}

// NewJSONMarkdownView creates a markdown view with lazy loading.
func NewJSONMarkdownView() *JSONMarkdownView {
	v := &JSONMarkdownView{chunk: defaultChunkLines}
	v.rich = widget.NewRichTextFromMarkdown("")
	v.rich.Wrapping = fyne.TextWrapWord
	v.scroll = container.NewScroll(v.rich)
	v.scroll.OnScrolled = func(_ fyne.Position) {
		v.tryLoadMore()
	}
	return v
}

// View returns the scrollable markdown view.
func (v *JSONMarkdownView) View() fyne.CanvasObject {
	return v.scroll
}

// SetJSON resets content and loads the first chunk.
func (v *JSONMarkdownView) SetJSON(s string) {
	v.mu.Lock()
	v.lines = splitLines(s)
	v.loaded = 0
	v.loading = false
	v.mu.Unlock()

	if strings.TrimSpace(s) == "" {
		v.setMarkdown("")
		return
	}

	v.loadMore()
}

func (v *JSONMarkdownView) tryLoadMore() {
	v.mu.Lock()
	if v.loading {
		v.mu.Unlock()
		return
	}
	v.loading = true
	v.mu.Unlock()

	fyne.Do(func() {
		v.mu.Lock()
		v.loading = false
		v.mu.Unlock()

		if v.scroll.Content == nil {
			return
		}
		if v.scroll.Offset.Y+v.scroll.Size().Height < v.scroll.Content.Size().Height-scrollThreshold {
			return
		}
		v.loadMore()
	})
}

func (v *JSONMarkdownView) loadMore() {
	v.mu.Lock()
	if len(v.lines) == 0 {
		v.mu.Unlock()
		v.setMarkdown("```json\n\n```")
		return
	}
	if v.loaded >= len(v.lines) {
		v.mu.Unlock()
		return
	}
	end := v.loaded + v.chunk
	if end > len(v.lines) {
		end = len(v.lines)
	}
	v.loaded = end
	chunk := strings.Join(v.lines[:v.loaded], "\n")
	v.mu.Unlock()

	md := "```json\n" + chunk + "\n```"
	v.setMarkdown(md)
}

func (v *JSONMarkdownView) setMarkdown(md string) {
	fyne.Do(func() {
		v.rich.ParseMarkdown(md)
		v.rich.Refresh()
		v.scroll.Refresh()
	})
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
