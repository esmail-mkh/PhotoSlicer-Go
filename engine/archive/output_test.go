package archive

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRejectsMissingInputs(t *testing.T) {
	dir := t.TempDir()
	for _, files := range [][]string{nil, {filepath.Join(dir, "missing.png")}, {dir}} {
		if err := CreateZip(filepath.Join(dir, "result.zip"), files); err == nil {
			t.Fatalf("expected error for inputs %v", files)
		}
	}
}

func TestListOutputFilesErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ListOutputFiles(dir); err == nil {
		t.Fatal("empty directory accepted")
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ListOutputFiles(dir); err == nil {
		t.Fatal("directory without files accepted")
	}
	if _, err := ListOutputFiles(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing directory accepted")
	}
}
