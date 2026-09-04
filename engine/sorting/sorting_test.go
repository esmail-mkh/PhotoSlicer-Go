package sorting

import (
	"reflect"
	"testing"
)

func TestSortKeyImproved(t *testing.T) {
	t.Run("NaturalSortHandlesMultiDigitNumbers", func(t *testing.T) {
		input := []string{"page10.jpg", "page2.jpg", "page1.jpg"}
		expected := []string{"page1.jpg", "page2.jpg", "page10.jpg"}
		actual := SortKeyImproved(input)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	})

	t.Run("SuppliedChapterKeepsSinglePagesBetweenTwoLevelPages", func(t *testing.T) {
		expected := []string{
			"001__001.jpg", "001__002.jpg",
			"002__001.jpg", "002__002.jpg",
			"003__001.jpg", "003__002.jpg", "004.webp",
			"005__001.jpg", "005__002.jpg",
			"006__001.jpg", "006__002.jpg",
			"007__001.jpg", "007__002.jpg", "008.webp",
			"009__001.jpg", "009__002.jpg", "010.webp",
			"011__001.jpg", "011__002.jpg", "012.webp",
			"013__001.jpg", "013__002.jpg",
			"014__001.jpg", "014__002.jpg",
			"015__001.jpg", "015__002.jpg", "016.webp",
			"017__001.jpg", "017__002.jpg",
			"018__001.jpg", "018__002.jpg",
			"019__001.jpg", "019__002.jpg", "020.webp",
			"021__001.jpg", "021__002.jpg", "022.webp",
			"023__001.jpg", "023__002.jpg", "024.webp",
			"025__001.jpg", "025__002.jpg", "026.webp",
			"027__001.jpg", "027__002.jpg", "028.webp",
			"029__001.jpg", "029__002.jpg",
			"030__001.jpg", "030__002.jpg",
			"031__001.jpg", "031__002.jpg", "032.webp",
		}
		// Reverse input
		rev := make([]string, len(expected))
		for i := range expected {
			rev[i] = expected[len(expected)-1-i]
		}
		actual := SortKeyImproved(rev)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	})

	t.Run("SeparatorPatternDoesNotGetGlobalPriority", func(t *testing.T) {
		input := []string{"episode 12__3.jpg", "episode 1.jpg"}
		expected := []string{"episode 1.jpg", "episode 12__3.jpg"}
		actual := SortKeyImproved(input)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	})

	t.Run("DeterministicForCaseAndZeroPaddingTies", func(t *testing.T) {
		input := []string{"Page01.jpg", "page1.jpg", "page001.jpg"}
		expected := []string{"page1.jpg", "Page01.jpg", "page001.jpg"}
		actual := SortKeyImproved(input)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	})

	t.Run("UnicodeDigitsNaturalSort", func(t *testing.T) {
		input := []string{"page ۱۰b.jpg", "page ۲a.jpg", "page ۱a.jpg"}
		expected := []string{"page ۱a.jpg", "page ۲a.jpg", "page ۱۰b.jpg"}
		actual := SortKeyImproved(input)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}

		// Zero padding with Persian digits vs 3-digit English: 2-digit Persian ۰۱ should sort before 3-digit 001
		inputPad := []string{"page 001.jpg", "page ۰۱.jpg"}
		expectedPad := []string{"page ۰۱.jpg", "page 001.jpg"}
		actualPad := SortKeyImproved(inputPad)
		if !reflect.DeepEqual(actualPad, expectedPad) {
			t.Errorf("Expected %v, got %v", expectedPad, actualPad)
		}
	})

	t.Run("ExtensionBreaksSameStemTie", func(t *testing.T) {
		input := []string{"page2.png", "page10.jpg", "page2.jpg", "page1.webp"}
		expected := []string{"page1.webp", "page2.jpg", "page2.png", "page10.jpg"}
		actual := SortKeyImproved(input)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	})

	t.Run("OriginalStemSpellingBreaksTiesBeforeExtension", func(t *testing.T) {
		input := []string{"Page1.png", "page1.jpg"}
		expected := []string{"Page1.png", "page1.jpg"}
		actual := SortKeyImproved(input)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	})

	t.Run("SupplementaryUnicodeDigits", func(t *testing.T) {
		// Mathematical Bold Digit Zero U+1D7CE is in Nd and > 0xFFFF
		r, ok := runeToDecimalDigit(0x1D7CE)
		if !ok || r != '0' {
			t.Errorf("Expected '0', got %c (ok: %v)", r, ok)
		}
	})
}
