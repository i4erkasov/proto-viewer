package application

import "fyne.io/fyne/v2"

type UI struct {
	w       fyne.Window
	content fyne.CanvasObject
}

func New(w fyne.Window) *UI {
	u := &UI{w: w}
	u.content = build(w) // функция из layout.go
	return u
}

func (u *UI) Content() fyne.CanvasObject { return u.content }
