// Package assets вшивает в бинарь файлы из каталога assets (через go:embed),
// чтобы они были доступны без зависимости от рабочего каталога.
package assets

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Help — встроенная справка (Markdown), показывается в Help → Documentation.
//
//go:embed docs/help.md
var Help string

//go:embed icons/compare.svg
var diffSVG []byte

// DiffIcon — иконка кнопки Diff (моно-SVG, перекрашивается под тему).
var DiffIcon = theme.NewThemedResource(fyne.NewStaticResource("compare.svg", diffSVG))

//go:embed icons/compare-arrow.svg
var compareSVG []byte

// CompareIcon — иконка кнопки Compare (в окне выбора источника B).
var CompareIcon = theme.NewThemedResource(fyne.NewStaticResource("compare-arrow.svg", compareSVG))

//go:embed icons/convert.svg
var convertSVG []byte

// ConvertIcon — иконка кнопки Decode.
var ConvertIcon = theme.NewThemedResource(fyne.NewStaticResource("convert.svg", convertSVG))

//go:embed about.png
var aboutPNG []byte

// AboutImage — картинка для окна About (как есть, без перекраски).
var AboutImage = fyne.NewStaticResource("about.png", aboutPNG)
