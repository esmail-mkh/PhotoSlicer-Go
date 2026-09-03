package imageio

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"photoslicer/engine/constants"

	"github.com/chai2010/webp"
	"github.com/disintegration/imaging"
)

// OpenImageRobust robustly decodes an image from disk.
func OpenImageRobust(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".psd" {
		img, err := DecodePSDComposite(f)
		if err == nil {
			return img, nil
		}
		_, _ = f.Seek(0, 0)
	}
	if ext == ".webp" {
		img, err := webp.Decode(f)
		if err == nil {
			return img, nil
		}
		_, _ = f.Seek(0, 0)
	}

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image %s: %w", path, err)
	}
	return img, nil
}

// GetImageSizeFast gets image dimensions without loading full pixel data into memory.
func GetImageSizeFast(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".psd" {
		var header [26]byte
		if _, err := io.ReadFull(f, header[:]); err == nil && string(header[:4]) == "8BPS" {
			h := int(binary.BigEndian.Uint32(header[14:18]))
			w := int(binary.BigEndian.Uint32(header[18:22]))
			if w > 0 && h > 0 {
				return w, h, nil
			}
		}
		_, _ = f.Seek(0, 0)
	}

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		// Fallback to full decode if DecodeConfig is unsupported for the format
		_, _ = f.Seek(0, 0)
		img, err := OpenImageRobust(path)
		if err != nil {
			return 0, 0, err
		}
		b := img.Bounds()
		return b.Dx(), b.Dy(), nil
	}
	return cfg.Width, cfg.Height, nil
}

// AnyImageExceedsWebPLimit checks if any image in no-stitch mode exceeds WebP's
// maximum height limit after optional resizing.
func AnyImageExceedsWebPLimit(images []string, isCustomWidth bool, newWidth int) bool {
	for _, path := range images {
		w, h, err := GetImageSizeFast(path)
		if err != nil || w <= 0 || h <= 0 {
			continue
		}

		effH := h
		if isCustomWidth && newWidth > 0 {
			effH = int((float64(newWidth) / float64(w)) * float64(h))
		}
		if effH > constants.WebPMaxDimension {
			return true
		}
	}
	return false
}

// FlattenToRGB composites transparency onto a solid white background, returning *image.RGBA.
func FlattenToRGB(img image.Image) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	// Fill white
	white := image.NewUniform(color.White)
	draw.Draw(dst, dst.Bounds(), white, image.Point{}, draw.Src)
	// Overlay image over white
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// SaveImage saves the image in the requested format (JPG, PNG, WEBP) with the specified quality.
func SaveImage(img image.Image, outputPath string, format string, quality int) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := EncodeImage(f, img, format, quality); err != nil {
		_ = f.Close()
		_ = os.Remove(outputPath)
		return err
	}
	return nil
}

func EncodeImage(w io.Writer, img image.Image, format string, quality int) error {
	fmtLower := strings.ToLower(format)

	switch fmtLower {
	case "webp":
		if quality <= 0 {
			quality = 95
		}
		return webp.Encode(w, img, &webp.Options{
			Lossless: false,
			Quality:  float32(quality),
		})
	case "png":
		return png.Encode(w, img)
	case "jpg", "jpeg":
		fallthrough
	default:
		if quality <= 0 {
			quality = 95
		}
		// JPEG cannot store alpha; flatten onto white if not already opaque
		rgb := FlattenToRGB(img)
		return jpeg.Encode(w, rgb, &jpeg.Options{Quality: quality})
	}
}

// ResizeBicubic resizes image to target dimensions using Catmull-Rom (high quality Bicubic equivalent).
func ResizeBicubic(img image.Image, targetWidth, targetHeight int) *image.RGBA {
	if targetWidth <= 0 || targetHeight <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	b := img.Bounds()
	if b.Dx() == targetWidth && b.Dy() == targetHeight {
		return FlattenToRGB(img)
	}
	resized := imaging.Resize(img, targetWidth, targetHeight, imaging.CatmullRom)
	return FlattenToRGB(resized)
}
