package archive

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	tempDirsLock sync.Mutex
	tempDirs     []string
)

func RegisterTempDir(dir string) {
	tempDirsLock.Lock()
	defer tempDirsLock.Unlock()
	tempDirs = append(tempDirs, dir)
}

func CleanupAllTempDirs() {
	tempDirsLock.Lock()
	defer tempDirsLock.Unlock()
	for _, dir := range tempDirs {
		SafeRmtreeTemp(dir)
	}
	tempDirs = nil
}

// SafeRmtreeTemp safely deletes a temporary directory created by PhotoSlicer.
// Strictly verifies:
// 1. targetPath exists and is a directory.
// 2. targetPath is NOT a protected system or user directory.
// 3. targetPath is inside os.TempDir() OR has a directory name beginning with 'photoslicer_' or '_photoslicer_'.
func SafeRmtreeTemp(targetPath string) bool {
	if targetPath == "" {
		return false
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}
	absTarget, err = filepath.EvalSymlinks(absTarget)
	if err != nil {
		// If symlink evaluation fails because it already doesn't exist, we can't/don't need to delete
		if os.IsNotExist(err) {
			return true
		}
		absTarget, _ = filepath.Abs(targetPath)
	}

	fi, err := os.Stat(absTarget)
	if err != nil || !fi.IsDir() {
		return false
	}

	// Protected directories check
	protected := make(map[string]bool)

	if cwd, err := os.Getwd(); err == nil {
		if absCwd, err := filepath.EvalSymlinks(cwd); err == nil {
			protected[strings.ToLower(absCwd)] = true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if absHome, err := filepath.EvalSymlinks(home); err == nil {
			lowerHome := strings.ToLower(absHome)
			protected[lowerHome] = true
			protected[filepath.Join(lowerHome, "documents")] = true
			protected[filepath.Join(lowerHome, "desktop")] = true
			protected[filepath.Join(lowerHome, "downloads")] = true
		}
	}

	// Drive roots
	for _, drive := range []string{"c:\\", "d:\\", "e:\\", "f:\\", "g:\\", "h:\\", "m:\\", "/"} {
		protected[drive] = true
	}

	tempDir := os.TempDir()
	if absTemp, err := filepath.EvalSymlinks(tempDir); err == nil {
		tempDir = absTemp
	}
	tempLower := strings.ToLower(tempDir)
	protected[tempLower] = true

	targetLower := strings.ToLower(absTarget)
	if protected[targetLower] || targetLower == tempLower {
		return false
	}

	rel, err := filepath.Rel(tempLower, targetLower)
	isInTemp := (err == nil && rel != "." && !strings.HasPrefix(rel, ".."))
	baseLower := strings.ToLower(filepath.Base(absTarget))
	hasPrefix := strings.HasPrefix(baseLower, "photoslicer_") || strings.HasPrefix(baseLower, "_photoslicer_")

	if !isInTemp && !hasPrefix {
		// Neither inside system temp nor starts with photoslicer prefix
		return false
	}

	// Windows read-only removal helper
	_ = filepath.Walk(absTarget, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil {
			_ = os.Chmod(path, 0666)
		}
		return nil
	})

	err = os.RemoveAll(absTarget)
	return err == nil
}
