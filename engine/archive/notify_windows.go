//go:build windows

package archive

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modShell32         = syscall.NewLazyDLL("shell32.dll")
	procSHChangeNotify = modShell32.NewProc("SHChangeNotify")
)

const (
	shcneAllEvents = 0x7FFFFFFF
	shcnfPathW     = 0x0005
)

// NotifyShellChange notifies Windows Explorer that a file or directory has been created, modified, or deleted.
// This forces open Windows Explorer windows to immediately refresh and display new or removed files.
func NotifyShellChange(path string) {
	if path == "" {
		return
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	ptr, err := syscall.UTF16PtrFromString(absPath)
	if err != nil {
		return
	}
	procSHChangeNotify.Call(uintptr(shcneAllEvents), uintptr(shcnfPathW), uintptr(unsafe.Pointer(ptr)), 0)
}
