package constants

import (
	"os"
	"path/filepath"
)

const (
	Version           = "5.2"
	WebPMaxDimension  = 16383
	JpegMaxDimension  = 65500
	DefaultWidth      = 800
	DefaultHeight     = 16000
	DefaultQuality    = 100
	MinEnhanceHeight  = 32
	MaxEnhanceHeight  = 30000
)

var SupportedExtensions = map[string]bool{
	"jpg":  true,
	"jpeg": true,
	"png":  true,
	"webp": true,
	"avif": true,
	"psd":  true,
}

func GetSettingsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "Settings")
	}
	return filepath.Join(home, "Documents", "EMKH_Apps", "PhotoSlicer")
}

func GetSettingsFile() string {
	return filepath.Join(GetSettingsDir(), "settings.json")
}

func GetLogFile() string {
	return filepath.Join(GetSettingsDir(), "photoslicer_error.log")
}
