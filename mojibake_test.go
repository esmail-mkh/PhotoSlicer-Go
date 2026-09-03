package main

import (
	"testing"
)

func TestFixCP1256Mojibake(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "\u0639\u2020\u0637\u00b3\u0637\u00a8\u0637\u00a7\u0638\u2020\u0637\u00af\u0638\u2020 \u0637\u00b9\u0637\u00a7\u0637\u00af\u063a\u0152",
			expected: "چسباندن عادی",
		},
		{
			input:    "\u0639\u2020\u0637\u00b3\u0637\u00a8\u0637\u00a7\u0638\u2020\u0637\u00af\u0638\u2020 \u0637\u00ae\u0637\u00a7\u0637\u00b1\u0637\u00ac\u063a\u0152 \u0638\u02c6\u0637\u00a8 \u0638\u00be\u063a\u0152",
			expected: "چسباندن خارجی وب پی",
		},
		{
			input:    "Standard Preset",
			expected: "Standard Preset",
		},
		{
			input:    "پریست من",
			expected: "پریست من",
		},
	}

	for _, tc := range tests {
		result := FixCP1256Mojibake(tc.input)
		if result != tc.expected {
			t.Errorf("FixCP1256Mojibake(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestMarshalJSONAscii(t *testing.T) {
	data := map[string]string{
		"name": "چسباندن عادی",
	}
	out := marshalJSONAscii(data)
	expected := `{"name":"\u0686\u0633\u0628\u0627\u0646\u062f\u0646 \u0639\u0627\u062f\u06cc"}`
	if out != expected {
		t.Errorf("marshalJSONAscii(%v) = %q, expected %q", data, out, expected)
	}
}

func TestSanitizeSettingsPresets(t *testing.T) {
	settings := map[string]interface{}{
		"presets": []interface{}{
			map[string]interface{}{
				"name": "\u0639\u2020\u0637\u00b3\u0637\u00a8\u0637\u00a7\u0638\u2020\u0637\u00af\u0638\u2020 \u0637\u00b9\u0637\u00a7\u0637\u00af\u063a\u0152",
				"values": map[string]interface{}{
					"width": 800,
				},
			},
		},
		"default_preset": "\u0639\u2020\u0637\u00b3\u0637\u00a8\u0637\u00a7\u0638\u2020\u0637\u00af\u0638\u2020 \u0637\u00b9\u0637\u00a7\u0637\u00af\u063a\u0152",
	}

	modified := sanitizeSettingsPresets(settings)
	if !modified {
		t.Errorf("expected sanitizeSettingsPresets to return true")
	}

	presetList := settings["presets"].([]interface{})
	pMap := presetList[0].(map[string]interface{})
	if pMap["name"] != "چسباندن عادی" {
		t.Errorf("expected preset name 'چسباندن عادی', got %v", pMap["name"])
	}
	vals := pMap["values"].(map[string]interface{})
	if vals["enhance_engine"] != "fast" {
		t.Errorf("expected enhance_engine to be backfilled with 'fast', got %v", vals["enhance_engine"])
	}
	if settings["default_preset"] != "چسباندن عادی" {
		t.Errorf("expected default_preset 'چسباندن عادی', got %v", settings["default_preset"])
	}
}
