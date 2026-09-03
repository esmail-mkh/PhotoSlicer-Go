package watermark

import (
	"image"
	"math"
	"sort"
)

const (
	whiteThreshold       = 230
	blackThreshold       = 40
	bubbleWhiteThreshold = 235
	minGutterHeight      = 8
	minGutterCoverage    = 0.65
	edgeMargin           = 80
	adjustmentStep       = 15
	maxAdjustment        = 300
	minSafeScore         = 20
	bubbleMaskScale      = 4
	maskClearance        = 8
)

// Gutter represents an empty background run between comic panels.
type Gutter struct {
	Start  int
	End    int
	Type   string // "white", "black", "uniform"
	Height int
}

// RegionInfo contains diagnostic metrics for a candidate placement region.
type RegionInfo struct {
	Valid          bool
	MeanBrightness float64
	Variance       float64
	Saturation     float64
	HasText        bool
	TextConfidence float64
	IsSpeechBubble bool
	BubbleOverlap  bool
	IsFace         bool
	FaceConfidence float64
	BubbleAtTop    bool
	BubbleAtBottom bool
	OverlapCells   int
	MaskOverlap    float64
}

type candidate struct {
	y          int
	score      float64
	edgeType   string
	gutterType string
	adjusted   bool
	adjustment int
	info       RegionInfo
}

// ExtractGrayAndSaturation converts image to uint8 grayscale and uint8 saturation buffers.
func ExtractGrayAndSaturation(img image.Image) ([]byte, []byte, int, int) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	total := w * h

	gray := make([]byte, total)
	sat := make([]byte, total)

	if rgba, ok := img.(*image.RGBA); ok {
		pix := rgba.Pix
		stride := rgba.Stride
		for y := 0; y < h; y++ {
			rowOff := y * stride
			destRow := y * w
			for x := 0; x < w; x++ {
				idx := rowOff + x*4
				r := uint32(pix[idx])
				g := uint32(pix[idx+1])
				bl := uint32(pix[idx+2])

				// Grayscale: (77*r + 150*g + 29*b) >> 8
				gray[destRow+x] = uint8((r*77 + g*150 + bl*29) >> 8)

				// Saturation: ((max - min) * 255) / max
				maxVal := r
				if g > maxVal {
					maxVal = g
				}
				if bl > maxVal {
					maxVal = bl
				}

				minVal := r
				if g < minVal {
					minVal = g
				}
				if bl < minVal {
					minVal = bl
				}

				if maxVal > 0 {
					sat[destRow+x] = uint8(((maxVal - minVal) * 255) / maxVal)
				} else {
					sat[destRow+x] = 0
				}
			}
		}
		return gray, sat, w, h
	}

	// General image fallback
	for y := 0; y < h; y++ {
		destRow := y * w
		for x := 0; x < w; x++ {
			c := img.At(x+b.Min.X, y+b.Min.Y)
			r32, g32, b32, _ := c.RGBA()
			r := r32 >> 8
			g := g32 >> 8
			bl := b32 >> 8

			gray[destRow+x] = uint8((r*77 + g*150 + bl*29) >> 8)

			maxVal := r
			if g > maxVal {
				maxVal = g
			}
			if bl > maxVal {
				maxVal = bl
			}

			minVal := r
			if g < minVal {
				minVal = g
			}
			if bl < minVal {
				minVal = bl
			}

			if maxVal > 0 {
				sat[destRow+x] = uint8(((maxVal - minVal) * 255) / maxVal)
			} else {
				sat[destRow+x] = 0
			}
		}
	}

	return gray, sat, w, h
}

// BuildBubbleMask creates a downscaled connected speech-bubble mask.
func BuildBubbleMask(gray, sat []byte, w, h int) ([]bool, int, int) {
	scale := bubbleMaskScale
	hh := (h / scale) * scale
	ww := (w / scale) * scale
	mh := hh / scale
	mw := ww / scale

	if mh < 8 || mw < 8 {
		return nil, 0, 0
	}

	cellsTotal := mh * mw
	white := make([]bool, cellsTotal)
	dark := make([]bool, cellsTotal)

	for cy := 0; cy < mh; cy++ {
		for cx := 0; cx < mw; cx++ {
			y0 := cy * scale
			x0 := cx * scale

			var sumG, sumS, minG int
			minG = 255

			for dy := 0; dy < scale; dy++ {
				rowOff := (y0 + dy) * w
				for dx := 0; dx < scale; dx++ {
					g := int(gray[rowOff+x0+dx])
					s := int(sat[rowOff+x0+dx])
					sumG += g
					sumS += s
					if g < minG {
						minG = g
					}
				}
			}

			gMean := sumG / 16
			sMean := sumS / 16

			cIdx := cy*mw + cx
			// White bubble-interior: high mean, min not crossed by dark border, unsaturated
			white[cIdx] = (gMean > 225 && minG > 165 && sMean < 26) // 0.10 * 255 = 25.5
			dark[cIdx] = (minG < 100)
		}
	}

	// Text seeds from tiles
	tile := 48 / scale
	if tile < 6 {
		tile = 6
	}
	th := mh / tile
	tw := mw / tile

	if th < 1 || tw < 1 {
		return nil, 0, 0
	}

	seeds := make([]bool, cellsTotal)
	var seedCount int

	for ty := 0; ty < th; ty++ {
		for tx := 0; tx < tw; tx++ {
			var whiteCells, darkCells, transCount int

			// Check dark columns / rows (borders)
			borderCol := false
			borderRow := false

			for dy := 0; dy < tile; dy++ {
				rowAllDark := true
				cy := ty*tile + dy
				for dx := 0; dx < tile; dx++ {
					cx := tx*tile + dx
					cIdx := cy*mw + cx
					if dark[cIdx] {
						darkCells++
					} else {
						rowAllDark = false
					}
					if white[cIdx] {
						whiteCells++
					}
					if dx < tile-1 {
						nextIdx := cy*mw + cx + 1
						if dark[cIdx] != dark[nextIdx] {
							transCount++
						}
					}
				}
				if rowAllDark {
					borderRow = true
				}
			}

			for dx := 0; dx < tile; dx++ {
				colAllDark := true
				cx := tx*tile + dx
				for dy := 0; dy < tile; dy++ {
					cy := ty*tile + dy
					if !dark[cy*mw+cx] {
						colAllDark = false
						break
					}
				}
				if colAllDark {
					borderCol = true
					break
				}
			}

			tileCells := float64(tile * tile)
			whiteRatio := float64(whiteCells) / tileCells
			darkRatio := float64(darkCells) / tileCells
			transRate := float64(transCount) / float64(tile*(tile-1))

			isTextTile := (whiteRatio > 0.35 && darkRatio > 0.08 && darkRatio < 0.45 && transRate > 0.15 && !borderCol && !borderRow)

			if isTextTile {
				for dy := 0; dy < tile; dy++ {
					cy := ty*tile + dy
					for dx := 0; dx < tile; dx++ {
						cx := tx*tile + dx
						cIdx := cy*mw + cx
						if white[cIdx] {
							seeds[cIdx] = true
							seedCount++
						}
					}
				}
			}
		}
	}

	if seedCount == 0 {
		return nil, 0, 0
	}

	// Grow seeds through connected white cells
	mask := make([]bool, cellsTotal)
	frontier := make([]int, 0, seedCount)
	for i, s := range seeds {
		if s {
			mask[i] = true
			frontier = append(frontier, i)
		}
	}

	maxIters := 16
	if 64/scale < maxIters {
		maxIters = 64 / scale
	}
	if maxIters < 10 {
		maxIters = 10
	}

	for iter := 0; iter < maxIters; iter++ {
		if len(frontier) == 0 {
			break
		}
		var nextFrontier []int
		for _, idx := range frontier {
			cy := idx / mw
			cx := idx % mw

			// Up
			if cy > 0 {
				nIdx := (cy-1)*mw + cx
				if white[nIdx] && !mask[nIdx] {
					mask[nIdx] = true
					nextFrontier = append(nextFrontier, nIdx)
				}
			}
			// Down
			if cy < mh-1 {
				nIdx := (cy+1)*mw + cx
				if white[nIdx] && !mask[nIdx] {
					mask[nIdx] = true
					nextFrontier = append(nextFrontier, nIdx)
				}
			}
			// Left
			if cx > 0 {
				nIdx := cy*mw + cx - 1
				if white[nIdx] && !mask[nIdx] {
					mask[nIdx] = true
					nextFrontier = append(nextFrontier, nIdx)
				}
			}
			// Right
			if cx < mw-1 {
				nIdx := cy*mw + cx + 1
				if white[nIdx] && !mask[nIdx] {
					mask[nIdx] = true
					nextFrontier = append(nextFrontier, nIdx)
				}
			}
		}
		frontier = nextFrontier
	}

	// Safety valve: if mask exceeds 50% of the image, seeding misfired
	var totalMasked int
	for _, m := range mask {
		if m {
			totalMasked++
		}
	}
	if float64(totalMasked)/float64(cellsTotal) > 0.5 {
		return nil, 0, 0
	}

	return mask, mw, mh
}

// FindGutters locates all horizontal panel gutters in the image.
func FindGutters(gray, sat []byte, w, h int) []Gutter {
	types := make([]uint8, h)
	cov := minGutterCoverage

	for y := 0; y < h; y++ {
		rowOff := y * w
		var whiteCount, blackCount int
		var sumG, sumS int64

		for x := 0; x < w; x++ {
			g := int64(gray[rowOff+x])
			sumG += g
			if g > whiteThreshold {
				whiteCount++
			} else if g < blackThreshold {
				blackCount++
			}
			sumS += int64(sat[rowOff+x])
		}

		whiteRatio := float64(whiteCount) / float64(w)
		blackRatio := float64(blackCount) / float64(w)
		satMean := float64(sumS) / float64(w)

		if whiteRatio >= cov && satMean <= 38.25 { // 0.15 * 255
			types[y] = 1 // white
		} else if blackRatio >= cov {
			types[y] = 2 // black
		} else {
			// Check uniform variance
			meanG := float64(sumG) / float64(w)
			var varianceSum float64
			for x := 0; x < w; x++ {
				diff := float64(gray[rowOff+x]) - meanG
				varianceSum += diff * diff
			}
			if (varianceSum / float64(w)) < 25.0 {
				types[y] = 3 // uniform
			}
		}
	}

	var gutters []Gutter
	start := 0
	currType := types[0]

	for y := 1; y < h; y++ {
		if types[y] != currType {
			if currType != 0 && (y-start) >= minGutterHeight {
				gType := "white"
				if currType == 2 {
					gType = "black"
				} else if currType == 3 {
					gType = "uniform"
				}
				gutters = append(gutters, Gutter{
					Start:  start,
					End:    y,
					Type:   gType,
					Height: y - start,
				})
			}
			currType = types[y]
			start = y
		}
	}
	if currType != 0 && (h-start) >= minGutterHeight {
		gType := "white"
		if currType == 2 {
			gType = "black"
		} else if currType == 3 {
			gType = "uniform"
		}
		gutters = append(gutters, Gutter{
			Start:  start,
			End:    h,
			Type:   gType,
			Height: h - start,
		})
	}

	return gutters
}

func detectTextPattern(gray []byte, w, h, x0, y0, x1, y1 int) (bool, float64) {
	rw := x1 - x0
	rh := y1 - y0
	total := rw * rh
	if total <= 0 {
		return false, 0
	}

	var veryWhite, dark, mediumDark int
	for y := y0; y < y1; y++ {
		rowOff := y * w
		for x := x0; x < x1; x++ {
			g := gray[rowOff+x]
			if g > 235 {
				veryWhite++
			}
			if g > 10 && g < 80 {
				dark++
			}
			if g >= 80 && g < 150 {
				mediumDark++
			}
		}
	}

	veryWhiteRatio := float64(veryWhite) / float64(total)
	if veryWhiteRatio < 0.15 {
		return false, 0
	}

	darkRatio := float64(dark) / float64(total)
	mediumRatio := float64(mediumDark) / float64(total)

	hasText := (darkRatio > 0.02 && darkRatio < 0.25) || (darkRatio > 0.01 && mediumRatio > 0.05)
	confidence := 0.0
	if hasText {
		confidence = math.Min(1.0, darkRatio*10.0)
	}
	return hasText, confidence
}

func detectFaceRegion(gray, sat []byte, w, h, x0, y0, x1, y1 int) (bool, float64) {
	rw := x1 - x0
	rh := y1 - y0
	total := rw * rh
	if total <= 0 || rw <= 2 || rh <= 2 {
		return false, 0
	}

	var sumG, sumS int64
	for y := y0; y < y1; y++ {
		rowOff := y * w
		for x := x0; x < x1; x++ {
			sumG += int64(gray[rowOff+x])
			sumS += int64(sat[rowOff+x])
		}
	}
	meanG := float64(sumG) / float64(total)
	meanSat := (float64(sumS) / float64(total)) / 255.0

	var varSum float64
	var edgeY, edgeX int
	for y := y0; y < y1; y++ {
		rowOff := y * w
		for x := x0; x < x1; x++ {
			diff := float64(gray[rowOff+x]) - meanG
			varSum += diff * diff

			if y < y1-1 {
				dy := int(gray[rowOff+w+x]) - int(gray[rowOff+x])
				if dy < 0 {
					dy = -dy
				}
				if dy > 20 {
					edgeY++
				}
			}
			if x < x1-1 {
				dx := int(gray[rowOff+x+1]) - int(gray[rowOff+x])
				if dx < 0 {
					dx = -dx
				}
				if dx > 20 {
					edgeX++
				}
			}
		}
	}

	variance := varSum / float64(total)
	edgeDensity := (float64(edgeY)/float64(rw*(rh-1)) + float64(edgeX)/float64((rw-1)*rh)) / 2.0

	isFace := (variance > 1500 && edgeDensity > 0.15 && meanSat > 0.1 && meanSat < 0.5)
	confidence := 0.0
	if isFace {
		confidence = math.Min(1.0, edgeDensity*3.0)
	}
	return isFace, confidence
}

func detectBubbleOverlap(gray, sat []byte, w, h, y, yEnd, xStart, xEnd int, colWhite []float64) (bool, int, bool, bool) {
	clearance := 24
	y0 := y - clearance
	if y0 < 0 {
		y0 = 0
	}
	y1 := yEnd + clearance
	if y1 > h {
		y1 = h
	}

	for y0 < y {
		var cnt int
		for x := xStart; x < xEnd; x++ {
			if gray[y0*w+x] > bubbleWhiteThreshold {
				cnt++
			}
		}
		if float64(cnt)/float64(xEnd-xStart) > 0.85 {
			y0++
		} else {
			break
		}
	}

	for y1 > yEnd {
		var cnt int
		for x := xStart; x < xEnd; x++ {
			if gray[(y1-1)*w+x] > bubbleWhiteThreshold {
				cnt++
			}
		}
		if float64(cnt)/float64(xEnd-xStart) > 0.85 {
			y1--
		} else {
			break
		}
	}

	cwX0 := xStart
	for cwX0 < xEnd-3 && cwX0-xStart < len(colWhite) && colWhite[cwX0-xStart] > 0.9 {
		cwX0++
	}
	cwX1 := xEnd
	for cwX1 > cwX0+3 && cwX1-1-xStart < len(colWhite) && colWhite[cwX1-1-xStart] > 0.9 {
		cwX1--
	}

	rh := y1 - y0
	rw := cwX1 - cwX0
	if rh < 3 || rw < 3 {
		return false, 0, false, false
	}

	rowEdges := [4]int{y0, y0 + rh/3, y0 + (2 * rh) / 3, y1}
	colEdges := [4]int{cwX0, cwX0 + rw/3, cwX0 + (2 * rw) / 3, cwX1}

	var overlapCount int
	hasTop := false
	hasBottom := false

	for r := 0; r < 3; r++ {
		ry0, ry1 := rowEdges[r], rowEdges[r+1]

		// Band check
		var bandWhite int
		bandTotal := rw * (ry1 - ry0)
		for by := ry0; by < ry1; by++ {
			for bx := cwX0; bx < cwX1; bx++ {
				if gray[by*w+bx] > bubbleWhiteThreshold {
					bandWhite++
				}
			}
		}
		if float64(bandWhite)/float64(bandTotal) >= minGutterCoverage {
			var bandEdges int
			for by := ry0; by < ry1-1; by++ {
				for bx := cwX0; bx < cwX1; bx++ {
					dy := int(gray[(by+1)*w+bx]) - int(gray[by*w+bx])
					if dy < 0 {
						dy = -dy
					}
					if dy > 40 {
						bandEdges++
					}
				}
			}
			if float64(bandEdges)/float64(rw*(ry1-ry0-1)) < 0.01 {
				continue
			}
		}

		for c := 0; c < 3; c++ {
			cx0, cx1 := colEdges[c], colEdges[c+1]
			cellSize := (ry1 - ry0) * (cx1 - cx0)
			if cellSize < 25 {
				continue
			}

			var veryWhite int
			var satSum int
			for cy := ry0; cy < ry1; cy++ {
				for cx := cx0; cx < cx1; cx++ {
					g := gray[cy*w+cx]
					if g > bubbleWhiteThreshold {
						veryWhite++
						satSum += int(sat[cy*w+cx])
					}
				}
			}

			veryWhiteRatio := float64(veryWhite) / float64(cellSize)
			if veryWhiteRatio < 0.05 {
				continue
			}
			if veryWhite > 0 && (float64(satSum)/float64(veryWhite)) > 38.25 {
				continue
			}

			hasText, _ := detectTextPattern(gray, w, h, cx0, ry0, cx1, ry1)

			var edgeCount int
			for cy := ry0; cy < ry1; cy++ {
				for cx := cx0; cx < cx1; cx++ {
					if cy < ry1-1 {
						dy := int(gray[(cy+1)*w+cx]) - int(gray[cy*w+cx])
						if dy > 40 || dy < -40 {
							edgeCount++
						}
					}
					if cx < cx1-1 {
						dx := int(gray[cy*w+cx+1]) - int(gray[cy*w+cx])
						if dx > 40 || dx < -40 {
							edgeCount++
						}
					}
				}
			}
			edgeDensity := float64(edgeCount) / float64(cellSize*2)

			if hasText || edgeDensity > 0.015 || veryWhiteRatio > 0.75 {
				overlapCount++
				if r == 0 {
					hasTop = true
				}
				if r == 2 {
					hasBottom = true
				}
			}
		}
	}

	overlapAtTop := hasTop && !hasBottom
	overlapAtBottom := hasBottom && !hasTop
	return overlapCount > 0, overlapCount, overlapAtTop, overlapAtBottom
}

// AnalyzeRegionDetailed scores a candidate placement region.
func AnalyzeRegionDetailed(
	gray, sat []byte,
	w, h int,
	y, wmH, wmW int,
	edge string,
	xMargin int,
	bubbleMask []bool,
	maskW, maskH int,
	panelEdges []int,
	colWhite []float64,
) (float64, RegionInfo) {
	yEnd := y + wmH
	if yEnd > h {
		yEnd = h
	}

	var xStart, xEnd int
	if edge == "left" {
		xStart = xMargin
		xEnd = xMargin + wmW
		if xEnd > w {
			xEnd = w
		}
	} else {
		xStart = w - xMargin - wmW
		if xStart < 0 {
			xStart = 0
		}
		xEnd = w - xMargin
	}

	if yEnd <= y || xEnd <= xStart {
		return -999, RegionInfo{Valid: false}
	}

	regW := xEnd - xStart
	regH := yEnd - y
	nRegion := regW * regH

	var sumG, sumS int64
	var veryWhite, white, black int

	for rY := y; rY < yEnd; rY++ {
		rowOff := rY * w
		for rX := xStart; rX < xEnd; rX++ {
			g := gray[rowOff+rX]
			sumG += int64(g)
			sumS += int64(sat[rowOff+rX])
			if g > 235 {
				veryWhite++
			}
			if g > 220 {
				white++
			}
			if g < 30 {
				black++
			}
		}
	}

	meanBrightness := float64(sumG) / float64(nRegion)
	meanSaturation := (float64(sumS) / float64(nRegion)) / 255.0
	veryWhiteRatio := float64(veryWhite) / float64(nRegion)
	blackRatio := float64(black) / float64(nRegion)

	var varSum float64
	for rY := y; rY < yEnd; rY++ {
		rowOff := rY * w
		for rX := xStart; rX < xEnd; rX++ {
			d := float64(gray[rowOff+rX]) - meanBrightness
			varSum += d * d
		}
	}
	variance := varSum / float64(nRegion)

	// Speech bubble detection
	hasText, textConfidence := detectTextPattern(gray, w, h, xStart, y, xEnd, yEnd)
	isSpeechBubble := (hasText && veryWhiteRatio > 0.15) || (veryWhiteRatio > 0.65 && meanSaturation < 0.06)

	bubbleOverlap, overlapCells, overlapAtTop, overlapAtBottom := detectBubbleOverlap(
		gray, sat, w, h, y, yEnd, xStart, xEnd, colWhite,
	)

	// Connected bubble mask check
	var maskOverlap float64
	maskHit := false
	maskAtTop := false
	maskAtBottom := false

	if len(bubbleMask) > 0 && maskW > 0 && maskH > 0 {
		clr := maskClearance
		if len(panelEdges) > 0 {
			minEdgeDist := 999999
			for _, e := range panelEdges {
				d := e - y
				if d < 0 {
					d = -d
				}
				if d < minEdgeDist {
					minEdgeDist = d
				}
			}
			if minEdgeDist <= 12 {
				clr = 0
			}
		}

		my0 := (y - clr) / bubbleMaskScale
		if my0 < 0 {
			my0 = 0
		}
		my1 := (yEnd + clr + bubbleMaskScale - 1) / bubbleMaskScale
		if my1 > maskH {
			my1 = maskH
		}

		mx0 := xStart / bubbleMaskScale
		if mx0 >= maskW {
			mx0 = maskW - 1
		}
		mx1 := (xEnd + bubbleMaskScale - 1) / bubbleMaskScale
		if mx1 > maskW {
			mx1 = maskW
		}

		var mCount, mTop, mBottom, mTotal int
		midY := (my0 + my1) / 2

		for my := my0; my < my1; my++ {
			for mx := mx0; mx < mx1; mx++ {
				mTotal++
				if bubbleMask[my*maskW+mx] {
					mCount++
					if my < midY {
						mTop++
					} else {
						mBottom++
					}
				}
			}
		}

		if mTotal > 0 {
			maskOverlap = float64(mCount) / float64(mTotal)
			maskHit = maskOverlap > 0.02
			if maskHit && mTotal > 1 {
				maskAtTop = mTop > 2*mBottom
				maskAtBottom = mBottom > 2*mTop
			}
		}
	}

	// Face detection
	isFace, faceConfidence := detectFaceRegion(gray, sat, w, h, xStart, y, xEnd, yEnd)

	// Top vs Bottom half analysis
	midY := wmH / 2
	topBubble := isSpeechBubble
	bottomBubble := isSpeechBubble
	if midY > 0 && regH > midY {
		hasTextTop, _ := detectTextPattern(gray, w, h, xStart, y, xEnd, y+midY)
		hasTextBottom, _ := detectTextPattern(gray, w, h, xStart, y+midY, xEnd, yEnd)

		var vwTop, vwBottom int
		for rY := y; rY < y+midY; rY++ {
			for rX := xStart; rX < xEnd; rX++ {
				if gray[rY*w+rX] > 235 {
					vwTop++
				}
			}
		}
		for rY := y + midY; rY < yEnd; rY++ {
			for rX := xStart; rX < xEnd; rX++ {
				if gray[rY*w+rX] > 235 {
					vwBottom++
				}
			}
		}
		vwTopRatio := float64(vwTop) / float64(regW*midY)
		vwBottomRatio := float64(vwBottom) / float64(regW*(regH-midY))

		topBubble = hasTextTop || (vwTopRatio > 0.55 && meanSaturation < 0.08)
		bottomBubble = hasTextBottom || (vwBottomRatio > 0.55 && meanSaturation < 0.08)
	}

	// === Score Calculation ===
	score := 50.0

	// Speech bubble penalty (HEAVY)
	if isSpeechBubble {
		if hasText {
			score -= 200
		} else if veryWhiteRatio > 0.4 {
			score -= 180
		} else if veryWhiteRatio > 0.25 {
			score -= 120
		} else {
			score -= 80
		}
	}

	if topBubble && !bottomBubble {
		score -= 60
	} else if bottomBubble && !topBubble {
		score -= 60
	}

	if bubbleOverlap && !isSpeechBubble {
		cells := overlapCells
		if cells > 3 {
			cells = 3
		}
		score -= float64(90 * cells)
	}

	if maskHit {
		mO := maskOverlap
		if mO > 0.5 {
			mO = 0.5
		}
		score -= (150.0 + 400.0*mO)
	}

	// Artwork brightness optimization:
	// 100..185 is the ideal rich artwork zone where watermark is sharp and clear.
	if meanBrightness < 90 {
		score -= 130.0 * (1.0 - meanBrightness/90.0)
	} else if meanBrightness > 235 {
		score -= 100
	} else if meanBrightness > 220 {
		score -= 35
	} else if meanBrightness >= 100 && meanBrightness <= 185 {
		score += 35
	} else if meanBrightness > 185 && meanBrightness <= 220 {
		score += 15
	}

	if blackRatio > 0.5 {
		score -= 150
	} else if blackRatio > 0.3 {
		score -= 80
	}

	if isFace {
		score -= 100.0 * faceConfidence
	}

	// Color bonus
	if meanSaturation > 0.25 {
		score += 35
	} else if meanSaturation > 0.15 {
		score += 20
	} else if meanSaturation > 0.08 {
		score += 10
	}

	// Texture / detail bonus
	if variance > 200 && variance < 3500 {
		score += 20
	} else if variance < 100 {
		score -= 20
	} else if variance > 5000 {
		score -= 20
	}

	// Panel-Edge Affinity
	if len(panelEdges) > 0 {
		minEdgeDist := 999999
		for _, e := range panelEdges {
			d1 := e - y
			if d1 < 0 {
				d1 = -d1
			}
			d2 := e - yEnd
			if d2 < 0 {
				d2 = -d2
			}
			if d1 < minEdgeDist {
				minEdgeDist = d1
			}
			if d2 < minEdgeDist {
				minEdgeDist = d2
			}
		}

		if minEdgeDist <= 15 {
			score += 90
		} else if minEdgeDist <= 40 {
			score += 60
		} else if minEdgeDist <= 90 {
			score += 25
		} else if minEdgeDist >= 150 && len(panelEdges) > 2 {
			score -= 80
		}
	}

	// Proximity check (border sensor)
	checkMargin := 4
	if y > checkMargin {
		nearGutter := false
		if len(panelEdges) > 0 {
			for _, e := range panelEdges {
				d := e - y
				if d < 0 {
					d = -d
				}
				if d <= 10 {
					nearGutter = true
					break
				}
			}
		}
		if !nearGutter {
			var topWhite int
			for ty := y - checkMargin; ty < y; ty++ {
				for tx := xStart; tx < xEnd; tx++ {
					if gray[ty*w+tx] > 240 {
						topWhite++
					}
				}
			}
			if float64(topWhite)/float64(regW*checkMargin) > 0.75 {
				score -= 120
			}
		}
	}

	if yEnd < h-checkMargin {
		nearGutter := false
		if len(panelEdges) > 0 {
			for _, e := range panelEdges {
				d := e - yEnd
				if d < 0 {
					d = -d
				}
				if d <= 10 {
					nearGutter = true
					break
				}
			}
		}
		if !nearGutter {
			var btmWhite int
			for ty := yEnd; ty < yEnd+checkMargin; ty++ {
				for tx := xStart; tx < xEnd; tx++ {
					if gray[ty*w+tx] > 240 {
						btmWhite++
					}
				}
			}
			if float64(btmWhite)/float64(regW*checkMargin) > 0.75 {
				score -= 120
			}
		}
	}

	info := RegionInfo{
		Valid:          true,
		MeanBrightness: meanBrightness,
		Variance:       variance,
		Saturation:     meanSaturation,
		HasText:        hasText,
		TextConfidence: textConfidence,
		IsSpeechBubble: isSpeechBubble,
		BubbleOverlap:  bubbleOverlap || maskHit,
		IsFace:         isFace,
		FaceConfidence: faceConfidence,
		BubbleAtTop:    (topBubble && !bottomBubble) || overlapAtTop || maskAtTop,
		BubbleAtBottom: (bottomBubble && !topBubble) || overlapAtBottom || maskAtBottom,
		OverlapCells:   overlapCells,
		MaskOverlap:    maskOverlap,
	}

	return score, info
}

func findAdjustedPosition(
	gray, sat []byte,
	w, h int,
	initialY, direction, wmH, wmW int,
	rangeStart, rangeEnd, margin int,
	initialScore float64,
	initialInfo RegionInfo,
	edge string,
	xMargin int,
	bubbleMask []bool,
	maskW, maskH int,
	panelEdges []int,
	colWhite []float64,
) (int, float64, int, RegionInfo) {
	bestY := initialY
	bestScore := initialScore
	bestInfo := initialInfo

	step := adjustmentStep
	maxAdj := maxAdjustment

	for offset := step; offset <= maxAdj; offset += step {
		testY := initialY + (offset * direction)
		if testY < rangeStart+margin || testY+wmH > rangeEnd-margin {
			break
		}

		testScore, testInfo := AnalyzeRegionDetailed(
			gray, sat, w, h, testY, wmH, wmW, edge, xMargin,
			bubbleMask, maskW, maskH, panelEdges, colWhite,
		)

		if testScore > bestScore {
			bestY = testY
			bestScore = testScore
			bestInfo = testInfo
		}

		if testScore > minSafeScore && !testInfo.IsSpeechBubble && !testInfo.BubbleOverlap && !testInfo.IsFace {
			break
		}
	}

	adjustment := bestY - initialY
	return bestY, bestScore, adjustment, bestInfo
}

func fallbackScan(
	gray, sat []byte,
	w, h int,
	rangeStart, rangeEnd, wmW, wmH, margin int,
	edge string,
	xMargin int,
	bubbleMask []bool,
	maskW, maskH int,
	panelEdges []int,
	colWhite []float64,
) (int, float64) {
	scanStart := rangeStart + margin
	scanEnd := rangeEnd - wmH - margin

	if scanEnd <= scanStart {
		cenY := rangeStart + (rangeEnd-rangeStart-wmH)/2
		if cenY < 0 {
			cenY = 0
		}
		if cenY+wmH > h {
			cenY = h - wmH
		}
		return cenY, 0
	}

	bestY := scanStart
	bestScore := -999999.0

	evalPos := func(yPos int) float64 {
		score, _ := AnalyzeRegionDetailed(
			gray, sat, w, h, yPos, wmH, wmW, edge, xMargin,
			bubbleMask, maskW, maskH, panelEdges, colWhite,
		)
		distToSeg := yPos - rangeStart
		dEnd := rangeEnd - (yPos + wmH)
		if dEnd < distToSeg {
			distToSeg = dEnd
		}
		if distToSeg <= 50 {
			score += 50
		} else if distToSeg <= 120 {
			score += 25
		} else if distToSeg >= 250 && len(panelEdges) <= 2 {
			score -= 40
		}
		return score
	}

	coarseStep := wmH / 3
	if coarseStep > 40 {
		coarseStep = 40
	}
	if coarseStep < 10 {
		coarseStep = 10
	}

	for y := scanStart; y <= scanEnd; y += coarseStep {
		s := evalPos(y)
		if s > bestScore {
			bestScore = s
			bestY = y
		}
	}

	fineStart := bestY - coarseStep
	if fineStart < scanStart {
		fineStart = scanStart
	}
	fineEnd := bestY + coarseStep
	if fineEnd > scanEnd {
		fineEnd = scanEnd
	}

	for y := fineStart; y <= fineEnd; y += 5 {
		s := evalPos(y)
		if s > bestScore {
			bestScore = s
			bestY = y
		}
	}

	return bestY, bestScore
}

// FindBestWatermarkPosition finds the optimal content-aware watermark position inside a segment.
func FindBestWatermarkPosition(
	gray, sat []byte,
	w, h int,
	wmW, wmH int,
	rangeStart, rangeEnd int,
	edge string,
	xMargin int,
	bubbleMask []bool,
	maskW, maskH int,
	gutters []Gutter,
) (int, int, float64) {
	segH := rangeEnd - rangeStart
	margin := int(float64(segH) * 0.1)
	if margin > edgeMargin {
		margin = edgeMargin
	}
	if margin < 10 {
		margin = 10
	}

	// Column whiteness over the watermark's x-span
	var cwX0, cwX1 int
	if edge == "left" {
		cwX0 = xMargin
		cwX1 = xMargin + wmW
		if cwX1 > w {
			cwX1 = w
		}
	} else {
		cwX0 = w - xMargin - wmW
		if cwX0 < 0 {
			cwX0 = 0
		}
		cwX1 = w - xMargin
	}

	colWhite := make([]float64, cwX1-cwX0)
	for x := cwX0; x < cwX1; x++ {
		var cnt int
		for y := 0; y < h; y++ {
			if gray[y*w+x] > bubbleWhiteThreshold {
				cnt++
			}
		}
		colWhite[x-cwX0] = float64(cnt) / float64(h)
	}

	panelEdges := []int{0, h}
	for _, g := range gutters {
		panelEdges = append(panelEdges, g.Start, g.End)
	}

	type edgeItem struct {
		y          int
		edgeType   string
		gutterType string
	}
	var edges []edgeItem
	for _, g := range gutters {
		if g.Start > rangeStart+margin && g.Start < rangeEnd-margin {
			edges = append(edges, edgeItem{y: g.Start, edgeType: "panel_end", gutterType: g.Type})
		}
		if g.End > rangeStart+margin && g.End < rangeEnd-margin {
			edges = append(edges, edgeItem{y: g.End, edgeType: "panel_start", gutterType: g.Type})
		}
	}

	var candidates []candidate

	for _, ei := range edges {
		var initialY int
		if ei.edgeType == "panel_start" {
			initialY = ei.y
		} else {
			initialY = ei.y - wmH
		}

		if initialY < rangeStart+margin || initialY+wmH > rangeEnd-margin {
			continue
		}

		score, info := AnalyzeRegionDetailed(
			gray, sat, w, h, initialY, wmH, wmW, edge, xMargin,
			bubbleMask, maskW, maskH, panelEdges, colWhite,
		)

		finalY := initialY
		adjustment := 0
		wasAdjusted := false

		needsAdjustment := (info.IsSpeechBubble || info.BubbleOverlap || info.IsFace || score < minSafeScore)

		if needsAdjustment {
			direction := 1
			if info.BubbleAtTop {
				direction = 1
			} else if info.BubbleAtBottom {
				direction = -1
			} else if ei.edgeType == "panel_start" {
				direction = 1
			} else {
				direction = -1
			}

			adjY, adjScore, adjAmt, adjInfo := findAdjustedPosition(
				gray, sat, w, h, initialY, direction, wmH, wmW,
				rangeStart, rangeEnd, margin, score, info, edge, xMargin,
				bubbleMask, maskW, maskH, panelEdges, colWhite,
			)

			penalizedScore := adjScore - (math.Abs(float64(adjAmt)) * 2.0)
			if math.Abs(float64(adjAmt)) > 40 {
				penalizedScore -= 80
			}

			if penalizedScore > score {
				finalY = adjY
				score = penalizedScore
				info = adjInfo
				adjustment = adjAmt
				wasAdjusted = true
			}
		}

		if ei.gutterType == "white" {
			score += 20
		}
		if !wasAdjusted {
			score += 20
		}

		candidates = append(candidates, candidate{
			y:          finalY,
			score:      score,
			edgeType:   ei.edgeType,
			gutterType: ei.gutterType,
			adjusted:   wasAdjusted,
			adjustment: adjustment,
			info:       info,
		})
	}

	var xPos int
	if edge == "left" {
		xPos = xMargin
	} else {
		xPos = w - xMargin - wmW
		if xPos < 0 {
			xPos = 0
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	var best *candidate
	if len(candidates) > 0 {
		best = &candidates[0]
	}

	bestUnsafe := (best == nil ||
		best.score < minSafeScore ||
		best.info.IsSpeechBubble ||
		best.info.BubbleOverlap ||
		best.info.IsFace)

	if bestUnsafe {
		scanY, scanScore := fallbackScan(
			gray, sat, w, h, rangeStart, rangeEnd, wmW, wmH, margin, edge, xMargin,
			bubbleMask, maskW, maskH, panelEdges, colWhite,
		)
		if best == nil || scanScore > best.score {
			return xPos, scanY, scanScore
		}
	}

	return xPos, best.y, best.score
}
