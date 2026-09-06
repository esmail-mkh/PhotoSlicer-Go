package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "wails.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

func main() {
	root := findProjectRoot()
	constFile := filepath.Join(root, "engine", "constants", "constants.go")
	constBytes, err := os.ReadFile(constFile)
	if err != nil {
		fmt.Printf("Warning: cannot read %s: %v\n", constFile, err)
		return
	}

	var version string
	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) != "" {
		version = strings.TrimPrefix(strings.TrimSpace(os.Args[1]), "v")
		reVer := regexp.MustCompile(`(Version\s*=\s*)"[^"]+"`)
		newConst := reVer.ReplaceAll(constBytes, []byte(fmt.Sprintf(`${1}"%s"`, version)))
		if err := os.WriteFile(constFile, newConst, 0644); err != nil {
			fmt.Printf("Error writing to %s: %v\n", constFile, err)
			return
		}
		fmt.Printf("Updated %s to version %s\n", constFile, version)
	} else {
		reVer := regexp.MustCompile(`Version\s*=\s*"([^"]+)"`)
		m := reVer.FindSubmatch(constBytes)
		if len(m) < 2 {
			fmt.Println("Warning: Version constant not found in constants.go")
			return
		}
		version = strings.TrimSpace(string(m[1]))
		fmt.Printf("Syncing from constants.go: %s\n", version)
	}

	// 2. Read and update wails.json
	wailsFile := filepath.Join(root, "wails.json")
	wailsBytes, err := os.ReadFile(wailsFile)
	if err == nil {
		var wailsMap map[string]interface{}
		if err := json.Unmarshal(wailsBytes, &wailsMap); err == nil {
			targetOutput := fmt.Sprintf("PhotoSlicer v%s", version)
			changed := false

			if wailsMap["outputfilename"] != targetOutput {
				wailsMap["outputfilename"] = targetOutput
				changed = true
			}

			if info, ok := wailsMap["info"].(map[string]interface{}); ok {
				prodVer := version
				if strings.Count(prodVer, ".") == 1 {
					prodVer = prodVer + ".0"
				}
				if info["productVersion"] != prodVer {
					info["productVersion"] = prodVer
					changed = true
				}
			}

			if changed {
				newWails, err := json.MarshalIndent(wailsMap, "", "  ")
				if err == nil {
					_ = os.WriteFile(wailsFile, append(newWails, '\n'), 0644)
					fmt.Println("wails.json updated successfully.")
				}
			} else {
				fmt.Println("wails.json already up-to-date.")
			}
		}
	}

	// 3. Read and update build/windows/info.json
	infoPath := filepath.Join(root, "build", "windows", "info.json")
	infoBytes, err := os.ReadFile(infoPath)
	if err == nil {
		var infoMap map[string]interface{}
		if err := json.Unmarshal(infoBytes, &infoMap); err == nil {
			parts := strings.Split(version, ".")
			for len(parts) < 4 {
				parts = append(parts, "0")
			}
			quadVer := strings.Join(parts[:4], ".")

			infoChanged := false
			if fixed, ok := infoMap["fixed"].(map[string]interface{}); ok {
				if fixed["file_version"] != quadVer || fixed["product_version"] != quadVer {
					fixed["file_version"] = quadVer
					fixed["product_version"] = quadVer
					infoChanged = true
				}
			}

			if infoSec, ok := infoMap["info"].(map[string]interface{}); ok {
				for _, langSec := range infoSec {
					if langMap, ok := langSec.(map[string]interface{}); ok {
						if langMap["FileVersion"] != quadVer || langMap["ProductVersion"] != quadVer {
							langMap["FileVersion"] = quadVer
							langMap["ProductVersion"] = quadVer
							infoChanged = true
						}
					}
				}
			}

			if infoChanged {
				newInfo, err := json.MarshalIndent(infoMap, "", "\t")
				if err == nil {
					_ = os.WriteFile(infoPath, append(newInfo, '\n'), 0644)
					fmt.Println("build/windows/info.json updated successfully.")
				}
			} else {
				fmt.Println("build/windows/info.json already up-to-date.")
			}
		}
	}
}
