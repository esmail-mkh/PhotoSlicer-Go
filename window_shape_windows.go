//go:build windows

package main

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// The frameless window has no native frame, so its corners are square unless we
// round them:
//   - Windows 11 (build 22000+) rounds windows itself; we only make the choice
//     explicit.
//   - Windows 10 cannot, so the window is clipped to a rounded rectangle with a
//     window region. A region has no anti-aliasing, so the frontend also draws a
//     one pixel rim along the same curve (.window-frame) to soften the edge.

const (
	wailsWindowClass      = "wailsWindow" // Wails' default window class
	windows11FirstBuild   = 22000
	dwmwaWindowCornerPref = 33 // DWMWA_WINDOW_CORNER_PREFERENCE
	dwmwcpRound           = 2  // DWMWCP_ROUND
	defaultDPI            = 96
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")
	ntdll  = syscall.NewLazyDLL("ntdll.dll")
	dwmapi = syscall.NewLazyDLL("dwmapi.dll")

	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procIsWindow                 = user32.NewProc("IsWindow")
	procIsZoomed                 = user32.NewProc("IsZoomed")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procSetWindowRgn             = user32.NewProc("SetWindowRgn")
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procCreateRoundRectRgn       = gdi32.NewProc("CreateRoundRectRgn")
	procRtlGetVersion            = ntdll.NewProc("RtlGetVersion")
	procDwmSetWindowAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")
)

type rect struct{ left, top, right, bottom int32 }

// osVersionInfoEx is OSVERSIONINFOEXW.
type osVersionInfoEx struct {
	size         uint32
	major, minor uint32
	build        uint32
	platform     uint32
	csdVersion   [128]uint16
	spMajor      uint16
	spMinor      uint16
	suiteMask    uint16
	productType  byte
	reserved     byte
}

var (
	shapeMu   sync.Mutex
	appWindow uintptr

	// EnumWindows callbacks cannot be released, so there is exactly one.
	enumPid      uint32
	enumFound    uintptr
	enumCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != enumPid {
			return 1 // keep looking
		}
		name := make([]uint16, 64)
		n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
		if syscall.UTF16ToString(name[:n]) != wailsWindowClass {
			return 1
		}
		enumFound = hwnd
		return 0 // stop
	})
)

// findAppWindow returns this process's main window, or 0 if it does not exist
// yet. The caller must hold shapeMu.
func findAppWindow() uintptr {
	if appWindow != 0 {
		if ok, _, _ := procIsWindow.Call(appWindow); ok != 0 {
			return appWindow
		}
		appWindow = 0
	}
	enumPid = uint32(os.Getpid())
	enumFound = 0
	procEnumWindows.Call(enumCallback, 0)
	appWindow = enumFound
	return appWindow
}

func windowsBuild() uint32 {
	var info osVersionInfoEx
	info.size = uint32(unsafe.Sizeof(info))
	if status, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&info))); status != 0 {
		return 0
	}
	return info.build
}

func applyWindowShape(radius int) {
	shapeMu.Lock()
	defer shapeMu.Unlock()

	hwnd := findAppWindow()
	if hwnd == 0 {
		return
	}

	if windowsBuild() >= windows11FirstBuild {
		preference := uint32(dwmwcpRound)
		procDwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPref, uintptr(unsafe.Pointer(&preference)), unsafe.Sizeof(preference))
		return
	}

	// Maximised windows fill the screen, so they stay square
	if zoomed, _, _ := procIsZoomed.Call(hwnd); zoomed != 0 || radius <= 0 {
		procSetWindowRgn.Call(hwnd, 0, 1)
		return
	}

	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	dpi := uintptr(defaultDPI)
	if v, _, _ := procGetDpiForWindow.Call(hwnd); v != 0 {
		dpi = v
	}
	diameter := uintptr(radius) * 2 * dpi / defaultDPI
	width, height := uintptr(r.right-r.left), uintptr(r.bottom-r.top)

	// CreateRoundRectRgn's right/bottom edges are exclusive, hence the +1
	region, _, _ := procCreateRoundRectRgn.Call(0, 0, width+1, height+1, diameter, diameter)
	if region == 0 {
		return
	}
	// The system owns the region once it is set, so it must not be deleted here
	procSetWindowRgn.Call(hwnd, region, 1)
}
