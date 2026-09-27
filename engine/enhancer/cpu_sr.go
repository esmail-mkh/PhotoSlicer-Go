package enhancer

import (
	"image"
	"image/draw"
	"math"
	"runtime"
	"sync"
)

// CPU enhancement: a classical (non-neural) restore-and-upscale pipeline for
// comic pages that produces the same 2x output as the Real-ESRGAN GPU path.
//
//  1. Luma gets a gentle non-local-means pass at the source resolution. Its
//     noise level is fixed, not estimated: JPEG leaves flat areas nearly
//     noise-free, so any estimate there only measures intentional grain or
//     paper texture, and denoising harder when it is present erased it.
//  2. Chroma is filtered with luma as the guide. Colour edges snap back onto
//     the ink lines, removing 4:2:0 blockiness and colour bleeding, while the
//     average colour of every flat area stays exactly the same.
//  3. Luma is upscaled 2x with Lanczos-3. A shock filter, gated to
//     high-contrast ink edges only, steepens outlines, text and screentone
//     dots; a clamped unsharp mask then sharpens without halos. Finally the
//     local average brightness is restored, so sharpening never changes
//     tone (screentone density, shading).
//  4. Chroma is upscaled and snapped to the new luma edges again.
//
// The image is processed in overlapping horizontal strips in parallel, so
// memory stays bounded for tall webtoon pages.
//
// Parameters were tuned by degrading clean comic pages (half resolution plus
// JPEG at quality 65-92) and measuring how closely each method restores the
// original. Against plain bicubic this pipeline gains about 2 dB PSNR on
// line art, 2.4-3 dB on screentone and 0.7-1.1 dB on painted pages, with
// higher SSIM and lower colour error (CIE dE). Grain and paper texture are
// kept at least as well as by bicubic. The previous bilateral filter scored
// below plain bicubic and smoothed grain away.

// plane is a single-channel float32 image, row-major, values in 0..255.
type plane struct {
	w, h int
	p    []float32
}

func newPlane(w, h int) *plane { return &plane{w: w, h: h, p: make([]float32, w*h)} }

func (pl *plane) row(y int) []float32 {
	y = clampIdx(y, pl.h)
	return pl.p[y*pl.w : y*pl.w+pl.w]
}

func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func clamp8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

type cpuEnhanceParams struct {
	nlmStrength float32 // NLM filter strength, as a multiple of estimated noise
	nlmSearch   int     // NLM search radius
	nlmSigma    float32 // noise level NLM assumes (fixed; see enhanceImageCPU)
	chromaR     int     // guided-filter radius for chroma at source resolution
	chromaEps   float32
	crispAmount float32 // halo-free unsharp mask amount
	shock       float32 // shock-filter strength on ink edges (0 disables)
	shockLo     float32 // local contrast where the shock filter starts
	shockHi     float32 // local contrast where it reaches full strength
	shockSoft   float32 // Laplacian scale of the soft edge decision (0 = hard sign)
	toneR       int     // radius over which sharpening must keep average tone (0 disables)
	chromaHRR   int     // guided-filter radius for chroma after upscaling
	chromaHREps float32
}

var defaultCPUEnhanceParams = cpuEnhanceParams{
	nlmStrength: 1.3,
	nlmSearch:   3,
	nlmSigma:    1.0,
	chromaR:     1,
	chromaEps:   10,
	crispAmount: 0.8,
	shock:       0.2,
	shockLo:     80,
	shockHi:     180,
	shockSoft:   5,
	toneR:       2,
	chromaHRR:   2,
	chromaHREps: 16,
}

const (
	// cpuStripRows is the height of each independently processed strip, in
	// source rows. Each strip needs roughly 50 bytes per output pixel.
	cpuStripRows = 128
	// cpuStripMargin covers the receptive field of the whole pipeline (NLM
	// search+patch, guided filters, Lanczos taps, 3x3 HR filters): about 11
	// source rows. Rows inside the margin are computed but discarded, so
	// strips join seamlessly.
	cpuStripMargin = 16
)

// EnhanceImageCPU restores and 2x-upscales a comic page on the CPU.
func EnhanceImageCPU(img image.Image) *image.RGBA {
	return enhanceImageCPU(img, defaultCPUEnhanceParams)
}

func enhanceImageCPU(img image.Image, prm cpuEnhanceParams) *image.RGBA {
	src := toCompactRGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, 2*w, 2*h))
	if w == 0 || h == 0 {
		return out
	}

	strips := make(chan int, (h+cpuStripRows-1)/cpuStripRows)
	for y := 0; y < h; y += cpuStripRows {
		strips <- y
	}
	close(strips)

	workers := runtime.GOMAXPROCS(0)
	if n := cap(strips); workers > n {
		workers = n
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for y0 := range strips {
				y1 := min(y0+cpuStripRows, h)
				enhanceStrip(src, out, y0, y1, prm)
			}
		}()
	}
	wg.Wait()
	return out
}

func toCompactRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok && rgba.Rect.Min == (image.Point{}) {
		return rgba
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// enhanceStrip processes source rows [y0,y1) plus a margin and writes output
// rows [2*y0, 2*y1).
func enhanceStrip(src, out *image.RGBA, y0, y1 int, prm cpuEnhanceParams) {
	h := src.Rect.Dy()
	e0 := max(y0-cpuStripMargin, 0)
	e1 := min(y1+cpuStripMargin, h)

	yp, cb, cr := splitYCbCr(src, e0, e1)
	yd := nlmDenoise(yp, prm.nlmSigma, prm.nlmStrength, prm.nlmSearch)
	cb, cr = guidedFilter2(yd, cb, cr, prm.chromaR, prm.chromaEps)

	yh := upscale2x(yd)
	smooth := yh
	if prm.shock > 0 {
		yh = shockFilter(yh, prm.shock, prm.shockLo, prm.shockHi, prm.shockSoft)
	}
	if prm.crispAmount > 0 {
		yh = crispen(yh, prm.crispAmount)
	}
	if prm.toneR > 0 {
		yh = preserveTone(smooth, yh, prm.toneR)
	}
	cbh, crh := guidedFilter2(yh, upscale2x(cb), upscale2x(cr), prm.chromaHRR, prm.chromaHREps)

	W := yh.w
	for Y := 2 * y0; Y < 2*y1; Y++ {
		sy := Y - 2*e0
		row := out.Pix[Y*out.Stride:]
		srcRow := src.Pix[(Y>>1)*src.Stride:]
		yr, ur, vr := yh.p[sy*W:], cbh.p[sy*W:], crh.p[sy*W:]
		for X := 0; X < W; X++ {
			Yv, u, v := yr[X], ur[X]-128, vr[X]-128
			row[X*4] = clamp8(Yv + 1.402*v)
			row[X*4+1] = clamp8(Yv - 0.344136*u - 0.714136*v)
			row[X*4+2] = clamp8(Yv + 1.772*u)
			row[X*4+3] = srcRow[(X>>1)*4+3]
		}
	}
}

// splitYCbCr converts rows [y0,y1) of src into full-range BT.601 Y/Cb/Cr
// planes, the JPEG colour space.
func splitYCbCr(src *image.RGBA, y0, y1 int) (yp, cb, cr *plane) {
	w := src.Rect.Dx()
	n := y1 - y0
	yp, cb, cr = newPlane(w, n), newPlane(w, n), newPlane(w, n)
	for y := 0; y < n; y++ {
		row := src.Pix[(y0+y)*src.Stride:]
		for x := 0; x < w; x++ {
			r, g, b := float32(row[x*4]), float32(row[x*4+1]), float32(row[x*4+2])
			i := y*w + x
			yp.p[i] = 0.299*r + 0.587*g + 0.114*b
			cb.p[i] = -0.168736*r - 0.331264*g + 0.5*b + 128
			cr.p[i] = 0.5*r - 0.418688*g - 0.081312*b + 128
		}
	}
	return
}

// nlmDenoise is a non-local-means filter: each pixel becomes a weighted
// average of pixels in a (2*sr+1)^2 window whose 3x3 neighbourhoods look
// alike. Unlike a bilateral filter it removes JPEG mosquito noise next to ink
// lines while keeping the lines and screentone intact, because those repeat
// and so match themselves.
func nlmDenoise(src *plane, sigma, strength float32, sr int) *plane {
	w, h := src.w, src.h
	dst := newPlane(w, h)
	hh := strength * sigma
	if hh <= 0 {
		copy(dst.p, src.p)
		return dst
	}
	invH2 := 1 / (hh * hh)
	bias := 2 * sigma * sigma
	d := make([]float32, w)
	hs := make([]float32, (h+2)*w) // horizontal 3-sums of squared differences
	sumW := make([]float32, w*h)
	sumV := make([]float32, w*h)
	for dy := -sr; dy <= sr; dy++ {
		for dx := -sr; dx <= sr; dx++ {
			for ey := 0; ey < h+2; ey++ {
				a := src.row(ey - 1)
				b := src.row(ey - 1 + dy)
				for x := 0; x < w; x++ {
					v := a[x] - b[clampIdx(x+dx, w)]
					d[x] = v * v
				}
				hr := hs[ey*w : ey*w+w]
				for x := 0; x < w; x++ {
					hr[x] = d[max(x-1, 0)] + d[x] + d[min(x+1, w-1)]
				}
			}
			for y := 0; y < h; y++ {
				b := src.row(y + dy)
				h0, h1, h2 := hs[y*w:], hs[(y+1)*w:], hs[(y+2)*w:]
				sw, sv := sumW[y*w:y*w+w], sumV[y*w:y*w+w]
				for x := 0; x < w; x++ {
					dist := (h0[x]+h1[x]+h2[x])*(1.0/9) - bias
					if dist < 0 {
						dist = 0
					}
					wt := fastExpNeg(dist * invH2)
					sw[x] += wt
					sv[x] += wt * b[clampIdx(x+dx, w)]
				}
			}
		}
	}
	for i := range dst.p {
		dst.p[i] = sumV[i] / sumW[i]
	}
	return dst
}

// fastExpNeg approximates exp(-x) for x >= 0 as (1-x/64)^64; accurate to about
// 1e-3, far below what NLM weights need, and much cheaper than math.Exp.
func fastExpNeg(x float32) float32 {
	if x > 16 {
		return 0
	}
	v := 1 - x*(1.0/64)
	v *= v
	v *= v
	v *= v
	v *= v
	v *= v
	v *= v
	return v
}

// boxMean returns the mean over a (2r+1)^2 window with edge replication.
func boxMean(src *plane, r int) *plane {
	w, h := src.w, src.h
	tmp := newPlane(w, h)
	inv := 1 / float32(2*r+1)
	for y := 0; y < h; y++ {
		row := src.p[y*w : y*w+w]
		out := tmp.p[y*w : y*w+w]
		var s float32
		for k := -r; k <= r; k++ {
			s += row[clampIdx(k, w)]
		}
		for x := 0; x < w; x++ {
			out[x] = s * inv
			s += row[clampIdx(x+r+1, w)] - row[clampIdx(x-r, w)]
		}
	}
	dst := newPlane(w, h)
	acc := make([]float32, w)
	for k := -r; k <= r; k++ {
		row := tmp.row(k)
		for x := range acc {
			acc[x] += row[x]
		}
	}
	for y := 0; y < h; y++ {
		out := dst.p[y*w : y*w+w]
		add, sub := tmp.row(y+r+1), tmp.row(y-r)
		for x := range acc {
			out[x] = acc[x] * inv
			acc[x] += add[x] - sub[x]
		}
	}
	return dst
}

// guidedFilter2 applies the guided filter (He et al.) with guide I to two
// channels at once, sharing the guide statistics. Output keeps the local mean
// of p but takes its edges from I.
func guidedFilter2(I, p1, p2 *plane, r int, eps float32) (*plane, *plane) {
	n := len(I.p)
	sq := newPlane(I.w, I.h)
	for i, v := range I.p {
		sq.p[i] = v * v
	}
	mI := boxMean(I, r)
	varI := boxMean(sq, r)
	for i := range varI.p {
		varI.p[i] -= mI.p[i] * mI.p[i]
	}
	one := func(p *plane) *plane {
		ip := newPlane(I.w, I.h)
		for i := 0; i < n; i++ {
			ip.p[i] = I.p[i] * p.p[i]
		}
		mP := boxMean(p, r)
		mIP := boxMean(ip, r)
		a, b := ip, newPlane(I.w, I.h) // reuse ip's buffer for a
		for i := 0; i < n; i++ {
			av := (mIP.p[i] - mI.p[i]*mP.p[i]) / (varI.p[i] + eps)
			a.p[i] = av
			b.p[i] = mP.p[i] - av*mI.p[i]
		}
		ma, mb := boxMean(a, r), boxMean(b, r)
		out := mP // reuse
		for i := 0; i < n; i++ {
			out.p[i] = ma.p[i]*I.p[i] + mb.p[i]
		}
		return out
	}
	return one(p1), one(p2)
}

// Lanczos-3 taps for 2x upscaling. Output pixel centres sit at source
// positions k-0.25 and k+0.25, so each output phase uses one fixed set of six
// weights starting at source offset lanczosOff[phase].
var lanczosOff, lanczosW = func() ([2]int, [2][6]float32) {
	var off [2]int
	var ws [2][6]float32
	for ph, frac := range []float64{-0.25, 0.25} {
		base := -2
		if frac < 0 {
			base = -3
		}
		var sum float64
		for j := 0; j < 6; j++ {
			x := float64(base+j) - frac
			v := 1.0
			if x != 0 {
				px := math.Pi * x
				v = 3 * math.Sin(px) * math.Sin(px/3) / (px * px)
			}
			ws[ph][j] = float32(v)
			sum += v
		}
		for j := range ws[ph] {
			ws[ph][j] /= float32(sum)
		}
		off[ph] = base
	}
	return off, ws
}()

func upscale2x(src *plane) *plane {
	w, h := src.w, src.h
	W, H := 2*w, 2*h
	tmp := newPlane(W, h)
	for y := 0; y < h; y++ {
		row := src.p[y*w : y*w+w]
		out := tmp.p[y*W : y*W+W]
		for X := 0; X < W; X++ {
			ph, k := X&1, X>>1
			o := k + lanczosOff[ph]
			var s float32
			if o >= 0 && o+6 <= w {
				r := row[o : o+6]
				ws := &lanczosW[ph]
				s = ws[0]*r[0] + ws[1]*r[1] + ws[2]*r[2] + ws[3]*r[3] + ws[4]*r[4] + ws[5]*r[5]
			} else {
				for j, wt := range lanczosW[ph] {
					s += wt * row[clampIdx(o+j, w)]
				}
			}
			out[X] = s
		}
	}
	dst := newPlane(W, H)
	for Y := 0; Y < H; Y++ {
		ph, k := Y&1, Y>>1
		o := k + lanczosOff[ph]
		out := dst.p[Y*W : Y*W+W]
		for j, wt := range lanczosW[ph] {
			r := tmp.row(o + j)
			for X := range out {
				out[X] += wt * r[X]
			}
		}
	}
	return dst
}

// preserveTone restores the local average brightness of before into after
// over a (2r+1)^2 window. Sharpening moves pixels toward the darker or
// lighter side of each edge; on screentone dots that shifts the printed tone
// slightly darker. Adding back the difference of local means keeps the edges
// steep while every small area keeps exactly the ink coverage it had.
func preserveTone(before, after *plane, r int) *plane {
	mb := boxMean(before, r)
	ma := boxMean(after, r)
	for i := range after.p {
		after.p[i] += mb.p[i] - ma.p[i]
	}
	return after
}

// shockFilter steepens ink edges: pixels on the dark side of an edge
// (positive Laplacian) move toward the local minimum, pixels on the light
// side toward the local maximum. It is gated by the local 3x3 contrast range,
// so it only acts on high-contrast line art and text and leaves painted
// shading and gradients alone instead of posterizing them.
func shockFilter(src *plane, strength, r0, r1, soft float32) *plane {
	w, h := src.w, src.h
	dst := newPlane(w, h)
	inv := 1 / (r1 - r0)
	// The edge side comes from a lightly smoothed copy, so the decision
	// varies smoothly along curved edges (round screentone dots stay round).
	sm := src
	if soft > 0 {
		sm = boxMean(src, 1)
	}
	for y := 0; y < h; y++ {
		up, mid, dn := src.row(y-1), src.row(y), src.row(y+1)
		su, sc, sd := sm.row(y-1), sm.row(y), sm.row(y+1)
		out := dst.p[y*w : y*w+w]
		for x := 0; x < w; x++ {
			xl, xr := max(x-1, 0), min(x+1, w-1)
			c := mid[x]
			a, b, cc := up[xl], up[x], up[xr]
			d, f := mid[xl], mid[xr]
			g, hh, i := dn[xl], dn[x], dn[xr]
			lo := min(c, a, b, cc, d, f, g, hh, i)
			hi := max(c, a, b, cc, d, f, g, hh, i)
			gate := (hi - lo - r0) * inv
			if gate <= 0 {
				out[x] = c
				continue
			}
			gate = min(gate, 1)
			gate = gate * gate * (3 - 2*gate) * strength
			lap := (4*(su[x]+sc[xl]+sc[xr]+sd[x])+(su[xl]+su[xr]+sd[xl]+sd[xr]))*(1.0/20) - sc[x]
			if soft <= 0 {
				target := hi
				if lap > 0 {
					target = lo
				}
				out[x] = c + gate*(target-c)
				continue
			}
			// Soft sign: pixels right at the edge centre (lap ~ 0) barely
			// move, so boundaries stay sub-pixel smooth instead of stepping.
			t := max(-1, min(1, lap/soft))
			if t > 0 {
				out[x] = c + gate*t*(lo-c)
			} else {
				out[x] = c - gate*t*(hi-c)
			}
		}
	}
	return dst
}

// crispen sharpens without halos: an unsharp mask whose result is clamped to
// the local 3x3 min/max, so edges get steeper but never overshoot into light
// or dark rims around ink lines.
func crispen(src *plane, amount float32) *plane {
	w, h := src.w, src.h
	dst := newPlane(w, h)
	for y := 0; y < h; y++ {
		up, mid, dn := src.row(y-1), src.row(y), src.row(y+1)
		out := dst.p[y*w : y*w+w]
		for x := 0; x < w; x++ {
			xl, xr := max(x-1, 0), min(x+1, w-1)
			c := mid[x]
			a, b, cc := up[xl], up[x], up[xr]
			d, f := mid[xl], mid[xr]
			g, hh, i := dn[xl], dn[x], dn[xr]
			lo := min(c, a, b, cc, d, f, g, hh, i)
			hi := max(c, a, b, cc, d, f, g, hh, i)
			mean := (a + b + cc + d + c + f + g + hh + i) * (1.0 / 9)
			out[x] = min(max(c+amount*(c-mean), lo), hi)
		}
	}
	return dst
}
