package imageio

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"
)

type dimResult struct {
	index int
	w     int
	h     int
	err   error
}

type resizeTask struct {
	index   int
	path    string
	targetW int
	targetH int
	yOffset int
}


// GetConcatVOptimized stitches images vertically into a single tall canvas.
// 1. Concurrently inspects dimensions.
// 2. Concurrently decodes and resizes images in worker goroutines.
// 3. Pastes normalized buffers sequentially onto a white canvas.
func GetConcatVOptimized(imagePaths []string, newWidth int, isCustomWidth bool, maxWorkers int) (*image.RGBA, error) {
	if len(imagePaths) == 0 {
		return nil, fmt.Errorf("no images provided for stitching")
	}

	if maxWorkers <= 0 {
		maxWorkers = 4
	}

	totalImgs := len(imagePaths)

	// --- Pass 1: Scan Dimensions Concurrently ---
	dimResults := make([]dimResult, totalImgs)
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxWorkers)

	for i, p := range imagePaths {
		wg.Add(1)
		go func(idx int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			w, h, err := GetImageSizeFast(path)
			dimResults[idx] = dimResult{index: idx, w: w, h: h, err: err}
		}(i, p)
	}
	wg.Wait()

	maxW := 0
	for _, dr := range dimResults {
		if dr.w > maxW {
			maxW = dr.w
		}
	}

	targetWidth := maxW
	if isCustomWidth && newWidth > 0 {
		targetWidth = newWidth
	}
	if targetWidth <= 0 {
		return nil, fmt.Errorf("invalid target width: %d", targetWidth)
	}

	finalHeights := make([]int, totalImgs)
	var validIndices []int

	for i, dr := range dimResults {
		if dr.err == nil && dr.w > 0 {
			newH := int(math.Round((float64(targetWidth) / float64(dr.w)) * float64(dr.h)))
			if newH < 1 {
				newH = 1
			}
			finalHeights[i] = newH
			validIndices = append(validIndices, i)
		}
	}

	if len(validIndices) == 0 {
		return nil, fmt.Errorf("no valid images could be processed for stitching")
	}

	yOffsets := make([]int, totalImgs)
	currentY := 0
	for _, idx := range validIndices {
		yOffsets[idx] = currentY
		currentY += finalHeights[idx]
	}
	totalHeight := currentY

	if totalHeight <= 0 {
		return nil, fmt.Errorf("no valid images could be processed for stitching")
	}

	totalPixels := int64(targetWidth) * int64(totalHeight)
	if totalPixels > 300_000_000 {
		return nil, fmt.Errorf("stitched image exceeds memory limit (%d pixels, height %dpx); please use No-Stitch mode or reduce width", totalPixels, totalHeight)
	}

	// Create composite canvas with white background
	canvas := image.NewRGBA(image.Rect(0, 0, targetWidth, totalHeight))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	// --- Pass 2: Concurrent Resize & Direct Memory-Optimized Blit ---
	tasksChan := make(chan resizeTask, len(validIndices))
	var canvasMu sync.Mutex
	var workerWg sync.WaitGroup

	for w := 0; w < maxWorkers; w++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for task := range tasksChan {
				srcImg, err := OpenImageRobust(task.path)
				if err != nil {
					continue
				}

				resized := ResizeBicubic(srcImg, task.targetW, task.targetH)
				canvasMu.Lock()
				dstRect := image.Rect(0, task.yOffset, targetWidth, task.yOffset+resized.Bounds().Dy())
				draw.Draw(canvas, dstRect, resized, resized.Bounds().Min, draw.Src)
				canvasMu.Unlock()
			}
		}()
	}

	for _, idx := range validIndices {
		tasksChan <- resizeTask{
			index:   idx,
			path:    imagePaths[idx],
			targetW: targetWidth,
			targetH: finalHeights[idx],
			yOffset: yOffsets[idx],
		}
	}
	close(tasksChan)
	workerWg.Wait()

	return canvas, nil
}
