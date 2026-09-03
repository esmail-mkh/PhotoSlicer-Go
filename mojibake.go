package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var cp1256ToByte = map[rune]byte{
	0x20AC: 0x80, 0x067E: 0x81, 0x201A: 0x82, 0x0192: 0x83, 0x201E: 0x84, 0x2026: 0x85, 0x2020: 0x86, 0x2021: 0x87,
	0x02C6: 0x88, 0x2030: 0x89, 0x0679: 0x8A, 0x2039: 0x8B, 0x0152: 0x8C, 0x0686: 0x8D, 0x0698: 0x8E, 0x0688: 0x8F,
	0x06AF: 0x90, 0x2018: 0x91, 0x2019: 0x92, 0x201C: 0x93, 0x201D: 0x94, 0x2022: 0x95, 0x2013: 0x96, 0x2014: 0x97,
	0x06A9: 0x98, 0x2122: 0x99, 0x0691: 0x9A, 0x203A: 0x9B, 0x0153: 0x9C, 0x200C: 0x9D, 0x200D: 0x9E, 0x06BA: 0x9F,
	0x00A0: 0xA0, 0x060C: 0xA1, 0x00A2: 0xA2, 0x00A3: 0xA3, 0x00A4: 0xA4, 0x00A5: 0xA5, 0x00A6: 0xA6, 0x00A7: 0xA7,
	0x00A8: 0xA8, 0x00A9: 0xA9, 0x06BE: 0xAA, 0x00AB: 0xAB, 0x00AC: 0xAC, 0x00AD: 0xAD, 0x00AE: 0xAE, 0x00AF: 0xAF,
	0x00B0: 0xB0, 0x00B1: 0xB1, 0x00B2: 0xB2, 0x00B3: 0xB3, 0x00B4: 0xB4, 0x00B5: 0xB5, 0x00B6: 0xB6, 0x00B7: 0xB7,
	0x00B8: 0xB8, 0x00B9: 0xB9, 0x061B: 0xBA, 0x00BB: 0xBB, 0x00BC: 0xBC, 0x00BD: 0xBD, 0x00BE: 0xBE, 0x061F: 0xBF,
	0x06C1: 0xC0, 0x0621: 0xC1, 0x0622: 0xC2, 0x0623: 0xC3, 0x0624: 0xC4, 0x0625: 0xC5, 0x0626: 0xC6, 0x0627: 0xC7,
	0x0628: 0xC8, 0x0629: 0xC9, 0x062A: 0xCA, 0x062B: 0xCB, 0x062C: 0xCC, 0x062D: 0xCD, 0x062E: 0xCE, 0x062F: 0xCF,
	0x0630: 0xD0, 0x0631: 0xD1, 0x0632: 0xD2, 0x0633: 0xD3, 0x0634: 0xD4, 0x0635: 0xD5, 0x0636: 0xD6, 0x00D7: 0xD7,
	0x0637: 0xD8, 0x0638: 0xD9, 0x0639: 0xDA, 0x063A: 0xDB, 0x0640: 0xDC, 0x0641: 0xDD, 0x0642: 0xDE, 0x0643: 0xDF,
	0x00E0: 0xE0, 0x0644: 0xE1, 0x00E2: 0xE2, 0x0645: 0xE3, 0x0646: 0xE4, 0x0647: 0xE5, 0x0648: 0xE6, 0x00E7: 0xE7,
	0x00E8: 0xE8, 0x00E9: 0xE9, 0x00EA: 0xEA, 0x00EB: 0xEB, 0x0649: 0xEC, 0x064A: 0xED, 0x00EE: 0xEE, 0x00EF: 0xEF,
	0x064B: 0xF0, 0x064C: 0xF1, 0x064D: 0xF2, 0x064E: 0xF3, 0x00F4: 0xF4, 0x064F: 0xF5, 0x0650: 0xF6, 0x00F7: 0xF7,
	0x0651: 0xF8, 0x00F9: 0xF9, 0x0652: 0xFA, 0x00FB: 0xFB, 0x00FC: 0xFC, 0x200E: 0xFD, 0x200F: 0xFE, 0x06D2: 0xFF,
}

// FixCP1256Mojibake repairs UTF-8 bytes that were improperly decoded as Windows-1256 in Python.
func FixCP1256Mojibake(s string) string {
	if !strings.ContainsAny(s, "\u0637\u0638\u0639\u063a") {
		return s
	}

	bytes := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			bytes = append(bytes, byte(r))
		} else if b, ok := cp1256ToByte[r]; ok {
			bytes = append(bytes, b)
		} else {
			return s // Encountered a rune that doesn't exist in CP-1256; leave unchanged
		}
	}

	if utf8.Valid(bytes) {
		decoded := string(bytes)
		if decoded != s {
			return decoded
		}
	}
	return s
}

// sanitizeSettingsPresets repairs mojibake in presets and default_preset, and backfills missing fields.
func sanitizeSettingsPresets(settings map[string]interface{}) bool {
	modified := false
	if presetsRaw, ok := settings["presets"].([]interface{}); ok {
		for _, p := range presetsRaw {
			if pMap, ok := p.(map[string]interface{}); ok {
				if name, ok := pMap["name"].(string); ok {
					fixed := FixCP1256Mojibake(name)
					if fixed != name {
						pMap["name"] = fixed
						modified = true
					}
				}
				if vals, ok := pMap["values"].(map[string]interface{}); ok {
					if _, hasEng := vals["enhance_engine"]; !hasEng {
						vals["enhance_engine"] = "fast"
						modified = true
					}
				}
			}
		}
	}
	if defRaw, ok := settings["default_preset"].(string); ok && defRaw != "" {
		fixed := FixCP1256Mojibake(defRaw)
		if fixed != defRaw {
			settings["default_preset"] = fixed
			modified = true
		}
	}
	return modified
}

// fixPresetsJSONMojibake repairs mojibake in raw imported JSON text.
func fixPresetsJSONMojibake(jsonText string) string {
	var val interface{}
	if err := json.Unmarshal([]byte(jsonText), &val); err != nil {
		return jsonText
	}

	modified := false
	switch v := val.(type) {
	case []interface{}:
		for _, item := range v {
			if pMap, ok := item.(map[string]interface{}); ok {
				if name, ok := pMap["name"].(string); ok {
					fixed := FixCP1256Mojibake(name)
					if fixed != name {
						pMap["name"] = fixed
						modified = true
					}
				}
				if vals, ok := pMap["values"].(map[string]interface{}); ok {
					if _, hasEng := vals["enhance_engine"]; !hasEng {
						vals["enhance_engine"] = "fast"
						modified = true
					}
				}
			}
		}
	case map[string]interface{}:
		if name, ok := v["name"].(string); ok {
			fixed := FixCP1256Mojibake(name)
			if fixed != name {
				v["name"] = fixed
				modified = true
			}
		}
		if vals, ok := v["values"].(map[string]interface{}); ok {
			if _, hasEng := vals["enhance_engine"]; !hasEng {
				vals["enhance_engine"] = "fast"
				modified = true
			}
		}
		if presets, ok := v["presets"].([]interface{}); ok {
			for _, item := range presets {
				if pMap, ok := item.(map[string]interface{}); ok {
					if name, ok := pMap["name"].(string); ok {
						fixed := FixCP1256Mojibake(name)
						if fixed != name {
							pMap["name"] = fixed
							modified = true
						}
					}
					if vals, ok := pMap["values"].(map[string]interface{}); ok {
						if _, hasEng := vals["enhance_engine"]; !hasEng {
							vals["enhance_engine"] = "fast"
							modified = true
						}
					}
				}
			}
		}
	}

	if modified {
		reEncoded, err := json.Marshal(val)
		if err == nil {
			return string(reEncoded)
		}
	}
	return jsonText
}

// marshalJSONAscii encodes values to JSON while escaping non-ASCII runes to \uXXXX.
// This prevents any OS or WebView2 codepage corruption during script injection.
func marshalJSONAscii(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	var buf strings.Builder
	for _, r := range string(b) {
		if r < 128 {
			buf.WriteRune(r)
		} else if r <= 0xFFFF {
			buf.WriteString(fmt.Sprintf("\\u%04x", r))
		} else {
			r1, r2 := utf16.EncodeRune(r)
			buf.WriteString(fmt.Sprintf("\\u%04x\\u%04x", r1, r2))
		}
	}
	return buf.String()
}
