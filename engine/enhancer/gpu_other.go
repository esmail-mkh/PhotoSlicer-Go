//go:build !windows

package enhancer

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// DetectPrimaryGPU scans display adapters on macOS and Linux.
func DetectPrimaryGPU() GPUInfo {
	switch runtime.GOOS {
	case "darwin":
		return detectDarwinGPU()
	case "linux":
		return detectLinuxGPU()
	default:
		return GPUInfo{
			Name:            "Generic GPU",
			Vendor:          "Unknown",
			DedicatedVRAMMB: 0,
			IsSoftware:      false,
			IsCapable:       false,
		}
	}
}

// detectDarwinGPU detects Apple Silicon or discrete GPUs on macOS.
func detectDarwinGPU() GPUInfo {
	// Check for Apple Silicon M-series (unified memory, hardware accelerated)
	archCmd := exec.Command("uname", "-m")
	if out, err := archCmd.Output(); err == nil && strings.Contains(string(out), "arm64") {
		return GPUInfo{
			Name:            "Apple Silicon GPU",
			Vendor:          "Apple",
			DedicatedVRAMMB: 8192, // Apple unified memory provides plenty of VRAM for Real-ESRGAN
			IsSoftware:      false,
			IsCapable:       true,
		}
	}

	// Intel Mac fallback: query system_profiler
	cmd := exec.Command("system_profiler", "SPDisplaysDataType")
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		var name string
		var vramMB int
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Chipset Model:") {
				name = strings.TrimSpace(strings.TrimPrefix(trimmed, "Chipset Model:"))
			}
			if strings.HasPrefix(trimmed, "VRAM (Total):") || strings.HasPrefix(trimmed, "VRAM (Dynamic, Max):") {
				parts := strings.Fields(trimmed)
				for i, p := range parts {
					if strings.EqualFold(p, "GB") && i > 0 {
						if gb, err := strconv.Atoi(parts[i-1]); err == nil {
							vramMB = gb * 1024
						}
					} else if strings.EqualFold(p, "MB") && i > 0 {
						if mb, err := strconv.Atoi(parts[i-1]); err == nil {
							vramMB = mb
						}
					}
				}
			}
		}

		if name != "" {
			return GPUInfo{
				Name:            name,
				Vendor:          NormalizeVendor(name),
				DedicatedVRAMMB: vramMB,
				IsSoftware:      false,
				IsCapable:       vramMB >= MinCapableVRAMMB,
			}
		}
	}

	return GPUInfo{
		Name:            "macOS Display Adapter",
		Vendor:          "Apple",
		DedicatedVRAMMB: 2048,
		IsSoftware:      false,
		IsCapable:       true,
	}
}

// detectLinuxGPU detects discrete GPUs on Linux using nvidia-smi or lspci.
func detectLinuxGPU() GPUInfo {
	// 1. Check NVIDIA GPU via nvidia-smi
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		line := strings.TrimSpace(string(out))
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			name := strings.TrimSpace(parts[0])
			mb, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			return GPUInfo{
				Name:            name,
				Vendor:          "NVIDIA",
				DedicatedVRAMMB: mb,
				IsSoftware:      false,
				IsCapable:       mb >= MinCapableVRAMMB,
			}
		}
	}

	// 2. Check lspci for VGA/3D controller
	lspciCmd := exec.Command("lspci")
	if out, err := lspciCmd.Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "vga compatible controller") || strings.Contains(lower, "3d controller") {
				name := line
				if idx := strings.Index(line, ": "); idx != -1 {
					name = line[idx+2:]
				}
				vendor := NormalizeVendor(name)
				isCapable := vendor == "NVIDIA" || vendor == "AMD"
				return GPUInfo{
					Name:            strings.TrimSpace(name),
					Vendor:          vendor,
					DedicatedVRAMMB: 2048,
					IsSoftware:      false,
					IsCapable:       isCapable,
				}
			}
		}
	}

	return GPUInfo{
		Name:            "Linux Display Adapter",
		Vendor:          "Unknown",
		DedicatedVRAMMB: 0,
		IsSoftware:      false,
		IsCapable:       false,
	}
}
