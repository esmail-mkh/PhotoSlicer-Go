package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"photoslicer/engine/archive"
	"photoslicer/engine/sorting"
)

func createAppPDF(t *testing.T, path string) {
	t.Helper()
	imgPath := filepath.Join(t.TempDir(), "page.png")
	img := image.NewRGBA(image.Rect(0, 0, 30, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 30; x++ {
			img.Set(x, y, color.RGBA{200, 80, 40, 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgPath, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := archive.CreatePdfFromImages(path, []string{imgPath, imgPath}); err != nil {
		t.Fatal(err)
	}
}

func TestInspectPDFAndBatchPDF(t *testing.T) {
	a := NewApp()
	a.settingsPathOverride = filepath.Join(t.TempDir(), "settings.json")
	root := t.TempDir()
	path := filepath.Join(root, "فصل ۱.PDF")
	createAppPDF(t, path)
	res := a.InspectDirectory(path)
	if res["status"] != "ok" || res["mode"] != "archive_pdf" || res["item_count"] != 2 {
		t.Fatalf("PDF inspection: %v", res)
	}
	res = a.InspectDirectory(root)
	if res["status"] != "ok" || res["mode"] != "batch" || res["item_count"] != 1 {
		t.Fatalf("batch inspection: %v", res)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatal("inspection wrote files next to source")
	}
	badPath := filepath.Join(t.TempDir(), "bad.pdf")
	if err := os.WriteFile(badPath, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if res := a.InspectDirectory(badPath); res["status"] != "error" || res["mode"] != "archive_pdf" {
		t.Fatalf("corrupt PDF accepted: %v", res)
	}
	if _, err := os.Stat(a.settingsPathOverride); !os.IsNotExist(err) {
		t.Fatal("inspection touched settings")
	}
}

func TestStartPDFDirectAndBatch(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "batch"}[batch], func(t *testing.T) {
			a := NewApp()
			a.settingsPathOverride = filepath.Join(t.TempDir(), "settings.json")
			settings := []byte(`{"language":"en","presets":[{"name":"پریست اصلی","values":{"width":777}}],"watermark_margin":19}`)
			if err := os.WriteFile(a.settingsPathOverride, settings, 0600); err != nil {
				t.Fatal(err)
			}
			// Existing settings loading normalizes legacy presets. Establish the
			// normalized baseline before exercising the PDF processing path.
			a.loadSettings()
			settings, _ = os.ReadFile(a.settingsPathOverride)
			sourceRoot := filepath.Join(t.TempDir(), "Chapters")
			if err := os.Mkdir(sourceRoot, 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(sourceRoot, "فصل ۱.PDF")
			createAppPDF(t, source)
			original, _ := os.ReadFile(source)
			input := source
			if batch {
				input = sourceRoot
			}
			a.Start(map[string]interface{}{
				"directory": input, "save_next_to_source": true, "output_suffix": " [Processed]",
				"save_format": "PNG", "save_quality": float64(95), "no_stitch_checked": true,
				"height_limit": float64(1000), "thread_count": float64(1), "filename_digits": float64(3),
			})
			deadline := time.Now().Add(30 * time.Second)
			for atomic.LoadInt32(&a.isBusy) != 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if atomic.LoadInt32(&a.isBusy) != 0 {
				if c := a.getController(); c != nil {
					c.Stop()
				}
				t.Fatal("PDF processing did not finish")
			}
			output := a.getLastOutput()
			if output == "" {
				t.Fatal("PDF processing produced no output")
			}
			expectedParent := sourceRoot
			if batch {
				expectedParent = filepath.Join(filepath.Dir(sourceRoot), "Chapters [Processed]")
			}
			rel, err := filepath.Rel(expectedParent, output)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				t.Fatalf("output not beside source: %s", output)
			}
			var images []string
			err = filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					files, err := sorting.GetAllImagesDirectory(path)
					if err != nil {
						return err
					}
					images = append(images, files...)
				}
				return nil
			})
			if err != nil || len(images) != 2 {
				t.Fatalf("output pages: %v, %v", images, err)
			}
			after, _ := os.ReadFile(source)
			if !bytes.Equal(original, after) {
				t.Fatal("original PDF changed")
			}
			settingsAfter, _ := os.ReadFile(a.settingsPathOverride)
			if !bytes.Equal(settings, settingsAfter) {
				t.Fatal("processing changed settings/presets")
			}
		})
	}
}
