package sorting

import (
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"photoslicer/engine/constants"

	"golang.org/x/text/unicode/norm"
)

type tokenType int

const (
	tokenNumber tokenType = 0
	tokenText   tokenType = 1
)

type sortToken struct {
	kind       tokenType
	numVal     *big.Int
	numLen     int
	digitsText string
	textVal    string
}

func compareTokens(a, b []sortToken) int {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	for i := 0; i < minLen; i++ {
		ta, tb := a[i], b[i]
		if ta.kind != tb.kind {
			if ta.kind < tb.kind {
				return -1
			}
			return 1
		}

		if ta.kind == tokenNumber {
			cmp := ta.numVal.Cmp(tb.numVal)
			if cmp != 0 {
				return cmp
			}
			if ta.numLen != tb.numLen {
				if ta.numLen < tb.numLen {
					return -1
				}
				return 1
			}
			if ta.digitsText != tb.digitsText {
				if ta.digitsText < tb.digitsText {
					return -1
				}
				return 1
			}
		} else {
			if ta.textVal != tb.textVal {
				if ta.textVal < tb.textVal {
					return -1
				}
				return 1
			}
		}
	}

	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

func normalizeSortText(s string) string {
	nfkc := norm.NFKC.String(s)
	// Unicode case fold
	var b strings.Builder
	for _, r := range nfkc {
		// unicode.ToLower handles casefold well for sorting
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func runeToDecimalDigit(r rune) (rune, bool) {
	if r >= '0' && r <= '9' {
		return r, true
	}
	if unicode.Is(unicode.Nd, r) {
		// In Unicode, all Nd ranges consist of groups of 10 consecutive digits 0..9
		if r <= 0xFFFF {
			for _, r16 := range unicode.Nd.R16 {
				if uint16(r) >= r16.Lo && uint16(r) <= r16.Hi {
					val := (r - rune(r16.Lo)) % 10
					return '0' + rune(val), true
				}
			}
		} else {
			for _, r32 := range unicode.Nd.R32 {
				if uint32(r) >= r32.Lo && uint32(r) <= r32.Hi {
					val := (r - rune(r32.Lo)) % 10
					return '0' + rune(val), true
				}
			}
		}
	}
	return 0, false
}

func naturalSortTokens(value string) []sortToken {
	normalized := normalizeSortText(value)
	var tokens []sortToken

	runes := []rune(normalized)
	n := len(runes)
	i := 0

	for i < n {
		r := runes[i]
		if _, ok := runeToDecimalDigit(r); ok {
			start := i
			for i < n {
				if _, ok2 := runeToDecimalDigit(runes[i]); ok2 {
					i++
				} else {
					break
				}
			}
			var decimalDigits strings.Builder
			for _, dr := range runes[start:i] {
				digitRune, _ := runeToDecimalDigit(dr)
				decimalDigits.WriteRune(digitRune)
			}

			dStr := decimalDigits.String()
			bigInt := new(big.Int)
			bigInt.SetString(dStr, 10)
			tokens = append(tokens, sortToken{
				kind:       tokenNumber,
				numVal:     bigInt,
				numLen:     i - start,
				digitsText: dStr,
			})
		} else {
			start := i
			for i < n {
				if _, ok := runeToDecimalDigit(runes[i]); !ok {
					i++
				} else {
					break
				}
			}
			tokens = append(tokens, sortToken{
				kind:    tokenText,
				textVal: string(runes[start:i]),
			})
		}
	}

	return tokens
}

// CompareNatural implements the 10-tuple comparison of sort_key_improved from Python:
// 1. _natural_sort_tokens(stem)
// 2. _normalize_sort_text(stem)
// 3. stem
// 4. _natural_sort_tokens(extension[1:])
// 5. _normalize_sort_text(extension[1:])
// 6. extension[1:]
// 7. _normalize_sort_text(basename)
// 8. basename
// 9. _normalize_sort_text(path_text)
// 10. path_text
func CompareNatural(filePathA, filePathB string) int {
	baseA := filepath.Base(filePathA)
	baseB := filepath.Base(filePathB)

	extA := filepath.Ext(baseA)
	extB := filepath.Ext(baseB)

	stemA := strings.TrimSuffix(baseA, extA)
	stemB := strings.TrimSuffix(baseB, extB)

	subExtA := ""
	if len(extA) > 1 {
		subExtA = extA[1:]
	}
	subExtB := ""
	if len(extB) > 1 {
		subExtB = extB[1:]
	}

	// 1. _natural_sort_tokens(stem)
	c := compareTokens(naturalSortTokens(stemA), naturalSortTokens(stemB))
	if c != 0 {
		return c
	}

	// 2. _normalize_sort_text(stem)
	normStemA := normalizeSortText(stemA)
	normStemB := normalizeSortText(stemB)
	if normStemA != normStemB {
		if normStemA < normStemB {
			return -1
		}
		return 1
	}

	// 3. stem
	if stemA != stemB {
		if stemA < stemB {
			return -1
		}
		return 1
	}

	// 4. _natural_sort_tokens(extension[1:])
	c = compareTokens(naturalSortTokens(subExtA), naturalSortTokens(subExtB))
	if c != 0 {
		return c
	}

	// 5. _normalize_sort_text(extension[1:])
	normExtA := normalizeSortText(subExtA)
	normExtB := normalizeSortText(subExtB)
	if normExtA != normExtB {
		if normExtA < normExtB {
			return -1
		}
		return 1
	}

	// 6. extension[1:]
	if subExtA != subExtB {
		if subExtA < subExtB {
			return -1
		}
		return 1
	}

	// 7. _normalize_sort_text(basename)
	normBaseA := normalizeSortText(baseA)
	normBaseB := normalizeSortText(baseB)
	if normBaseA != normBaseB {
		if normBaseA < normBaseB {
			return -1
		}
		return 1
	}

	// 8. basename
	if baseA != baseB {
		if baseA < baseB {
			return -1
		}
		return 1
	}

	// 9. _normalize_sort_text(path_text)
	normPathA := normalizeSortText(filePathA)
	normPathB := normalizeSortText(filePathB)
	if normPathA != normPathB {
		if normPathA < normPathB {
			return -1
		}
		return 1
	}

	// 10. path_text
	if filePathA != filePathB {
		if filePathA < filePathB {
			return -1
		}
		return 1
	}

	return 0
}

func SortKeyImproved(files []string) []string {
	result := make([]string, len(files))
	copy(result, files)
	sort.Slice(result, func(i, j int) bool {
		return CompareNatural(result[i], result[j]) < 0
	})
	return result
}

// GetAllImagesDirectory finds and returns naturally-sorted file paths of all supported images in a directory.
// Supports JPG, JPEG, PNG, WEBP, AVIF, and PSD formats, ignoring files starting with dot.
func GetAllImagesDirectory(imagesPath string) ([]string, error) {
	entries, err := os.ReadDir(imagesPath)
	if err != nil {
		return nil, err
	}

	var validPaths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if constants.SupportedExtensions[ext] {
			validPaths = append(validPaths, filepath.Join(imagesPath, name))
		}
	}

	return SortKeyImproved(validPaths), nil
}
