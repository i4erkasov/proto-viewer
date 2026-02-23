package components

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// JSONView is a lightweight read-only viewer with basic syntax highlighting.
// It formats JSON (2-space indent) and then tokenizes it into colored segments.
//
// This is intentionally simple (no AST features, folding, searching, etc.),
// but looks much nicer than plain text output.
type JSONView struct {
	*widget.RichText
	lastPretty string
}

const maxHighlightBytes = 300_000 // ~300KB: баланс красоты и производительности

func NewJSONView() *JSONView {
	rt := widget.NewRichText()
	rt.Wrapping = fyne.TextWrapWord
	return &JSONView{RichText: rt}
}

func (v *JSONView) SetJSON(raw string) {
	pretty, ok := formatJSON(raw)
	if !ok {
		v.lastPretty = raw
		v.Segments = []widget.RichTextSegment{monoSegment(raw)}
		v.Refresh()
		return
	}

	v.lastPretty = pretty

	// На больших JSON подсветка может быть слишком тяжёлой — показываем моноширинно без токенизации.
	if len(pretty) > maxHighlightBytes {
		v.Segments = []widget.RichTextSegment{monoSegment(pretty)}
		v.Refresh()
		return
	}

	v.Segments = jsonSegments(pretty)
	v.Refresh()
}

func (v *JSONView) Clear() {
	v.lastPretty = ""
	v.Segments = nil
	v.Refresh()
}

func monoSegment(s string) widget.RichTextSegment {
	seg := &widget.TextSegment{Text: s}
	seg.Style.Inline = true
	seg.Style.TextStyle = fyne.TextStyle{Monospace: true}
	seg.Style.ColorName = theme.ColorNameForeground
	return seg
}

func formatJSON(s string) (string, bool) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return "", false
	}
	return buf.String(), true
}

func jsonSegments(s string) []widget.RichTextSegment {
	mk := func(text string, cn fyne.ThemeColorName, style fyne.TextStyle) widget.RichTextSegment {
		seg := &widget.TextSegment{Text: text}
		seg.Style.Inline = true
		seg.Style.ColorName = cn
		seg.Style.TextStyle = style
		return seg
	}

	mono := fyne.TextStyle{Monospace: true}
	monoBold := fyne.TextStyle{Monospace: true, Bold: true}

	colPunct := theme.ColorNameDisabled
	colKey := theme.ColorNamePrimary
	colString := theme.ColorNameForeground
	colNumber := theme.ColorNamePrimary
	colBoolNull := theme.ColorNamePrimary

	isNum := func(r rune) bool {
		return r == '-' || r == '+' || r == '.' || r == 'e' || r == 'E' || unicode.IsDigit(r)
	}

	// helper: check if the string at position j (after a string token) is a key (next non-space is ':')
	isKeyAfter := func(j int) bool {
		for j < len(s) {
			c := s[j]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				j++
				continue
			}
			return c == ':'
		}
		return false
	}

	var out []widget.RichTextSegment
	for i := 0; i < len(s); {
		ch := s[i]
		switch ch {
		case '{', '}', '[', ']', ':', ',':
			out = append(out, mk(string(ch), colPunct, mono))
			i++
		case ' ', '\t', '\n', '\r':
			j := i + 1
			for j < len(s) {
				c := s[j]
				if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
					break
				}
				j++
			}
			out = append(out, mk(s[i:j], colPunct, mono))
			i = j
		case '"':
			j := i + 1
			esc := false
			for j < len(s) {
				c := s[j]
				if esc {
					esc = false
					j++
					continue
				}
				if c == '\\' {
					esc = true
					j++
					continue
				}
				if c == '"' {
					j++
					break
				}
				j++
			}
			strTok := s[i:j]
			if isKeyAfter(j) {
				out = append(out, mk(strTok, colKey, monoBold))
			} else {
				out = append(out, mk(strTok, colString, mono))
			}
			i = j
		default:
			j := i
			for j < len(s) {
				c := s[j]
				if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == ']' || c == '}' {
					break
				}
				j++
			}
			tok := s[i:j]
			if tok == "true" || tok == "false" || tok == "null" {
				out = append(out, mk(tok, colBoolNull, mono))
			} else {
				isNumber := len(tok) > 0
				for _, r := range tok {
					if !isNum(r) {
						isNumber = false
						break
					}
				}
				if isNumber {
					out = append(out, mk(tok, colNumber, mono))
				} else {
					out = append(out, mk(tok, colString, mono))
				}
			}
			i = j
		}
	}
	return out
}

func (v *JSONView) PlainText() string {
	if v == nil {
		return ""
	}
	if v.lastPretty != "" {
		return v.lastPretty
	}
	if v.RichText == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range v.Segments {
		if s != nil {
			b.WriteString(s.Textual())
		}
	}
	return b.String()
}
