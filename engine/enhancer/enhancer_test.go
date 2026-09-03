package enhancer

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"photoslicer/engine/pipeline"
)

func TestIsPureASCII(t *testing.T) {
	if !isPureASCII("C:\\Users\\Public\\Temp") {
		t.Errorf("expected pure ASCII to be true")
	}
	if isPureASCII("C:\\Users\\فارسی\\Temp") {
		t.Errorf("expected Persian string to be false")
	}
}

func TestGetSafeAsciiTempDir(t *testing.T) {
	dir, err := getSafeAsciiTempDir("photoslicer_test_")
	if err != nil {
		t.Fatalf("getSafeAsciiTempDir failed: %v", err)
	}
	defer os.RemoveAll(dir)

	if !isPureASCII(dir) {
		t.Errorf("expected temp dir to be pure ASCII, got: %s", dir)
	}
}

func TestFindRealEsrganExecutable(t *testing.T) {
	exe := FindRealEsrganExecutable("")
	if exe == "" {
		t.Logf("Real-ESRGAN binary not found (may not be present in CI environment)")
	} else {
		t.Logf("Found Real-ESRGAN executable at: %s", exe)
		if fi, err := os.Stat(exe); err != nil || fi.IsDir() {
			t.Errorf("expected valid executable file, got error: %v", err)
		}
	}
}

func TestRunRealEsrganAIWithPersianPathAndFile(t *testing.T) {
	exe := FindRealEsrganExecutable("")
	if exe == "" {
		t.Skip("Real-ESRGAN executable not found, skipping test")
	}

	// Create a test input folder with Persian name and a Persian file name
	testDir, err := os.MkdirTemp("", "تست_پوشه_فارسی_")
	if err != nil {
		t.Fatalf("failed to create Persian test dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Copy a sample image into testDir with Persian filename
	srcImg := filepath.Join("..", "..", "assets", "app-v4.2-en-image.jpg")
	dstImg := filepath.Join(testDir, "تصویر شماره ۱.jpg")

	if err := copyFile(srcImg, dstImg); err != nil {
		t.Fatalf("failed to copy sample image: %v", err)
	}

	progressCalled := false
	outDir, err := RunRealEsrganAI(exe, testDir, "", func(pct, curr, total int) {
		progressCalled = true
	})
	if err != nil {
		t.Fatalf("RunRealEsrganAI failed on Persian path/file: %v", err)
	}
	defer os.RemoveAll(outDir)

	if !progressCalled {
		t.Errorf("expected progress callback to be called")
	}

	// Verify that the enhanced image exists with the original Persian stem
	expectedOut := filepath.Join(outDir, "تصویر شماره ۱.jpg")
	fi, err := os.Stat(expectedOut)
	if err != nil {
		t.Fatalf("expected enhanced output file %s, but stat failed: %v", expectedOut, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("output file is empty")
	}
}

func TestEnhancerAndPipelineIntegration(t *testing.T) {
	exe := FindRealEsrganExecutable("")
	if exe == "" {
		t.Skip("Real-ESRGAN executable not found, skipping test")
	}

	testDir, err := os.MkdirTemp("", "پوشه_ورودی_")
	if err != nil {
		t.Fatalf("failed to create Persian test dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	srcImg := filepath.Join("..", "..", "assets", "app-v4.2-en-image.jpg")
	dstImg := filepath.Join(testDir, "عکس اول.jpg")
	if err := copyFile(srcImg, dstImg); err != nil {
		t.Fatalf("failed to copy sample image: %v", err)
	}

	enhancedDir, err := RunRealEsrganAI(exe, testDir, "", nil)
	if err != nil {
		t.Fatalf("RunRealEsrganAI failed: %v", err)
	}
	defer os.RemoveAll(enhancedDir)

	// Now run pipeline on the enhancedDir
	outBase, err := os.MkdirTemp("", "خروجی_پایانی_")
	if err != nil {
		t.Fatalf("failed to create output base dir: %v", err)
	}
	defer os.RemoveAll(outBase)

	opts := pipeline.PipelineOptions{
		Mode:            "single",
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     800,
		CurrentDate:     "2026-09-03",
		OutputBase:      outBase,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	finalPath, err := pipeline.MergerImages(enhancedDir, opts)
	if err != nil {
		t.Fatalf("pipeline.MergerImages failed on enhancedDir: %v", err)
	}

	entries, err := os.ReadDir(finalPath)
	if err != nil {
		t.Fatalf("failed to read final path: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected sliced output files in %s, found none", finalPath)
	}
}

func TestFastDenoiseImage(t *testing.T) {
	// Create a test image with distinct features:
	// - White background with slight compression noise (250, 250, 250)
	// - Skin tone region (255, 200, 180)
	// - Sharp black ink line (25, 25, 25)
	const w, h = 100, 100
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x == 50: // Black ink line
				img.SetRGBA(x, y, color.RGBA{R: 25, G: 25, B: 25, A: 255})
			case x < 50: // Skin tone region
				noise := uint8((x + y) % 3) // tiny ± noise
				img.SetRGBA(x, y, color.RGBA{R: 240 + noise, G: 190 + noise, B: 170 + noise, A: 255})
			default: // Background with JPEG-like near-white noise
				noise := uint8((x * y) % 4)
				img.SetRGBA(x, y, color.RGBA{R: 249 + noise, G: 249 + noise, B: 249 + noise, A: 255})
			}
		}
	}

	result := FastDenoiseImage(img)
	if result.Bounds() != img.Bounds() {
		t.Fatalf("bounds mismatch: got %v, want %v", result.Bounds(), img.Bounds())
	}

	// 1. Verify that the ink line at x=50 stayed dark and crisp
	inkPixel := result.RGBAAt(50, 50)
	if inkPixel.R > 35 || inkPixel.G > 35 || inkPixel.B > 35 {
		t.Errorf("ink line was washed out: got %v", inkPixel)
	}

	// 2. Verify that the white background was cleaned
	bgPixel := result.RGBAAt(80, 50)
	if bgPixel.R < 250 || bgPixel.G < 250 || bgPixel.B < 250 {
		t.Errorf("expected white background to be clean, got %v", bgPixel)
	}

	// 3. Verify Chroma (Cb, Cr) preservation on skin tone region (x=25, y=50)
	origColor := img.RGBAAt(25, 50)
	_, origCb, origCr := color.RGBToYCbCr(origColor.R, origColor.G, origColor.B)
	resColor := result.RGBAAt(25, 50)
	_, resCb, resCr := color.RGBToYCbCr(resColor.R, resColor.G, resColor.B)

	diffCb := int(origCb) - int(resCb)
	if diffCb < 0 {
		diffCb = -diffCb
	}
	diffCr := int(origCr) - int(resCr)
	if diffCr < 0 {
		diffCr = -diffCr
	}

	// Tolerance of at most 1 unit due to standard integer rounding in YCbCr-RGB conversion
	if diffCb > 1 || diffCr > 1 {
		t.Errorf("chroma shifted significantly: origCb=%d resCb=%d, origCr=%d resCr=%d", origCb, resCb, origCr, resCr)
	}
}

func TestRunFastEnhancementBatch(t *testing.T) {
	testDir, err := os.MkdirTemp("", "تست_سریع_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	srcImg := filepath.Join("..", "..", "assets", "app-v4.2-en-image.jpg")
	dstImg1 := filepath.Join(testDir, "تصویر_تست_۱.jpg")
	dstImg2 := filepath.Join(testDir, "تصویر_تست_۲.jpg")

	_ = copyFile(srcImg, dstImg1)
	_ = copyFile(srcImg, dstImg2)

	progressCount := 0
	outDir, err := RunFastEnhancement(testDir, 4, func(pct, curr, total int) {
		progressCount++
	})
	if err != nil {
		t.Fatalf("RunFastEnhancement failed: %v", err)
	}
	defer os.RemoveAll(outDir)

	if progressCount == 0 {
		t.Errorf("expected progress callback to be called")
	}

	files, err := os.ReadDir(outDir)
	if err != nil || len(files) < 2 {
		t.Fatalf("expected at least 2 processed files in outDir, got %d", len(files))
	}
}

func BenchmarkFastDenoiseImage(b *testing.B) {
	const w, h = 800, 1200
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FastDenoiseImage(img)
	}
}


