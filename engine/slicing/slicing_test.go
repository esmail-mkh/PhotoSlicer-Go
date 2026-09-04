package slicing

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSlicing(t *testing.T) {
	t.Run("FormatFilenamePlaceholders", func(t *testing.T) {
		pattern := "[folder]_[number]_page"
		result := FormatFilename(pattern, 5, 3, "jpg", "Chapter1", 10)
		expected := "Chapter1_005_page.jpg"
		if result != expected {
			t.Errorf("Expected %s, got %s", expected, result)
		}

		// Trailing space/dot cleanup & empty fallback
		resBlank := FormatFilename(".  ", 5, 3, "jpg", "", 10)
		if resBlank != "005.jpg" {
			t.Errorf("Expected fallback 005.jpg, got %s", resBlank)
		}
	})

	t.Run("CapSliceGapsLimitsExcessiveHeights", func(t *testing.T) {
		cuts := []int{0, 5000, 15000}
		capped := CapSliceGaps(cuts, 4000)
		expected := []int{0, 4000, 5000, 9000, 13000, 15000}
		if !reflect.DeepEqual(capped, expected) {
			t.Errorf("Expected %v, got %v", expected, capped)
		}
	})

	t.Run("FindSafeCutPoints", func(t *testing.T) {
		// Create a synthetic image with two panels separated by a white gutter
		img := image.NewRGBA(image.Rect(0, 0, 100, 300))
		// Fill panels with dark, and gutter [140, 160] with solid white
		for y := 0; y < 300; y++ {
			c := color.RGBA{R: 50, G: 50, B: 50, A: 255}
			if y >= 135 && y <= 165 {
				c = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			for x := 0; x < 100; x++ {
				img.Set(x, y, c)
			}
		}

		cuts := FindSafeCutPoints(img, 2)
		if len(cuts) == 0 {
			t.Fatalf("expected cut points, got none")
		}
		// The cut should fall inside or close to the white gutter [135, 165]
		foundInGutter := false
		for _, cp := range cuts {
			if cp >= 130 && cp <= 170 {
				foundInGutter = true
				break
			}
		}
		if !foundInGutter {
			t.Errorf("expected a cut point in white gutter [130, 170], got %v", cuts)
		}
	})

	t.Run("SlicerMultiArchiveBothZipAndPdf", func(t *testing.T) {
		tempOut := t.TempDir()
		img := image.NewRGBA(image.Rect(0, 0, 100, 200))
		draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 100, G: 150, B: 200, A: 255}}, image.Point{}, draw.Src)

		opts := SlicerOptions{
			SaveFormat:      "JPG",
			SlicesCount:     2,
			SaveQuality:     90,
			Mode:            "single",
			OutputBase:      tempOut,
			MaxWorkers:      2,
			IsZip:           true,
			IsPdf:           true,
			FilenamePattern: "[number]",
			FilenameDigits:  2,
		}

		resPath, err := Slicer(img, opts)
		if err != nil {
			t.Fatalf("Slicer failed: %v", err)
		}

		baseDir := filepath.Dir(resPath)
		baseStem := strings.TrimSuffix(filepath.Base(resPath), filepath.Ext(resPath))

		zipPath := filepath.Join(baseDir, baseStem+".zip")
		pdfPath := filepath.Join(baseDir, baseStem+".pdf")

		if fi, err := os.Stat(zipPath); err != nil || fi.Size() == 0 {
			t.Errorf("expected valid zip at %s", zipPath)
		}
		if fi, err := os.Stat(pdfPath); err != nil || fi.Size() == 0 {
			t.Errorf("expected valid pdf at %s", pdfPath)
		}
	})

	t.Run("SlicerSubImageNonZeroBounds", func(t *testing.T) {
		tempOut := t.TempDir()
		fullImg := image.NewRGBA(image.Rect(0, 0, 200, 300))
		draw.Draw(fullImg, fullImg.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 100, B: 50, A: 255}}, image.Point{}, draw.Src)

		sub := fullImg.SubImage(image.Rect(20, 30, 180, 270))

		opts := SlicerOptions{
			SaveFormat:      "JPG",
			SlicesCount:     2,
			SaveQuality:     90,
			Mode:            "single",
			OutputBase:      tempOut,
			MaxWorkers:      2,
			FilenamePattern: "[number]",
			FilenameDigits:  2,
		}

		resPath, err := Slicer(sub, opts)
		if err != nil {
			t.Fatalf("Slicer failed with SubImage: %v", err)
		}

		files, err := os.ReadDir(resPath)
		if err != nil || len(files) == 0 {
			t.Fatalf("expected output slices from SubImage, got none")
		}
	})
}
