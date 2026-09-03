package pipeline

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"photoslicer/engine/archive"
	"photoslicer/engine/imageio"
	"photoslicer/engine/psd"
	"photoslicer/engine/slicing"
	"photoslicer/engine/sorting"
	"photoslicer/engine/watermark"
)

type Controller struct {
	mu         sync.Mutex
	cond       *sync.Cond
	isPaused   bool
	isStopped  bool
	ctx        context.Context
	cancelFunc context.CancelFunc
}

func NewController() *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		ctx:        ctx,
		cancelFunc: cancel,
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *Controller) Pause() {
	c.mu.Lock()
	c.isPaused = true
	c.mu.Unlock()
}

func (c *Controller) Resume() {
	c.mu.Lock()
	c.isPaused = false
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *Controller) Stop() {
	c.mu.Lock()
	c.isStopped = true
	c.cancelFunc()
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *Controller) CheckState() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for c.isPaused && !c.isStopped {
		c.cond.Wait()
	}

	if c.isStopped {
		return fmt.Errorf("process stopped by user")
	}
	return nil
}

type PipelineOptions struct {
	Mode                  string // "single" or "multi"
	NewWidth              int
	IsCustomWidth         bool
	SaveFormat            string
	SaveQuality           int
	SaveDirectory         string
	HeightLimit           int
	CurrentDate           string
	IsZip                 bool
	IsPdf                 bool
	IsCbz                 bool
	IsNoStitch            bool
	ProgressCallback      func(percent float64, curr, total int, item string)
	WebpFallbackCallback  func()
	OutputBase            string
	MaxWorkers            int
	FilenamePattern       string
	FilenameDigits        int
	WatermarkEnabled      bool
	WatermarkPath         string
	WatermarkCount        int
	WatermarkEdge         string
	WatermarkWidthPercent int
	WatermarkMargin       int
	Controller            *Controller
}

func ProcessBatchNoStitch(images []string, savePath string, opts PipelineOptions) (string, error) {
	if err := os.MkdirAll(savePath, 0755); err != nil {
		return "", err
	}

	total := len(images)
	if total == 0 {
		return savePath, nil
	}

	if opts.ProgressCallback != nil {
		opts.ProgressCallback(0, 0, total, "Processing...")
	}

	type noStitchTask struct {
		index int
		path  string
	}

	taskChan := make(chan noStitchTask, total)
	var completed int64

	workers := opts.MaxWorkers
	if workers <= 0 {
		workers = 4
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				if opts.Controller != nil {
					if err := opts.Controller.CheckState(); err != nil {
						return
					}
				}

				img, err := imageio.OpenImageRobust(task.path)
				if err != nil {
					continue
				}

				// Resize if custom width
				if opts.IsCustomWidth && opts.NewWidth > 0 {
					wPercent := float64(opts.NewWidth) / float64(img.Bounds().Dx())
					hSize := int(math.Round(float64(img.Bounds().Dy()) * wPercent))
					if hSize < 1 {
						hSize = 1
					}
					img = imageio.ResizeBicubic(img, opts.NewWidth, hSize)
				}

				isPsd := strings.ToLower(opts.SaveFormat) == "psd"
				if opts.WatermarkEnabled && !isPsd && opts.WatermarkPath != "" {
					img = watermark.ApplyWatermark(
						img,
						opts.WatermarkPath,
						opts.WatermarkCount,
						opts.WatermarkEdge,
						opts.WatermarkWidthPercent,
						opts.WatermarkMargin,
					)
				}

				filename := slicing.FormatFilename(
					opts.FilenamePattern,
					task.index+1,
					opts.FilenameDigits,
					opts.SaveFormat,
					filepath.Base(savePath),
					total,
				)
				destFile := filepath.Join(savePath, filename)

				if isPsd {
					_ = psd.SavePSDLayered(
						img,
						destFile,
						opts.WatermarkEnabled,
						opts.WatermarkPath,
						opts.WatermarkCount,
						opts.WatermarkEdge,
						opts.WatermarkWidthPercent,
						opts.WatermarkMargin,
					)
				} else {
					_ = imageio.SaveImage(img, destFile, opts.SaveFormat, opts.SaveQuality)
				}

				curr := atomic.AddInt64(&completed, 1)
				if opts.ProgressCallback != nil {
					pct := (float64(curr) / float64(total)) * 100.0
					opts.ProgressCallback(pct, int(curr), total, filename)
				}
			}
		}()
	}

	for i, p := range images {
		taskChan <- noStitchTask{index: i, path: p}
	}
	close(taskChan)
	wg.Wait()

	finalResultPath := savePath
	baseName := filepath.Base(savePath)
	archiveCreated := false

	if opts.IsZip {
		zipPath := filepath.Join(filepath.Dir(savePath), baseName+".zip")
		files, _ := filepath.Glob(filepath.Join(savePath, "*"))
		files = sorting.SortKeyImproved(files)
		if err := archive.CreateZip(zipPath, files); err == nil {
			archiveCreated = true
			finalResultPath = zipPath
		}
	}
	if opts.IsCbz {
		cbzPath := filepath.Join(filepath.Dir(savePath), baseName+".cbz")
		files, _ := filepath.Glob(filepath.Join(savePath, "*"))
		files = sorting.SortKeyImproved(files)
		if err := archive.CreateCbz(cbzPath, files); err == nil {
			archiveCreated = true
			finalResultPath = cbzPath
		}
	}
	if opts.IsPdf {
		pdfPath := filepath.Join(filepath.Dir(savePath), baseName+".pdf")
		files, _ := filepath.Glob(filepath.Join(savePath, "*"))
		files = sorting.SortKeyImproved(files)
		if err := archive.CreatePdfFromImages(pdfPath, files); err == nil {
			archiveCreated = true
			finalResultPath = pdfPath
		}
	}

	if archiveCreated {
		_ = os.RemoveAll(savePath)
	}

	return finalResultPath, nil
}

// MergerImages executes the full image stitching/slicing or no-stitch pipeline for a folder.
func MergerImages(inputFolder string, opts PipelineOptions) (string, error) {
	if opts.Controller != nil {
		if err := opts.Controller.CheckState(); err != nil {
			return "", err
		}
	}

	images, err := sorting.GetAllImagesDirectory(inputFolder)
	if err != nil || len(images) == 0 {
		return "", fmt.Errorf("no images found in directory: %s", inputFolder)
	}

	// WebP fallback: if no-stitch mode selected with WebP output and any image exceeds WebP limit,
	// fall back to stitched mode (where slice height is capped under 16383).
	if opts.IsNoStitch && strings.ToLower(opts.SaveFormat) == "webp" {
		if imageio.AnyImageExceedsWebPLimit(images, opts.IsCustomWidth, opts.NewWidth) {
			opts.IsNoStitch = false
			if opts.WebpFallbackCallback != nil {
				opts.WebpFallbackCallback()
			}
		}
	}

	// Determine output save path
	baseFolder := opts.OutputBase
	if baseFolder == "" {
		baseFolder = "./Results"
	}

	folderName := opts.SaveDirectory
	if folderName == "" {
		if opts.Mode == "single" {
			folderName = filepath.Base(inputFolder)
		} else {
			folderName = opts.CurrentDate
		}
	}

	var savePath string
	if opts.Mode == "single" {
		savePath = filepath.Join(baseFolder, folderName)
	} else {
		savePath = filepath.Join(baseFolder, opts.CurrentDate, folderName)
	}

	originalSavePath := savePath
	counter := 0
	for {
		zipPath := savePath + ".zip"
		cbzPath := savePath + ".cbz"
		pdfPath := savePath + ".pdf"
		_, errDir := os.Stat(savePath)
		_, errZip := os.Stat(zipPath)
		_, errCbz := os.Stat(cbzPath)
		_, errPdf := os.Stat(pdfPath)

		if os.IsNotExist(errDir) && os.IsNotExist(errZip) && os.IsNotExist(errCbz) && os.IsNotExist(errPdf) {
			break
		}
		counter++
		savePath = fmt.Sprintf("%s (%d)", originalSavePath, counter)
	}
	// PSD format cannot be embedded in PDF; fallback to JPG if PDF is requested
	if opts.IsPdf && strings.ToUpper(opts.SaveFormat) == "PSD" {
		opts.SaveFormat = "JPG"
	}

	if opts.IsNoStitch {
		return ProcessBatchNoStitch(images, savePath, opts)
	}

	// Stitched mode
	if opts.Controller != nil {
		if err := opts.Controller.CheckState(); err != nil {
			return "", err
		}
	}

	result, err := imageio.GetConcatVOptimized(images, opts.NewWidth, opts.IsCustomWidth, opts.MaxWorkers)
	if err != nil || result == nil {
		return "", fmt.Errorf("failed to stitch images: %w", err)
	}

	if opts.Controller != nil {
		if err := opts.Controller.CheckState(); err != nil {
			return "", err
		}
	}

	hLimit := opts.HeightLimit
	if hLimit <= 0 {
		hLimit = 16000
	}
	slicesCount := float64(result.Bounds().Dy()) / float64(hLimit)
	if slicesCount <= 0 {
		slicesCount = 1
	}

	slicerOpts := slicing.SlicerOptions{
		SaveFormat:            opts.SaveFormat,
		SlicesCount:           slicesCount,
		SaveQuality:           opts.SaveQuality,
		Mode:                  opts.Mode,
		CurrentDate:           opts.CurrentDate,
		SaveDirectory:         filepath.Base(savePath),
		IsZip:                 opts.IsZip,
		IsPdf:                 opts.IsPdf,
		IsCbz:                 opts.IsCbz,
		ProgressCallback:      opts.ProgressCallback,
		OutputBase:            filepath.Dir(savePath),
		MaxWorkers:            opts.MaxWorkers,
		FilenamePattern:       opts.FilenamePattern,
		FilenameDigits:        opts.FilenameDigits,
		WatermarkEnabled:      opts.WatermarkEnabled,
		WatermarkPath:         opts.WatermarkPath,
		WatermarkCount:        opts.WatermarkCount,
		WatermarkEdge:         opts.WatermarkEdge,
		WatermarkWidthPercent: opts.WatermarkWidthPercent,
		WatermarkMargin:       opts.WatermarkMargin,
	}

	return slicing.Slicer(result, slicerOpts)
}
