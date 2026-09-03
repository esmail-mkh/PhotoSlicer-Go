package psd

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestSavePSDLayered(t *testing.T) {
	tempDir := t.TempDir()

	const w, h = 100, 150
	baseImg := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			baseImg.Set(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y), B: 180, A: 255})
		}
	}

	psdPath := filepath.Join(tempDir, "test.psd")
	if err := SavePSDLayered(baseImg, psdPath, false, "", 1, "right", 0, 0); err != nil {
		t.Fatalf("SavePSDLayered failed: %v", err)
	}

	fi, err := os.Stat(psdPath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected valid PSD file on disk, got error: %v", err)
	}

	// Verify header starts with 8BPS
	data, err := os.ReadFile(psdPath)
	if err != nil {
		t.Fatalf("failed to read psd: %v", err)
	}
	if string(data[:4]) != "8BPS" {
		t.Errorf("expected 8BPS header signature, got: %s", string(data[:4]))
	}
}
