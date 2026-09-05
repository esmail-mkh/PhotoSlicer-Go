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

var invalidArchiveChars = regexp.MustCompile(`[\x00-\x1f\\/:*?"<>|]`)

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

	if err := w.Close(); err != nil {
		return err
	}
	_ = zipFile.Sync()
	if err := zipFile.Close(); err != nil {
		return err
	}

	NotifyShellChange(outputPath)

	return nil
}

func CreateCbz(outputPath string, files []string) error {
	return CreateZip(outputPath, files)
}

func sanitizeArchiveComponent(component string) string {
	component = norm.NFKC.String(component)
	component = strings.TrimSpace(component)
	component = strings.TrimRight(component, ". ")
	component = invalidArchiveChars.ReplaceAllString(component, "_")
	if component == "" {
		return "_"
	}
	return component
}

const (
	MaxArchiveFiles         = 5000
	MaxArchiveFileSizeBytes = 500 * 1024 * 1024      // 500 MB per file
	MaxArchiveTotalBytes    = 2 * 1024 * 1024 * 1024 // 2 GB total uncompressed
)

// CountImagesInArchive inspects the ZIP/CBZ archive headers and counts valid images without extracting them to disk.
func CountImagesInArchive(archivePath string) (int, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, err
	}
	defer r.Close()

	count := 0
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		cleanName := filepath.ToSlash(f.Name)
		if strings.Contains(cleanName, "__MACOSX") || strings.HasPrefix(filepath.Base(cleanName), ".") {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(cleanName), "."))
		if constants.SupportedExtensions[ext] {
			count++
		}
	}
	return count, nil
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
	var totalExtractedBytes int64

	for _, f := range r.File {
		if extractedCount >= MaxArchiveFiles {
			return "", fmt.Errorf("archive contains too many files (limit: %d)", MaxArchiveFiles)
		}

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

		if len(validParts) == 0 {
			continue
		}

		// Skip hidden files, macOS metadata, and system trash
		hasHidden := false
		for _, p := range validParts {
			if strings.HasPrefix(p, ".") || p == "__MACOSX" {
				hasHidden = true
				break
			}
		}
		if hasHidden {
			continue
		}

		rawName := validParts[len(validParts)-1]
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

		limitReader := io.LimitReader(rc, MaxArchiveFileSizeBytes+1)

		dst, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			continue
		}

		n, err := io.Copy(dst, limitReader)
		dst.Close()
		rc.Close()

		if err != nil {
			_ = os.Remove(destPath)
			continue
		}

		if n > MaxArchiveFileSizeBytes {
			_ = os.Remove(destPath)
			return "", fmt.Errorf("file %s exceeds maximum allowed size (500 MB)", rawName)
		}

		totalExtractedBytes += n
		if totalExtractedBytes > MaxArchiveTotalBytes {
			return "", fmt.Errorf("total extracted archive size exceeds 2 GB limit")
		}

		extractedCount++
	}

	if extractedCount == 0 {
		return "", fmt.Errorf("no valid images found in zip archive")
	}

	return outputDir, nil
}

// FastScanDir scans subdirectories and extracts ZIP/CBZ files into temporary directories.
func FastScanDir(dirname string) ([]string, error) {
	entries, err := os.ReadDir(dirname)
	if err != nil {
		return nil, err
	}

	var tempRoot string
	var result []string

	for _, entry := range entries {
		fullPath := filepath.Join(dirname, entry.Name())
		if entry.IsDir() {
			result = append(result, fullPath)
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".zip" || ext == ".cbz" {
			if tempRoot == "" {
				tempRoot, err = os.MkdirTemp("", "photoslicer_extract_")
				if err == nil {
					RegisterTempDir(tempRoot)
				}
			}
			if tempRoot != "" {
				extracted, err := ExtractImagesFromZip(fullPath, tempRoot)
				if err == nil && extracted != "" {
					result = append(result, extracted)
				}
			}
		}
	}

	return sorting.SortKeyImproved(result), nil
}
