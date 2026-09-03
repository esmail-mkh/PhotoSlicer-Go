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
}

type resizeResult struct {
	index int
	img   *image.RGBA
	err   error
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
	totalHeight := 0

	for i, dr := range dimResults {
		if dr.err == nil && dr.w > 0 {
			newH := int(math.Round((float64(targetWidth) / float64(dr.w)) * float64(dr.h)))
			if newH < 1 {
				newH = 1
			}
			finalHeights[i] = newH
			validIndices = append(validIndices, i)
			totalHeight += newH
		}
	}

	if totalHeight <= 0 || len(validIndices) == 0 {
		return nil, fmt.Errorf("no valid images could be processed for stitching")
	}

	// Create composite canvas with white background
	canvas := image.NewRGBA(image.Rect(0, 0, targetWidth, totalHeight))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	// --- Pass 2: Concurrent Resize & Sequential Direct Blit ---
	tasksChan := make(chan resizeTask, len(validIndices))
	resultsChan := make(chan resizeResult, len(validIndices))

	var workerWg sync.WaitGroup
	for w := 0; w < maxWorkers; w++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for task := range tasksChan {
				srcImg, err := OpenImageRobust(task.path)
				if err != nil {
					resultsChan <- resizeResult{index: task.index, err: err}
					continue
				}

				resized := ResizeBicubic(srcImg, task.targetW, task.targetH)
				resultsChan <- resizeResult{index: task.index, img: resized}
			}
		}()
	}

	for _, idx := range validIndices {
		tasksChan <- resizeTask{
			index:   idx,
			path:    imagePaths[idx],
			targetW: targetWidth,
			targetH: finalHeights[idx],
		}
	}
	close(tasksChan)

	go func() {
		workerWg.Wait()
		close(resultsChan)
	}()

	resultsMap := make(map[int]*image.RGBA)
	for res := range resultsChan {
		if res.err == nil && res.img != nil {
			resultsMap[res.index] = res.img
		}
	}

	currentY := 0
	for _, idx := range validIndices {
		img, ok := resultsMap[idx]
		if !ok || img == nil {
			continue
		}

		h := img.Bounds().Dy()
		dstRect := image.Rect(0, currentY, targetWidth, currentY+h)
		draw.Draw(canvas, dstRect, img, img.Bounds().Min, draw.Src)
		currentY += h
	}

	if currentY < totalHeight {
		if currentY > 0 {
			// Sub-image crop to actual pasted height
			return canvas.SubImage(image.Rect(0, 0, targetWidth, currentY)).(*image.RGBA), nil
		}
		return nil, fmt.Errorf("failed to stitch any images")
	}

	return canvas, nil
}
