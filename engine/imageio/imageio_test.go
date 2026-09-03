package imageio

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"photoslicer/engine/constants"

	"github.com/chai2010/webp"
)

func createTestPsd(path string, w, h int) error {
	var buf bytes.Buffer
	buf.WriteString("8BPS")
	buf.Write([]byte{0, 1})
	buf.Write(make([]byte, 6))
	_ = binary.Write(&buf, binary.BigEndian, uint16(3))
	_ = binary.Write(&buf, binary.BigEndian, uint32(h))
	_ = binary.Write(&buf, binary.BigEndian, uint32(w))
	_ = binary.Write(&buf, binary.BigEndian, uint16(8))
	_ = binary.Write(&buf, binary.BigEndian, uint16(3))
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	buf.Write(make([]byte, 3*w*h))
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func createTestPng(path string, w, h int) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 50, G: 120, B: 200, A: 255}}, image.Point{}, draw.Src)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func createTestWebp(path string, w, h int) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 80, G: 180, B: 90, A: 255}}, image.Point{}, draw.Src)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return webp.Encode(f, img, &webp.Options{Quality: 90})
}

func createTestJpeg(path string, w, h int) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 80, B: 40, A: 255}}, image.Point{}, draw.Src)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
}

func TestOpenImageRobustFormats(t *testing.T) {
	tempDir := t.TempDir()

	pngPath := filepath.Join(tempDir, "test.png")
	if err := createTestPng(pngPath, 100, 150); err != nil {
		t.Fatalf("failed to create png: %v", err)
	}

	jpgPath := filepath.Join(tempDir, "test.jpg")
	if err := createTestJpeg(jpgPath, 120, 80); err != nil {
		t.Fatalf("failed to create jpg: %v", err)
	}

	webpPath := filepath.Join(tempDir, "test.webp")
	if err := createTestWebp(webpPath, 90, 110); err != nil {
		t.Fatalf("failed to create webp: %v", err)
	}

	psdPath := filepath.Join(tempDir, "test.psd")
	if err := createTestPsd(psdPath, 70, 95); err != nil {
		t.Fatalf("failed to create psd: %v", err)
	}

	// Test decoding all supported formats
	formats := []struct {
		name string
		path string
		w    int
		h    int
	}{
		{"PNG", pngPath, 100, 150},
		{"JPEG", jpgPath, 120, 80},
		{"WEBP", webpPath, 90, 110},
		{"PSD", psdPath, 70, 95},
	}

	for _, tc := range formats {
		t.Run(tc.name, func(t *testing.T) {
			img, err := OpenImageRobust(tc.path)
			if err != nil {
				t.Fatalf("OpenImageRobust failed for %s: %v", tc.name, err)
			}
			b := img.Bounds()
			if b.Dx() != tc.w || b.Dy() != tc.h {
				t.Errorf("%s bounds mismatch: expected %dx%d, got %dx%d", tc.name, tc.w, tc.h, b.Dx(), b.Dy())
			}

			// Test GetImageSizeFast
			w, h, err := GetImageSizeFast(tc.path)
			if err != nil {
				t.Fatalf("GetImageSizeFast failed for %s: %v", tc.name, err)
			}
			if w != tc.w || h != tc.h {
				t.Errorf("GetImageSizeFast %s size mismatch: expected %dx%d, got %dx%d", tc.name, tc.w, tc.h, w, h)
			}
		})
	}
}

func TestAnyImageExceedsWebPLimit(t *testing.T) {
	tempDir := t.TempDir()

	smallPath := filepath.Join(tempDir, "small.png")
	_ = createTestPng(smallPath, 800, 2000)

	if AnyImageExceedsWebPLimit([]string{smallPath}, false, 800) {
		t.Errorf("expected small image not to exceed limit")
	}

	tallPath := filepath.Join(tempDir, "tall.png")
	_ = createTestPng(tallPath, 800, constants.WebPMaxDimension+500)

	if !AnyImageExceedsWebPLimit([]string{tallPath}, false, 800) {
		t.Errorf("expected tall image to exceed WebP limit")
	}
}

func TestResizeBicubic(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 400))
	resized := ResizeBicubic(img, 100, 200)
	b := resized.Bounds()
	if b.Dx() != 100 || b.Dy() != 200 {
		t.Errorf("expected 100x200, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestSaveImageFormats(t *testing.T) {
	tempDir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))

	for _, fmtName := range []string{"JPG", "PNG", "WEBP"} {
		outPath := filepath.Join(tempDir, "out."+fmtName)
		if err := SaveImage(img, outPath, fmtName, 90); err != nil {
			t.Errorf("SaveImage failed for %s: %v", fmtName, err)
		}
		fi, err := os.Stat(outPath)
		if err != nil || fi.Size() == 0 {
			t.Errorf("expected non-empty file for %s", fmtName)
		}
	}
}

func TestGetConcatVOptimized(t *testing.T) {
	tempDir := t.TempDir()

	p1 := filepath.Join(tempDir, "p1.jpg")
	p2 := filepath.Join(tempDir, "p2.jpg")
	_ = createTestJpeg(p1, 200, 300)
	_ = createTestJpeg(p2, 200, 400)

	concat, err := GetConcatVOptimized([]string{p1, p2}, 200, true, 2)
	if err != nil {
		t.Fatalf("GetConcatVOptimized failed: %v", err)
	}

	b := concat.Bounds()
	if b.Dx() != 200 {
		t.Errorf("expected width 200, got %d", b.Dx())
	}
	if b.Dy() != 700 {
		t.Errorf("expected height 700 (300+400), got %d", b.Dy())
	}
}

func TestPSDDecodingScenarios(t *testing.T) {
	t.Run("PositiveLayerCount_IgnoredAlphaChannel", func(t *testing.T) {
		w, h := 10, 10
		var buf bytes.Buffer
		buf.WriteString("8BPS")
		buf.Write([]byte{0, 1})
		buf.Write(make([]byte, 6))
		_ = binary.Write(&buf, binary.BigEndian, uint16(4)) // 4 channels
		_ = binary.Write(&buf, binary.BigEndian, uint32(h))
		_ = binary.Write(&buf, binary.BigEndian, uint32(w))
		_ = binary.Write(&buf, binary.BigEndian, uint16(8)) // 8-bit
		_ = binary.Write(&buf, binary.BigEndian, uint16(3)) // RGB
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // Color mode
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // Image resources

		// Layer & Mask section with layerCount = +2 (positive)
		var lmBuf bytes.Buffer
		_ = binary.Write(&lmBuf, binary.BigEndian, uint32(10)) // layer info len
		_ = binary.Write(&lmBuf, binary.BigEndian, int16(2))   // layerCount = +2
		lmBuf.Write(make([]byte, 8))                           // padding
		_ = binary.Write(&buf, binary.BigEndian, uint32(lmBuf.Len()))
		buf.Write(lmBuf.Bytes())

		// Image Data (compression = 0: raw bytes)
		_ = binary.Write(&buf, binary.BigEndian, uint16(0))
		rPlane := bytes.Repeat([]byte{200}, w*h)
		gPlane := bytes.Repeat([]byte{100}, w*h)
		bPlane := bytes.Repeat([]byte{50}, w*h)
		aPlane := bytes.Repeat([]byte{0}, w*h) // User channel is all 0s
		buf.Write(rPlane)
		buf.Write(gPlane)
		buf.Write(bPlane)
		buf.Write(aPlane)

		img, err := DecodePSDComposite(&buf)
		if err != nil {
			t.Fatalf("DecodePSDComposite failed: %v", err)
		}
		rgba := img.(*image.RGBA)
		// Alpha must be 255 because layerCount >= 0
		for i := 0; i < len(rgba.Pix); i += 4 {
			if rgba.Pix[i] != 200 || rgba.Pix[i+1] != 100 || rgba.Pix[i+2] != 50 || rgba.Pix[i+3] != 255 {
				t.Fatalf("Pixel %d mismatch: got RGBA(%d,%d,%d,%d), expected (200,100,50,255)",
					i/4, rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2], rgba.Pix[i+3])
			}
		}
	})

	t.Run("NegativeLayerCount_RealTransparency", func(t *testing.T) {
		w, h := 5, 5
		var buf bytes.Buffer
		buf.WriteString("8BPS")
		buf.Write([]byte{0, 1})
		buf.Write(make([]byte, 6))
		_ = binary.Write(&buf, binary.BigEndian, uint16(4)) // 4 channels
		_ = binary.Write(&buf, binary.BigEndian, uint32(h))
		_ = binary.Write(&buf, binary.BigEndian, uint32(w))
		_ = binary.Write(&buf, binary.BigEndian, uint16(8)) // 8-bit
		_ = binary.Write(&buf, binary.BigEndian, uint16(3)) // RGB
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // Color mode
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // Image resources

		// Layer & Mask section with layerCount = -1 (negative: transparency present)
		var lmBuf bytes.Buffer
		_ = binary.Write(&lmBuf, binary.BigEndian, uint32(10))
		_ = binary.Write(&lmBuf, binary.BigEndian, int16(-1)) // layerCount = -1
		lmBuf.Write(make([]byte, 8))
		_ = binary.Write(&buf, binary.BigEndian, uint32(lmBuf.Len()))
		buf.Write(lmBuf.Bytes())

		// Image Data
		_ = binary.Write(&buf, binary.BigEndian, uint16(0))
		rPlane := bytes.Repeat([]byte{255}, w*h)
		gPlane := bytes.Repeat([]byte{255}, w*h)
		bPlane := bytes.Repeat([]byte{255}, w*h)
		aPlane := bytes.Repeat([]byte{128}, w*h) // 50% transparency
		buf.Write(rPlane)
		buf.Write(gPlane)
		buf.Write(bPlane)
		buf.Write(aPlane)

		img, err := DecodePSDComposite(&buf)
		if err != nil {
			t.Fatalf("DecodePSDComposite failed: %v", err)
		}
		rgba := img.(*image.RGBA)
		if rgba.Pix[3] != 128 {
			t.Errorf("expected alpha 128, got %d", rgba.Pix[3])
		}
	})

	t.Run("CMYKDecoding", func(t *testing.T) {
		w, h := 4, 4
		var buf bytes.Buffer
		buf.WriteString("8BPS")
		buf.Write([]byte{0, 1})
		buf.Write(make([]byte, 6))
		_ = binary.Write(&buf, binary.BigEndian, uint16(4)) // 4 channels (CMYK)
		_ = binary.Write(&buf, binary.BigEndian, uint32(h))
		_ = binary.Write(&buf, binary.BigEndian, uint32(w))
		_ = binary.Write(&buf, binary.BigEndian, uint16(8)) // 8-bit
		_ = binary.Write(&buf, binary.BigEndian, uint16(4)) // CMYK
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // Color mode
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // Image resources
		_ = binary.Write(&buf, binary.BigEndian, uint32(0)) // Layer and mask

		// Image Data: pure cyan (C=255, M=0, Y=0, K=0)
		_ = binary.Write(&buf, binary.BigEndian, uint16(0))
		buf.Write(bytes.Repeat([]byte{255}, w*h)) // C
		buf.Write(bytes.Repeat([]byte{0}, w*h))   // M
		buf.Write(bytes.Repeat([]byte{0}, w*h))   // Y
		buf.Write(bytes.Repeat([]byte{0}, w*h))   // K

		img, err := DecodePSDComposite(&buf)
		if err != nil {
			t.Fatalf("DecodePSDComposite failed: %v", err)
		}
		rgba := img.(*image.RGBA)
		// Pure cyan in RGB is (0, 255, 255)
		if rgba.Pix[0] != 0 || rgba.Pix[1] != 255 || rgba.Pix[2] != 255 || rgba.Pix[3] != 255 {
			t.Errorf("expected RGB (0, 255, 255, 255), got (%d, %d, %d, %d)",
				rgba.Pix[0], rgba.Pix[1], rgba.Pix[2], rgba.Pix[3])
		}
	})
}
