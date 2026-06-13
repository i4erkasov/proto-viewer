// Package assets вшивает в бинарь файлы из каталога assets (через go:embed),
// чтобы они были доступны без зависимости от рабочего каталога.
package assets

import _ "embed"

// Help — встроенная справка (Markdown), показывается в Help → Documentation.
//
//go:embed help.md
var Help string
