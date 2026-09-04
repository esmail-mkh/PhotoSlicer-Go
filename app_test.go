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
}
