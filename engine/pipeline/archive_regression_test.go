package pipeline

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveOutputWithLiteralBrackets(t *testing.T) {
	for _, noStitch := range []bool{false, true} {
		for _, format := range []string{"zip", "cbz", "pdf"} {
			t.Run(fmt.Sprintf("noStitch=%v/%s", noStitch, format), func(t *testing.T) {
				src := t.TempDir()
				createTestImages(t, src, 2, 20, 20)
				base := filepath.Join(t.TempDir(), "[parent]")
				out, err := MergerImages(src, PipelineOptions{Mode: "single", OutputBase: base, SaveDirectory: "Chapter [Stitched]", SaveFormat: "JPG", SaveQuality: 90, HeightLimit: 100, IsNoStitch: noStitch, MaxWorkers: 2, IsZip: format == "zip", IsCbz: format == "cbz", IsPdf: format == "pdf"})
				if err != nil {
					t.Fatal(err)
				}
				if format == "pdf" {
					data, err := os.ReadFile(out)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(data, []byte("/Subtype /Image")) || !bytes.HasSuffix(data, []byte("%%EOF\n")) {
						t.Fatal("missing PDF image or trailer")
					}
				} else {
					z, err := zip.OpenReader(out)
					if err != nil {
						t.Fatal(err)
					}
					defer z.Close()
					expected := 1
					if noStitch {
						expected = 2
					}
					if len(z.File) != expected {
						t.Fatalf("entries=%d want %d", len(z.File), expected)
					}
					for _, entry := range z.File {
						r, err := entry.Open()
						if err != nil {
							t.Fatal(err)
						}
						data, err := io.ReadAll(r)
						r.Close()
						if err != nil {
							t.Fatal(err)
						}
						img, err := jpeg.Decode(bytes.NewReader(data))
						if err != nil {
							t.Fatal(err)
						}
						if img.Bounds().Size() == (image.Point{}) {
							t.Fatal("empty image")
						}
					}
				}
				if _, err := os.Stat(filepath.Join(base, "Chapter [Stitched]")); !os.IsNotExist(err) {
					t.Fatalf("output directory not cleaned: %v", err)
				}
			})
		}
	}
}
