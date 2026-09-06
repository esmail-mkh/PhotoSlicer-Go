package enhancer

import (
	"strings"
)

// MinCapableVRAMMB is the minimum dedicated video memory (in MB) recommended for
// running Real-ESRGAN AI upscaling smoothly without out-of-memory errors.
// Set to 1800 MB to safely accommodate standard 2GB (2048 MB) GPUs after OS/driver reservations.
const MinCapableVRAMMB = 1800

// GPUInfo contains detected graphics adapter specifications.
type GPUInfo struct {
	Name            string `json:"name"`
	Vendor          string `json:"vendor"`
	DedicatedVRAMMB int    `json:"dedicated_vram_mb"`
	IsSoftware      bool   `json:"is_software"`
	IsCapable       bool   `json:"is_capable"`
}

// IsCapableGPU determines if the detected GPU has sufficient dedicated hardware
// resources (e.g. >= ~2GB dedicated VRAM and not a software fallback driver).
func IsCapableGPU(info GPUInfo) bool {
	if info.IsSoftware {
		return false
	}
	if info.DedicatedVRAMMB >= MinCapableVRAMMB {
		return true
	}
	return false
}

// NormalizeVendor detects common GPU vendor brands from adapter name or vendor string.
func NormalizeVendor(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "nvidia") || strings.Contains(lower, "geforce") || strings.Contains(lower, "quadro") || strings.Contains(lower, "rtx") || strings.Contains(lower, "gtx"):
		return "NVIDIA"
	case strings.Contains(lower, "amd") || strings.Contains(lower, "radeon") || strings.Contains(lower, "ati"):
		return "AMD"
	case strings.Contains(lower, "intel") || strings.Contains(lower, "arc") || strings.Contains(lower, "iris"):
		return "Intel"
	case strings.Contains(lower, "apple"):
		return "Apple"
	default:
		return "Unknown"
	}
}

// GetOptimalEnhanceEngine evaluates the host hardware and presence of the
// Real-ESRGAN binary to recommend the best enhancement engine.
// Returns "realesrgan" if a capable GPU (>= 2GB VRAM) and the binary are available,
// otherwise returns "fast".
func GetOptimalEnhanceEngine() (engine string, info GPUInfo) {
	info = DetectPrimaryGPU()
	hasExe := FindRealEsrganExecutable("") != ""

	if info.IsCapable && hasExe {
		return "realesrgan", info
	}
	return "fast", info
}
