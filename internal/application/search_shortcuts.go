//go:build darwin || linux || windows

package application

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func registerSearchShortcuts(c fyne.Canvas, setVisible func(bool), isVisible func() bool) {
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierShortcutDefault}, func(shortcut fyne.Shortcut) {
		setVisible(true)
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierControl}, func(shortcut fyne.Shortcut) {
		setVisible(true)
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierSuper}, func(shortcut fyne.Shortcut) {
		setVisible(true)
	})
	c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(shortcut fyne.Shortcut) {
		if isVisible() {
			setVisible(false)
		}
	})
}
