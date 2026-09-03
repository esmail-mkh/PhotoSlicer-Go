package enhancer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"photoslicer/engine/archive"
	"photoslicer/engine/constants"
	"photoslicer/engine/imageio"
	"photoslicer/engine/slicing"
	"photoslicer/engine/sorting"

	"github.com/disintegration/imaging"
)

// RGBToHSV converts RGB [0..255] to HSV (H: 0..360, S: 0..1, V: 0..1)
func RGBToHSV(r, g, b uint8) (float64, float64, float64) {
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0

	max := math.Max(rf, math.Max(gf, bf))
	min := math.Min(rf, math.Min(gf, bf))
	delta := max - min

	v := max
	var s, h float64

	if max > 0 {
		s = delta / max
	} else {
		s = 0
	}

	if delta == 0 {
		h = 0
	} else if max == rf {
		h = 60.0 * math.Mod((gf-bf)/delta, 6.0)
	} else if max == gf {
		h = 60.0 * (((bf - rf) / delta) + 2.0)
	} else {
		h = 60.0 * (((rf - gf) / delta) + 4.0)
	}

	if h < 0 {
		h += 360.0
	}

	return h, s, v
}

// HSVToRGB converts HSV to RGB [0..255]
func HSVToRGB(h, s, v float64) (uint8, uint8, uint8) {
	c := v * s
	x := c * (1.0 - math.Abs(math.Mod(h/60.0, 2.0)-1.0))
	m := v - c

	var rf, gf, bf float64
	switch {
	case h >= 0 && h < 60:
		rf, gf, bf = c, x, 0
	case h >= 60 && h < 120:
		rf, gf, bf = x, c, 0
	case h >= 120 && h < 180:
		rf, gf, bf = 0, c, x
	case h >= 180 && h < 240:
		rf, gf, bf = 0, x, c
	case h >= 240 && h < 300:
		rf, gf, bf = x, 0, c
	default:
		rf, gf, bf = c, 0, x
	}

	r := uint8(math.Round((rf + m) * 255.0))
	g := uint8(math.Round((gf + m) * 255.0))
	b := uint8(math.Round((bf + m) * 255.0))
	return r, g, b
}

// FastDenoiseImage applies ink line deepening (v < 80) and paper cleaning (v > 242)
// strictly in HSV Value space with unsharp masking for webtoons.
func FastDenoiseImage(img image.Image) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.At(x+b.Min.X, y+b.Min.Y)
			r32, g32, b32, a32 := c.RGBA()
			r := uint8(r32 >> 8)
			g := uint8(g32 >> 8)
			bl := uint8(b32 >> 8)
			a := uint8(a32 >> 8)

			hVal, sVal, vVal := RGBToHSV(r, g, bl)

			// vVal is 0..1. Multiply by 255 for comparison:
			v255 := vVal * 255.0
			if v255 < 80.0 {
				v255 *= 0.88 // Deepen ink lines
			} else if v255 > 242.0 {
				v255 = math.Min(255.0, v255*1.03) // Clean whites
			}
			newV := v255 / 255.0

			nr, ng, nb := HSVToRGB(hVal, sVal, newV)
			dst.SetRGBA(x, y, color.RGBA{R: nr, G: ng, B: nb, A: a})
		}
	}

	// Unsharp mask (sharpen subtly)
	sharpened := imaging.Sharpen(dst, 0.7)
	res := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(res, res.Bounds(), sharpened, sharpened.Bounds().Min, draw.Src)
	return res
}

// RunFastEnhancement batch-processes images in inputFolder using fast CPU denoising.
func RunFastEnhancement(inputFolder string, maxWorkers int, progressCallback func(pct, curr, total int)) (string, error) {
	files, err := sorting.GetAllImagesDirectory(inputFolder)
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("no valid images in folder: %s", inputFolder)
	}

	total := len(files)
	outDir, err := os.MkdirTemp("", "photoslicer_fast_")
	if err != nil {
		return "", err
	}
	archive.RegisterTempDir(outDir)

	if maxWorkers <= 0 {
		maxWorkers = 4
	}

	taskChan := make(chan string, total)
	var completed int64

	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for srcPath := range taskChan {
				base := filepath.Base(srcPath)
				stem := strings.TrimSuffix(base, filepath.Ext(base))
				dstPath := filepath.Join(outDir, stem+".jpg")

				img, err := imageio.OpenImageRobust(srcPath)
				if err != nil {
					// Copy original on failure
					data, _ := os.ReadFile(srcPath)
					_ = os.WriteFile(dstPath, data, 0644)
				} else {
					h := img.Bounds().Dy()
					if h < constants.MinEnhanceHeight {
						data, _ := os.ReadFile(srcPath)
						_ = os.WriteFile(dstPath, data, 0644)
					} else if h <= constants.MaxEnhanceHeight {
						enhanced := FastDenoiseImage(img)
						_ = imageio.SaveImage(enhanced, dstPath, "JPG", 98)
					} else {
						// Split tall images
						slicesCount := math.Ceil(float64(h) / float64(constants.MaxEnhanceHeight))
						cuts := append([]int{0}, slicing.FindSafeCutPoints(img, slicesCount)...)
						for j := 0; j < len(cuts)-1; j++ {
							top := cuts[j]
							bot := cuts[j+1]
							subRect := image.Rect(0, top, img.Bounds().Dx(), bot)
							var chunk image.Image
							if sub, ok := img.(interface {
								SubImage(r image.Rectangle) image.Image
							}); ok {
								chunk = sub.SubImage(subRect)
							} else {
								chunk = img
							}
							enhancedChunk := FastDenoiseImage(chunk)
							partDst := filepath.Join(outDir, fmt.Sprintf("%s__part_%04d.jpg", stem, j))
							_ = imageio.SaveImage(enhancedChunk, partDst, "JPG", 98)
						}
					}
				}

				curr := int(atomic.AddInt64(&completed, 1))
				if progressCallback != nil {
					pct := int(math.Round(float64(curr) / float64(total) * 100.0))
					progressCallback(pct, curr, total)
				}
			}
		}()
	}

	for _, f := range files {
		taskChan <- f
	}
	close(taskChan)
	wg.Wait()

	return outDir, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func isPureASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

func getSafeAsciiTempDir(prefix string) (string, error) {
	candidates := []string{
		os.TempDir(),
	}
	if pub := os.Getenv("PUBLIC"); pub != "" {
		candidates = append(candidates, filepath.Join(pub, "PhotoSlicerTemp"))
	}
	if sysDrive := os.Getenv("SystemDrive"); sysDrive != "" {
		candidates = append(candidates, filepath.Join(sysDrive+"\\", "PhotoSlicerTemp"))
	}
	candidates = append(candidates, "C:\\PhotoSlicerTemp")

	for _, base := range candidates {
		if !isPureASCII(base) {
			continue
		}
		if err := os.MkdirAll(base, 0755); err != nil {
			continue
		}
		tmp, err := os.MkdirTemp(base, prefix)
		if err == nil {
			return tmp, nil
		}
	}
	return os.MkdirTemp("", prefix)
}

// FindRealEsrganExecutable locates the Real-ESRGAN binary.
func FindRealEsrganExecutable(appDir string) string {
	var searchDirs []string

	// 1. Check directory of currently running executable and parents
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		searchDirs = append(searchDirs,
			exeDir,
			filepath.Join(exeDir, ".."),
			filepath.Join(exeDir, "..", ".."),
			filepath.Join(exeDir, "..", "Resources"),
		)
	}

	// 2. Check current working directory and parent dirs
	if cwd, err := os.Getwd(); err == nil {
		searchDirs = append(searchDirs,
			cwd,
			filepath.Join(cwd, "build", "bin"),
			filepath.Join(cwd, ".."),
			filepath.Join(cwd, "..", "build", "bin"),
			filepath.Join(cwd, "..", ".."),
			filepath.Join(cwd, "..", "..", "build", "bin"),
		)
	}

	// 3. User-specified appDir
	if appDir != "" && appDir != "." {
		searchDirs = append(searchDirs, appDir)
	}
	searchDirs = append(searchDirs, ".")

	var binNames []string
	switch runtime.GOOS {
	case "windows":
		binNames = []string{"realesrgan-ncnn-vulkan.exe"}
	case "darwin":
		binNames = []string{"realesrgan-ncnn-vulkan-macos", "realesrgan-ncnn-vulkan"}
	default:
		binNames = []string{"realesrgan-ncnn-vulkan-ubuntu", "realesrgan-ncnn-vulkan"}
	}

	for _, dir := range searchDirs {
		for _, name := range binNames {
			p1 := filepath.Join(dir, "up-model", name)
			if fi, err := os.Stat(p1); err == nil && !fi.IsDir() {
				return p1
			}
			p2 := filepath.Join(dir, name)
			if fi, err := os.Stat(p2); err == nil && !fi.IsDir() {
				return p2
			}
		}
	}

	for _, name := range binNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}

	return ""
}

type stagedItem struct {
	stagedName string // expected file name in stageOut (e.g. "task_00000.jpg")
	destName   string // final destination file name in outDir (e.g. "صفحه ۰۱.jpg")
}

// RunRealEsrganAI executes Real-ESRGAN Vulkan executable on the input folder using safe ASCII staging.
func RunRealEsrganAI(
	exePath string,
	inputFolder string,
	modelName string,
	progressCallback func(pct, curr, total int),
) (string, error) {
	files, err := sorting.GetAllImagesDirectory(inputFolder)
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("no valid images in folder: %s", inputFolder)
	}

	// Create final output folder
	outDir, err := os.MkdirTemp("", "photoslicer_ai_out_")
	if err != nil {
		return "", err
	}
	archive.RegisterTempDir(outDir)

	// Create safe pure-ASCII staging directories
	stageBase, err := getSafeAsciiTempDir("photoslicer_stage_")
	if err != nil {
		return "", fmt.Errorf("failed to create safe staging directory: %w", err)
	}
	archive.RegisterTempDir(stageBase)
	defer func() {
		_ = os.RemoveAll(stageBase)
	}()

	stageIn := filepath.Join(stageBase, "in")
	stageOut := filepath.Join(stageBase, "out")
	if err := os.MkdirAll(stageIn, 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(stageOut, 0755); err != nil {
		return "", err
	}

	var stagedList []stagedItem
	taskIdx := 0

	for _, srcPath := range files {
		base := filepath.Base(srcPath)
		ext := strings.ToLower(filepath.Ext(base))
		stem := strings.TrimSuffix(base, filepath.Ext(base))

		img, err := imageio.OpenImageRobust(srcPath)
		if err != nil {
			// Copy original directly to outDir on open failure
			_ = copyFile(srcPath, filepath.Join(outDir, base))
			continue
		}

		h := img.Bounds().Dy()
		if h < constants.MinEnhanceHeight {
			_ = copyFile(srcPath, filepath.Join(outDir, base))
			continue
		}

		if h <= constants.MaxEnhanceHeight {
			var stagedInName string
			stagedOutName := fmt.Sprintf("task_%05d.jpg", taskIdx)

			if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" {
				stagedInName = fmt.Sprintf("task_%05d%s", taskIdx, ext)
				if err := copyFile(srcPath, filepath.Join(stageIn, stagedInName)); err != nil {
					_ = imageio.SaveImage(img, filepath.Join(stageIn, stagedInName), "PNG", 100)
				}
			} else {
				stagedInName = fmt.Sprintf("task_%05d.png", taskIdx)
				_ = imageio.SaveImage(img, filepath.Join(stageIn, stagedInName), "PNG", 100)
			}

			stagedList = append(stagedList, stagedItem{
				stagedName: stagedOutName,
				destName:   stem + ".jpg",
			})
			taskIdx++
		} else {
			// Split tall image to prevent JPEG height overflow (>65535) and GPU memory issues
			slicesCount := math.Ceil(float64(h) / float64(constants.MaxEnhanceHeight))
			cuts := append([]int{0}, slicing.FindSafeCutPoints(img, slicesCount)...)
			for j := 0; j < len(cuts)-1; j++ {
				top := cuts[j]
				bot := cuts[j+1]
				subRect := image.Rect(0, top, img.Bounds().Dx(), bot)
				var chunk image.Image
				if sub, ok := img.(interface {
					SubImage(r image.Rectangle) image.Image
				}); ok {
					chunk = sub.SubImage(subRect)
				} else {
					chunk = img
				}

				stagedInName := fmt.Sprintf("task_%05d_p%04d.jpg", taskIdx, j)
				stagedOutName := fmt.Sprintf("task_%05d_p%04d.jpg", taskIdx, j)
				_ = imageio.SaveImage(chunk, filepath.Join(stageIn, stagedInName), "JPG", 98)

				stagedList = append(stagedList, stagedItem{
					stagedName: stagedOutName,
					destName:   fmt.Sprintf("%s__part_%04d.jpg", stem, j),
				})
			}
			taskIdx++
		}
	}

	if len(stagedList) == 0 {
		return outDir, nil
	}

	totalTasks := len(stagedList)

	if modelName == "" {
		modelName = "realesr-animevideov3"
	}

	modelsDir := filepath.Join(filepath.Dir(exePath), "models")
	args := []string{"-i", stageIn, "-o", stageOut, "-n", modelName, "-s", "2", "-t", "400", "-f", "jpg"}
	if fi, err := os.Stat(modelsDir); err == nil && fi.IsDir() {
		args = append(args, "-m", modelsDir)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = filepath.Dir(exePath)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(exePath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	prepareCommand(cmd)

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start Real-ESRGAN: %w", err)
	}

	// Poll output folder to track progress
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			if err != nil {
				errMsg := strings.TrimSpace(stderrBuf.String())
				if errMsg != "" {
					return "", fmt.Errorf("realesrgan failed: %v (%s)", err, errMsg)
				}
				return "", fmt.Errorf("realesrgan failed: %w", err)
			}

			// Successfully finished: move/copy all staged items to outDir with their destName
			for _, item := range stagedList {
				src := filepath.Join(stageOut, item.stagedName)
				dst := filepath.Join(outDir, item.destName)
				if _, statErr := os.Stat(src); statErr == nil {
					if renErr := os.Rename(src, dst); renErr != nil {
						_ = copyFile(src, dst)
					}
				}
			}

			if progressCallback != nil {
				progressCallback(100, totalTasks, totalTasks)
			}
			return outDir, nil

		case <-ticker.C:
			outFiles, _ := filepath.Glob(filepath.Join(stageOut, "*.jpg"))
			curr := len(outFiles)
			if curr > totalTasks {
				curr = totalTasks
			}
			pct := int(math.Round(float64(curr) / float64(totalTasks) * 100.0))
			if progressCallback != nil {
				progressCallback(pct, curr, totalTasks)
			}
		}
	}
}
