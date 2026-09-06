package enhancer

import (
	"runtime"
	"testing"
)

func TestIsCapableGPU(t *testing.T) {
	tests := []struct {
		name     string
		info     GPUInfo
		expected bool
	}{
		{
			name: "Dedicated NVIDIA RTX 3070 8GB",
			info: GPUInfo{
				Name:            "NVIDIA GeForce RTX 3070",
				Vendor:          "NVIDIA",
				DedicatedVRAMMB: 8018,
				IsSoftware:      false,
			},
			expected: true,
		},
		{
			name: "Dedicated GTX 1050 2GB (above threshold)",
			info: GPUInfo{
				Name:            "NVIDIA GeForce GTX 1050",
				Vendor:          "NVIDIA",
				DedicatedVRAMMB: 2048,
				IsSoftware:      false,
			},
			expected: true,
		},
		{
			name: "Dedicated GTX 750 Ti 2GB (with driver reservation 1900MB)",
			info: GPUInfo{
				Name:            "NVIDIA GeForce GTX 750 Ti",
				Vendor:          "NVIDIA",
				DedicatedVRAMMB: 1900,
				IsSoftware:      false,
			},
			expected: true,
		},
		{
			name: "Integrated Intel UHD 630 128MB",
			info: GPUInfo{
				Name:            "Intel(R) UHD Graphics 630",
				Vendor:          "Intel",
				DedicatedVRAMMB: 128,
				IsSoftware:      false,
			},
			expected: false,
		},
		{
			name: "Microsoft Basic Render Driver (software emulator)",
			info: GPUInfo{
				Name:            "Microsoft Basic Render Driver",
				Vendor:          "Unknown",
				DedicatedVRAMMB: 8000,
				IsSoftware:      true,
			},
			expected: false,
		},
		{
			name: "Low spec legacy GPU 1GB",
			info: GPUInfo{
				Name:            "NVIDIA GeForce GT 710",
				Vendor:          "NVIDIA",
				DedicatedVRAMMB: 1024,
				IsSoftware:      false,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsCapableGPU(tt.info)
			if got != tt.expected {
				t.Errorf("IsCapableGPU() for %s = %v, expected %v", tt.name, got, tt.expected)
			}
		})
	}
}

func TestNormalizeVendor(t *testing.T) {
	cases := map[string]string{
		"NVIDIA GeForce RTX 4090":      "NVIDIA",
		"AMD Radeon RX 7900 XTX":       "AMD",
		"Intel(R) Arc(TM) A770 Graphics": "Intel",
		"Apple M2 Max":                 "Apple",
		"Generic VGA":                  "Unknown",
	}

	for input, expected := range cases {
		got := NormalizeVendor(input)
		if got != expected {
			t.Errorf("NormalizeVendor(%q) = %q, expected %q", input, got, expected)
		}
	}
}

func TestDetectPrimaryGPU(t *testing.T) {
	info := DetectPrimaryGPU()
	t.Logf("Detected primary GPU: Name=%q, Vendor=%q, VRAM=%dMB, IsCapable=%v, Software=%v",
		info.Name, info.Vendor, info.DedicatedVRAMMB, info.IsCapable, info.IsSoftware)

	if runtime.GOOS == "windows" {
		if info.Name == "" {
			t.Errorf("expected detected GPU name on Windows, got empty")
		}
	}
}

func TestGetOptimalEnhanceEngine(t *testing.T) {
	engine, info := GetOptimalEnhanceEngine()
	t.Logf("Optimal engine: %s (GPU: %s, VRAM: %dMB)", engine, info.Name, info.DedicatedVRAMMB)

	if engine != "realesrgan" && engine != "fast" {
		t.Errorf("expected engine to be 'realesrgan' or 'fast', got %s", engine)
	}

	hasExe := FindRealEsrganExecutable("") != ""
	if !hasExe && engine == "realesrgan" {
		t.Errorf("engine cannot be 'realesrgan' when executable is missing")
	}
}
