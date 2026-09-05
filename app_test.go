package main

import (
	"archive/zip"
	"os"
	"path/filepath"
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

