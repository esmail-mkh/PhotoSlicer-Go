package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"photoslicer/engine/constants"
	"photoslicer/engine/sorting"

	"golang.org/x/text/unicode/norm"
)

func CreateZip(outputPath string, files []string) error {
	zipFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	defer w.Close()

	for _, file := range files {
		fi, err := os.Stat(file)
		if err != nil || fi.IsDir() {
			continue
		}

		header, err := zip.FileInfoHeader(fi)
		if err != nil {
			continue
		}
		header.Name = filepath.Base(file)
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		f, err := os.Open(file)
		if err != nil {
			continue
		}
		_, err = io.Copy(writer, f)
		f.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func CreateCbz(outputPath string, files []string) error {
	return CreateZip(outputPath, files)
}

func sanitizeArchiveComponent(component string) string {
	component = norm.NFKC.String(component)
	component = strings.TrimSpace(component)
	component = strings.TrimRight(component, ". ")
	re := regexp.MustCompile(`[\x00-\x1f\\/:*?"<>|]`)
	component = re.ReplaceAllString(component, "_")
	if component == "" {
		return "_"
	}
	return component
}

// ExtractImagesFromZip extracts supported images that are directly in the ZIP archive root.
// Returns the path to the directory containing extracted images, or an error.
func ExtractImagesFromZip(zipPath string, extractBaseDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	folderName := strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath))
	outputDir := filepath.Join(extractBaseDir, folderName)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}

	var extractedCount int

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		cleanName := filepath.ToSlash(f.Name)
		parts := strings.Split(cleanName, "/")
		var validParts []string
		for _, p := range parts {
			if p != "" && p != "." && p != ".." {
				validParts = append(validParts, p)
			}
		}

		if len(validParts) != 1 || strings.HasPrefix(validParts[0], ".") {
			continue
		}

		rawName := validParts[0]
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rawName), "."))
		if !constants.SupportedExtensions[ext] {
			continue
		}

		filename := sanitizeArchiveComponent(rawName)
		destPath := filepath.Join(outputDir, filename)

		counter := 0
		stem := strings.TrimSuffix(filename, filepath.Ext(filename))
		suffix := filepath.Ext(filename)
		for {
			if _, err := os.Stat(destPath); os.IsNotExist(err) {
				break
			}
			counter++
			destPath = filepath.Join(outputDir, fmt.Sprintf("%s_%d%s", stem, counter, suffix))
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}

		dst, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			continue
		}

		_, err = io.Copy(dst, rc)
		dst.Close()
		rc.Close()

		if err == nil {
			extractedCount++
		}
	}

	if extractedCount == 0 {
		return "", fmt.Errorf("no valid images found in root of zip")
	}

	return outputDir, nil
}

// FastScanDir scans subdirectories and extracts ZIP/CBZ/PDF files into temporary directories.
func FastScanDir(dirname string) ([]string, error) {
	entries, err := os.ReadDir(dirname)
	if err != nil {
		return nil, err
	}

	tempRoot, err := os.MkdirTemp("", "photoslicer_extract_")
	if err == nil {
		RegisterTempDir(tempRoot)
	}

	var result []string

	for _, entry := range entries {
		fullPath := filepath.Join(dirname, entry.Name())
		if entry.IsDir() {
			result = append(result, fullPath)
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if (ext == ".zip" || ext == ".cbz") && tempRoot != "" {
			extracted, err := ExtractImagesFromZip(fullPath, tempRoot)
			if err == nil && extracted != "" {
				result = append(result, extracted)
			}
		}
	}

	return sorting.SortKeyImproved(result), nil
}
