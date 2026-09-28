package main

// windowCornerRadius is the radius, in logical pixels, of the window's rounded
// corners. Keep it equal to --window-radius in frontend/styles.css.
const windowCornerRadius = 8

// UpdateWindowShape rounds the corners of the frameless window. The frontend
// calls it after every size change, because on Windows 10 the shape is a window
// region that has to be recomputed for the new size (and dropped when the
// window is maximised).
func (a *App) UpdateWindowShape() {
	applyWindowShape(windowCornerRadius)
}
