package archive

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/chai2010/webp"

	"photoslicer/engine/imageio"
)

type pdfObject struct {
	offset int64
}

// CreatePdfFromImages creates a multi-page PDF where each image is rendered on its own page
// with dimensions matching the image's pixel dimensions.
func CreatePdfFromImages(outputPath string, imagePaths []string) error {
	if len(imagePaths) == 0 {
		return fmt.Errorf("no images provided for PDF")
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	var currentOffset int64
	writeStr := func(s string) error {
		n, err := io.WriteString(outFile, s)
		currentOffset += int64(n)
		return err
	}
	writeBytes := func(b []byte) error {
		n, err := outFile.Write(b)
		currentOffset += int64(n)
		return err
	}

	// 1. PDF Header
	if err := writeStr("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"); err != nil {
		return err
	}

	// Objects map: 1-indexed object IDs to offsets
	var offsets []int64
	// offset[0] is dummy for 0 0 R
	offsets = append(offsets, 0)

	numPages := len(imagePaths)

	// Object numbering plan:
	// 1: Catalog
	// 2: Pages root
	// For page i (0-indexed):
	//   pageObjID: 3 + i*3
	//   contentObjID: 4 + i*3
	//   imageObjID: 5 + i*3

	catalogID := 1
	pagesID := 2

	// We will record objects sequentially.
	// First let's write Catalog (obj 1)
	offsets = append(offsets, currentOffset)
	if err := writeStr(fmt.Sprintf("%d 0 obj\n<< /Type /Catalog /Pages %d 0 R >>\nendobj\n", catalogID, pagesID)); err != nil {
		return err
	}

	// Pages root (obj 2)
	offsets = append(offsets, currentOffset)
	var kids []string
	for i := 0; i < numPages; i++ {
		pageObjID := 3 + i*3
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObjID))
	}
	kidsStr := strings.Join(kids, " ")
	if err := writeStr(fmt.Sprintf("%d 0 obj\n<< /Type /Pages /Kids [ %s ] /Count %d >>\nendobj\n", pagesID, kidsStr, numPages)); err != nil {
		return err
	}

	// Acrobat Reader limit: maximum page dimension is 14,400 points (200 inches).
	// Beyond 14,400 points, Acrobat Reader displays:
	// "The dimensions of this page are out-of-range. Page content might be truncated."
	// and truncates content. We scale MediaBox and page coordinates to <= 14,000 points
	// while preserving 100% of the original pixel resolution in the Image XObject.
	const maxPDFDimension = 14000.0

	maxDim := 0
	for _, imgPath := range imagePaths {
		w, h, err := imageio.GetImageSizeFast(imgPath)
		if err == nil {
			if w > maxDim {
				maxDim = w
			}
			if h > maxDim {
				maxDim = h
			}
		}
	}

	globalScale := 1.0
	if float64(maxDim) > maxPDFDimension {
		globalScale = maxPDFDimension / float64(maxDim)
	}

	// For each page: Page, Content, Image
	for i, imgPath := range imagePaths {
		pageObjID := 3 + i*3
		contentObjID := 4 + i*3
		imageObjID := 5 + i*3

		// Read and normalize image to JPEG bytes
		imgData, w, h, colorSpace, err := loadImageAsJpegBytes(imgPath)
		if err != nil {
			return fmt.Errorf("error processing image %s for PDF: %w", imgPath, err)
		}

		pageScale := globalScale
		if float64(w)*pageScale > maxPDFDimension || float64(h)*pageScale > maxPDFDimension {
			maxSide := math.Max(float64(w), float64(h))
			if maxSide > 0 {
				pageScale = maxPDFDimension / maxSide
			}
		}

		pageW := int(math.Round(float64(w) * pageScale))
		pageH := int(math.Round(float64(h) * pageScale))
		if pageW < 1 {
			pageW = 1
		}
		if pageH < 1 {
			pageH = 1
		}

		// Page object
		offsets = append(offsets, currentOffset)
		pageHeader := fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent %d 0 R /MediaBox [ 0 0 %d %d ] /Contents %d 0 R /Resources << /XObject << /Im %d 0 R >> >> >>\nendobj\n",
			pageObjID, pagesID, pageW, pageH, contentObjID, imageObjID)
		if err := writeStr(pageHeader); err != nil {
			return err
		}

		// Content stream: paint image /Im across entire page (0 0 to pageW pageH)
		contentStream := fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im Do\nQ\n", pageW, pageH)
		offsets = append(offsets, currentOffset)
		contentHeader := fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n",
			contentObjID, len(contentStream), contentStream)
		if err := writeStr(contentHeader); err != nil {
			return err
		}

		// Image XObject (preserves 100% full original pixel resolution w x h)
		offsets = append(offsets, currentOffset)
		imageHeader := fmt.Sprintf("%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
			imageObjID, w, h, colorSpace, len(imgData))
		if err := writeStr(imageHeader); err != nil {
			return err
		}
		if err := writeBytes(imgData); err != nil {
			return err
		}
		if err := writeStr("\nendstream\nendobj\n"); err != nil {
			return err
		}
	}

	// xref table
	xrefOffset := currentOffset
	totalObjs := len(offsets) - 1 // object 1 through totalObjs
	if err := writeStr(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", totalObjs+1)); err != nil {
		return err
	}
	for o := 1; o <= totalObjs; o++ {
		if err := writeStr(fmt.Sprintf("%010d 00000 n \n", offsets[o])); err != nil {
			return err
		}
	}

	// trailer
	trailer := fmt.Sprintf("trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		totalObjs+1, catalogID, xrefOffset)
	if err := writeStr(trailer); err != nil {
		return err
	}
	_ = outFile.Sync()
	if err := outFile.Close(); err != nil {
		return err
	}
	NotifyShellChange(outputPath)
	return nil
}

func loadImageAsJpegBytes(path string) ([]byte, int, int, string, error) {
	ext := strings.ToLower(filepath.Ext(path))

	// Fast path for JPEG
	if ext == ".jpg" || ext == ".jpeg" {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, 0, "", err
		}
		defer f.Close()

		cfg, err := jpeg.DecodeConfig(f)
		if err == nil && cfg.Width > 0 && cfg.Height > 0 {
			if cfg.ColorModel == color.GrayModel {
				_, _ = f.Seek(0, 0)
				data, err := io.ReadAll(f)
				if err == nil {
					return data, cfg.Width, cfg.Height, "/DeviceGray", nil
				}
			} else if cfg.ColorModel == color.YCbCrModel || cfg.ColorModel == color.RGBAModel {
				_, _ = f.Seek(0, 0)
				data, err := io.ReadAll(f)
				if err == nil {
					return data, cfg.Width, cfg.Height, "/DeviceRGB", nil
				}
			}
			// If CMYK or non-standard JPEG, fallback below to decode and normalize to RGB
		}
	}

	// Other formats (PNG, WebP, AVIF, PSD composite) or fallback: decode to image and normalize
	img, err := imageio.OpenImageRobust(path)
	if err != nil {
		return nil, 0, 0, "", err
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	if gray, ok := img.(*image.Gray); ok {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, gray, &jpeg.Options{Quality: 95}); err != nil {
			return nil, 0, 0, "", err
		}
		return buf.Bytes(), w, h, "/DeviceGray", nil
	}

	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 95}); err != nil {
		return nil, 0, 0, "", err
	}

	return buf.Bytes(), w, h, "/DeviceRGB", nil
}
