package enhancer

import (
	"bytes"
	"fmt"
	"image"
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
)

// RunFastEnhancement batch-processes images in inputFolder with the CPU
// enhancer (EnhanceImageCPU), producing 2x JPEGs like the GPU path.
func RunFastEnhancement(
	inputFolder string,
	maxWorkers int,
	checkCanceled func() error,
	progressCallback func(pct, curr, total int),
) (string, error) {
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

	// EnhanceImageCPU already uses every core for one image, and each image
	// in flight holds its source plus a 4x-larger output, so only overlap
	// two images (one enhancing while the other decodes or encodes).
	if maxWorkers <= 0 || maxWorkers > 2 {
		maxWorkers = 2
	}

	taskChan := make(chan string, total)
	var completed int64
	var progressMu sync.Mutex
	var (
		errOnce  sync.Once
		firstErr error
	)
	setFirstErr := func(err error) {
		if err != nil {
			errOnce.Do(func() {
				firstErr = err
			})
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for srcPath := range taskChan {
				if checkCanceled != nil {
					if err := checkCanceled(); err != nil {
						setFirstErr(err)
						return
					}
				}

				base := filepath.Base(srcPath)
				stem := strings.TrimSuffix(base, filepath.Ext(base))
				dstPath := filepath.Join(outDir, stem+".jpg")

				img, err := imageio.OpenImageRobust(srcPath)
				if err != nil {
					// Copy original on failure
					if copyErr := copyFile(srcPath, dstPath); copyErr != nil {
						setFirstErr(fmt.Errorf("failed to copy file %s: %w", srcPath, copyErr))
						continue
					}
				} else {
					h := img.Bounds().Dy()
					if h < constants.MinEnhanceHeight {
						if copyErr := copyFile(srcPath, dstPath); copyErr != nil {
							setFirstErr(fmt.Errorf("failed to copy file %s: %w", srcPath, copyErr))
							continue
						}
					} else if h <= constants.MaxEnhanceHeight {
						enhanced := EnhanceImageCPU(img)
						if saveErr := imageio.SaveImage(enhanced, dstPath, "JPG", 98); saveErr != nil {
							setFirstErr(fmt.Errorf("failed to save image %s: %w", dstPath, saveErr))
							continue
						}
					} else {
						// Split tall images
						slicesCount := math.Ceil(float64(h) / float64(constants.MaxEnhanceHeight))
						cuts := append([]int{0}, slicing.FindSafeCutPoints(img, slicesCount)...)
						for j := 0; j < len(cuts)-1; j++ {
							if checkCanceled != nil {
								if err := checkCanceled(); err != nil {
									setFirstErr(err)
									return
								}
							}
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
							enhancedChunk := EnhanceImageCPU(chunk)
							partDst := filepath.Join(outDir, fmt.Sprintf("%s__part_%04d.jpg", stem, j))
							if saveErr := imageio.SaveImage(enhancedChunk, partDst, "JPG", 98); saveErr != nil {
								setFirstErr(fmt.Errorf("failed to save part %s: %w", partDst, saveErr))
								break
							}
						}
					}
				}

				curr := int(atomic.AddInt64(&completed, 1))
				if progressCallback != nil {
					pct := int(math.Round(float64(curr) / float64(total) * 100.0))
					progressMu.Lock()
					progressCallback(pct, curr, total)
					progressMu.Unlock()
				}
			}
		}()
	}

	for _, f := range files {
		taskChan <- f
	}
	close(taskChan)
	wg.Wait()

	if firstErr != nil {
		return "", firstErr
	}

	if checkCanceled != nil {
		if err := checkCanceled(); err != nil {
			return "", err
		}
	}

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

	// 1. User-specified appDir
	if appDir != "" && appDir != "." {
		searchDirs = append(searchDirs, appDir, filepath.Join(appDir, "up-model"))
	}

	// 2. Directory of currently running executable (standard release install)
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		searchDirs = append(searchDirs,
			exeDir,
			filepath.Join(exeDir, "up-model"),
			filepath.Join(exeDir, "..", "Resources", "up-model"),
			filepath.Join(exeDir, "..", "Resources"),
		)
	}

	// 3. Current working directory and project root (dev & test mode)
	if cwd, err := os.Getwd(); err == nil {
		searchDirs = append(searchDirs,
			cwd,
			filepath.Join(cwd, "up-model"),
			filepath.Join(cwd, "build", "bin"),
			filepath.Join(cwd, "build", "bin", "up-model"),
			filepath.Join(cwd, "..", ".."),
			filepath.Join(cwd, "..", "..", "up-model"),
		)
	}

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
	checkCanceled func() error,
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
		if checkCanceled != nil {
			if err := checkCanceled(); err != nil {
				return "", err
			}
		}

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
			} else if ext == ".jfif" {
				stagedInName = fmt.Sprintf("task_%05d.jpg", taskIdx)
				if err := copyFile(srcPath, filepath.Join(stageIn, stagedInName)); err != nil {
					_ = imageio.SaveImage(img, filepath.Join(stageIn, stagedInName), "JPG", 100)
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
				if checkCanceled != nil {
					if err := checkCanceled(); err != nil {
						return "", err
					}
				}
				top := cuts[j]
				bot := cuts[j+1]
				b := img.Bounds()
				subRect := image.Rect(b.Min.X, b.Min.Y+top, b.Min.X+b.Dx(), b.Min.Y+bot)
				var chunk image.Image
				if sub, ok := img.(interface {
					SubImage(r image.Rectangle) image.Image
				}); ok {
					chunk = sub.SubImage(subRect)
				} else {
					rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), bot-top))
					for sy := top; sy < bot; sy++ {
						for sx := 0; sx < b.Dx(); sx++ {
							rgba.Set(sx, sy-top, img.At(b.Min.X+sx, b.Min.Y+sy))
						}
					}
					chunk = rgba
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
			if checkCanceled != nil {
				if err := checkCanceled(); err != nil {
					if cmd.Process != nil {
						_ = cmd.Process.Kill()
						select {
						case <-done:
						case <-time.After(2 * time.Second):
						}
					}
					return "", err
				}
			}
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
