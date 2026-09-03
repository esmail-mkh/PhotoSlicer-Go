package enhancer

import (
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

