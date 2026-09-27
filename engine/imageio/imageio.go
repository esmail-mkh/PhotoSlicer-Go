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
	"runtime"
	"strings"
	"sync"

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

// AVIF encoding goes through gen2brain/avif, which uses a system libavif when
// one can be loaded and otherwise falls back to a bundled, single-threaded
// libaom compiled to WASM. The two need opposite scheduling:
//
//   - libavif (dynamic) already spreads one encode across every CPU core, so
//     running several at once only multiplies memory; encodes are serialized.
//   - The WASM encoder uses exactly one core per encode, so serializing them
//     leaves the rest of the CPU idle; encodes run in parallel, bounded by the
//     core count and by a budget of in-flight pixels so tall frames cannot
//     exhaust RAM.
const (
	// avifPixelBudget caps the pixels being encoded at once. A WASM encode
	// peaks at roughly 150 bytes of RAM per pixel (RGBA copy, YUV planes,
	// encoder frame buffers, WASM heap growth), so this keeps concurrent AVIF
	// work around 1.8 GB: several short slices run together, while a default
	// 800x16000 slice runs alone.
	avifPixelBudget = 12_000_000

	// avifSpeedDefault is libaom's cpu-used. Measured on 800px-wide comic
	// pages, speed 8 is ~5x faster than speed 6 at the same file size and
	// within ~0.6 dB PSNR; slower presets are not worth it for this workload.
	avifSpeedDefault = 8
	// avifSpeedLarge is used above avifLargeFramePixels, where speed 8 gets
	// both slow and memory-hungry. On a default 800x16000 slice speed 9 is
	// ~2.8x faster and peaks at about half the RAM (1.6 GB vs 3.0 GB), for
	// ~11% larger files at the same PSNR.
	avifSpeedLarge       = 9
	avifLargeFramePixels = 8_000_000
)

var (
	avifLimiterOnce sync.Once
	avifLimiter     *pixelLimiter
	avifWarmOnce    sync.Once
)

func getAVIFLimiter() *pixelLimiter {
	avifLimiterOnce.Do(func() {
		if avif.Dynamic() == nil {
			avifLimiter = newPixelLimiter(avifPixelBudget, 1)
		} else {
			avifLimiter = newPixelLimiter(avifPixelBudget, runtime.GOMAXPROCS(0))
		}
	})
	return avifLimiter
}

// PrewarmAVIF compiles the AVIF encoder in the background. The first encode
// otherwise pays a one-time WASM compilation (1-2 s) on the critical path;
// calling this before decoding/stitching overlaps that cost with other work.
func PrewarmAVIF() {
	avifWarmOnce.Do(func() {
		go func() {
			_ = getAVIFLimiter()
			_ = avif.Encode(io.Discard, image.NewRGBA(image.Rect(0, 0, 16, 16)), avif.Options{Speed: 10})
		}()
	})
}

// compactRGBA returns img as an *image.RGBA whose Pix holds exactly the
// image's rows with a tight stride. The AVIF encoder copies len(Pix) bytes and
// assumes stride == width*4, but a SubImage slice of a stitched strip keeps
// the parent's Pix up to its end, so passing it directly copies the whole
// remaining strip into the encoder for every slice.
func compactRGBA(img image.Image) image.Image {
	src, ok := img.(*image.RGBA)
	if !ok {
		// The encoder converts other types into a fresh, compact RGBA itself.
		return img
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return img
	}
	rowBytes := w * 4
	off := src.PixOffset(b.Min.X, b.Min.Y)
	if src.Stride == rowBytes {
		end := off + h*rowBytes
		return &image.RGBA{Pix: src.Pix[off:end:end], Stride: rowBytes, Rect: image.Rect(0, 0, w, h)}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		copy(dst.Pix[y*rowBytes:(y+1)*rowBytes], src.Pix[off+y*src.Stride:])
	}
	return dst
}

func encodeAVIF(w io.Writer, img image.Image, quality int) error {
	if quality <= 0 {
		quality = 60
	}
	b := img.Bounds()
	pixels := int64(b.Dx()) * int64(b.Dy())
	speed := avifSpeedDefault
	if pixels > avifLargeFramePixels {
		speed = avifSpeedLarge
	}

	limiter := getAVIFLimiter()
	limiter.acquire(pixels)
	defer limiter.release(pixels)
	return avif.Encode(w, compactRGBA(img), avif.Options{
		Quality: quality,
		Speed:   speed,
	})
}

// pixelLimiter is a weighted semaphore: holders reserve their pixel count
// against a shared budget and at most maxActive may run at once. A request
// larger than the whole budget still runs, but only when nothing else does.
type pixelLimiter struct {
	mu        sync.Mutex
	cond      *sync.Cond
	budget    int64
	maxActive int
	used      int64
	active    int
}

func newPixelLimiter(budget int64, maxActive int) *pixelLimiter {
	if maxActive < 1 {
		maxActive = 1
	}
	l := &pixelLimiter{budget: budget, maxActive: maxActive}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *pixelLimiter) weight(pixels int64) int64 {
	if pixels < 1 {
		return 1
	}
	if pixels > l.budget {
		return l.budget
	}
	return pixels
}

func (l *pixelLimiter) acquire(pixels int64) {
	w := l.weight(pixels)
	l.mu.Lock()
	for l.active > 0 && (l.active >= l.maxActive || l.used+w > l.budget) {
		l.cond.Wait()
	}
	l.used += w
	l.active++
	l.mu.Unlock()
}

func (l *pixelLimiter) release(pixels int64) {
	w := l.weight(pixels)
	l.mu.Lock()
	l.used -= w
	l.active--
	l.mu.Unlock()
	l.cond.Broadcast()
}

func EncodeImage(w io.Writer, img image.Image, format string, quality int) error {
	fmtLower := strings.ToLower(format)

	switch fmtLower {
	case "avif":
		return encodeAVIF(w, img, quality)
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
