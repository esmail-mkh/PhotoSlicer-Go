package archive

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
)

// Keep the compiled renderer, but release each document's WASM memory on close.
// PDFium is embedded: no Python, external executable, or PDF DLL is required.
var pdfPool = sync.OnceValues(func() (pdfium.Pool, error) {
	return webassembly.Init(webassembly.Config{
		MaxIdle: 1, MaxTotal: 2,
		FSConfig:      wazero.NewFSConfig(), // Documents are passed through a read-only reader.
		RuntimeConfig: wazero.NewRuntimeConfig().WithMemoryLimitPages(8192),
		Stdout:        io.Discard, Stderr: io.Discard,
	})
})

const maxPDFPixels = 100_000_000

type pdfInput struct {
	instance pdfium.Pdfium
	document references.FPDF_DOCUMENT
	file     *os.File
	pages    int
}

func openPDFInput(path string) (*pdfInput, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxArchiveTotalBytes {
		f.Close()
		return nil, fmt.Errorf("PDF must be a regular file no larger than 2 GB")
	}
	pool, err := pdfPool()
	if err != nil {
		f.Close()
		return nil, err
	}
	instance, err := pool.GetInstance(30 * time.Second)
	if err != nil {
		f.Close()
		return nil, err
	}
	doc, err := instance.OpenDocument(&requests.OpenDocument{FileReader: f, FileReaderSize: info.Size()})
	if err != nil {
		instance.Close()
		f.Close()
		return nil, fmt.Errorf("cannot open PDF %s (it may be damaged or password-protected): %w", filepath.Base(path), err)
	}
	d := &pdfInput{instance: instance, document: doc.Document, file: f}
	count, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		d.close()
		return nil, err
	}
	if count.PageCount < 1 || count.PageCount > MaxArchiveFiles {
		d.close()
		return nil, fmt.Errorf("PDF page count must be between 1 and %d", MaxArchiveFiles)
	}
	d.pages = count.PageCount
	return d, nil
}

func (d *pdfInput) close() {
	d.instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: d.document})
	d.instance.Close()
	d.file.Close()
}

// CountPDFPages inspects a PDF without extracting images or writing to disk.
func CountPDFPages(path string) (int, error) {
	d, err := openPDFInput(path)
	if err != nil {
		return 0, err
	}
	defer d.close()
	return d.pages, nil
}

func ExtractImagesFromPDF(path, extractBaseDir string) (string, error) {
	return extractImagesFromPDF(path, extractBaseDir, nil)
}

// ExtractInputFile handles a single virtual chapter. checkState supports pause/stop.
func ExtractInputFile(path, extractBaseDir string, checkState func() error) (string, error) {
	if strings.EqualFold(filepath.Ext(path), ".pdf") {
		return extractImagesFromPDF(path, extractBaseDir, checkState)
	}
	return ExtractImagesFromZip(path, extractBaseDir)
}

func extractImagesFromPDF(path, extractBaseDir string, checkState func() error) (output string, err error) {
	if checkState == nil {
		checkState = func() error { return nil }
	}
	if err := checkState(); err != nil {
		return "", err
	}
	d, err := openPDFInput(path)
	if err != nil {
		return "", err
	}
	defer d.close()
	// An owned parent prevents collisions with another chapter of the same name.
	root, err := os.MkdirTemp(extractBaseDir, "photoslicer_pdf_")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			SafeRmtreeTemp(root)
		}
	}()
	output = filepath.Join(root, sanitizeArchiveComponent(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))))
	if err = os.Mkdir(output, 0755); err != nil {
		return "", err
	}
	var totalBytes int64
	imageCount := 0
	for p := 0; p < d.pages; p++ {
		if err = checkState(); err != nil {
			return "", err
		}
		page := requests.Page{ByIndex: &requests.PageByIndex{Document: d.document, Index: p}}
		objects, e := d.imageObjects(page, checkState)
		if e != nil {
			return "", fmt.Errorf("PDF page %d: %w", p+1, e)
		}
		// Like the Python version, preserve embedded image resolution. Render
		// image-free pages individually so mixed documents don't lose those pages.
		if len(objects) == 0 {
			size, e := d.instance.GetPageSizeInPixels(&requests.GetPageSizeInPixels{Page: page, DPI: 144})
			if e != nil {
				return "", e
			}
			if e = validatePDFDimensions(float64(size.Width), float64(size.Height)); e != nil {
				return "", e
			}
			render, e := d.instance.RenderPageInDPI(&requests.RenderPageInDPI{Page: page, DPI: 144, RenderFlags: enums.FPDF_RENDER_FLAG_ANNOT})
			if e != nil {
				return "", fmt.Errorf("render PDF page %d: %w", p+1, e)
			}
			e = writePDFPNG(filepath.Join(output, fmt.Sprintf("%04d.png", p+1)), render.Result.Image, &totalBytes)
			render.Cleanup()
			if e != nil {
				return "", e
			}
			imageCount++
		} else {
			for i, obj := range objects {
				if err = checkState(); err != nil {
					return "", err
				}
				if imageCount >= MaxArchiveFiles {
					return "", fmt.Errorf("PDF contains too many images (limit: %d)", MaxArchiveFiles)
				}
				img, e := d.embeddedImage(obj)
				if e != nil {
					return "", fmt.Errorf("extract PDF page %d image %d: %w", p+1, i+1, e)
				}
				if e = writePDFPNG(filepath.Join(output, fmt.Sprintf("%04d_%03d.png", p+1, i+1)), img, &totalBytes); e != nil {
					return "", e
				}
				imageCount++
			}
		}
		if imageCount > MaxArchiveFiles {
			return "", fmt.Errorf("PDF contains too many images")
		}
	}
	if err = checkState(); err != nil {
		return "", err
	}
	return output, nil
}

func validatePDFDimensions(w, h float64) error {
	if math.IsNaN(w) || math.IsNaN(h) || w < 1 || h < 1 || w*h > maxPDFPixels {
		return fmt.Errorf("PDF image dimensions are invalid or exceed %d pixels", maxPDFPixels)
	}
	return nil
}

// Visit form XObjects too, since PDF producers often wrap page images in forms.
func (d *pdfInput) imageObjects(page requests.Page, checkState func() error) ([]references.FPDF_PAGEOBJECT, error) {
	var images []references.FPDF_PAGEOBJECT
	visited := 0
	var visit func(references.FPDF_PAGEOBJECT, int) error
	visit = func(obj references.FPDF_PAGEOBJECT, depth int) error {
		if err := checkState(); err != nil {
			return err
		}
		visited++
		if depth > 32 || visited > 100000 {
			return fmt.Errorf("PDF page object nesting or count exceeds limit")
		}
		kind, err := d.instance.FPDFPageObj_GetType(&requests.FPDFPageObj_GetType{PageObject: obj})
		if err != nil {
			return err
		}
		switch kind.Type {
		case enums.FPDF_PAGEOBJ_IMAGE:
			images = append(images, obj)
			if len(images) > MaxArchiveFiles {
				return fmt.Errorf("PDF page contains too many images")
			}
		case enums.FPDF_PAGEOBJ_FORM:
			count, err := d.instance.FPDFFormObj_CountObjects(&requests.FPDFFormObj_CountObjects{PageObject: obj})
			if err != nil {
				return err
			}
			for i := 0; i < count.Count; i++ {
				child, err := d.instance.FPDFFormObj_GetObject(&requests.FPDFFormObj_GetObject{PageObject: obj, Index: uint64(i)})
				if err != nil {
					return err
				}
				if err := visit(child.PageObject, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	count, err := d.instance.FPDFPage_CountObjects(&requests.FPDFPage_CountObjects{Page: page})
	if err != nil {
		return nil, err
	}
	for i := 0; i < count.Count; i++ {
		obj, err := d.instance.FPDFPage_GetObject(&requests.FPDFPage_GetObject{Page: page, Index: i})
		if err != nil {
			return nil, err
		}
		if err := visit(obj.PageObject, 0); err != nil {
			return nil, err
		}
	}
	return images, nil
}

func (d *pdfInput) embeddedImage(obj references.FPDF_PAGEOBJECT) (image.Image, error) {
	size, err := d.instance.FPDFImageObj_GetImagePixelSize(&requests.FPDFImageObj_GetImagePixelSize{ImageObject: obj})
	if err != nil {
		return nil, err
	}
	if err := validatePDFDimensions(float64(size.Width), float64(size.Height)); err != nil {
		return nil, err
	}
	bitmap, err := d.instance.FPDFImageObj_GetBitmap(&requests.FPDFImageObj_GetBitmap{ImageObject: obj})
	if err != nil {
		return nil, err
	}
	b := bitmap.Bitmap
	defer d.instance.FPDFBitmap_Destroy(&requests.FPDFBitmap_Destroy{Bitmap: b})
	w, err := d.instance.FPDFBitmap_GetWidth(&requests.FPDFBitmap_GetWidth{Bitmap: b})
	if err != nil {
		return nil, err
	}
	h, err := d.instance.FPDFBitmap_GetHeight(&requests.FPDFBitmap_GetHeight{Bitmap: b})
	if err != nil {
		return nil, err
	}
	if err := validatePDFDimensions(float64(w.Width), float64(h.Height)); err != nil {
		return nil, err
	}
	stride, err := d.instance.FPDFBitmap_GetStride(&requests.FPDFBitmap_GetStride{Bitmap: b})
	if err != nil {
		return nil, err
	}
	format, err := d.instance.FPDFBitmap_GetFormat(&requests.FPDFBitmap_GetFormat{Bitmap: b})
	if err != nil {
		return nil, err
	}
	buffer, err := d.instance.FPDFBitmap_GetBuffer(&requests.FPDFBitmap_GetBuffer{Bitmap: b})
	if err != nil {
		return nil, err
	}
	// PDFium bitmaps are Gray, BGR, BGRx, or BGRA (formats 1 through 4).
	channels := 4
	switch int(format.Format) {
	case 1:
		channels = 1
	case 2:
		channels = 3
	case 3, 4:
	default:
		return nil, fmt.Errorf("unsupported PDF bitmap format: %v", format.Format)
	}
	if stride.Stride < w.Width*channels || int64(stride.Stride)*int64(h.Height) > int64(len(buffer.Buffer)) {
		return nil, fmt.Errorf("invalid PDF bitmap buffer")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w.Width, h.Height))
	for y := 0; y < h.Height; y++ {
		for x := 0; x < w.Width; x++ {
			src, dst := y*stride.Stride+x*channels, y*img.Stride+x*4
			if channels == 1 {
				img.Pix[dst], img.Pix[dst+1], img.Pix[dst+2] = buffer.Buffer[src], buffer.Buffer[src], buffer.Buffer[src]
			} else {
				img.Pix[dst], img.Pix[dst+1], img.Pix[dst+2] = buffer.Buffer[src+2], buffer.Buffer[src+1], buffer.Buffer[src]
			}
			img.Pix[dst+3] = 255
			if int(format.Format) == 4 {
				img.Pix[dst+3] = buffer.Buffer[src+3]
			}
		}
	}
	return img, nil
}

func writePDFPNG(path string, img image.Image, total *int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	encodeErr := png.Encode(f, img)
	info, statErr := f.Stat()
	closeErr := f.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if statErr != nil {
		return statErr
	}
	if closeErr != nil {
		return closeErr
	}
	*total += info.Size()
	if info.Size() > MaxArchiveFileSizeBytes || *total > MaxArchiveTotalBytes {
		return fmt.Errorf("extracted PDF exceeds archive size limits")
	}
	return nil
}
