package slicing

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"photoslicer/engine/archive"
	"photoslicer/engine/constants"
	"photoslicer/engine/imageio"
	"photoslicer/engine/psd"
	"photoslicer/engine/sorting"
	"photoslicer/engine/watermark"
)

type SlicerOptions struct {
	SaveFormat            string
	SlicesCount           float64
	SaveQuality           int
	Mode                  string // "single" or "multi"
	CurrentDate           string
	SaveDirectory         string
	IsZip                 bool
	IsPdf                 bool
	IsCbz                 bool
	ProgressCallback      func(percent float64)
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
}

// Slicer segments a tall composite image into slices and packages them into folders/archives.
func Slicer(img image.Image, opts SlicerOptions) (string, error) {
	if opts.OutputBase == "" {
		opts.OutputBase = "./Results"
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 4
	}
	if opts.FilenamePattern == "" {
		opts.FilenamePattern = "[number]"
	}
	if opts.FilenameDigits <= 0 {
		opts.FilenameDigits = 3
	}
	if opts.WatermarkWidthPercent <= 0 {
		opts.WatermarkWidthPercent = 12
	}

	folderName := opts.SaveDirectory
	if folderName == "" {
		if opts.Mode == "single" {
			folderName = "PhotoSlicer_Output"
		} else {
			folderName = opts.CurrentDate
		}
	}

	var savePath string
	if opts.Mode == "single" {
		savePath = filepath.Join(opts.OutputBase, folderName)
	} else {
		savePath = filepath.Join(opts.OutputBase, opts.CurrentDate, folderName)
	}

	// Avoid duplicate folder/archive names
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

	if err := os.MkdirAll(savePath, 0755); err != nil {
		return "", err
	}

	b := img.Bounds()
	imgWidth := b.Dx()
	imgHeight := b.Dy()

	rawCuts := FindSafeCutPoints(img, opts.SlicesCount)
	cutPoints := append([]int{0}, rawCuts...)

	targetMaxH := imgHeight
	if opts.SlicesCount > 0 {
		targetMaxH = int(float64(imgHeight) / opts.SlicesCount)
	}
	if targetMaxH <= 0 {
		targetMaxH = imgHeight
	}

	fmtLower := strings.ToLower(opts.SaveFormat)
	if fmtLower == "webp" {
		if targetMaxH > constants.WebPMaxDimension {
			targetMaxH = constants.WebPMaxDimension
		}
	} else {
		if targetMaxH > constants.JpegMaxDimension {
			targetMaxH = constants.JpegMaxDimension
		}
	}

	cutPoints = CapSliceGaps(cutPoints, targetMaxH)

	// Filter tiny sub-5px slice gaps
	var filteredCuts []int
	filteredCuts = append(filteredCuts, cutPoints[0])
	for i := 1; i < len(cutPoints); i++ {
		cp := cutPoints[i]
		last := filteredCuts[len(filteredCuts)-1]
		if cp-last >= 5 {
			filteredCuts = append(filteredCuts, cp)
		} else if i == len(cutPoints)-1 {
			hardMax := constants.JpegMaxDimension
			if fmtLower == "webp" {
				hardMax = constants.WebPMaxDimension
			}
			if cp > hardMax {
				if len(filteredCuts) >= 2 {
					floor := filteredCuts[len(filteredCuts)-2] + 5
					newPrev := cp - 5
					if floor > newPrev {
						newPrev = floor
					}
					filteredCuts[len(filteredCuts)-1] = newPrev
					filteredCuts = append(filteredCuts, cp)
				} else {
					filteredCuts[len(filteredCuts)-1] = hardMax
				}
			} else {
				filteredCuts[len(filteredCuts)-1] = cp
			}
		}
	}
	cutPoints = filteredCuts

	numSlices := len(cutPoints) - 1
	if numSlices <= 0 {
		numSlices = 1
		cutPoints = []int{0, imgHeight}
	}

	type sliceTask struct {
		index int
		start int
		end   int
	}

	taskChan := make(chan sliceTask, numSlices)
	var completedCount int64

	var workerWg sync.WaitGroup
	for w := 0; w < opts.MaxWorkers; w++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for t := range taskChan {
				subRect := image.Rect(0, t.start, imgWidth, t.end)
				var sliceImg image.Image
				if sub, ok := img.(interface {
					SubImage(r image.Rectangle) image.Image
				}); ok {
					sliceImg = sub.SubImage(subRect)
				} else {
					rgba := image.NewRGBA(image.Rect(0, 0, imgWidth, t.end-t.start))
					for sy := t.start; sy < t.end; sy++ {
						for sx := 0; sx < imgWidth; sx++ {
							rgba.Set(sx, sy-t.start, img.At(sx, sy))
						}
					}
					sliceImg = rgba
				}

				isPsd := fmtLower == "psd"
				if opts.WatermarkEnabled && !isPsd && opts.WatermarkPath != "" {
					sliceImg = watermark.ApplyWatermark(
						sliceImg,
						opts.WatermarkPath,
						opts.WatermarkCount,
						opts.WatermarkEdge,
						opts.WatermarkWidthPercent,
						opts.WatermarkMargin,
					)
				}

				filename := FormatFilename(
					opts.FilenamePattern,
					t.index,
					opts.FilenameDigits,
					opts.SaveFormat,
					filepath.Base(savePath),
					numSlices,
				)
				destFile := filepath.Join(savePath, filename)

				if isPsd {
					_ = psd.SavePSDLayered(
						sliceImg,
						destFile,
						opts.WatermarkEnabled,
						opts.WatermarkPath,
						opts.WatermarkCount,
						opts.WatermarkEdge,
						opts.WatermarkWidthPercent,
						opts.WatermarkMargin,
					)
				} else {
					_ = imageio.SaveImage(sliceImg, destFile, opts.SaveFormat, opts.SaveQuality)
				}

				done := atomic.AddInt64(&completedCount, 1)
				if opts.ProgressCallback != nil {
					pct := (float64(done) / float64(numSlices)) * 100.0
					opts.ProgressCallback(pct)
				}
			}
		}()
	}

	for i := 1; i < len(cutPoints); i++ {
		taskChan <- sliceTask{
			index: i,
			start: cutPoints[i-1],
			end:   cutPoints[i],
		}
	}
	close(taskChan)
	workerWg.Wait()

	// Handle archive outputs
	finalResultPath := savePath
	baseName := filepath.Base(savePath)

	if opts.IsZip {
		zipPath := filepath.Join(filepath.Dir(savePath), baseName+".zip")
		files, _ := filepath.Glob(filepath.Join(savePath, "*"))
		files = sorting.SortKeyImproved(files)
		if err := archive.CreateZip(zipPath, files); err == nil {
			_ = os.RemoveAll(savePath)
			finalResultPath = zipPath
		}
	} else if opts.IsCbz {
		cbzPath := filepath.Join(filepath.Dir(savePath), baseName+".cbz")
		files, _ := filepath.Glob(filepath.Join(savePath, "*"))
		files = sorting.SortKeyImproved(files)
		if err := archive.CreateCbz(cbzPath, files); err == nil {
			_ = os.RemoveAll(savePath)
			finalResultPath = cbzPath
		}
	} else if opts.IsPdf {
		pdfPath := filepath.Join(filepath.Dir(savePath), baseName+".pdf")
		files, _ := filepath.Glob(filepath.Join(savePath, "*"))
		files = sorting.SortKeyImproved(files)
		if err := archive.CreatePdfFromImages(pdfPath, files); err == nil {
			_ = os.RemoveAll(savePath)
			finalResultPath = pdfPath
		}
	}

	return finalResultPath, nil
}
