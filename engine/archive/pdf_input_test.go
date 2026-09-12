package archive

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photoslicer/engine/sorting"
)

// Small valid PDFs exercise the actual embedded renderer, without external tools.
func makeInputPDF(t *testing.T, path string, pages ...string) {
	t.Helper()
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}
	var kids []string
	for _, kind := range pages {
		pageID := len(objects) + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", pageID))
		resources, content := "", "0 0 1 rg 0 0 20 30 re f"
		if kind == "image" || kind == "form" {
			resources = fmt.Sprintf("/XObject << /Im %d 0 R >>", pageID+2)
			content = "q 20 0 0 30 0 0 cm /Im Do Q"
		}
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 20 30] /Resources << %s >> /Contents %d 0 R >>", resources, pageID+1))
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		if kind == "form" {
			form := "/Im Do"
			objects = append(objects, fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 1 1] /Resources << /XObject << /Im %d 0 R >> >> /Length %d >>\nstream\n%s\nendstream", pageID+3, len(form), form))
		}
		if kind == "image" || kind == "form" {
			pixels := strings.Repeat("\xff\x00\x00", 6) // Red, 2 x 3, RGB.
			objects = append(objects, fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 3 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Length %d >>\nstream\n%s\nendstream", len(pixels), pixels))
		}
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), strings.Join(kids, " "))
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	var offsets []int
	for i, obj := range objects {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPDFImagesAndRenderFallback(t *testing.T) {
	for _, pages := range [][]string{{"image", "image"}, {"vector", "vector"}, {"image", "vector", "form"}} {
		t.Run(strings.Join(pages, "-"), func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "فصل فارسی.PDF")
			makeInputPDF(t, source, pages...)
			before, _ := os.ReadFile(source)
			count, err := CountPDFPages(source)
			if err != nil || count != len(pages) {
				t.Fatalf("page count: %d, %v", count, err)
			}
			root := t.TempDir()
			out, err := ExtractImagesFromPDF(source, root)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(out) != "فصل فارسی" {
				t.Fatalf("chapter name lost: %s", out)
			}
			files, err := sorting.GetAllImagesDirectory(out)
			if err != nil || len(files) != len(pages) {
				t.Fatalf("images: %v, %v", files, err)
			}
			for i, path := range files {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				img, _, err := image.Decode(f)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				w, h, name := 2, 3, fmt.Sprintf("%04d_001.png", i+1)
				if pages[i] == "vector" {
					w, h, name = 40, 60, fmt.Sprintf("%04d.png", i+1)
				}
				if img.Bounds().Dx() != w || img.Bounds().Dy() != h || filepath.Base(path) != name {
					t.Fatalf("unexpected image %s: %v", path, img.Bounds())
				}
				r, g, b, a := img.At(w/2, h/2).RGBA()
				if g != 0 || a != 65535 || (pages[i] == "vector" && (b != 65535 || r != 0)) || (pages[i] != "vector" && (r != 65535 || b != 0)) {
					t.Fatalf("wrong rendered colors: %d %d %d %d", r, g, b, a)
				}
			}
			after, _ := os.ReadFile(source)
			if !bytes.Equal(before, after) {
				t.Fatal("source PDF was changed")
			}
		})
	}
}

func TestPDFCancellationAndFailedExtractionCleanup(t *testing.T) {
	source := filepath.Join(t.TempDir(), "chapter.pdf")
	makeInputPDF(t, source, "image", "image")
	root := t.TempDir()
	stopped := errors.New("stopped")
	calls := 0
	_, err := ExtractInputFile(source, root, func() error {
		calls++
		// Cancel after the first image was written.
		if calls >= 5 {
			return stopped
		}
		return nil
	})
	if !errors.Is(err, stopped) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("partial extraction remains: %v", entries)
	}
	// The same source can be reopened: cancellation releases the document/worker.
	if _, err := ExtractImagesFromPDF(source, root); err != nil {
		t.Fatal(err)
	}
}

func TestPDFInvalidAndEmpty(t *testing.T) {
	for _, empty := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "bad.pdf")
		if empty {
			makeInputPDF(t, path)
		} else if err := os.WriteFile(path, []byte("not a PDF"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := CountPDFPages(path); err == nil {
			t.Fatal("invalid/empty PDF was accepted")
		}
		root := t.TempDir()
		if _, err := ExtractImagesFromPDF(path, root); err == nil {
			t.Fatal("invalid/empty PDF was extracted")
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 0 {
			t.Fatal("invalid input left output files")
		}
	}
}

func TestFastScanPDFChaptersAndNameCollisions(t *testing.T) {
	defer CleanupAllTempDirs()
	root := t.TempDir()
	makeInputPDF(t, filepath.Join(root, "Chapter 10.PDF"), "image")
	makeInputPDF(t, filepath.Join(root, "Chapter 2.pdf"), "vector")
	img := filepath.Join(t.TempDir(), "01.jpg")
	createSampleJpeg(t, img, 10, 20)
	if err := CreateZip(filepath.Join(root, "Chapter 2.zip"), []string{img}); err != nil {
		t.Fatal(err)
	}
	paths, err := FastScanDir(root)
	if err != nil || len(paths) != 3 {
		t.Fatalf("batch: %v, %v", paths, err)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			t.Fatal("PDF and ZIP chapters share an extraction directory")
		}
		seen[path] = true
		imgs, _ := sorting.GetAllImagesDirectory(path)
		if len(imgs) != 1 {
			t.Fatalf("chapter contaminated: %s: %v", path, imgs)
		}
	}
	if filepath.Base(paths[2]) != "Chapter 10" {
		t.Fatalf("incorrect natural chapter order: %v", paths)
	}
	CleanupAllTempDirs()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary chapter remains: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Chapter 2.pdf")); err != nil {
		t.Fatal("source removed")
	}
}

func TestPDFDimensionLimits(t *testing.T) {
	for _, dims := range [][2]float64{{0, 1}, {1, -1}, {math.NaN(), 1}, {math.Inf(1), 1}, {100000, 100000}} {
		if validatePDFDimensions(dims[0], dims[1]) == nil {
			t.Fatalf("accepted dimensions %v", dims)
		}
	}
}
