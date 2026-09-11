//go:build windows

package enhancer

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	moddxgi                = syscall.NewLazyDLL("dxgi.dll")
	procCreateDXGIFactory1 = moddxgi.NewProc("CreateDXGIFactory1")
)

type dxgiGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// IID_IDXGIFactory1: 770aae78-f26f-4dba-a829-253c83d1b387
var iidIDXGIFactory1 = dxgiGUID{
	0x770aae78,
	0xf26f,
	0x4dba,
	[8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87},
}

type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorId              uint32
	DeviceId              uint32
	SubSysId              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLuidLow        uint32
	AdapterLuidHigh       int32
	Flags                 uint32
}

const (
	dxgiAdapterFlagNone     = 0
	dxgiAdapterFlagSoftware = 2 // DXGI_ADAPTER_FLAG_SOFTWARE
)

// DetectPrimaryGPU scans system display adapters using Windows DXGI.
// In systems with multiple GPUs (e.g. integrated + discrete), it selects
// the adapter with the highest dedicated video memory.
func DetectPrimaryGPU() GPUInfo {
	best, err := detectGPUViaDXGI()
	if err == nil && best.Name != "" {
		return best
	}

	// Fallback to WMI/CIM if DXGI enumeration encountered an error
	fallback := detectGPUViaCIM()
	if fallback.Name != "" {
		return fallback
	}

	return GPUInfo{
		Name:            "Unknown GPU",
		Vendor:          "Unknown",
		DedicatedVRAMMB: 0,
		IsSoftware:      false,
		IsCapable:       false,
	}
}

func detectGPUViaDXGI() (GPUInfo, error) {
	if err := moddxgi.Load(); err != nil {
		return GPUInfo{}, err
	}

	var factory *struct{ VTable *[13]uintptr }
	r1, _, err := procCreateDXGIFactory1.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory1)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if r1 != 0 || factory == nil {
		return GPUInfo{}, err
	}

	factoryVtbl := factory.VTable
	releaseFactory := factoryVtbl[2]
	enumAdapters1 := factoryVtbl[12]

	defer func() {
		syscall.SyscallN(releaseFactory, uintptr(unsafe.Pointer(factory)))
	}()

	var best GPUInfo
	highestVRAM := -1

	for i := uintptr(0); ; i++ {
		var adapter *struct{ VTable *[11]uintptr }
		res, _, _ := syscall.SyscallN(enumAdapters1, uintptr(unsafe.Pointer(factory)), i, uintptr(unsafe.Pointer(&adapter)))
		if res != 0 || adapter == nil {
			break
		}

		adapterVtbl := adapter.VTable
		releaseAdapter := adapterVtbl[2]
		getDesc1 := adapterVtbl[10]

		var desc dxgiAdapterDesc1
		resDesc, _, _ := syscall.SyscallN(getDesc1, uintptr(unsafe.Pointer(adapter)), uintptr(unsafe.Pointer(&desc)))
		if resDesc == 0 {
			name := strings.TrimSpace(syscall.UTF16ToString(desc.Description[:]))
			vramMB := int(desc.DedicatedVideoMemory / (1024 * 1024))
			isSoftware := (desc.Flags & dxgiAdapterFlagSoftware) != 0

			info := GPUInfo{
				Name:            name,
				Vendor:          NormalizeVendor(name),
				DedicatedVRAMMB: vramMB,
				IsSoftware:      isSoftware,
				IsCapable:       !isSoftware && vramMB >= MinCapableVRAMMB,
			}

			// Prioritize non-software adapters with highest dedicated VRAM
			if !isSoftware && vramMB > highestVRAM {
				highestVRAM = vramMB
				best = info
			} else if best.Name == "" {
				best = info
			}
		}
		syscall.SyscallN(releaseAdapter, uintptr(unsafe.Pointer(adapter)))
	}

	return best, nil
}

// detectGPUViaCIM performs an emergency fallback query using CIM.
func detectGPUViaCIM() GPUInfo {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Get-CimInstance Win32_VideoController | Select-Object Name, AdapterRAM | ConvertTo-Json`)
	prepareCommand(cmd)

	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return GPUInfo{}
	}

	// Output might be single object or array of objects
	type cimGPU struct {
		Name       string      `json:"Name"`
		AdapterRAM interface{} `json:"AdapterRAM"`
	}

	var list []cimGPU
	if err := json.Unmarshal(out, &list); err != nil {
		var single cimGPU
		if err2 := json.Unmarshal(out, &single); err2 == nil {
			list = []cimGPU{single}
		} else {
			return GPUInfo{}
		}
	}

	var best GPUInfo
	highestRAM := -1

	for _, g := range list {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}

		var ramBytes int64
		switch v := g.AdapterRAM.(type) {
		case float64:
			ramBytes = int64(v)
		case string:
			ramBytes, _ = strconv.ParseInt(v, 10, 64)
		}

		vramMB := int(ramBytes / (1024 * 1024))
		isSoftware := strings.Contains(strings.ToLower(name), "basic render") ||
			strings.Contains(strings.ToLower(name), "remote desktop") ||
			strings.Contains(strings.ToLower(name), "virtual")

		info := GPUInfo{
			Name:            name,
			Vendor:          NormalizeVendor(name),
			DedicatedVRAMMB: vramMB,
			IsSoftware:      isSoftware,
			IsCapable:       !isSoftware && vramMB >= MinCapableVRAMMB,
		}

		if !isSoftware && vramMB > highestRAM {
			highestRAM = vramMB
			best = info
		} else if best.Name == "" {
			best = info
		}
	}

	_ = bytes.NewBuffer(nil) // prevent unused import warning
	return best
}
