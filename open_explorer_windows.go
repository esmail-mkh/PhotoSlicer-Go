//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func openExplorerPlatform(cleanPath string, isDir bool) {
	if !isDir {
		cmd := exec.Command("explorer")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CmdLine: fmt.Sprintf(`explorer.exe /select,"%s"`, cleanPath),
		}
		_ = cmd.Start()
		return
	}
	cmd := exec.Command("explorer")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: fmt.Sprintf(`explorer.exe "%s"`, cleanPath),
	}
	_ = cmd.Start()
}
