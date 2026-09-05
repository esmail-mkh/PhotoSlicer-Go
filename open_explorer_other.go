//go:build !windows

package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

func openExplorerPlatform(cleanPath string, isDir bool) {
	switch runtime.GOOS {
	case "darwin":
		if !isDir {
			cmd := exec.Command("open", "-R", cleanPath)
			_ = cmd.Start()
			return
		}
		cmd := exec.Command("open", cleanPath)
		_ = cmd.Start()
	default: // linux, bsd
		targetDir := cleanPath
		if !isDir {
			targetDir = filepath.Dir(cleanPath)
		}
		cmd := exec.Command("xdg-open", targetDir)
		_ = cmd.Start()
	}
}
