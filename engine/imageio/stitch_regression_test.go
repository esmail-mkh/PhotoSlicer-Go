package imageio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStitchRejectsCorruptPages(t *testing.T) {
	for _, headerOnly := range []bool{false, true} {
		name := "invalid header"
		if headerOnly {
			name = "invalid pixels"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			good := filepath.Join(root, "1.png")
			bad := filepath.Join(root, "2.png")
			if err := createTestPng(good, 10, 10); err != nil {
				t.Fatal(err)
			}
			data := []byte("broken")
			if headerOnly {
				var err error
				data, err = os.ReadFile(good)
				if err != nil {
					t.Fatal(err)
				}
				data = data[:33]
			}
			if err := os.WriteFile(bad, data, 0600); err != nil {
				t.Fatal(err)
			}
			if headerOnly {
				if _, _, err := GetImageSizeFast(bad); err != nil {
					t.Fatalf("fixture header must be readable: %v", err)
				}
			}
			img, err := GetConcatVOptimized([]string{good, bad}, 10, true, 2)
			if err == nil || img != nil || !strings.Contains(err.Error(), bad) {
				t.Fatalf("expected filename error and no partial output; image=%v err=%v", img, err)
			}
		})
	}
}
