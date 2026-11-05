package archive

import (
	"archive/zip"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"photoslicer/engine/sorting"
)

func createSampleJpeg(t *testing.T, path string, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 100, B: 50, A: 255}}, image.Point{}, draw.Src)

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create image file %s: %v", path, err)
	}
	defer f.Close()

	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("failed to encode jpeg: %v", err)
	}
}

func TestCreateZipAndCbz(t *testing.T) {
	tempDir := t.TempDir()

	img1 := filepath.Join(tempDir, "01.jpg")
	img2 := filepath.Join(tempDir, "02.jpg")
	createSampleJpeg(t, img1, 100, 100)
	createSampleJpeg(t, img2, 100, 100)

	files := []string{img1, img2}

	// Test ZIP
	zipOut := filepath.Join(tempDir, "test.zip")
	if err := CreateZip(zipOut, files); err != nil {
		t.Fatalf("CreateZip failed: %v", err)
	}
	fi, err := os.Stat(zipOut)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("zip file invalid or empty")
	}

	// Test CBZ
	cbzOut := filepath.Join(tempDir, "test.cbz")
	if err := CreateCbz(cbzOut, files); err != nil {
		t.Fatalf("CreateCbz failed: %v", err)
	}
	fi2, err := os.Stat(cbzOut)
	if err != nil || fi2.Size() == 0 {
		t.Fatalf("cbz file invalid or empty")
	}
}

func TestExtractImagesFromZipNestedSubfolders(t *testing.T) {
	tempDir := t.TempDir()

	// Create a zip with nested subdirectories:
	// - Chapter 01/Subfolder/001.jpg
	// - Chapter 01/002.jpg
	// - __MACOSX/._001.jpg (should be skipped)
	// - Chapter 01/notes.txt (should be skipped)
	zipPath := filepath.Join(tempDir, "nested.zip")
	zFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("failed to create test zip: %v", err)
	}

	w := zip.NewWriter(zFile)

	imgTmp := filepath.Join(tempDir, "sample.jpg")
	createSampleJpeg(t, imgTmp, 80, 80)
	imgBytes, _ := os.ReadFile(imgTmp)

	// Entry 1: nested image
	f1, _ := w.Create("Chapter 01/Sub/001.jpg")
	_, _ = f1.Write(imgBytes)

	// Entry 2: direct chapter image
	f2, _ := w.Create("Chapter 01/002.jpg")
	_, _ = f2.Write(imgBytes)

	// Entry 3: macos metadata (must be ignored)
	f3, _ := w.Create("__MACOSX/._001.jpg")
	_, _ = f3.Write([]byte("fake macos metadata"))

	// Entry 4: text file (must be ignored)
	f4, _ := w.Create("Chapter 01/readme.txt")
	_, _ = f4.Write([]byte("some text"))

	_ = w.Close()
	_ = zFile.Close()

	extractBase := filepath.Join(tempDir, "extract")
	outDir, err := ExtractImagesFromZip(zipPath, extractBase)
	if err != nil {
		t.Fatalf("ExtractImagesFromZip failed on nested zip: %v", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("failed to read extracted dir: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("expected exactly 2 extracted images, got %d", len(entries))
	}
}

func TestCreatePdfFromImages(t *testing.T) {
	tempDir := t.TempDir()

	img1 := filepath.Join(tempDir, "page1.jpg")
	img2 := filepath.Join(tempDir, "page2.jpg")
	createSampleJpeg(t, img1, 200, 300)
	createSampleJpeg(t, img2, 200, 400)

	pdfOut := filepath.Join(tempDir, "output.pdf")
	if err := CreatePdfFromImages(pdfOut, []string{img1, img2}); err != nil {
		t.Fatalf("CreatePdfFromImages failed: %v", err)
	}

	fi, err := os.Stat(pdfOut)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("pdf file invalid or empty")
	}

	// Read header and trailer
	data, err := os.ReadFile(pdfOut)
	if err != nil {
		t.Fatalf("failed to read pdf: %v", err)
	}
	if string(data[:5]) != "%PDF-" {
		t.Errorf("expected PDF header, got: %s", string(data[:10]))
	}
	if !containsSubstring(data, []byte("%%EOF")) {
		t.Errorf("expected %%EOF in pdf trailer")
	}

	t.Run("GrayscaleSupport", func(t *testing.T) {
		grayImgPath := filepath.Join(tempDir, "gray.jpg")
		grayImg := image.NewGray(image.Rect(0, 0, 100, 150))
		f, err := os.Create(grayImgPath)
		if err != nil {
			t.Fatalf("failed to create gray file: %v", err)
		}
		if err := jpeg.Encode(f, grayImg, &jpeg.Options{Quality: 90}); err != nil {
			f.Close()
			t.Fatalf("failed to encode gray JPEG: %v", err)
		}
		f.Close()

		grayPdf := filepath.Join(tempDir, "gray.pdf")
		if err := CreatePdfFromImages(grayPdf, []string{grayImgPath}); err != nil {
			t.Fatalf("CreatePdfFromImages failed for gray: %v", err)
		}
		pdfData, err := os.ReadFile(grayPdf)
		if err != nil {
			t.Fatalf("failed to read gray pdf: %v", err)
		}
		if !containsSubstring(pdfData, []byte("/DeviceGray")) {
			t.Errorf("expected /DeviceGray in PDF dictionary for grayscale image")
		}
	})

	t.Run("Acrobat14400PointLimitConstrained", func(t *testing.T) {
		tallImgPath := filepath.Join(tempDir, "tall_16000.jpg")
		createSampleJpeg(t, tallImgPath, 800, 16000)

		tallPdf := filepath.Join(tempDir, "tall.pdf")
		if err := CreatePdfFromImages(tallPdf, []string{tallImgPath}); err != nil {
			t.Fatalf("CreatePdfFromImages failed: %v", err)
		}

		pdfData, err := os.ReadFile(tallPdf)
		if err != nil {
			t.Fatalf("failed to read tall pdf: %v", err)
		}

		// Verify Image XObject retains full 800x16000 resolution
		if !containsSubstring(pdfData, []byte("/Width 800 /Height 16000")) {
			t.Errorf("expected Image XObject to preserve full /Width 800 /Height 16000")
		}

		// Verify MediaBox does NOT exceed 14,400 points
		if containsSubstring(pdfData, []byte("/MediaBox [ 0 0 800 16000 ]")) {
			t.Errorf("MediaBox exceeds 14,400 points limit, triggering Acrobat out-of-range error!")
		}
		if !containsSubstring(pdfData, []byte("/MediaBox [ 0 0 700 14000 ]")) {
			t.Errorf("expected MediaBox to be scaled to [ 0 0 700 14000 ]")
		}
	})
}

func TestSafeRmtreeTemp(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "photoslicer_test_cleanup_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	RegisterTempDir(tempRoot)
	if !SafeRmtreeTemp(tempRoot) {
		t.Errorf("SafeRmtreeTemp failed to delete temporary folder %s", tempRoot)
	}

	if _, err := os.Stat(tempRoot); !os.IsNotExist(err) {
		t.Errorf("expected %s to be deleted", tempRoot)
	}

	// Protected directories check - should refuse to delete system/home/cwd/temp
	if cwd, err := os.Getwd(); err == nil {
		if SafeRmtreeTemp(cwd) {
			t.Errorf("SafeRmtreeTemp should NEVER delete current working directory!")
		}
	}
	if SafeRmtreeTemp(os.TempDir()) {
		t.Errorf("SafeRmtreeTemp should NEVER delete system temp directory!")
	}
	if runtime.GOOS == "windows" {
		if SafeRmtreeTemp("C:\\") {
			t.Errorf("SafeRmtreeTemp should NEVER delete C:\\ root!")
		}
	}
	if SafeRmtreeTemp("/") {
		t.Errorf("SafeRmtreeTemp should NEVER delete root /!")
	}
}

func containsSubstring(data []byte, sub []byte) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(data); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if data[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestCountImagesInArchive(t *testing.T) {
	tempDir := t.TempDir()
	zipPath := filepath.Join(tempDir, "count_test.zip")
	zFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("failed to create zip: %v", err)
	}

	w := zip.NewWriter(zFile)
	imgTmp := filepath.Join(tempDir, "sample.jpg")
	createSampleJpeg(t, imgTmp, 50, 50)
	imgBytes, _ := os.ReadFile(imgTmp)

	f1, _ := w.Create("001.jpg")
	_, _ = f1.Write(imgBytes)
	f2, _ := w.Create("002.png")
	_, _ = f2.Write(imgBytes)
	f3, _ := w.Create("003.jfif")
	_, _ = f3.Write(imgBytes)
	f4, _ := w.Create("notes.txt")
	_, _ = f4.Write([]byte("not an image"))
	f5, _ := w.Create("__MACOSX/._001.jpg")
	_, _ = f5.Write([]byte("metadata"))

	_ = w.Close()
	_ = zFile.Close()

	count, err := CountImagesInArchive(zipPath)
	if err != nil {
		t.Fatalf("CountImagesInArchive failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 images, got %d", count)
	}
}

func TestNotifyShellChange(t *testing.T) {
	// Should not panic on empty string, non-existent path, or real directory
	NotifyShellChange("")
	NotifyShellChange(t.TempDir())
	NotifyShellChange("non_existent_path_test")
}

// Two archives with the same name in one batch folder must extract into
// separate chapter folders instead of mixing their images together.
func TestFastScanDirSameNameArchivesStaySeparate(t *testing.T) {
	tempDir := t.TempDir()
	img := filepath.Join(tempDir, "page.jpg")
	createSampleJpeg(t, img, 20, 20)
	imgData, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}

	batchDir := filepath.Join(tempDir, "batch")
	if err := os.Mkdir(batchDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeArchive := func(name string, entries ...string) {
		f, err := os.Create(filepath.Join(batchDir, name))
		if err != nil {
			t.Fatal(err)
		}
		w := zip.NewWriter(f)
		for _, e := range entries {
			ew, err := w.Create(e)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ew.Write(imgData); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeArchive("1.zip", "a.jpg", "b.jpg")
	writeArchive("1.cbz", "a.jpg", "c.jpg", "d.jpg")
	t.Cleanup(CleanupAllTempDirs)

	dirs, err := FastScanDir(batchDir)
	if err != nil {
		t.Fatalf("FastScanDir failed: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 chapter folders, got %d: %v", len(dirs), dirs)
	}
	if dirs[0] == dirs[1] {
		t.Fatalf("both archives extracted into the same folder: %s", dirs[0])
	}

	counts := map[int]bool{}
	for _, d := range dirs {
		if filepath.Base(d) != "1" {
			t.Errorf("chapter folder should keep the archive name, got %q", filepath.Base(d))
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		counts[len(entries)] = true
	}
	if !counts[2] || !counts[3] {
		t.Errorf("expected one folder with 2 images and one with 3, got sizes %v", counts)
	}
}

// Pages in different subfolders of one ZIP must keep folder order, even when
// both folders use the same file names.
func TestExtractImagesFromZipKeepsSubfolderOrder(t *testing.T) {
	tempDir := t.TempDir()
	img := filepath.Join(tempDir, "page.jpg")
	createSampleJpeg(t, img, 10, 10)
	imgData, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(tempDir, "volume.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	// Written out of order on purpose; Ch10 must come after Ch2.
	names := []string{"Ch2/002.jpg", "Ch10/001.jpg", "Ch2/001.jpg", "Ch1/002.jpg", "Ch1/001.jpg", "Ch2/010.jpg"}
	for _, n := range names {
		ew, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		// Tag each image with its entry name so the order can be checked.
		if _, err := ew.Write(append(append([]byte{}, imgData...), []byte(n)...)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	outDir, err := ExtractImagesFromZip(zipPath, filepath.Join(tempDir, "extract"))
	if err != nil {
		t.Fatalf("ExtractImagesFromZip failed: %v", err)
	}
	imgs, err := sorting.GetAllImagesDirectory(outDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Ch1/001.jpg", "Ch1/002.jpg", "Ch2/001.jpg", "Ch2/002.jpg", "Ch2/010.jpg", "Ch10/001.jpg"}
	if len(imgs) != len(want) {
		t.Fatalf("expected %d images, got %d", len(want), len(imgs))
	}
	for i, p := range imgs {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(data[len(imgData):]); got != want[i] {
			t.Errorf("page %d: got %s, want %s", i+1, got, want[i])
		}
	}
}

// A broken archive in a batch folder is reported instead of being skipped
// silently, and the other chapters still come through.
func TestScanBatchDirReportsBrokenArchives(t *testing.T) {
	defer CleanupAllTempDirs()
	root := t.TempDir()
	chapter := filepath.Join(root, "Chapter 1")
	if err := os.Mkdir(chapter, 0755); err != nil {
		t.Fatal(err)
	}
	createSampleJpeg(t, filepath.Join(chapter, "01.jpg"), 10, 10)
	for _, name := range []string{"Chapter 2.zip", "Chapter 3.cbz"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("not a zip"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	dirs, failures, err := ScanBatchDir(root, nil)
	if err != nil {
		t.Fatalf("ScanBatchDir failed: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != chapter {
		t.Fatalf("expected only the good chapter, got %v", dirs)
	}
	if len(failures) != 2 || failures[0].Name != "Chapter 2.zip" || failures[1].Name != "Chapter 3.cbz" {
		t.Fatalf("expected both broken archives reported, got %v", failures)
	}
	if _, err := FastScanDir(root); err == nil {
		t.Fatal("FastScanDir hid a broken archive")
	}
}

// Same-name chapters come in a fixed order by source name: the folder, then
// the archives by extension.
func TestScanBatchDirOrdersSameNameChapters(t *testing.T) {
	defer CleanupAllTempDirs()
	root := t.TempDir()
	img := filepath.Join(t.TempDir(), "01.jpg")
	createSampleJpeg(t, img, 10, 10)
	if err := os.Mkdir(filepath.Join(root, "1"), 0755); err != nil {
		t.Fatal(err)
	}
	createSampleJpeg(t, filepath.Join(root, "1", "01.jpg"), 10, 10)
	img2 := filepath.Join(filepath.Dir(img), "02.jpg")
	createSampleJpeg(t, img2, 10, 10)
	// 1.cbz holds one page and 1.zip two, so the two "1" chapters can be told apart.
	archives := map[string][]string{"1.cbz": {img}, "1.zip": {img, img2}, "2.zip": {img}}
	for name, files := range archives {
		if err := CreateZip(filepath.Join(root, name), files); err != nil {
			t.Fatal(err)
		}
	}

	for run := 0; run < 5; run++ {
		dirs, failures, err := ScanBatchDir(root, nil)
		if err != nil || len(failures) != 0 {
			t.Fatalf("ScanBatchDir: %v, %v", failures, err)
		}
		if len(dirs) != 4 {
			t.Fatalf("expected 4 chapters, got %v", dirs)
		}
		if dirs[0] != filepath.Join(root, "1") {
			t.Fatalf("folder should come first, got %v", dirs)
		}
		if filepath.Base(dirs[1]) != "1" || filepath.Base(dirs[2]) != "1" || filepath.Base(dirs[3]) != "2" {
			t.Fatalf("unexpected order: %v", dirs)
		}
		cbzImgs, _ := sorting.GetAllImagesDirectory(dirs[1])
		zipImgs, _ := sorting.GetAllImagesDirectory(dirs[2])
		if len(cbzImgs) != 1 || len(zipImgs) != 2 {
			t.Fatalf("expected 1.cbz before 1.zip, got %d then %d pages", len(cbzImgs), len(zipImgs))
		}
		CleanupAllTempDirs()
	}
}
