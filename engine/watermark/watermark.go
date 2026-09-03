package watermark

import (
	"image"
	"image/draw"
	"math"
	"os"
	"sync"

	"photoslicer/engine/imageio"

	"github.com/disintegration/imaging"
)

type cacheEntry struct {
	mtime int64
	size  int64
	img   *image.RGBA
}

var (
	wmCacheLock sync.Mutex
	wmCache     = make(map[string]cacheEntry)
)

func getCachedWatermark(path string) *image.RGBA {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}

	wmCacheLock.Lock()
	defer wmCacheLock.Unlock()

	if entry, ok := wmCache[path]; ok {
		if entry.mtime == fi.ModTime().UnixNano() && entry.size == fi.Size() {
			return entry.img
		}
	}

	src, err := imageio.OpenImageRobust(path)
	if err != nil {
		return nil
	}

	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	wmCache[path] = cacheEntry{
		mtime: fi.ModTime().UnixNano(),
		size:  fi.Size(),
		img:   rgba,
	}

	return rgba
}

// PrepareWatermark scales the watermark image to fit the canvas:
// never wider than canvas width, and never taller than 80% of one of the `count` segments.
func PrepareWatermark(watermarkPath string, canvasW, canvasH, count int, widthPercent int) *image.RGBA {
	wmOrig := getCachedWatermark(watermarkPath)
	if wmOrig == nil {
		return nil
	}

	origW := wmOrig.Bounds().Dx()
	origH := wmOrig.Bounds().Dy()

	// Base target width from widthPercent (default ~12% of canvas width)
	targetW := int(float64(canvasW) * (float64(widthPercent) / 100.0))
	if targetW < 10 {
		targetW = origW
	}
	if targetW > canvasW {
		targetW = canvasW
	}

	targetH := int((float64(targetW) / float64(origW)) * float64(origH))
	if targetH < 5 {
		targetH = 5
	}

	if count <= 0 {
		count = 1
	}
	maxAllowedH := int(float64(canvasH) / float64(count) * 0.8)
	if maxAllowedH > 0 && targetH > maxAllowedH {
		targetH = maxAllowedH
		if targetH < 5 {
			targetH = 5
		}
		targetW = int((float64(targetH) / float64(origH)) * float64(origW))
		if targetW < 10 {
			targetW = 10
		}
	}

	resized := imaging.Resize(wmOrig, targetW, targetH, imaging.Lanczos)
	b := resized.Bounds()
	resRGBA := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(resRGBA, resRGBA.Bounds(), resized, b.Min, draw.Src)
	return resRGBA
}

type Point struct {
	X int
	Y int
}

type Placement struct {
	Image *image.RGBA
	X     int
	Y     int
	Name  string
}

// DefaultWatermarkPlacements gives deterministic fallback positions:
// one watermark per segment, vertically centered, aligned to left or right edge.
func DefaultWatermarkPlacements(canvasW, canvasH, count int, wmSize image.Point, edge string, margin int) []Point {
	if count <= 0 {
		count = 1
	}

	var xPos int
	if edge == "left" {
		xPos = margin
		if xPos > canvasW-wmSize.X {
			xPos = canvasW - wmSize.X
		}
		if xPos < 0 {
			xPos = 0
		}
	} else {
		xPos = canvasW - margin - wmSize.X
		if xPos < 0 {
			xPos = 0
		}
	}

	var placements []Point
	segmentHeight := float64(canvasH) / float64(count)

	for i := 0; i < count; i++ {
		segStart := int(float64(i) * segmentHeight)
		segEnd := int(float64(i+1) * segmentHeight)
		segH := segEnd - segStart
		yPos := segStart + int(math.Max(0, float64(segH-wmSize.Y)/2.0))
		placements = append(placements, Point{X: xPos, Y: yPos})
	}

	return placements
}

// ComputeWatermarkPlacements computes optimal non-overlapping placements.
func ComputeWatermarkPlacements(img image.Image, watermarkPath string, count int, edge string, widthPercent int, margin int) ([]Placement, *image.RGBA) {
	b := img.Bounds()
	canvasW := b.Dx()
	canvasH := b.Dy()

	wm := PrepareWatermark(watermarkPath, canvasW, canvasH, count, widthPercent)
	if wm == nil {
		return nil, nil
	}

	wmW := wm.Bounds().Dx()
	wmH := wm.Bounds().Dy()

	points := DefaultWatermarkPlacements(canvasW, canvasH, count, image.Pt(wmW, wmH), edge, margin)

	var result []Placement
	for i, pt := range points {
		result = append(result, Placement{
			Image: wm,
			X:     pt.X,
			Y:     pt.Y,
			Name:  "Watermark",
		})
		_ = i
	}

	return result, wm
}

// ApplyWatermark composites watermark onto an image.
func ApplyWatermark(img image.Image, watermarkPath string, count int, edge string, widthPercent int, margin int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)

	placements, _ := ComputeWatermarkPlacements(img, watermarkPath, count, edge, widthPercent, margin)
	for _, p := range placements {
		if p.Image == nil {
			continue
		}
		wmB := p.Image.Bounds()
		dstRect := image.Rect(p.X, p.Y, p.X+wmB.Dx(), p.Y+wmB.Dy())
		draw.Draw(dst, dstRect, p.Image, wmB.Min, draw.Over)
	}

	return dst
}
