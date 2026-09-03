package pipeline

import (
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestImages(t *testing.T, dir string, count int, w, h int) []string {
	var paths []string
	for i := 1; i <= count; i++ {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		// Fill with color
		col := color.RGBA{R: uint8(i * 40), G: uint8(i * 30), B: uint8(i * 50), A: 255}
		draw.Draw(img, img.Bounds(), image.NewUniform(col), image.Point{}, draw.Src)

		path := filepath.Join(dir, filepath.Base(filepath.Join(dir, filepath.Clean(filepath.FromSlash(filepath.Join("00"+string(rune('0'+i))+".jpg"))))))
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("Failed to create image: %v", err)
		}
		if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
			f.Close()
			t.Fatalf("Failed to encode image: %v", err)
		}
		f.Close()
		paths = append(paths, path)
	}
	return paths
}

func TestPipelineStitched(t *testing.T) {
	tempSrc := t.TempDir()
	tempOut := t.TempDir()

	_ = createTestImages(t, tempSrc, 3, 200, 300)

	opts := PipelineOptions{
		Mode:            "single",
		NewWidth:        200,
		IsCustomWidth:   true,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     400,
		CurrentDate:     "2026-09-03",
		OutputBase:      tempOut,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  3,
	}

	resPath, err := MergerImages(tempSrc, opts)
	if err != nil {
		t.Fatalf("MergerImages failed: %v", err)
	}

	files, err := os.ReadDir(resPath)
	if err != nil {
		t.Fatalf("Failed to read output dir: %v", err)
	}

	if len(files) < 2 {
		t.Errorf("Expected at least 2 slices for 900px total height with 400px limit, got %d", len(files))
	}
}

func TestPipelineZipAndPdf(t *testing.T) {
	tempSrc := t.TempDir()
	tempOut := t.TempDir()

	_ = createTestImages(t, tempSrc, 2, 200, 200)

	optsZip := PipelineOptions{
		Mode:            "single",
		NewWidth:        200,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     250,
		CurrentDate:     "2026-09-03",
		IsZip:           true,
		OutputBase:      tempOut,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	resZip, err := MergerImages(tempSrc, optsZip)
	if err != nil {
		t.Fatalf("MergerImages zip failed: %v", err)
	}
	if fi, err := os.Stat(resZip); err != nil || fi.Size() == 0 {
		t.Errorf("Expected non-empty zip at %s", resZip)
	}

	optsPdf := PipelineOptions{
		Mode:            "single",
		NewWidth:        200,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     250,
		CurrentDate:     "2026-09-03",
		IsPdf:           true,
		OutputBase:      tempOut,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	resPdf, err := MergerImages(tempSrc, optsPdf)
	if err != nil {
		t.Fatalf("MergerImages pdf failed: %v", err)
	}
	if fi, err := os.Stat(resPdf); err != nil || fi.Size() == 0 {
		t.Errorf("Expected non-empty pdf at %s", resPdf)
	}
}

func TestProcessBatchNoStitchReturnsZip(t *testing.T) {
	tempSrc := t.TempDir()
	tempOut := t.TempDir()

	_ = createTestImages(t, tempSrc, 2, 200, 200)

	opts := PipelineOptions{
		Mode:            "single",
		IsNoStitch:      true,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		IsZip:           true,
		OutputBase:      tempOut,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	resPath, err := MergerImages(tempSrc, opts)
	if err != nil {
		t.Fatalf("MergerImages no-stitch failed: %v", err)
	}

	// Verify that resPath is the actual existing ZIP file and NOT the deleted raw directory
	fi, err := os.Stat(resPath)
	if err != nil {
		t.Fatalf("returned path does not exist on disk: %s (err: %v)", resPath, err)
	}
	if fi.IsDir() {
		t.Errorf("expected return path to be the zip file, but got a directory: %s", resPath)
	}
	if filepath.Ext(resPath) != ".zip" {
		t.Errorf("expected .zip extension, got %s", resPath)
	}
}

func TestPipelineMultiArchiveBothZipAndPdf(t *testing.T) {
	tempSrc := t.TempDir()
	tempOut := t.TempDir()

	_ = createTestImages(t, tempSrc, 2, 200, 200)

	opts := PipelineOptions{
		Mode:            "single",
		NewWidth:        200,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     250,
		CurrentDate:     "2026-09-03",
		IsZip:           true,
		IsPdf:           true,
		OutputBase:      tempOut,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	resPath, err := MergerImages(tempSrc, opts)
	if err != nil {
		t.Fatalf("MergerImages multi-archive failed: %v", err)
	}

	// Both .zip and .pdf must exist on disk!
	baseDir := filepath.Dir(resPath)
	baseStem := strings.TrimSuffix(filepath.Base(resPath), filepath.Ext(resPath))

	zipFile := filepath.Join(baseDir, baseStem+".zip")
	pdfFile := filepath.Join(baseDir, baseStem+".pdf")

	if fi, err := os.Stat(zipFile); err != nil || fi.Size() == 0 {
		t.Errorf("expected valid zip file at %s", zipFile)
	}
	if fi, err := os.Stat(pdfFile); err != nil || fi.Size() == 0 {
		t.Errorf("expected valid pdf file at %s", pdfFile)
	}
}

