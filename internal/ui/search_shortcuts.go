//go:build darwin || linux || windows

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func registerSearchShortcuts(c fyne.Canvas, setVisible func(bool), isVisible func() bool) {
	// Cmd/Ctrl+F переключает поиск: показать, если скрыт, и скрыть при повторном
	// нажатии. Escape обрабатывается единым обработчиком в layout (чтобы закрывать
	// поиск даже когда поле ввода не в фокусе).
	toggle := func(_ fyne.Shortcut) {
		setVisible(!isVisible())
	}
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierShortcutDefault}, toggle)
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierControl}, toggle)
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierSuper}, toggle)
}
