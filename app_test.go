package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectDirectory(t *testing.T) {
	app := NewApp()

	// 1. Not found
	res := app.InspectDirectory("non_existent_folder_xyz_123")
	if res["status"] != "not_found" {
		t.Errorf("expected not_found, got %v", res["status"])
	}

	// 2. Single folder with image
	tempDir := t.TempDir()
	img1 := filepath.Join(tempDir, "01.jpg")
	_ = os.WriteFile(img1, []byte("fake"), 0644)

	res = app.InspectDirectory(tempDir)
	if res["status"] != "ok" || res["mode"] != "single" || res["item_count"] != 1 {
		t.Errorf("expected ok single 1, got %v", res)
	}

	// 3. Batch folder with subfolders
	batchDir := t.TempDir()
	sub1 := filepath.Join(batchDir, "Chapter 1")
	_ = os.MkdirAll(sub1, 0755)
	_ = os.WriteFile(filepath.Join(sub1, "01.jpg"), []byte("fake"), 0644)
	sub2 := filepath.Join(batchDir, "Chapter 2")
	_ = os.MkdirAll(sub2, 0755)
	_ = os.WriteFile(filepath.Join(sub2, "02.jpg"), []byte("fake"), 0644)

	res = app.InspectDirectory(batchDir)
	if res["status"] != "ok" || res["mode"] != "batch" || res["item_count"] != 2 {
		t.Errorf("expected ok batch 2, got %v", res)
	}

	// 4. Archive file (.cbz)
	cbzPath := filepath.Join(tempDir, "comic.cbz")
	zf, err := os.Create(cbzPath)
	if err != nil {
		t.Fatalf("failed to create cbz: %v", err)
	}
	w := zip.NewWriter(zf)
	f, _ := w.Create("page01.jpg")
	_, _ = f.Write([]byte("fake"))
	_ = w.Close()
	_ = zf.Close()

	res = app.InspectDirectory(cbzPath)
	if res["status"] != "ok" || res["mode"] != "archive_cbz" || res["item_count"] != 1 {
		t.Errorf("expected ok archive_cbz 1, got %v", res)
	}

	// 5. Batch folder containing archives
	batchArchivesDir := t.TempDir()
	ch1Zip := filepath.Join(batchArchivesDir, "Chapter 1.zip")
	zf1, _ := os.Create(ch1Zip)
	w1 := zip.NewWriter(zf1)
	f1, _ := w1.Create("01.jpg")
	_, _ = f1.Write([]byte("fake"))
	_ = w1.Close()
	_ = zf1.Close()

	res = app.InspectDirectory(batchArchivesDir)
	if res["status"] != "ok" || res["mode"] != "batch" || res["item_count"] != 1 {
		t.Errorf("expected ok batch 1 for folder with archive chapters, got %v", res)
	}
}

func TestSaveSettingsToDiskAtomic(t *testing.T) {
	app := NewApp()
	tempFile := filepath.Join(t.TempDir(), "settings.json")
	app.settingsPathOverride = tempFile

	settings := map[string]interface{}{
		"language": "en",
		"width":    float64(1200),
	}
	app.saveSettingsToDisk(settings)
	loaded := app.loadSettings()
	if loaded["language"] != "en" {
		t.Errorf("expected language 'en', got %v", loaded["language"])
	}
	if w, ok := loaded["width"].(float64); !ok || w != 1200 {
		t.Errorf("expected width 1200, got %v", loaded["width"])
	}
}

func TestOpenFileExplorer(t *testing.T) {
	app := NewApp()
	// Should return early safely with empty or non-existent path
	app.OpenFileExplorer("")
	app.OpenFileExplorer("non_existent_path_xyz")
}

func TestAutoRecoverMissingSettingsFileFromBak(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	settingsFile := filepath.Join(tempDir, "settings.json")
	bakFile := settingsFile + ".bak"
	app.settingsPathOverride = settingsFile

	bakContent := `{
		"language": "fa",
		"width": 800,
		"custom_theme_color": "#ff0011",
		"presets": [
			{"name": "Preset 1", "values": {"width": 800}}
		]
	}`
	if err := os.WriteFile(bakFile, []byte(bakContent), 0644); err != nil {
		t.Fatalf("failed to write bak file: %v", err)
	}

	loaded := app.loadSettings()
	if loaded["width"] != float64(800) {
		t.Errorf("expected width 800 recovered from bak, got %v", loaded["width"])
	}
	if loaded["custom_theme_color"] != "#ff0011" {
		t.Errorf("expected theme color #ff0011, got %v", loaded["custom_theme_color"])
	}
	presets, ok := loaded["presets"].([]interface{})
	if !ok || len(presets) != 1 {
		t.Fatalf("expected 1 preset recovered from bak, got %v", presets)
	}

	// Verify that settings.json was recreated on disk
	if _, err := os.Stat(settingsFile); os.IsNotExist(err) {
		t.Errorf("expected settings.json to be recreated from bak, but not found")
	}
}

func TestAutoRecoverCorruptedJSONFromBak(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	settingsFile := filepath.Join(tempDir, "settings.json")
	bakFile := settingsFile + ".bak"
	app.settingsPathOverride = settingsFile

	// Corrupted primary file
	if err := os.WriteFile(settingsFile, []byte("{not valid json..."), 0644); err != nil {
		t.Fatalf("failed to write corrupt settings file: %v", err)
	}

	bakContent := `{
		"language": "fa",
		"width": 850,
		"presets": [
			{"name": "Preset Recovered", "values": {"width": 850}}
		]
	}`
	if err := os.WriteFile(bakFile, []byte(bakContent), 0644); err != nil {
		t.Fatalf("failed to write bak file: %v", err)
	}

	loaded := app.loadSettings()
	if loaded["width"] != float64(850) {
		t.Errorf("expected width 850 recovered from bak, got %v", loaded["width"])
	}
	presets, ok := loaded["presets"].([]interface{})
	if !ok || len(presets) != 1 {
		t.Fatalf("expected preset recovered from bak, got %v", presets)
	}
}

func TestAutoRecoverEmptyPresetsFromBak(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	settingsFile := filepath.Join(tempDir, "settings.json")
	bakFile := settingsFile + ".bak"
	app.settingsPathOverride = settingsFile

	// Primary has empty presets (wiped)
	primaryContent := `{
		"language": "fa",
		"width": 800,
		"presets": []
	}`
	if err := os.WriteFile(settingsFile, []byte(primaryContent), 0644); err != nil {
		t.Fatalf("failed to write primary settings file: %v", err)
	}

	bakContent := `{
		"language": "fa",
		"width": 800,
		"custom_theme_color": "#ff0011",
		"watermark_path": "logo.png",
		"watermark_enabled": true,
		"presets": [
			{"name": "Persian Preset", "values": {"width": 800}}
		]
	}`
	if err := os.WriteFile(bakFile, []byte(bakContent), 0644); err != nil {
		t.Fatalf("failed to write bak file: %v", err)
	}

	loaded := app.loadSettings()
	presets, ok := loaded["presets"].([]interface{})
	if !ok || len(presets) != 1 {
		t.Fatalf("expected presets restored from bak, got %v", presets)
	}
	if loaded["custom_theme_color"] != "#ff0011" {
		t.Errorf("expected theme color restored, got %v", loaded["custom_theme_color"])
	}
	if loaded["watermark_path"] != "logo.png" {
		t.Errorf("expected watermark_path restored, got %v", loaded["watermark_path"])
	}
}

func TestDoNotOverwriteValidBakWithEmptyPresets(t *testing.T) {
	tempDir := t.TempDir()
	app := NewApp()
	settingsFile := filepath.Join(tempDir, "settings.json")
	bakFile := settingsFile + ".bak"
	app.settingsPathOverride = settingsFile

	// Create valid backup
	bakContent := `{
		"language": "fa",
		"presets": [
			{"name": "Protected Preset", "values": {"width": 800}}
		]
	}`
	if err := os.WriteFile(bakFile, []byte(bakContent), 0644); err != nil {
		t.Fatalf("failed to write bak file: %v", err)
	}

	// Try saving settings with empty presets
	app.saveSettingsToDisk(map[string]interface{}{
		"width":   float64(900),
		"presets": []interface{}{},
	})

	// Check that backup file was NOT overwritten with empty presets
	bakData, err := os.ReadFile(bakFile)
	if err != nil {
		t.Fatalf("failed to read bak file: %v", err)
	}
	if !strings.Contains(string(bakData), "Protected Preset") {
		t.Errorf("expected backup to retain 'Protected Preset', but it was wiped: %s", string(bakData))
	}
}

func TestGPUAutoDetectionAndIsolation(t *testing.T) {
	app := NewApp()
	tempFile := filepath.Join(t.TempDir(), "settings.json")
	app.settingsPathOverride = tempFile

	// 1. GetGPUInfo
	info := app.GetGPUInfo()
	if info == nil {
		t.Fatal("expected non-nil GPUInfo map")
	}
	if _, ok := info["name"]; !ok {
		t.Error("expected name key in GPUInfo")
	}

	// 2. AutoDetectEnhanceEngine with isolated settings
	res := app.AutoDetectEnhanceEngine()
	engine, ok := res["engine"].(string)
	if !ok || (engine != "realesrgan" && engine != "fast") {
		t.Errorf("expected engine to be 'realesrgan' or 'fast', got %v", res["engine"])
	}

	// 3. Verify settings saved in isolated temp file
	loaded := app.loadSettings()
	if loaded["enhance_engine"] != engine {
		t.Errorf("expected enhance_engine to be saved as %s, got %v", engine, loaded["enhance_engine"])
	}
	if loaded["gpu_detected"] != true {
		t.Errorf("expected gpu_detected to be true, got %v", loaded["gpu_detected"])
	}
}

func TestDefaultSettingsDynamicEnhanceEngine(t *testing.T) {
	app := NewApp()
	defaults := app.defaultSettings()
	engine, ok := defaults["enhance_engine"].(string)
	if !ok || (engine != "realesrgan" && engine != "fast") {
		t.Errorf("expected default enhance_engine to be 'realesrgan' or 'fast', got %v", defaults["enhance_engine"])
	}
}


