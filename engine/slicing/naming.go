package slicing

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var invalidFilenameChars = regexp.MustCompile(`[\\/:*?"<>|]`)

// FormatFilename formats slice filenames with placeholder substitutions:
// [number] - zero-padded slice index
// [folder] - folder name
// [date]   - current date in YYYY-MM-DD
// [total]  - total number of slices
func FormatFilename(pattern string, number int, digits int, extension string, folderName string, total int) string {
	if digits < 1 {
		digits = 1
	}
	if digits > 6 {
		digits = 6
	}

	padFmt := fmt.Sprintf("%%0%dd", digits)
	padded := fmt.Sprintf(padFmt, number)

	name := pattern
	if strings.Contains(name, "[number]") {
		name = strings.ReplaceAll(name, "[number]", padded)
	} else {
		name = fmt.Sprintf("%s_%s", name, padded)
	}

	if strings.Contains(name, "[folder]") {
		name = strings.ReplaceAll(name, "[folder]", folderName)
	}
	if strings.Contains(name, "[date]") {
		today := time.Now().Format("2006-01-02")
		name = strings.ReplaceAll(name, "[date]", today)
	}
	if strings.Contains(name, "[total]") {
		name = strings.ReplaceAll(name, "[total]", fmt.Sprintf("%d", total))
	}

	// Clean invalid filesystem characters
	name = invalidFilenameChars.ReplaceAllString(name, "_")
	extLower := strings.ToLower(strings.TrimPrefix(extension, "."))
	return fmt.Sprintf("%s.%s", name, extLower)
}
