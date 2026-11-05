package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"photoslicer/engine/constants"
	"photoslicer/engine/sorting"

	"golang.org/x/text/unicode/norm"
)

var invalidArchiveChars = regexp.MustCompile(`[\x00-\x1f\\/:*?"<>|]`)

func CreateZip(outputPath string, files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("no files provided for archive")
	}
	zipFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	defer w.Close()

	for _, file := range files {
		fi, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("cannot stat archive input %s: %w", file, err)
		}
		if fi.IsDir() {
			return fmt.Errorf("archive input is a directory: %s", file)
		}

		header, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		header.Name = filepath.Base(file)
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("cannot open archive input %s: %w", file, err)
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
	if err := zipFile.Sync(); err != nil {
		return err
	}
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

// zipImageEntry is one supported image inside a ZIP/CBZ, with its cleaned path.
type zipImageEntry struct {
	file  *zip.File
	parts []string
}

// compareArchivePaths orders archive entries folder by folder, so every page
// of "Ch1/" comes before "Ch2/" even when both folders hold a 001.jpg.
func compareArchivePaths(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := sorting.CompareNatural(a[i], b[i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

// ExtractImagesFromZip extracts the supported images of a ZIP/CBZ, including
// those in subfolders, into one flat chapter folder. Each file gets a number
// prefix that keeps the page order of the archive's folders, so pages from
// different subfolders never interleave.
// Returns the path to the directory containing extracted images, or an error.
func ExtractImagesFromZip(zipPath string, extractBaseDir string) (output string, err error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var entries []zipImageEntry
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		var parts []string
		for _, p := range strings.Split(filepath.ToSlash(f.Name), "/") {
			if p != "" && p != "." && p != ".." {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			continue
		}

		// Skip hidden files, macOS metadata, and system trash
		hidden := false
		for _, p := range parts {
			if strings.HasPrefix(p, ".") || p == "__MACOSX" {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(parts[len(parts)-1]), "."))
		if !constants.SupportedExtensions[ext] {
			continue
		}
		entries = append(entries, zipImageEntry{file: f, parts: parts})
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("no valid images found in zip archive")
	}
	if len(entries) > MaxArchiveFiles {
		return "", fmt.Errorf("archive contains too many files (limit: %d)", MaxArchiveFiles)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return compareArchivePaths(entries[i].parts, entries[j].parts) < 0
	})

	// An owned parent keeps archives with the same name (like 1.zip and 1.cbz)
	// from extracting into one shared folder. The chapter folder inside it
	// keeps the archive's name, which becomes the output name.
	if err := os.MkdirAll(extractBaseDir, 0755); err != nil {
		return "", err
	}
	root, err := os.MkdirTemp(extractBaseDir, "photoslicer_zip_")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			SafeRmtreeTemp(root)
		}
	}()

	folderName := strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath))
	if folderName == "" {
		folderName = "_"
	}
	outputDir := filepath.Join(root, folderName)
	if err := os.Mkdir(outputDir, 0755); err != nil {
		return "", err
	}

	width := len(strconv.Itoa(len(entries)))
	if width < 4 {
		width = 4
	}

	var extractedCount int
	var totalExtractedBytes int64

	for i, entry := range entries {
		rawName := entry.parts[len(entry.parts)-1]
		filename := fmt.Sprintf("%0*d_%s", width, i+1, sanitizeArchiveComponent(rawName))
		destPath := filepath.Join(outputDir, filename)

		rc, err := entry.file.Open()
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

// ScanFailure is a ZIP/CBZ/PDF in a batch folder that could not be read.
type ScanFailure struct {
	Name string
	Err  error
}

func (f ScanFailure) Error() string {
	return fmt.Sprintf("%s: %v", f.Name, f.Err)
}

// FastScanDir scans subdirectories and extracts ZIP/CBZ/PDF inputs into
// temporary directories. Any archive that cannot be read is an error.
func FastScanDir(dirname string) ([]string, error) {
	dirs, failures, err := ScanBatchDir(dirname, nil)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		return nil, failures[0]
	}
	return dirs, nil
}

// ScanBatchDir lists the chapter folders of a batch folder and extracts its
// ZIP/CBZ/PDF files into temporary folders. An archive that cannot be read is
// reported in failures, and the rest of the batch still goes ahead; err is
// only for a folder that cannot be read or a stop from checkState.
//
// Chapters are ordered by their source names, so same-name chapters (a folder
// "1", then "1.cbz", then "1.zip") always come in the same order.
func ScanBatchDir(dirname string, checkState func() error) (dirs []string, failures []ScanFailure, err error) {
	entries, err := os.ReadDir(dirname)
	if err != nil {
		return nil, nil, err
	}

	type chapter struct {
		source string
		path   string
	}
	var chapters []chapter
	var tempRoot string

	for _, entry := range entries {
		if checkState != nil {
			if err := checkState(); err != nil {
				return nil, nil, err
			}
		}
		if entry.IsDir() {
			chapters = append(chapters, chapter{entry.Name(), filepath.Join(dirname, entry.Name())})
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".zip" && ext != ".cbz" && ext != ".pdf" {
			continue
		}
		if tempRoot == "" {
			tempRoot, err = os.MkdirTemp("", "photoslicer_extract_")
			if err != nil {
				return nil, nil, err
			}
			RegisterTempDir(tempRoot)
		}
		extracted, err := ExtractInputFile(filepath.Join(dirname, entry.Name()), tempRoot, checkState)
		if err != nil {
			if checkState != nil {
				if stopErr := checkState(); stopErr != nil {
					return nil, nil, stopErr
				}
			}
			failures = append(failures, ScanFailure{Name: entry.Name(), Err: err})
			continue
		}
		chapters = append(chapters, chapter{entry.Name(), extracted})
	}

	sort.SliceStable(chapters, func(i, j int) bool {
		return sorting.CompareNatural(chapters[i].source, chapters[j].source) < 0
	})
	for _, c := range chapters {
		dirs = append(dirs, c.path)
	}
	return dirs, failures, nil
}
