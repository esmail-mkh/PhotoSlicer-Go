package imageio

import (
	"fmt"
	"image"
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
// 3. Pastes normalized buffers into their precomputed, non-overlapping slots.
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
	for i, dr := range dimResults {
		if dr.err != nil {
			return nil, fmt.Errorf("failed to read image dimensions %s: %w", imagePaths[i], dr.err)
		}
		if dr.w <= 0 || dr.h <= 0 {
			return nil, fmt.Errorf("invalid image dimensions for %s", imagePaths[i])
		}
	}

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

	// Each task overwrites its complete, non-overlapping slot. A failed task
	// causes the whole operation to return an error instead of a partial image.
	canvas := image.NewRGBA(image.Rect(0, 0, targetWidth, totalHeight))

	// --- Pass 2: Concurrent Resize & Direct Memory-Optimized Blit ---
	tasksChan := make(chan resizeTask, len(validIndices))
	var workerWg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	for w := 0; w < maxWorkers; w++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for task := range tasksChan {
				srcImg, err := OpenImageRobust(task.path)
				if err != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("failed to decode image %s: %w", task.path, err)
					})
					continue
				}

				resized := ResizeBicubic(srcImg, task.targetW, task.targetH)
				copyRGBARegion(canvas, resized, task.yOffset)
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
	if firstErr != nil {
		return nil, firstErr
	}

	return canvas, nil
}

// copyRGBARegion copies a resized image into its precomputed vertical slot.
// Slots are contiguous and non-overlapping, so a row copy is sufficient and
// avoids the generic draw path and lock overhead in the hot stitching loop.
func copyRGBARegion(dst, src *image.RGBA, dstY int) {
	srcBounds := src.Bounds()
	width := srcBounds.Dx()
	height := srcBounds.Dy()
	for y := 0; y < height; y++ {
		srcOffset := src.PixOffset(srcBounds.Min.X, srcBounds.Min.Y+y)
		dstOffset := dst.PixOffset(0, dstY+y)
		copy(dst.Pix[dstOffset:dstOffset+width*4], src.Pix[srcOffset:srcOffset+width*4])
	}
}
