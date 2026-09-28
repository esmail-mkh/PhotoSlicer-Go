package main

import (
	"testing"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func TestWindowLayoutKeepsBaseSizeOn1080p(t *testing.T) {
	if got := windowLayoutFor(1920, 1080); got != baseWindowLayout {
		t.Fatalf("1920x1080 must keep the base layout, got %+v", got)
	}
}

func TestWindowLayoutScalesWithScreenHeight(t *testing.T) {
	cases := []struct {
		name          string
		w, h          int
		width, height int
	}{
		{"1366x768 laptop", 1366, 768, 363, 590},
		{"1600x900", 1600, 900, 425, 692},
		{"1080p at 150% scaling", 1280, 720, 340, 553},
		{"1440p", 2560, 1440, 680, 1107},
		{"4K at 100%", 3840, 2160, 1020, 1660},
		{"ultrawide 1440p", 3440, 1440, 680, 1107},
	}
	for _, c := range cases {
		got := windowLayoutFor(c.w, c.h)
		if got.Width != c.width || got.Height != c.height {
			t.Errorf("%s: window = %dx%d, want %dx%d", c.name, got.Width, got.Height, c.width, c.height)
		}
	}
}

func TestWindowLayoutKeepsProportionsOfTheBaseLayout(t *testing.T) {
	base := float64(baseWindowLayout.Height) / float64(baseWindowLayout.Width)
	minBase := float64(baseWindowLayout.MinHeight) / float64(baseWindowLayout.MinWidth)
	for _, h := range []int{720, 768, 900, 1200, 1440, 2160} {
		l := windowLayoutFor(h*16/9, h)
		if r := float64(l.Height) / float64(l.Width); r < base-0.01 || r > base+0.01 {
			t.Errorf("height %d: window aspect %.3f drifted from %.3f", h, r, base)
		}
		if r := float64(l.MinHeight) / float64(l.MinWidth); r < minBase-0.02 || r > minBase+0.02 {
			t.Errorf("height %d: min aspect %.3f drifted from %.3f", h, r, minBase)
		}
		if l.MinWidth > l.Width || l.MinHeight > l.Height {
			t.Errorf("height %d: minimum %+v exceeds the window", h, l)
		}
	}
}

func TestWindowLayoutAlwaysFitsOnScreen(t *testing.T) {
	screens := [][2]int{
		{1920, 1080}, {1366, 768}, {1280, 720}, {800, 600}, {640, 480}, {400, 300},
		{1080, 1920}, {768, 1024}, {3840, 2160}, {5120, 1440},
	}
	for _, s := range screens {
		l := windowLayoutFor(s[0], s[1])
		if float64(l.Height) > maxScreenHeightShare*float64(s[1])+1 || float64(l.Width) > maxScreenWidthShare*float64(s[0])+1 {
			t.Errorf("screen %dx%d: window %dx%d does not fit", s[0], s[1], l.Width, l.Height)
		}
	}
}

func TestWindowLayoutFallsBackForUnknownScreens(t *testing.T) {
	for _, s := range [][2]int{{0, 0}, {1920, 0}, {0, 1080}, {-1, -1}} {
		if got := windowLayoutFor(s[0], s[1]); got != baseWindowLayout {
			t.Errorf("screen %v: got %+v, want the base layout", s, got)
		}
	}
}

func screenOf(w, h int, current, primary bool) wailsRuntime.Screen {
	s := wailsRuntime.Screen{IsCurrent: current, IsPrimary: primary}
	s.Size.Width, s.Size.Height = w, h
	return s
}

func TestPickScreenPrefersTheCurrentThenPrimaryMonitor(t *testing.T) {
	screens := []wailsRuntime.Screen{
		screenOf(1920, 1080, false, true),
		screenOf(2560, 1440, true, false),
	}
	if got, ok := pickScreen(screens); !ok || got.Size.Height != 1440 {
		t.Fatalf("expected the current monitor, got %+v ok=%v", got, ok)
	}

	screens[1].IsCurrent = false
	if got, ok := pickScreen(screens); !ok || got.Size.Height != 1080 {
		t.Fatalf("expected the primary monitor, got %+v ok=%v", got, ok)
	}

	screens[0].IsPrimary = false
	if got, ok := pickScreen(screens); !ok || got.Size.Height != 1080 {
		t.Fatalf("expected the first usable monitor, got %+v ok=%v", got, ok)
	}
}

func TestPickScreenSkipsMonitorsThatFailedToReport(t *testing.T) {
	// The Windows backend appends an empty Screen{} for a monitor it could not query.
	screens := []wailsRuntime.Screen{{IsCurrent: true}, screenOf(1366, 768, false, true)}
	if got, ok := pickScreen(screens); !ok || got.Size.Height != 768 {
		t.Fatalf("got %+v ok=%v, want the 768p monitor", got, ok)
	}
	if _, ok := pickScreen([]wailsRuntime.Screen{{}}); ok {
		t.Fatal("a screen with no size must not be picked")
	}
	if _, ok := pickScreen(nil); ok {
		t.Fatal("no screens must not yield a pick")
	}
}
