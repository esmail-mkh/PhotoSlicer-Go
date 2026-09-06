package updater

import (
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected []int
	}{
		{"5.3", []int{5, 3, 0}},
		{"v5.3.0", []int{5, 3, 0}},
		{"V5.3.1", []int{5, 3, 1}},
		{"v5.4", []int{5, 4, 0}},
		{"5.4.2.1", []int{5, 4, 2, 1}},
		{"v5.3.0-beta.1", []int{5, 3, 0}},
		{"5.3.0+build123", []int{5, 3, 0}},
		{"", []int{0, 0, 0}},
	}

	for _, tc := range tests {
		res := ParseVersion(tc.input)
		if len(res) != len(tc.expected) {
			t.Fatalf("ParseVersion(%q) returned len %d, expected %d", tc.input, len(res), len(tc.expected))
		}
		for i := range res {
			if res[i] != tc.expected[i] {
				t.Errorf("ParseVersion(%q)[%d] = %d; expected %d", tc.input, i, res[i], tc.expected[i])
			}
		}
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		latest   string
		current  string
		expected bool
	}{
		// Same version
		{"v5.3.0", "5.3", false},
		{"5.3", "5.3", false},
		{"v5.3.0", "5.3.0", false},

		// Newer versions
		{"v5.3.1", "5.3", true},
		{"v5.4", "5.3", true},
		{"v5.4.0", "5.3.0", true},
		{"v6.0.0", "5.3", true},
		{"v5.3.0.1", "5.3.0", true},

		// Older versions
		{"v5.2.9", "5.3", false},
		{"v4.9.9", "5.3", false},
		{"v5.3.0", "5.3.1", false},
	}

	for _, tc := range tests {
		res := IsNewerVersion(tc.latest, tc.current)
		if res != tc.expected {
			t.Errorf("IsNewerVersion(%q, %q) = %v; expected %v", tc.latest, tc.current, res, tc.expected)
		}
	}
}

func TestIsInternetConnected(t *testing.T) {
	// Simple sanity test - shouldn't panic or hang
	_ = IsInternetConnected()
}
