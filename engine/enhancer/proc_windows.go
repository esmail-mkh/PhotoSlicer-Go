//go:build windows

package enhancer

import (
	"os/exec"
	"syscall"
)

// prepareCommand configures the subprocess on Windows to hide its console window.
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
