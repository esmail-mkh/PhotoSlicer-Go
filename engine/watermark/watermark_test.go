package watermark

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func createTestWatermark(t *testing.T, dir string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Fill with semi-transparent red
	col := color.RGBA{R: 255, G: 0, B: 0, A: 128}
	draw.Draw(img, img.Bounds(), image.NewUniform(col), image.Point{}, draw.Src)

	path := filepath.Join(dir, fmt.Sprintf("watermark_%d_%d.png", w, h))
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create watermark file: %v", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		t.Fatalf("Failed to encode watermark PNG: %v", err)
	}
	return path
}

func TestPrepareWatermark(t *testing.T) {
	tempDir := t.TempDir()
	wmPath := createTestWatermark(t, tempDir, 250, 75)

	t.Run("NativeDimensionsPreserved", func(t *testing.T) {
		// When widthPercent is 0, watermark should retain exact native 250x75 dimensions
		wm := PrepareWatermark(wmPath, 800, 2000, 1, 0)
		if wm == nil {
			t.Fatal("Expected watermark, got nil")
		}

		gotW := wm.Bounds().Dx()
		gotH := wm.Bounds().Dy()
		if gotW != 250 || gotH != 75 {
			t.Errorf("Expected native size 250x75, got %dx%d", gotW, gotH)
		}
	})

	t.Run("DownscaleWhenWiderThanCanvas", func(t *testing.T) {
		largeWmPath := createTestWatermark(t, tempDir, 1000, 200)
		// Canvas is 800px wide, so watermark should scale down to width 800, height 160
		wm := PrepareWatermark(largeWmPath, 800, 3000, 1, 0)
		if wm == nil {
			t.Fatal("Expected watermark, got nil")
		}

		gotW := wm.Bounds().Dx()
		gotH := wm.Bounds().Dy()
		if gotW != 800 || gotH != 160 {
			t.Errorf("Expected scaled size 800x160, got %dx%d", gotW, gotH)
		}
	})

	t.Run("DownscaleWhenTallerThanSegment", func(t *testing.T) {
		tallWmPath := createTestWatermark(t, tempDir, 200, 500)
		// Canvas is 800x400 with count=2, so segment height is 200px
		// Watermark should scale down to height 200, width 80
		wm := PrepareWatermark(tallWmPath, 800, 400, 2, 0)
		if wm == nil {
			t.Fatal("Expected watermark, got nil")
		}

		gotW := wm.Bounds().Dx()
		gotH := wm.Bounds().Dy()
		if gotH != 200 || gotW != 80 {
			t.Errorf("Expected scaled size 80x200, got %dx%d", gotW, gotH)
		}
	})

	t.Run("CustomWidthPercentSupported", func(t *testing.T) {
		// If widthPercent > 0 (e.g. 50% of 800 = 400)
		wm := PrepareWatermark(wmPath, 800, 2000, 1, 50)
		if wm == nil {
			t.Fatal("Expected watermark, got nil")
		}

		gotW := wm.Bounds().Dx()
		gotH := wm.Bounds().Dy()
		if gotW != 400 || gotH != 120 {
			t.Errorf("Expected 50%% size 400x120, got %dx%d", gotW, gotH)
		}
	})
}

func TestWatermarkPlacements(t *testing.T) {
	tempDir := t.TempDir()
	wmPath := createTestWatermark(t, tempDir, 200, 50)

	canvas := image.NewRGBA(image.Rect(0, 0, 800, 1000))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)

	t.Run("RightEdgeWithMargin", func(t *testing.T) {
		placements, wm := ComputeWatermarkPlacements(canvas, wmPath, 1, "right", 0, 30)
		if len(placements) != 1 {
			t.Fatalf("Expected 1 placement, got %d", len(placements))
		}
		if wm.Bounds().Dx() != 200 || wm.Bounds().Dy() != 50 {
			t.Errorf("Expected native dimensions 200x50, got %dx%d", wm.Bounds().Dx(), wm.Bounds().Dy())
		}

		// x = 800 - 30 - 200 = 570
		expectedX := 570
		if placements[0].X != expectedX {
			t.Errorf("Expected X position %d, got %d", expectedX, placements[0].X)
		}
	})

	t.Run("LeftEdgeWithMargin", func(t *testing.T) {
		placements, wm := ComputeWatermarkPlacements(canvas, wmPath, 1, "left", 0, 25)
		if len(placements) != 1 {
			t.Fatalf("Expected 1 placement, got %d", len(placements))
		}
		if wm.Bounds().Dx() != 200 || wm.Bounds().Dy() != 50 {
			t.Errorf("Expected native dimensions 200x50, got %dx%d", wm.Bounds().Dx(), wm.Bounds().Dy())
		}

		expectedX := 25
		if placements[0].X != expectedX {
			t.Errorf("Expected X position %d, got %d", expectedX, placements[0].X)
		}
	})

	t.Run("ApplyWatermarkCompositesPixels", func(t *testing.T) {
		placements, _ := ComputeWatermarkPlacements(canvas, wmPath, 1, "left", 0, 0)
		if len(placements) == 0 {
			t.Fatal("Expected at least one placement")
		}

		result := ApplyWatermark(canvas, wmPath, 1, "left", 0, 0)
		if result == nil {
			t.Fatal("Expected valid composite image, got nil")
		}

		// Verify watermark was blended at placement coordinates
		c := result.At(placements[0].X+10, placements[0].Y+10)
		_, g, b, _ := c.RGBA()
		// Pure white is 65535, 65535, 65535. Watermark is semi-transparent red, so green & blue will be lower.
		if g == 65535 && b == 65535 {
			t.Errorf("Expected watermark blended at (%d, %d), but pixel is white: G=%d B=%d",
				placements[0].X+10, placements[0].Y+10, g, b)
		}
	})

	t.Run("AvoidsSpeechBubble", func(t *testing.T) {
		// Create canvas with artwork (mid-tone gray)
		comicCanvas := image.NewRGBA(image.Rect(0, 0, 800, 1000))
		draw.Draw(comicCanvas, comicCanvas.Bounds(), image.NewUniform(color.RGBA{R: 140, G: 140, B: 140, A: 255}), image.Point{}, draw.Src)

		// Draw a speech bubble at the left (x: 0..250, y: 10..150)
		// White interior
		bubbleRect := image.Rect(0, 10, 250, 150)
		draw.Draw(comicCanvas, bubbleRect, image.White, image.Point{}, draw.Src)
		// Scatter text pixels inside bubble (dark dots)
		for by := 30; by < 130; by += 8 {
			for bx := 20; bx < 220; bx += 4 {
				comicCanvas.SetRGBA(bx, by, color.RGBA{R: 20, G: 20, B: 20, A: 255})
			}
		}

		// Watermark is 200x50 on left edge. Without avoidance, it would sit in y: 10..150.
		// With smart detection, it must dodge the speech bubble!
		placements, _ := ComputeWatermarkPlacements(comicCanvas, wmPath, 1, "left", 0, 0)
		if len(placements) == 0 {
			t.Fatal("Expected placement, got none")
		}

		wmY := placements[0].Y
		// Watermark must not overlap the speech bubble interior (y: 10..150)
		if wmY >= 10 && wmY < 150 {
			t.Errorf("Watermark was placed inside speech bubble at Y=%d, expected avoidance!", wmY)
		}
	})

	t.Run("LargeMarginNoPanic", func(t *testing.T) {
		comicCanvas := image.NewRGBA(image.Rect(0, 0, 400, 800))
		draw.Draw(comicCanvas, comicCanvas.Bounds(), image.White, image.Point{}, draw.Src)

		// Test edge = right with margin > width (650 > 400)
		placements, _ := ComputeWatermarkPlacements(comicCanvas, wmPath, 1, "right", 0, 650)
		if len(placements) == 0 {
			t.Fatal("Expected fallback placement, got none")
		}

		// Test edge = left with margin > width
		placementsLeft, _ := ComputeWatermarkPlacements(comicCanvas, wmPath, 1, "left", 0, 500)
		if len(placementsLeft) == 0 {
			t.Fatal("Expected fallback placement, got none")
		}
	})
}

func TestExtractGrayAndSaturationSubImage(t *testing.T) {
	parent := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(parent, parent.Bounds(), image.White, image.Point{}, draw.Src)

	// In the bottom right corner (100, 100) to (200, 200), fill with red
	redRect := image.Rect(100, 100, 200, 200)
	draw.Draw(parent, redRect, image.NewUniform(color.RGBA{R: 255, G: 0, B: 0, A: 255}), image.Point{}, draw.Src)

	// Take a SubImage of the red area
	sub := parent.SubImage(redRect)
	gray, sat, w, h := ExtractGrayAndSaturation(sub)
	if w != 100 || h != 100 {
		t.Fatalf("Expected 100x100, got %dx%d", w, h)
	}

	// Red pixel in grayscale is (255*77) >> 8 = 76
	// Saturation is 255
	if gray[0] != 76 {
		t.Errorf("Expected gray[0] == 76 for SubImage, got %d", gray[0])
	}
	if sat[0] != 255 {
		t.Errorf("Expected sat[0] == 255 for SubImage, got %d", sat[0])
	}
}

