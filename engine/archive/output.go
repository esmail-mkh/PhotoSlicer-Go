package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"photoslicer/engine/sorting"
)

// ListOutputFiles treats directory names literally, including square brackets.
func ListOutputFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no output files in %s", dir)
	}
	return sorting.SortKeyImproved(files), nil
}
