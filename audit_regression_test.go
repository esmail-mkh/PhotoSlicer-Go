package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNullSettingsRecovery(t *testing.T) {
	for _, backup := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "backup"}[backup], func(t *testing.T) {
			a := NewApp()
			a.settingsPathOverride = filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(a.settingsPathOverride, []byte("null"), 0600); err != nil {
				t.Fatal(err)
			}
			if backup {
				if err := os.WriteFile(a.settingsPathOverride+".bak", []byte(`{"language":"en","watermark_margin":19,"presets":[{"name":"پریست","values":{"width":1234}}]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got := a.loadSettings()
			if got == nil || got["language"] == nil {
				t.Fatal("missing settings")
			}
			if backup && (got["language"] != "en" || got["watermark_margin"] != float64(19) || !hasValidPresets(got)) {
				t.Fatalf("backup not preserved: %v", got)
			}
		})
	}
}

func TestSettingsSnapshotIsolation(t *testing.T) {
	a := NewApp()
	a.settingsPathOverride = filepath.Join(t.TempDir(), "settings.json")
	input := map[string]interface{}{"language": "en", "presets": []interface{}{map[string]interface{}{"name": "اصل", "values": map[string]interface{}{"width": float64(800)}}}}
	a.saveSettingsToDisk(input)
	input["presets"].([]interface{})[0].(map[string]interface{})["name"] = "changed input"
	a.saveSettingsToDisk(map[string]interface{}{"language": "en"})
	snapshot := a.loadSettings()
	snapshot["presets"].([]interface{})[0].(map[string]interface{})["values"].(map[string]interface{})["width"] = float64(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			a.saveSettingsToDisk(map[string]interface{}{"language": "fa"})
		}
	}()
	for i := 0; i < 100000; i++ {
		if snapshot["language"] != "en" {
			t.Error("snapshot changed")
			break
		}
	}
	<-done
	got := a.loadSettings()["presets"].([]interface{})[0].(map[string]interface{})
	if got["name"] != "اصل" || got["values"].(map[string]interface{})["width"] != float64(800) {
		t.Fatalf("preset mutated: %v", got)
	}
}
