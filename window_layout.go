package main

import (
	"math"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// windowLayout is the window size and the smallest size the user may drag it
// to, both in Wails logical pixels.
type windowLayout struct {
	Width, Height       int
	MinWidth, MinHeight int
}

// baseWindowLayout is what the app is designed around and what it opens with
// on a 1080-pixel-high screen. Every other screen gets this layout scaled.
// The height is the content's 810 px plus the 48 px custom title bar (see
// DESIGN_HEIGHT and DESIGN_TITLEBAR in script.js), scaled like the width.
var baseWindowLayout = windowLayout{Width: 510, Height: 838, MinWidth: 420, MinHeight: 608}

const (
	// referenceScreenHeight is the logical screen height at which the window
	// keeps its base size. Sizes are logical, so a 1080p laptop at 150% display
	// scaling reports 720 here and gets a smaller window, which is what keeps
	// it inside the screen.
	referenceScreenHeight = 1080

	minWindowScale = 0.6
	maxWindowScale = 2.0

	// The window must leave room for the taskbar / dock and the title bar.
	maxScreenHeightShare = 0.92
	maxScreenWidthShare  = 0.9
)

// windowLayoutFor scales baseWindowLayout to a screen of the given logical
// size, keeping the window in the same proportion to the screen as the base
// layout has on a 1080p one. Unknown sizes fall back to the base layout.
func windowLayoutFor(screenWidth, screenHeight int) windowLayout {
	if screenWidth <= 0 || screenHeight <= 0 {
		return baseWindowLayout
	}
	scale := float64(screenHeight) / referenceScreenHeight
	scale = math.Max(minWindowScale, math.Min(maxWindowScale, scale))

	// Never let the window outgrow the screen (portrait or tiny displays).
	scale = math.Min(scale, maxScreenHeightShare*float64(screenHeight)/float64(baseWindowLayout.Height))
	scale = math.Min(scale, maxScreenWidthShare*float64(screenWidth)/float64(baseWindowLayout.Width))

	scaled := func(v int) int { return int(math.Round(float64(v) * scale)) }
	return windowLayout{
		Width:     scaled(baseWindowLayout.Width),
		Height:    scaled(baseWindowLayout.Height),
		MinWidth:  scaled(baseWindowLayout.MinWidth),
		MinHeight: scaled(baseWindowLayout.MinHeight),
	}
}

// pickScreen chooses the screen the window is on, falling back to the primary
// one and then to any screen that reported a size.
func pickScreen(screens []wailsRuntime.Screen) (wailsRuntime.Screen, bool) {
	usable := func(s wailsRuntime.Screen) bool { return s.Size.Width > 0 && s.Size.Height > 0 }
	for _, s := range screens {
		if s.IsCurrent && usable(s) {
			return s, true
		}
	}
	for _, s := range screens {
		if s.IsPrimary && usable(s) {
			return s, true
		}
	}
	for _, s := range screens {
		if usable(s) {
			return s, true
		}
	}
	return wailsRuntime.Screen{}, false
}
