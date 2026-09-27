package enhancer

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photoslicer/engine/imageio"
	"photoslicer/engine/pipeline"
)

func TestIsPureASCII(t *testing.T) {
	if !isPureASCII("C:\\Users\\Public\\Temp") {
		t.Errorf("expected pure ASCII to be true")
	}
	if isPureASCII("C:\\Users\\فارسی\\Temp") {
		t.Errorf("expected Persian string to be false")
	}
}

func TestGetSafeAsciiTempDir(t *testing.T) {
	dir, err := getSafeAsciiTempDir("photoslicer_test_")
	if err != nil {
		t.Fatalf("getSafeAsciiTempDir failed: %v", err)
	}
	defer os.RemoveAll(dir)

	if !isPureASCII(dir) {
		t.Errorf("expected temp dir to be pure ASCII, got: %s", dir)
	}
}

func TestFindRealEsrganExecutable(t *testing.T) {
	exe := FindRealEsrganExecutable("")
	if exe == "" {
		t.Logf("Real-ESRGAN binary not found (may not be present in CI environment)")
	} else {
		t.Logf("Found Real-ESRGAN executable at: %s", exe)
		if fi, err := os.Stat(exe); err != nil || fi.IsDir() {
			t.Errorf("expected valid executable file, got error: %v", err)
		}
	}
}

func TestRunRealEsrganAIWithPersianPathAndFile(t *testing.T) {
	exe := FindRealEsrganExecutable("")
	if exe == "" {
		t.Skip("Real-ESRGAN executable not found, skipping test")
	}

	if os.Getenv("CI") != "" {
		t.Skip("Skipping Real-ESRGAN execution in CI environment")
	}

	gpu := DetectPrimaryGPU()
	if !gpu.IsCapable || gpu.IsSoftware {
		t.Skipf("No capable dedicated Vulkan GPU detected (%s, VRAM: %dMB), skipping Real-ESRGAN execution in CI/headless environment", gpu.Name, gpu.DedicatedVRAMMB)
	}

	// Create a test input folder with Persian name and a Persian file name
	testDir, err := os.MkdirTemp("", "تست_پوشه_فارسی_")
	if err != nil {
		t.Fatalf("failed to create Persian test dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Copy a sample image into testDir with Persian filename
	srcImg := filepath.Join("..", "..", "assets", "app-v4.2-en-image.jpg")
	dstImg := filepath.Join(testDir, "تصویر شماره ۱.jpg")

	if err := copyFile(srcImg, dstImg); err != nil {
		t.Fatalf("failed to copy sample image: %v", err)
	}

	progressCalled := false
	outDir, err := RunRealEsrganAI(exe, testDir, "", nil, func(pct, curr, total int) {
		progressCalled = true
	})
	if err != nil {
		if strings.Contains(err.Error(), "vkCreateInstance") || strings.Contains(err.Error(), "invalid gpu device") || strings.Contains(err.Error(), "permission denied") {
			t.Skipf("Real-ESRGAN binary cannot be executed in this environment (%v), skipping", err)
		}
		t.Fatalf("RunRealEsrganAI failed on Persian path/file: %v", err)
	}
	defer os.RemoveAll(outDir)

	if !progressCalled {
		t.Errorf("expected progress callback to be called")
	}

	// Verify that the enhanced image exists with the original Persian stem
	expectedOut := filepath.Join(outDir, "تصویر شماره ۱.jpg")
	fi, err := os.Stat(expectedOut)
	if err != nil {
		t.Fatalf("expected enhanced output file %s, but stat failed: %v", expectedOut, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("output file is empty")
	}
}

func TestEnhancerAndPipelineIntegration(t *testing.T) {
	exe := FindRealEsrganExecutable("")
	if exe == "" {
		t.Skip("Real-ESRGAN executable not found, skipping test")
	}

	if os.Getenv("CI") != "" {
		t.Skip("Skipping Real-ESRGAN execution in CI environment")
	}

	gpu := DetectPrimaryGPU()
	if !gpu.IsCapable || gpu.IsSoftware {
		t.Skipf("No capable dedicated Vulkan GPU detected (%s, VRAM: %dMB), skipping Real-ESRGAN execution in CI/headless environment", gpu.Name, gpu.DedicatedVRAMMB)
	}

	testDir, err := os.MkdirTemp("", "پوشه_ورودی_")
	if err != nil {
		t.Fatalf("failed to create Persian test dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	srcImg := filepath.Join("..", "..", "assets", "app-v4.2-en-image.jpg")
	dstImg := filepath.Join(testDir, "عکس اول.jpg")
	if err := copyFile(srcImg, dstImg); err != nil {
		t.Fatalf("failed to copy sample image: %v", err)
	}

	enhancedDir, err := RunRealEsrganAI(exe, testDir, "", nil, nil)
	if err != nil {
		if strings.Contains(err.Error(), "vkCreateInstance") || strings.Contains(err.Error(), "invalid gpu device") || strings.Contains(err.Error(), "permission denied") {
			t.Skipf("Real-ESRGAN binary cannot be executed in this environment (%v), skipping", err)
		}
		t.Fatalf("RunRealEsrganAI failed: %v", err)
	}
	defer os.RemoveAll(enhancedDir)

	// Now run pipeline on the enhancedDir
	outBase, err := os.MkdirTemp("", "خروجی_پایانی_")
	if err != nil {
		t.Fatalf("failed to create output base dir: %v", err)
	}
	defer os.RemoveAll(outBase)

	opts := pipeline.PipelineOptions{
		Mode:            "single",
		SaveFormat:      "JPG",
		SaveQuality:     90,
		HeightLimit:     800,
		CurrentDate:     "2026-09-03",
		OutputBase:      outBase,
		MaxWorkers:      2,
		FilenamePattern: "[number]",
		FilenameDigits:  2,
	}

	finalPath, err := pipeline.MergerImages(enhancedDir, opts)
	if err != nil {
		t.Fatalf("pipeline.MergerImages failed on enhancedDir: %v", err)
	}

	entries, err := os.ReadDir(finalPath)
	if err != nil {
		t.Fatalf("failed to read final path: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected sliced output files in %s, found none", finalPath)
	}
}

// testComicPage draws a small comic-like page: flat skin tone and paper
// areas, a thick ink outline, thin hatching and a dark ink bar.
func testComicPage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{R: 250, G: 250, B: 248, A: 255}
			if x < w/2 {
				c = color.RGBA{R: 240, G: 190, B: 170, A: 255}
			}
			ink := func(cov float64, v uint8) {
				cov = math.Max(0, math.Min(1, cov))
				c.R = uint8(float64(c.R)*(1-cov) + float64(v)*cov)
				c.G = uint8(float64(c.G)*(1-cov) + float64(v)*cov)
				c.B = uint8(float64(c.B)*(1-cov) + float64(v)*cov)
			}
			// Anti-aliased ink ring, 3px wide.
			r := math.Hypot(float64(x-w/2)+0.5, float64(y-h/2)+0.5)
			ink(2-math.Abs(r-float64(w)/4-1.5), 20)
			// 3px diagonal hatching.
			if x > w*3/4 && (x+y)%12 < 3 {
				ink(1, 30)
			}
			if x >= w/2-2 && x < w/2+2 {
				c = color.RGBA{R: 25, G: 25, B: 25, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestEnhanceImageCPU(t *testing.T) {
	const w, h = 96, 96
	img := testComicPage(w, h)
	res := EnhanceImageCPU(img)
	if res.Bounds() != image.Rect(0, 0, 2*w, 2*h) {
		t.Fatalf("expected 2x output %v, got %v", image.Rect(0, 0, 2*w, 2*h), res.Bounds())
	}

	if p := res.RGBAAt(w, 2*10); p.R > 40 || p.G > 40 || p.B > 40 {
		t.Errorf("ink line was washed out: got %v", p)
	}
	if p := res.RGBAAt(2*60, 2*5); p.R < 246 || p.G < 246 || p.B < 244 {
		t.Errorf("paper background changed: got %v", p)
	}

	// Flat colour must keep its hue and saturation exactly.
	orig := img.RGBAAt(10, 10)
	_, oCb, oCr := color.RGBToYCbCr(orig.R, orig.G, orig.B)
	got := res.RGBAAt(20, 20)
	_, gCb, gCr := color.RGBToYCbCr(got.R, got.G, got.B)
	if abs(int(oCb)-int(gCb)) > 1 || abs(int(oCr)-int(gCr)) > 1 {
		t.Errorf("flat colour shifted: Cb %d->%d, Cr %d->%d", oCb, gCb, oCr, gCr)
	}
}

func TestEnhanceImageCPUKeepsGrayscale(t *testing.T) {
	src := testComicPage(64, 64)
	for i := 0; i < len(src.Pix); i += 4 {
		y := uint8((299*int(src.Pix[i]) + 587*int(src.Pix[i+1]) + 114*int(src.Pix[i+2])) / 1000)
		src.Pix[i], src.Pix[i+1], src.Pix[i+2] = y, y, y
	}
	res := EnhanceImageCPU(src)
	for i := 0; i < len(res.Pix); i += 4 {
		r, g, b := int(res.Pix[i]), int(res.Pix[i+1]), int(res.Pix[i+2])
		if abs(r-g) > 1 || abs(g-b) > 1 {
			t.Fatalf("grayscale page gained colour at pixel %d: %d,%d,%d", i/4, r, g, b)
		}
	}
}

func TestEnhanceImageCPUStripsAreSeamless(t *testing.T) {
	// Taller than several strips, with noise so every stage has work to do.
	const w, h = 48, cpuStripRows*3 + 17
	img := testComicPage(w, h)
	for i := range img.Pix {
		if i%4 != 3 {
			img.Pix[i] = uint8(min(255, max(0, int(img.Pix[i])+(i*7919%9)-4)))
		}
	}
	striped := EnhanceImageCPU(img)

	whole := image.NewRGBA(striped.Rect)
	enhanceStrip(img, whole, 0, h, defaultCPUEnhanceParams)

	for i := range whole.Pix {
		if abs(int(whole.Pix[i])-int(striped.Pix[i])) > 1 {
			t.Fatalf("strip seam at output row %d: whole=%d striped=%d", i/whole.Stride, whole.Pix[i], striped.Pix[i])
		}
	}
}

// TestEnhanceImageCPUBeatsBicubic degrades a drawing the way web comics are
// usually delivered (half resolution, JPEG) and checks that the CPU enhancer
// restores it more faithfully than plain bicubic upscaling.
func TestEnhanceImageCPUBeatsBicubic(t *testing.T) {
	const w, h = 256, 256
	hr := testComicPage(w, h)
	lr := image.NewRGBA(image.Rect(0, 0, w/2, h/2))
	for y := 0; y < h/2; y++ {
		for x := 0; x < w/2; x++ {
			for c := 0; c < 3; c++ {
				s := int(hr.Pix[(2*y)*hr.Stride+(2*x)*4+c]) + int(hr.Pix[(2*y)*hr.Stride+(2*x+1)*4+c]) +
					int(hr.Pix[(2*y+1)*hr.Stride+(2*x)*4+c]) + int(hr.Pix[(2*y+1)*hr.Stride+(2*x+1)*4+c])
				lr.Pix[y*lr.Stride+x*4+c] = uint8((s + 2) / 4)
			}
			lr.Pix[y*lr.Stride+x*4+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, lr, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	degraded, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}

	psnr := func(a, b *image.RGBA) float64 {
		var se float64
		for i := 0; i < len(a.Pix); i += 4 {
			for c := 0; c < 3; c++ {
				d := float64(a.Pix[i+c]) - float64(b.Pix[i+c])
				se += d * d
			}
		}
		return 10 * math.Log10(255*255/(se/float64(len(a.Pix)/4*3)))
	}
	bicubic := psnr(hr, imageio.ResizeBicubic(degraded, w, h))
	enhanced := psnr(hr, EnhanceImageCPU(degraded))
	t.Logf("PSNR bicubic=%.2f dB enhanced=%.2f dB", bicubic, enhanced)
	if enhanced < bicubic+0.5 {
		t.Errorf("CPU enhancer should beat bicubic by at least 0.5 dB: bicubic=%.2f enhanced=%.2f", bicubic, enhanced)
	}
}

// TestEnhanceImageCPUKeepsGrain checks that intentional grain / paper
// texture on a clean page survives: downsized back to the source size, the
// output must keep at least 90% of the grain and the same average colour.
func TestEnhanceImageCPUKeepsGrain(t *testing.T) {
	const w, h = 128, 128
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	base := [3]float64{235, 200, 180}
	for i := 0; i < w*h; i++ {
		n := rng.NormFloat64() * 6
		for c := 0; c < 3; c++ {
			img.Pix[i*4+c] = uint8(math.Max(0, math.Min(255, math.Round(base[c]+n))))
		}
		img.Pix[i*4+3] = 255
	}
	res := EnhanceImageCPU(img)

	lumaStats := func(get func(x, y, c int) float64) (mean [3]float64, grain float64) {
		var s1, s2 float64
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var l float64
				for c := 0; c < 3; c++ {
					v := get(x, y, c)
					mean[c] += v / (w * h)
					l += []float64{0.299, 0.587, 0.114}[c] * v
				}
				s1 += l
				s2 += l * l
			}
		}
		n := float64(w * h)
		return mean, math.Sqrt(s2/n - (s1/n)*(s1/n))
	}
	srcMean, srcGrain := lumaStats(func(x, y, c int) float64 { return float64(img.Pix[y*img.Stride+x*4+c]) })
	outMean, outGrain := lumaStats(func(x, y, c int) float64 {
		// 2x2 box average: the output seen at source size.
		var s float64
		for dy := 0; dy < 2; dy++ {
			for dx := 0; dx < 2; dx++ {
				s += float64(res.Pix[(2*y+dy)*res.Stride+(2*x+dx)*4+c])
			}
		}
		return s / 4
	})
	if outGrain < 0.9*srcGrain {
		t.Errorf("grain was smoothed away: source std %.2f, output std %.2f", srcGrain, outGrain)
	}
	for c := 0; c < 3; c++ {
		if math.Abs(outMean[c]-srcMean[c]) > 0.5 {
			t.Errorf("average colour channel %d shifted: %.2f -> %.2f", c, srcMean[c], outMean[c])
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestRunFastEnhancementBatch(t *testing.T) {
	testDir, err := os.MkdirTemp("", "تست_سریع_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	srcImg := filepath.Join("..", "..", "assets", "app-v4.2-en-image.jpg")
	dstImg1 := filepath.Join(testDir, "تصویر_تست_۱.jpg")
	dstImg2 := filepath.Join(testDir, "تصویر_تست_۲.jpg")

	_ = copyFile(srcImg, dstImg1)
	_ = copyFile(srcImg, dstImg2)

	progressCount := 0
	outDir, err := RunFastEnhancement(testDir, 4, nil, func(pct, curr, total int) {
		progressCount++
	})
	if err != nil {
		t.Fatalf("RunFastEnhancement failed: %v", err)
	}
	defer os.RemoveAll(outDir)

	if progressCount == 0 {
		t.Errorf("expected progress callback to be called")
	}

	files, err := os.ReadDir(outDir)
	if err != nil || len(files) < 2 {
		t.Fatalf("expected at least 2 processed files in outDir, got %d", len(files))
	}
}

func TestRunFastEnhancementCancellation(t *testing.T) {
	testDir, err := os.MkdirTemp("", "تست_کنسل_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	srcImg := filepath.Join("..", "..", "assets", "app-v4.2-en-image.jpg")
	for i := 0; i < 5; i++ {
		dstImg := filepath.Join(testDir, "test_img.jpg")
		_ = copyFile(srcImg, dstImg)
	}

	cancelledErr := os.ErrInvalid
	outDir, err := RunFastEnhancement(testDir, 1, func() error {
		return cancelledErr
	}, nil)

	if err != cancelledErr {
		t.Errorf("expected cancellation error %v, got %v", cancelledErr, err)
	}
	if outDir != "" {
		_ = os.RemoveAll(outDir)
	}
}

func BenchmarkEnhanceImageCPU(b *testing.B) {
	img := testComicPage(800, 1200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EnhanceImageCPU(img)
	}
}
