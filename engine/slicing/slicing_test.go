package slicing

import (
	"reflect"
	"testing"
)

func TestSlicing(t *testing.T) {
	t.Run("FormatFilenamePlaceholders", func(t *testing.T) {
		pattern := "[folder]_[number]_page"
		result := FormatFilename(pattern, 5, 3, "jpg", "Chapter1", 10)
		expected := "Chapter1_005_page.jpg"
		if result != expected {
			t.Errorf("Expected %s, got %s", expected, result)
		}
	})

	t.Run("CapSliceGapsLimitsExcessiveHeights", func(t *testing.T) {
		cuts := []int{0, 5000, 15000}
		capped := CapSliceGaps(cuts, 4000)
		expected := []int{0, 4000, 5000, 9000, 13000, 15000}
		if !reflect.DeepEqual(capped, expected) {
			t.Errorf("Expected %v, got %v", expected, capped)
		}
	})
}
