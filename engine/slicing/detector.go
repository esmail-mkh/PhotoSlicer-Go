package slicing

import (
	"image"
	"math"
	"sort"
)

type bandedRowAccessor struct {
	img        image.Image
	width      int
	height     int
	bandStart  int
	bandEnd    int
	bandGrays  [][]uint8
}

func newBandedRowAccessor(img image.Image) *bandedRowAccessor {
	b := img.Bounds()
	return &bandedRowAccessor{
		img:       img,
		width:     b.Dx(),
		height:    b.Dy(),
		bandStart: -1,
		bandEnd:   -1,
	}
}

func (a *bandedRowAccessor) getRow(r int) []uint8 {
	if r < 0 || r >= a.height {
		return nil
	}
	if !(a.bandStart <= r && r < a.bandEnd) {
		bStart := r - 500
		if bStart < 0 {
			bStart = 0
		}
		bEnd := r + 2000
		if bEnd > a.height {
			bEnd = a.height
		}

		bandH := bEnd - bStart
		band := make([][]uint8, bandH)
		for y := 0; y < bandH; y++ {
			row := make([]uint8, a.width)
			actualY := bStart + y
			for x := 0; x < a.width; x++ {
				c := a.img.At(x, actualY)
				r32, g32, b32, _ := c.RGBA()
				// Convert to 8-bit grayscale matching PIL's convert('L'): (r*299 + g*587 + b*114) / 1000
				gray := (uint32(r32>>8)*299 + uint32(g32>>8)*587 + uint32(b32>>8)*114) / 1000
				row[x] = uint8(gray)
			}
			band[y] = row
		}

		a.bandGrays = band
		a.bandStart = bStart
		a.bandEnd = bEnd
	}

	return a.bandGrays[r-a.bandStart]
}

func isRowUniform(accessor *bandedRowAccessor, row, width, ignorablePixels, threshold int) bool {
	rowPixels := accessor.getRow(row)
	if rowPixels == nil || len(rowPixels) <= ignorablePixels*2+1 {
		return false
	}

	span := rowPixels[ignorablePixels : width-ignorablePixels]
	n := len(span)
	if n > 8 {
		mid := n / 2
		q1 := n / 4
		q3 := 3 * n / 4

		absDiff := func(a, b uint8) int {
			if a > b {
				return int(a - b)
			}
			return int(b - a)
		}

		if absDiff(span[1], span[0]) > threshold ||
			absDiff(span[mid], span[mid-1]) > threshold ||
			absDiff(span[q1], span[q1-1]) > threshold ||
			absDiff(span[q3], span[q3-1]) > threshold {
			return false
		}
	}

	for i := 1; i < n; i++ {
		diff := int(span[i]) - int(span[i-1])
		if diff < 0 {
			diff = -diff
		}
		if diff > threshold {
			return false
		}
	}
	return true
}

// FindSafeCutPoints locates safe horizontal cut lines (e.g. gutters between panels)
// using on-demand banded window inspection and vertical neighborhood checks.
func FindSafeCutPoints(img image.Image, slicesCount float64) []int {
	b := img.Bounds()
	width := b.Dx()
	height := b.Dy()

	if height == 0 || slicesCount <= 0 {
		return nil
	}

	splitHeight := int(float64(height) / slicesCount)
	if splitHeight < 50 {
		var evenCuts []int
		for i := 1; i < int(slicesCount); i++ {
			evenCuts = append(evenCuts, int(float64(height*i)/slicesCount))
		}
		evenCuts = append(evenCuts, height)
		return evenCuts
	}

	accessor := newBandedRowAccessor(img)

	scanStep := 5
	ignorablePixels := int(float64(width) * 0.03)
	if ignorablePixels < 15 {
		ignorablePixels = 15
	}
	sensitivity := 90
	threshold := int(255.0 * (1.0 - (float64(sensitivity) / 100.0)))
	lastRow := height

	verticalCheckOffsets := []int{-25, -15, -8, 8, 15, 25}

	sliceLocations := []int{0}
	row := splitHeight
	moveUp := true

	for row < lastRow {
		canSlice := isRowUniform(accessor, row, width, ignorablePixels, threshold)

		if canSlice {
			for _, offset := range verticalCheckOffsets {
				checkRow := row + offset
				if checkRow >= 0 && checkRow < lastRow {
					if !isRowUniform(accessor, checkRow, width, ignorablePixels, threshold) {
						canSlice = false
						break
					}
				}
			}
		}

		if canSlice {
			sliceLocations = append(sliceLocations, row)
			row += splitHeight
			moveUp = true
			continue
		}

		if float64(row-sliceLocations[len(sliceLocations)-1]) <= 0.4*float64(splitHeight) {
			row = sliceLocations[len(sliceLocations)-1] + splitHeight
			moveUp = false
		}

		if moveUp {
			row -= scanStep
			if row <= sliceLocations[len(sliceLocations)-1] {
				row = sliceLocations[len(sliceLocations)-1] + scanStep
				moveUp = false
			}
			continue
		}

		row += scanStep
	}

	if sliceLocations[len(sliceLocations)-1] != lastRow {
		sliceLocations = append(sliceLocations, lastRow)
	}

	// Deduplicate & sort
	locMap := make(map[int]bool)
	var uniqueLocs []int
	for _, l := range sliceLocations {
		if !locMap[l] {
			locMap[l] = true
			uniqueLocs = append(uniqueLocs, l)
		}
	}
	sort.Ints(uniqueLocs)

	minHeight := 10
	var validatedCuts []int
	validatedCuts = append(validatedCuts, uniqueLocs[0])
	for i := 1; i < len(uniqueLocs); i++ {
		if uniqueLocs[i]-validatedCuts[len(validatedCuts)-1] >= minHeight {
			validatedCuts = append(validatedCuts, uniqueLocs[i])
		}
	}

	if validatedCuts[len(validatedCuts)-1] != height {
		validatedCuts[len(validatedCuts)-1] = height
	}

	// Fallback to even cuts if no safe cut points were found
	if len(validatedCuts) <= 2 && slicesCount > 1 {
		evenCuts := []int{0}
		for i := 1; i < int(math.Ceil(slicesCount)); i++ {
			cp := int(float64(height*i) / slicesCount)
			if cp > 0 && cp < height {
				evenCuts = append(evenCuts, cp)
			}
		}
		evenCuts = append(evenCuts, height)
		validatedCuts = evenCuts
	}

	return validatedCuts[1:]
}

// CapSliceGaps ensures the distance between two consecutive cuts does not exceed maxHeight.
func CapSliceGaps(cutPoints []int, maxHeight int) []int {
	if maxHeight <= 0 || len(cutPoints) == 0 {
		return cutPoints
	}

	capped := []int{cutPoints[0]}
	for i := 1; i < len(cutPoints); i++ {
		cp := cutPoints[i]
		prev := capped[len(capped)-1]
		for cp-prev > maxHeight {
			prev += maxHeight
			capped = append(capped, prev)
		}
		capped = append(capped, cp)
	}
	return capped
}
