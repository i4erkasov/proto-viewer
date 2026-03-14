package statuslabel

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
)

// Level describes semantic status used for coloring.
// Keep it simple so callers can map their own texts.
type Level int

const (
	LevelNeutral Level = iota
	LevelOK
	LevelWarn
	LevelError
)

// New creates a text object that can be recolored based on status level.
// Use Set() to update text+level.
func New(initial string) *canvas.Text {
	t := canvas.NewText(initial, theme.ForegroundColor())
	t.TextSize = theme.TextSize()
	t.Alignment = fyne.TextAlignTrailing
	return t
}

func colorFor(level Level) color.Color {
	switch level {
	case LevelOK:
		return theme.SuccessColor()
	case LevelWarn:
		// Fyne doesn't expose a warning color; use a readable amber.
		return color.NRGBA{R: 0xF5, G: 0xA6, B: 0x23, A: 0xFF}
	case LevelError:
		return theme.ErrorColor()
	default:
		return theme.ForegroundColor()
	}
}

// Set updates the label text and color.
func Set(t *canvas.Text, level Level, text string) {
	if t == nil {
		return
	}
	fyne.Do(func() {
		t.Text = text
		t.Color = colorFor(level)
		canvas.Refresh(t)
	})
}
