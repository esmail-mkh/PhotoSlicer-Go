package watermark

import (
	"image"
	"math"
)

// Content-aware watermark placement.
//
// This is a Go port of PlacementDetector from the Manhwa Watermark Tool
// (watermark_engine.py) and follows it line by line, so the same page gives the
// same position in both tools.
//
// Rule: the watermark sits at the page edge, INSIDE a panel — just under the
// panel's top line or just over its bottom line. Free space is used only when
// the page has no panel line/edge, or every edge spot covers something important
// (bubble, line art, face).
//
// Per-pixel maps are computed once per page; every box statistic is then an O(1)
// lookup in per-row cumulative sums, so the whole page can be scanned.

const (
	detMargin         = 80   // keep away from page top/bottom (pages are slices of one strip)
	detGutterCoverage = 0.95 // row is a gutter if this share of the band is white or black
	detSlide          = 200  // max distance to move inward from a soft/faded panel edge
	detMinGutterRows  = 6
	detLineStep       = 40  // brightness step that counts as a border line / hard panel boundary
	detLineCoverage   = 0.6 // ... across this share of the band
	detSideRows       = 24  // rows looked at on each side of a line to tell panel from background
	detCoarse         = 4   // downsampling for the outline map that tells textured gutters from panel art
	detArtWin         = 600 // rows looked at on each side of a line for that
	detArtRatio       = 1.6 // a side with this many times fewer outlines is gutter background
	detArtMin         = 0.05
	detArtMinWin      = 150 // look at least this far even when the next line is closer
	detEdgeOKCost     = 1.0 // panel-edge spots are used unless they all cover something important
	detScanStep       = 4
	detWide           = 50  // wider context for outlines/faces next to the box
	detFar            = 150 // context for "a character is right here" (skin nearby)
	detPad            = 12  // also look this far around the box (bubble borders, faces just outside)

	// box may contain at most this share of gutter colour (a watermark on dark art is fine;
	// white would read as bubble/gutter)
	detClearWhite = 0.05
	detClearBlack = 0.5
)

// Pixel map flags (same bits as psdfast.dll PixelMaps).
const (
	flagWhite  = 1 << iota // gray > 235
	flagDark               // gray < 25
	flagEdge               // a drawn line / detail (plain or gamma-brightened gradient > 30)
	flagSkin               // skin-coloured (Kovac rule, minus mauve/pink: blue well above green)
	flagBubble             // neutral white (gray > 235, max-min channel <= 8): speech bubble paper
)

type mapName int

const (
	mWhite mapName = iota
	mDark
	mEdge
	mSkin
	mLum
	mSkinSat
	mBubble
)

type cumKey struct {
	name   mapName
	x0, x1 int
}

type placementDetector struct {
	g       []byte // luma
	flags   []byte
	skinSat []byte // saturation*255 of skin pixels, else 0
	W, H    int
	right   bool // box is anchored to the right page edge (page-edge side is the right one)

	cache  map[cumKey][]int64
	artCum []int64
	artW   int
	artOK  bool
}

type edgeSpot struct {
	y     int
	kind  string
	gtype string
	inset int
}

type lineSpot struct {
	y    int
	kind string
}

type span struct{ y0, y1 int }

// pixelMaps builds the per-pixel maps. mirror=true measures horizontal gradients against the
// left neighbour instead of the right one, so a page gives the mirror image of its flipped copy
// (the right-edge watermark behaves exactly like the left-edge one on the flipped page).
func pixelMaps(img image.Image, mirror bool) (g, flags, skinSat []byte, W, H int) {
	b := img.Bounds()
	W, H = b.Dx(), b.Dy()
	n := W * H
	g = make([]byte, n)
	flags = make([]byte, n)
	skinSat = make([]byte, n)
	if n == 0 {
		return
	}

	// loadRow fills buf with the RGB bytes of row y (alpha is ignored, like the original tool)
	loadRow := func(y int, buf []byte) {
		switch im := img.(type) {
		case *image.RGBA:
			off := (b.Min.Y+y-im.Rect.Min.Y)*im.Stride + (b.Min.X-im.Rect.Min.X)*4
			for x, o := 0, 0; x < W; x, o, off = x+1, o+3, off+4 {
				buf[o], buf[o+1], buf[o+2] = im.Pix[off], im.Pix[off+1], im.Pix[off+2]
			}
		case *image.NRGBA:
			off := (b.Min.Y+y-im.Rect.Min.Y)*im.Stride + (b.Min.X-im.Rect.Min.X)*4
			for x, o := 0, 0; x < W; x, o, off = x+1, o+3, off+4 {
				buf[o], buf[o+1], buf[o+2] = im.Pix[off], im.Pix[off+1], im.Pix[off+2]
			}
		default:
			for x, o := 0, 0; x < W; x, o = x+1, o+3 {
				r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				buf[o], buf[o+1], buf[o+2] = byte(r>>8), byte(gg>>8), byte(bb>>8)
			}
		}
	}

	var gamma [256]int
	for v := range gamma {
		gamma[v] = int(math.Sqrt(float64(v)/255.0) * 255.0)
	}
	abs := func(a int) int {
		if a < 0 {
			return -a
		}
		return a
	}

	buf := make([]byte, W*3)
	for y := 0; y < H; y++ {
		loadRow(y, buf)
		row := g[y*W : (y+1)*W]
		for x := range row {
			row[x] = byte((int(buf[x*3])*77 + int(buf[x*3+1])*150 + int(buf[x*3+2])*29) >> 8)
		}
	}
	for y := 0; y < H; y++ {
		loadRow(y, buf)
		for x := 0; x < W; x++ {
			i := y*W + x
			v := int(g[i])
			// gradient = max(|down - here|, |right - here|)
			gp, gg := 0, 0
			if y+1 < H {
				d := int(g[i+W])
				gp, gg = abs(d-v), abs(gamma[d]-gamma[v])
			}
			if nx := x + 1 - 2*b2i(mirror); nx >= 0 && nx < W {
				d := int(g[y*W+nx])
				if a := abs(d - v); a > gp {
					gp = a
				}
				if a := abs(gamma[d] - gamma[v]); a > gg {
					gg = a
				}
			}
			r, gr, bl := int(buf[x*3]), int(buf[x*3+1]), int(buf[x*3+2])
			mx, mn := r, r
			if gr > mx {
				mx = gr
			}
			if bl > mx {
				mx = bl
			}
			if gr < mn {
				mn = gr
			}
			if bl < mn {
				mn = bl
			}
			var f byte
			if v > 235 {
				f |= flagWhite
				if mx-mn <= 8 {
					f |= flagBubble
				}
			}
			if v < 25 {
				f |= flagDark
			}
			if gp > 30 || gg > 30 {
				f |= flagEdge
			}
			if r > 95 && gr > 40 && bl > 20 && mx-mn > 15 && r-gr > 12 && r > bl && v <= 235 && bl-gr <= 15 {
				f |= flagSkin
				skinSat[i] = byte(float32(mx-mn) / float32(mx) * 255)
			}
			flags[i] = f
		}
	}
	return
}

func newPlacementDetector(img image.Image, right bool) *placementDetector {
	g, flags, skinSat, w, h := pixelMaps(img, right)
	return &placementDetector{
		g: g, flags: flags, skinSat: skinSat, W: w, H: h, right: right,
		cache: make(map[cumKey][]int64),
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// pySlice resolves a Python-style [start:stop] slice on a sequence of n items
// (negative indices count from the end), like the original tool does.
func pySlice(n, start, stop int) (int, int) {
	if start < 0 {
		start += n
		if start < 0 {
			start = 0
		}
	} else if start > n {
		start = n
	}
	if stop < 0 {
		stop += n
		if stop < 0 {
			stop = 0
		}
	} else if stop > n {
		stop = n
	}
	if stop < start {
		stop = start
	}
	return start, stop
}

// cum returns the running per-row total of a map over columns [x0, x1): index r holds
// the sum over rows [0, r).
func (d *placementDetector) cum(name mapName, x0, x1 int) []int64 {
	x0, x1 = clampInt(x0, 0, d.W), clampInt(x1, 0, d.W)
	key := cumKey{name, x0, x1}
	if c, ok := d.cache[key]; ok {
		return c
	}
	c := make([]int64, d.H+1)
	W := d.W
	var mask byte
	switch name {
	case mWhite:
		mask = flagWhite
	case mDark:
		mask = flagDark
	case mEdge:
		mask = flagEdge
	case mSkin:
		mask = flagSkin
	case mBubble:
		mask = flagBubble
	}
	for y := 0; y < d.H; y++ {
		var s int64
		if x1 > x0 {
			row := y * W
			switch name {
			case mLum:
				for _, v := range d.g[row+x0 : row+x1] {
					s += int64(v)
				}
			case mSkinSat:
				for _, v := range d.skinSat[row+x0 : row+x1] {
					s += int64(v)
				}
			default:
				for _, v := range d.flags[row+x0 : row+x1] {
					s += int64(v & mask)
				}
			}
		}
		if mask > 1 {
			// single-bit mask: count set bits, not the bit value
			s /= int64(mask)
		}
		c[y+1] = c[y] + s
	}
	d.cache[key] = c
	return c
}

// frac is the share of `name` pixels (mean value for lum / skinSat) in the box.
func (d *placementDetector) frac(name mapName, x0, x1, y0, y1 int) float64 {
	x0, x1 = maxInt(0, x0), minInt(d.W, x1)
	y0, y1 = maxInt(0, y0), minInt(d.H, y1)
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	c := d.cum(name, x0, x1)
	return float64(c[y1]-c[y0]) / float64((x1-x0)*(y1-y0))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// cost: 0 = flat background, higher = more important content under the box.
// flush 't'/'b': box touches a panel edge there, so don't look across it (the border
// line and the gutter behind it are not content). 0 = neither.
func (d *placementDetector) cost(x0, w, y, h int, flush byte) float64 {
	// inset panel: a gutter-coloured strip at the page edge under the box is not content
	lo, hi := -d.W, 2*d.W
	if d.touchesEdge(x0, w) {
		if m := d.margin(y, h, w/3); m > 0 {
			if d.right {
				w -= m
				hi = x0 + w // context doesn't reach into the strip
			} else {
				x0, w = x0+m, w-m
				lo = x0
			}
		}
	}
	xa, xb := maxInt(lo, x0-detPad), minInt(hi, x0+w+detPad)
	ya, yb := y-detPad, y+h+detPad
	if flush == 't' {
		ya = y
	}
	if flush == 'b' {
		yb = y + h - 1
	}
	white := d.frac(mBubble, x0, x0+w, y, y+h)
	whiteNear := d.frac(mBubble, xa, xb, ya, yb)
	edge := d.frac(mEdge, xa, xb, ya, yb)
	skin := d.frac(mSkin, xa, xb, ya, yb)
	// wider context: a face/figure outline just beside the box still counts
	w2 := detWide
	wa, wb := y-w2, y+h+w2
	if flush == 't' {
		wa = y
	}
	if flush == 'b' {
		wb = y + h - 1
	}
	wxa, wxb := maxInt(lo, x0-w2), minInt(hi, x0+w+w2)
	edgeWide := d.frac(mEdge, wxa, wxb, wa, wb)
	whiteWide := d.frac(mBubble, wxa, wxb, wa, wb) // a bubble just beside
	// skin anywhere nearby (arm, face) means the flat area under the box is likely clothing
	fa, fb := y-detFar, y+h+detFar
	if flush == 't' {
		fa = y
	}
	if flush == 'b' {
		fb = y + h - 1
	}
	fxa, fxb := maxInt(lo, x0-detFar), minInt(hi, x0+w+detFar)
	skinNear := d.frac(mSkin, fxa, fxb, fa, fb)
	// warm backgrounds look like skin too: skin counts fully only with line work around it (a face),
	// and when nearly everything around is saturated "skin" it is a warm background (bokeh, sunset);
	// a face close-up is also all skin but paler (saturation ~0.15-0.2)
	warmBg := skinNear > 0.7 && d.frac(mSkinSat, fxa, fxb, fa, fb)/skinNear > 0.27*255
	faceK := 0.3
	if warmBg {
		faceK = 0.0
	}
	face := skin * math.Min(1.0, faceK+edgeWide*8)
	if warmBg {
		skinNear = 0.0
	}
	// white inside a panel = speech bubble / narration box; lines = characters, text, objects
	// object boundary inside the box (half dark hair / half light background): block means differ.
	// Fine texture (bookshelf, bokeh) keeps block means similar, so it isn't penalized.
	minM, maxM := math.Inf(1), math.Inf(-1)
	for i := 0; i < 2; i++ {
		for j := 0; j < 4; j++ {
			bxa, bxb := d.split(x0, w, j, 4)
			m := d.frac(mLum, bxa, bxb, y+h*i/2, y+h*(i+1)/2)
			minM = math.Min(minM, m)
			maxM = math.Max(maxM, m)
		}
	}
	spread := (maxM - minM) / 255
	c := 2.0*white + 1.0*whiteNear + 1.0*whiteWide + 3.0*edge + 1.5*edgeWide + 1.5*face + 1.5*spread + 1.0*skinNear
	// big boxes: an average hides one bubble or a gutter strip, so check blocks
	ny, nx := -floorDiv(-h, 160), -floorDiv(-w, 240)
	if ny*nx > 1 {
		worst := 0.0
		for i := 0; i < ny; i++ {
			for j := 0; j < nx; j++ {
				bx0, bx1 := d.split(x0, w, j, nx)
				by0, by1 := y+h*i/ny, y+h*(i+1)/ny
				worst = math.Max(worst, d.frac(mWhite, bx0, bx1, by0, by1))
				worst = math.Max(worst, d.frac(mDark, bx0, bx1, by0, by1)-0.3)
				worst = math.Max(worst, 3.0*d.frac(mEdge, bx0, bx1, by0, by1)) // text / dense line art
				worst = math.Max(worst, d.frac(mSkin, bx0, bx1, by0, by1))     // a face
			}
		}
		c += 2.0 * worst
	}
	return c
}

// split returns columns of block j of n equal blocks across [x0, x0+w), counted from the
// page-edge side of the box.
func (d *placementDetector) split(x0, w, j, n int) (int, int) {
	if d.right {
		return x0 + w - w*(j+1)/n, x0 + w - w*j/n
	}
	return x0 + w*j/n, x0 + w*(j+1)/n
}

// touchesEdge: the box sits against the page edge it is anchored to.
func (d *placementDetector) touchesEdge(x0, w int) bool {
	if d.right {
		return x0+w == d.W
	}
	return x0 == 0
}

// art is the share of object outlines in rows [y0, y1), full width, at 1/detCoarse resolution:
// outlines survive the downsampling, fine texture (grain, halftone, sparkle) and gradients don't.
func (d *placementDetector) art(y0, y1 int) float64 {
	k := detCoarse
	if !d.artOK {
		hh, ww := d.H/k, d.W/k
		xoff := 0
		if d.right {
			xoff = d.W - ww*k // block grid starts at the page-edge side
		}
		s := make([]float64, hh*ww)
		for y := 0; y < hh; y++ {
			for x := 0; x < ww; x++ {
				var sum int
				for dy := 0; dy < k; dy++ {
					row := (y*k+dy)*d.W + xoff + x*k
					for dx := 0; dx < k; dx++ {
						sum += int(d.g[row+dx])
					}
				}
				s[y*ww+x] = float64(sum) / float64(k*k)
			}
		}
		rows := maxInt(hh-1, 0)
		cols := maxInt(ww-1, 0)
		d.artCum = make([]int64, rows+1)
		for y := 0; y < rows; y++ {
			var cnt int64
			for c := 0; c < cols; c++ {
				// block c against the one below it and the next one towards the page centre
				// (the mirror image of that when anchored at the right edge)
				x, nx := c, c+1
				if d.right {
					x, nx = ww-1-c, ww-2-c
				}
				a := s[y*ww+x]
				if math.Abs(s[(y+1)*ww+x]-a) > 25 || math.Abs(s[y*ww+nx]-a) > 25 {
					cnt++
				}
			}
			d.artCum[y+1] = d.artCum[y] + cnt
		}
		d.artW = maxInt(1, cols)
		d.artOK = true
	}
	a, b := maxInt(0, floorDiv(y0, k)), minInt(len(d.artCum)-1, floorDiv(y1, k))
	if b > a {
		return float64(d.artCum[b]-d.artCum[a]) / float64(d.artW*(b-a))
	}
	return 0.0
}

// margin is the width of a flat black/white strip at the page edge over rows [y, y+h), up to limit.
func (d *placementDetector) margin(y, h, limit int) int {
	y0, y1 := maxInt(0, y), minInt(d.H, y+h)
	n := y1 - y0
	if n <= 0 || limit <= 0 {
		return 0
	}
	limit = minInt(limit, d.W)
	for _, mask := range [2]byte{flagDark, flagWhite} {
		colOK := func(i int) bool {
			x := i
			if d.right {
				x = d.W - 1 - i
			}
			var cnt int
			for r := y0; r < y1; r++ {
				if d.flags[r*d.W+x]&mask != 0 {
					cnt++
				}
			}
			return float64(cnt)/float64(n) >= 0.95
		}
		if !colOK(0) {
			continue
		}
		for i := 1; i < limit; i++ {
			if !colOK(i) {
				return i
			}
		}
		return limit
	}
	return 0
}

// gutterRows marks rows that are gutters over the band [x0, x0+w): near-uniform white across
// the band, or black across the whole page.
func (d *placementDetector) gutterRows(x0, w int) (isGutter, isWhite []bool) {
	n := minInt(d.W, x0+w) - maxInt(0, x0)
	wr := d.cum(mWhite, x0, x0+w)
	dr := d.cum(mDark, x0, x0+w)
	full := d.cum(mDark, 0, d.W)
	isGutter = make([]bool, d.H)
	isWhite = make([]bool, d.H)
	for y := 0; y < d.H; y++ {
		white := float64(wr[y+1]-wr[y]) >= detGutterCoverage*float64(n)
		// black gutters span the page; black only inside the band is dark hair/clothes
		dark := float64(dr[y+1]-dr[y]) >= detGutterCoverage*float64(n) &&
			float64(full[y+1]-full[y]) >= 0.9*float64(d.W)
		isWhite[y] = white
		isGutter[y] = white || dark
	}
	return
}

// boxFracAll is the share of `name` pixels in the box for every top y.
func (d *placementDetector) boxFracAll(name mapName, x0, w, h int) []float64 {
	c := d.cum(name, x0, x0+w)
	out := make([]float64, maxInt(d.H-h+1, 0))
	for t := range out {
		out[t] = float64(c[t+h]-c[t]) / float64(w*h)
	}
	return out
}

// edgeSpots lists boxes against panel top/bottom edges within the band. Panels often fade into
// the gutter, so from each transition the box slides inward (up to detSlide px) to the first
// spot that is clear of gutter colour.
func (d *placementDetector) edgeSpots(x0, w, h int) ([]edgeSpot, []bool) {
	isGutter, isWhite := d.gutterRows(x0, w)
	nBox := d.H - h + 1
	if nBox <= 0 {
		return nil, isGutter
	}
	boxGut := map[string][]float64{
		"white": d.boxFracAll(mWhite, x0, w, h),
		"black": d.boxFracAll(mDark, x0, w, h),
	}
	clear := map[string]float64{"white": detClearWhite, "black": detClearBlack}

	// vertical speed lines fading into the gutter are an effect over the gutter, not panel art:
	// strong left-right changes, almost none top-bottom
	bx0, bx1 := maxInt(0, x0), minInt(d.W, x0+w)
	bw := bx1 - bx0
	dx := make([]int64, d.H+1)
	dy := make([]int64, d.H+1) // dy[H] stays 0, like the original's trailing 0
	flat := make([]int, d.H+1)
	for y := 0; y < d.H; y++ {
		row := d.g[y*d.W+bx0 : y*d.W+bx1]
		var sx int64
		var sum, sumsq int64
		for x, v := range row {
			iv := int64(v)
			sum += iv
			sumsq += iv * iv
			if x > 0 {
				df := iv - int64(row[x-1])
				if df < 0 {
					df = -df
				}
				sx += df
			}
		}
		dx[y+1] = dx[y] + sx
		isFlat := 0
		if bw > 0 && float64(int64(bw)*sumsq-sum*sum) < 16.0*float64(bw)*float64(bw) {
			isFlat = 1
		}
		flat[y+1] = flat[y] + isFlat
	}
	for y := 0; y+1 < d.H; y++ {
		var sy int64
		r0, r1 := d.g[y*d.W+bx0:y*d.W+bx1], d.g[(y+1)*d.W+bx0:(y+1)*d.W+bx1]
		for x := range r0 {
			df := int64(r1[x]) - int64(r0[x])
			if df < 0 {
				df = -df
			}
			sy += df
		}
		dy[y+1] = dy[y] + sy
	}
	// dy[i] is the running total of vertical changes above row i (i = 1..H-1), dy[H] = 0
	streak := make([]bool, nBox)
	fade := make([]bool, nBox)
	for t := 0; t < nBox; t++ {
		DX, DY := dx[t+h]-dx[t], dy[t+h]-dy[t]
		streak[t] = float64(DX) > float64(3*h*bw) && float64(DY) < 0.2*float64(DX)
		// sliding inward through a plain gradient (flat rows) stays in the gutter, it's not a faded panel
		fade[t] = streak[t] || float64(flat[t+h]-flat[t]) >= 0.8*float64(h)
	}

	var spots []edgeSpot
	// runs of non-gutter rows [s, e): panels
	y := 0
	for y < d.H {
		if isGutter[y] {
			y++
			continue
		}
		s := y
		for y < d.H && !isGutter[y] {
			y++
		}
		e := y
		if e-s < h {
			continue
		}
		if s >= detMinGutterRows && allTrue(isGutter[s-detMinGutterRows:s]) {
			gtype := "black"
			if isWhite[s-1] {
				gtype = "white"
			}
			bg := boxGut[gtype]
			hiIdx := minInt(s+detSlide, e-h)
			n := hiIdx - s + 1
			for k := 0; k < n; k++ {
				bad := fade[s+k]
				if k == 0 {
					bad = streak[s]
				}
				if bg[s+k] <= clear[gtype] && !bad {
					spots = append(spots, edgeSpot{s + k, "panel_start", gtype, k})
					break
				}
			}
		}
		if e+detMinGutterRows <= d.H && allTrue(isGutter[e:e+detMinGutterRows]) {
			gtype := "black"
			if isWhite[e] {
				gtype = "white"
			}
			bg := boxGut[gtype]
			lo := maxInt(s, e-h-detSlide)
			n := e - h + 1 - lo
			for k := n - 1; k >= 0; k-- {
				bad := fade[lo+k]
				if k == n-1 {
					bad = streak[lo+k]
				}
				if bg[lo+k] <= clear[gtype] && !bad {
					spots = append(spots, edgeSpot{lo + k, "panel_end", gtype, e - h - lo - k})
					break
				}
			}
		}
	}
	return spots, isGutter
}

func allTrue(b []bool) bool {
	for _, v := range b {
		if !v {
			return false
		}
	}
	return true
}

// lineSpots lists boxes flush against drawn border lines / hard boundaries in the band: rows where
// the brightness jumps sharply across most of the band. It also returns the textured-gutter ranges
// found next to those lines, and the boundary rows.
func (d *placementDetector) lineSpots(x0, w, h int) ([]lineSpot, []span, []int) {
	bx0, bx1 := maxInt(0, x0), minInt(d.W, x0+w)
	bw := bx1 - bx0
	if bw <= 0 || d.H < 2 {
		return nil, nil, nil
	}
	// the line must reach the page edge side of the band too (panel touches the page edge, not an inset panel)
	q := maxInt(1, bw/4)
	var b []int // boundary rows b|b+1
	for y := 0; y+1 < d.H; y++ {
		r0, r1 := d.g[y*d.W+bx0:y*d.W+bx1], d.g[(y+1)*d.W+bx0:(y+1)*d.W+bx1]
		var all, edgeSide int
		for x := 0; x < bw; x++ {
			df := int(r1[x]) - int(r0[x])
			if df < 0 {
				df = -df
			}
			if df >= detLineStep {
				all++
				if (!d.right && x < q) || (d.right && x >= bw-q) {
					edgeSide++
				}
			}
		}
		if float64(all)/float64(bw) >= detLineCoverage && float64(edgeSide)/float64(q) >= 0.5 {
			b = append(b, y)
		}
	}
	if len(b) == 0 {
		return nil, nil, b
	}

	// mean of rows [r0, r1) of the band, all pixels; ok=false when empty
	rowsMean := func(r0, r1 int) (float64, bool) {
		var sum int64
		var cnt int
		for y := r0; y < r1; y++ {
			for _, v := range d.g[y*d.W+bx0 : y*d.W+bx1] {
				sum += int64(v)
			}
			cnt += bw
		}
		if cnt == 0 {
			return 0, false
		}
		return float64(sum) / float64(cnt), true
	}
	rowFlat := func(y int) bool {
		var sum, sumsq int64
		for _, v := range d.g[y*d.W+bx0 : y*d.W+bx1] {
			iv := int64(v)
			sum += iv
			sumsq += iv * iv
		}
		return float64(int64(bw)*sumsq-sum*sum) < 16.0*float64(bw)*float64(bw)
	}

	// 'white'/'black' = flat page background (outside a panel), 'flat' = flat mid-tone
	// (usually a gradient gutter), 'art' = panel content.
	side := func(r0, r1 int) string {
		r0, r1 = pySlice(d.H, r0, r1)
		if r1 <= r0 {
			return "none"
		}
		flatRows := 0
		for y := r0; y < r1; y++ {
			if rowFlat(y) {
				flatRows++
			}
		}
		if float64(flatRows)/float64(r1-r0) >= 0.8 {
			m, _ := rowsMean(r0, r1)
			if m >= 240 {
				return "white"
			}
			if m <= 15 {
				return "black"
			}
			return "flat"
		}
		return "art"
	}
	// brightness keeps changing: a gradient
	fade := func(r0, r1 int) bool {
		a0, a1 := pySlice(d.H, maxInt(0, r0), r0+24)
		z0, z1 := pySlice(d.H, maxInt(0, r1-24), r1)
		am, aok := rowsMean(a0, a1)
		zm, zok := rowsMean(z0, z1)
		if !aok || !zok { // window runs off the page top/bottom
			return false
		}
		diff := int(am) - int(zm)
		if diff < 0 {
			diff = -diff
		}
		return diff > 15
	}
	isFlatBg := func(s string) bool { return s == "white" || s == "black" }
	isPanel := func(s string) bool { return s == "art" || s == "flat" }

	// a thin line gives two boundaries a few rows apart: treat them as one line
	type group struct{ top, bot int }
	var groups []group
	start := 0
	for i := 1; i <= len(b); i++ {
		if i == len(b) || b[i]-b[i-1] > 4 {
			groups = append(groups, group{b[start] + 1, b[i-1] + 1})
			start = i
		}
	}

	var spots []lineSpot
	var bg []span
	for i, gr := range groups { // line rows: [top, bot)
		top, bot := gr.top, gr.bot
		above := side(maxInt(0, top-detSideRows), top)
		below := side(bot+1, bot+1+detSideRows)
		// textured/gradient gutter (paper grain, halftone, sparkle dust, fade) vs panel art:
		// the gutter side has far fewer object outlines. Look up to the neighbouring line.
		prevBot := 0
		if i > 0 {
			prevBot = groups[i-1].bot
		}
		nextTop := d.H
		if i+1 < len(groups) {
			nextTop = groups[i+1].top
		}
		ra := minInt(detArtWin, maxInt(detArtMinWin, top-prevBot))
		rz := minInt(detArtWin, maxInt(detArtMinWin, nextTop-bot))
		a, z := d.art(top-ra, top-4), d.art(bot+5, bot+rz)
		sure := math.Max(a, z) > detArtMin
		busyAbove := sure && z*detArtRatio < a
		busyBelow := sure && a*detArtRatio < z
		if isPanel(above) && isPanel(below) {
			if busyBelow {
				above = "white"
				bg = append(bg, span{top - ra, top})
			} else if busyAbove {
				below = "white"
				bg = append(bg, span{bot, bot + rz})
			}
		}
		// a flat mid-tone fading next to a flat gutter/box is a gradient gutter, unless it clearly has art
		if above == "flat" && isFlatBg(below) && !busyAbove && fade(top-detArtMinWin, top) {
			above = "white"
		}
		if below == "flat" && isFlatBg(above) && !busyBelow && fade(bot+1, bot+detArtMinWin) {
			below = "white"
		}
		// the box goes inside the panel: under a panel's top line / over its bottom line.
		// White is always page background; flat black is background unless the other side
		// is white (then it's the dark bottom/top of a panel).
		if isPanel(below) || (below == "black" && above == "white") {
			spots = append(spots, lineSpot{bot, "line_below"}) // box top just under the line
		}
		if isPanel(above) || (above == "black" && below == "white") {
			spots = append(spots, lineSpot{top - h, "line_above"}) // box bottom just over the line
		}
	}
	return spots, bg, b
}

type found struct {
	y    int
	cost float64
	info string
}

// find returns the best y for a w*h box anchored at column x0, keeping to rows [rangeStart, rangeEnd).
func (d *placementDetector) find(x0, w, h, rangeStart, rangeEnd int) (found, bool) {
	lo := maxInt(detMargin, rangeStart)
	hi := minInt(d.H-detMargin-h, rangeEnd-h)
	if d.H == 0 || w <= 0 || h <= 0 || h > d.H {
		return found{}, false
	}
	allowed := func(y int) bool { return y >= lo && y <= hi }

	spots, isGutter := d.edgeSpots(x0, w, h)
	lines, bg, lineRows := d.lineSpots(x0, w, h)
	var best *found
	for _, sp := range spots {
		// the panel faded into the gutter, but a border line within slide range is the real
		// panel edge (a line spot); the faded strip between them is still outside the panel
		var g0, ya, yb int
		if sp.kind == "panel_start" {
			g0 = sp.y - sp.inset // gutter transition
			ya, yb = g0, g0+detSlide+h
		} else {
			g0 = sp.y + h + sp.inset
			ya, yb = g0-detSlide-h, g0
		}
		if sp.inset != 0 {
			skip := false
			for _, r := range lineRows {
				if ya <= r && r <= yb {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		inBg := false
		for _, s := range bg { // inside a textured gutter
			if s.y0 <= sp.y && sp.y+h <= s.y1 {
				inBg = true
				break
			}
		}
		if inBg || !allowed(sp.y) {
			continue
		}
		var flush byte
		if sp.inset == 0 {
			flush = 'b'
			if sp.kind == "panel_start" {
				flush = 't'
			}
		}
		// slid into a fade: less exact than a drawn line, and the further, the less sure it is panel
		// (+0.03: on a tie a drawn border line is the more exact edge)
		c := d.cost(x0, w, sp.y, h, flush) + 0.7*float64(sp.inset)/detSlide + 0.03
		if best == nil || c < best.cost {
			info := sp.kind + "(" + sp.gtype + ")"
			if sp.inset != 0 {
				info += "[" + itoa(sp.inset) + "px]"
			}
			best = &found{sp.y, c, info}
		}
	}
	for _, ln := range lines {
		if allowed(ln.y) {
			flush := byte('b')
			if ln.kind == "line_below" {
				flush = 't'
			}
			c := d.cost(x0, w, ln.y, h, flush)
			if best == nil || c < best.cost {
				best = &found{ln.y, c, ln.kind}
			}
		}
	}
	if best != nil && best.cost <= detEdgeOKCost {
		return *best, true
	}

	// free scan over content: box must not straddle a gutter (flat, or textured next to a panel line)
	gut := make([]bool, len(isGutter))
	copy(gut, isGutter)
	for _, s := range bg {
		for y := maxInt(0, s.y0); y < minInt(len(gut), maxInt(0, s.y1)); y++ {
			gut[y] = true
		}
	}
	gcum := make([]int, len(gut)+1)
	for i, v := range gut {
		gcum[i+1] = gcum[i]
		if v {
			gcum[i+1]++
		}
	}
	for y := maxInt(lo, 0); y <= hi; y += detScanStep {
		if float64(gcum[y+h]-gcum[y]) > float64(h)*0.1 || !allowed(y) {
			continue
		}
		c := d.cost(x0, w, y, h, 0) + 0.1 // small bias toward real edges
		if best == nil || c < best.cost {
			best = &found{y, c, "scan"}
		}
	}
	if best == nil {
		return found{}, false
	}
	return *best, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func placementScore(cost float64) float64 {
	return math.Round(1000*(1-cost)) / 10
}
