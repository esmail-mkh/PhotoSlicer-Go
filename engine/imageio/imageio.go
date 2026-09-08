package imageio

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	stdjpeg "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"photoslicer/engine/constants"

	"github.com/chai2010/webp"
	"github.com/disintegration/imaging"
	"github.com/gen2brain/avif"
	jpegfast "github.com/gen2brain/jpeg"
	"golang.org/x/sys/cpu"
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
	if ext == ".avif" {
		img, err := avif.Decode(f)
		if err == nil {
			return img, nil
		}
		_, _ = f.Seek(0, 0)
	}
	if ext == ".jpg" || ext == ".jpeg" || ext == ".jfif" {
		img, err := decodeJPEG(f)
		if err == nil {
			return img, nil
		}
		_, _ = f.Seek(0, 0)
		if img, err := stdjpeg.Decode(f); err == nil {
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
	if ext == ".avif" {
		cfg, err := avif.DecodeConfig(f)
		if err == nil && cfg.Width > 0 && cfg.Height > 0 {
			return cfg.Width, cfg.Height, nil
		}
		_, _ = f.Seek(0, 0)
	}
	if ext == ".jpg" || ext == ".jpeg" || ext == ".jfif" {
		cfg, err := decodeJPEGConfig(f)
		if err == nil && cfg.Width > 0 && cfg.Height > 0 {
			return cfg.Width, cfg.Height, nil
		}
		_, _ = f.Seek(0, 0)
		if cfg, err := stdjpeg.DecodeConfig(f); err == nil && cfg.Width > 0 && cfg.Height > 0 {
			return cfg.Width, cfg.Height, nil
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

// IsOpaqueRGBA reports whether img is an RGBA image whose visible pixels all
// have an opaque alpha channel. It is intentionally limited to *image.RGBA so
// callers can safely use the specialized JPEG encoder path below.
func IsOpaqueRGBA(img image.Image) bool {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return false
	}

	b := rgba.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		offset := rgba.PixOffset(b.Min.X, y)
		row := rgba.Pix[offset : offset+b.Dx()*4]
		for alpha := 3; alpha < len(row); alpha += 4 {
			if row[alpha] != 0xff {
				return false
			}
		}
	}
	return true
}

// SaveImage saves the image in the requested format (JPG, PNG, WEBP) with the specified quality.
func SaveImage(img image.Image, outputPath string, format string, quality int) error {
	return saveEncodedFile(outputPath, func(w io.Writer) error {
		return EncodeImage(w, img, format, quality)
	})
}

// SaveOpaqueJPEG writes an already-opaque image without making a flattened
// copy. It is used by the slicer after it has established that the complete
// composite is opaque, so every slice is opaque as well.
func SaveOpaqueJPEG(img image.Image, outputPath string, quality int) error {
	if quality <= 0 {
		quality = 95
	}
	return saveEncodedFile(outputPath, func(w io.Writer) error {
		return encodeOpaqueJPEG(w, img, quality)
	})
}

func saveEncodedFile(outputPath string, encode func(io.Writer) error) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}

	buffered := bufio.NewWriterSize(f, 64*1024)
	if err := encode(buffered); err != nil {
		_ = f.Close()
		_ = os.Remove(outputPath)
		return err
	}
	if err := buffered.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(outputPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(outputPath)
		return err
	}
	return nil
}

func encodeOpaqueJPEG(w io.Writer, img image.Image, quality int) error {
	if cpu.X86.HasAVX2 {
		return jpegfast.Encode(w, img, &jpegfast.Options{Quality: quality})
	}
	return stdjpeg.Encode(w, img, &stdjpeg.Options{Quality: quality})
}

func decodeJPEG(r io.Reader) (image.Image, error) {
	if cpu.X86.HasAVX2 {
		return jpegfast.Decode(r)
	}
	return stdjpeg.Decode(r)
}

func decodeJPEGConfig(r io.Reader) (image.Config, error) {
	if cpu.X86.HasAVX2 {
		return jpegfast.DecodeConfig(r)
	}
	return stdjpeg.DecodeConfig(r)
}

func isKnownOpaqueImage(img image.Image) bool {
	switch typed := img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16:
		return true
	case *image.RGBA:
		return IsOpaqueRGBA(typed)
	case *image.NRGBA:
		b := typed.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			offset := typed.PixOffset(b.Min.X, y)
			row := typed.Pix[offset : offset+b.Dx()*4]
			for alpha := 3; alpha < len(row); alpha += 4 {
				if row[alpha] != 0xff {
					return false
				}
			}
		}
		return true
	default:
		return false
	}
}

// opaqueNRGBAToRGBA reuses the pixel buffer because NRGBA and RGBA have the
// same byte layout. The caller must have established that every alpha byte is
// 255; only then are their premultiplied-alpha semantics equivalent.
func opaqueNRGBAToRGBA(src *image.NRGBA) *image.RGBA {
	return &image.RGBA{
		Pix:    src.Pix,
		Stride: src.Stride,
		Rect:   src.Rect,
	}
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
		return stdjpeg.Encode(w, rgb, &stdjpeg.Options{Quality: quality})
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
	if isKnownOpaqueImage(img) {
		// imaging.Resize returns an opaque *image.NRGBA for opaque inputs. Reuse
		// that buffer instead of allocating and compositing a second full image.
		return opaqueNRGBAToRGBA(resized)
	}
	return FlattenToRGB(resized)
}
