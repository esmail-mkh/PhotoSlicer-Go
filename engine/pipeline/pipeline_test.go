package pipeline

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestPipelineMultiModeDateNesting(t *testing.T) {
	tempSrc := t.TempDir()
	tempOut := t.TempDir()

	_ = createTestImages(t, tempSrc, 2, 200, 200)

	dateStr := "2026-09-04"
	opts := PipelineOptions{
		Mode:            "multi",
		NewWidth:        200,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     250,
		CurrentDate:     dateStr,
		OutputBase:      tempOut,
		SaveDirectory:   "Chapter_01",
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	resPath, err := MergerImages(tempSrc, opts)
	if err != nil {
		t.Fatalf("MergerImages in multi mode failed: %v", err)
	}

	expectedParent := filepath.Join(tempOut, dateStr)
	if filepath.Dir(resPath) != expectedParent {
		t.Errorf("expected parent directory to be %s, but got %s", expectedParent, filepath.Dir(resPath))
	}
}

func TestRevengeChapterNoShortSlices(t *testing.T) {
	srcDir := `C:\Users\Alteen Rayane\Downloads\Telegram Desktop\Revenge of Reborn Villainess - Lua Comic`
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		t.Skip("skipping test: chapter dir not found on this machine")
	}

	tempOut := t.TempDir()
	opts := PipelineOptions{
		Mode:            "single",
		NewWidth:        800,
		IsCustomWidth:   true,
		SaveFormat:      "JPG",
		SaveQuality:     95,
		HeightLimit:     15000,
		CurrentDate:     "2026-09-06",
		OutputBase:      tempOut,
		MaxWorkers:      4,
		FilenamePattern: "[number]",
		FilenameDigits:  3,
	}

	resPath, err := MergerImages(srcDir, opts)
	if err != nil {
		t.Fatalf("MergerImages failed: %v", err)
	}

	entries, err := os.ReadDir(resPath)
	if err != nil {
		t.Fatalf("Failed to read output dir: %v", err)
	}

	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".jpg") {
			p := filepath.Join(resPath, e.Name())
			f, err := os.Open(p)
			if err != nil {
				t.Fatalf("failed to open output: %v", err)
			}
			cfg, err := jpeg.DecodeConfig(f)
			f.Close()
			if err != nil {
				t.Fatalf("failed to decode config: %v", err)
			}
			t.Logf("Slice %s: %dx%d", e.Name(), cfg.Width, cfg.Height)
			if cfg.Height < 1000 {
				t.Errorf("Slice %s is unexpectedly short: %d pixels", e.Name(), cfg.Height)
			}
		}
	}
}

func TestPipelineJfifInput(t *testing.T) {
	tempSrc := t.TempDir()
	tempOut := t.TempDir()

	for i := 1; i <= 2; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 150, 200))
		col := color.RGBA{R: uint8(i * 60), G: 100, B: 150, A: 255}
		draw.Draw(img, img.Bounds(), image.NewUniform(col), image.Point{}, draw.Src)
		p := filepath.Join(tempSrc, fmt.Sprintf("page_%02d.jfif", i))
		f, err := os.Create(p)
		if err != nil {
			t.Fatalf("Failed to create jfif image: %v", err)
		}
		if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
			f.Close()
			t.Fatalf("Failed to encode jfif image: %v", err)
		}
		f.Close()
	}

	opts := PipelineOptions{
		Mode:            "single",
		NewWidth:        150,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     250,
		CurrentDate:     "2026-09-07",
		OutputBase:      tempOut,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	resPath, err := MergerImages(tempSrc, opts)
	if err != nil {
		t.Fatalf("MergerImages failed with jfif input: %v", err)
	}

	files, err := os.ReadDir(resPath)
	if err != nil {
		t.Fatalf("Failed to read output dir: %v", err)
	}
	if len(files) < 2 {
		t.Errorf("Expected at least 2 slices, got %d", len(files))
	}
}

func TestMergerImagesConcurrentRunsUseDistinctDirs(t *testing.T) {
	srcA := t.TempDir()
	srcB := t.TempDir()
	tempOut := t.TempDir()
	_ = createTestImages(t, srcA, 2, 40, 200)
	_ = createTestImages(t, srcB, 2, 40, 200)

	opts := PipelineOptions{
		Mode:            "multi",
		NewWidth:        40,
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     120,
		CurrentDate:     "2026-09-10 12-00-00",
		SaveDirectory:   "Chapter 1",
		OutputBase:      tempOut,
		MaxWorkers:      1,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	results := make(chan string, 2)
	var wg sync.WaitGroup
	for _, src := range []string{srcA, srcB} {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			res, err := MergerImages(src, opts)
			if err != nil {
				t.Errorf("MergerImages failed: %v", err)
				return
			}
			results <- res
		}(src)
	}
	wg.Wait()
	close(results)

	seen := make(map[string]bool)
	for p := range results {
		if seen[p] {
			t.Errorf("concurrent runs resolved to the same output: %s", p)
		}
		seen[p] = true
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Errorf("output directory missing on disk: %s", p)
		}
	}
	if len(seen) != 2 {
		t.Errorf("expected 2 distinct output directories, got %d", len(seen))
	}
}
