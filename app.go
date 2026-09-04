package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"photoslicer/engine/archive"
	"photoslicer/engine/constants"
	"photoslicer/engine/enhancer"
	"photoslicer/engine/pipeline"
	"photoslicer/engine/sorting"

	"github.com/atotto/clipboard"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var translations = map[string]map[string]string{
	"en": {
		"ready":                  "Ready to Slice",
		"app_window_title":      "PhotoSlicer v" + constants.Version,
		"paused":                "Paused...",
		"resuming":              "Resuming...",
		"idle_done":             "Done! Idle.",
		"error_folder":          "Please select a directory first.",
		"error_no_images":       "No images or subfolders found.",
		"error_valid_dir":       "Select Valid Directory!",
		"preparing":             "Preparing: %s...",
		"processing_single":     "Processing single folder...",
		"processing_multi":      "Processing %s - %d/%d...",
		"enhancer_missing":      "Enhancer not found! Ensure 'realesrgan-ncnn-vulkan.exe' is in the 'up-model' folder.",
		"enhancing_load":        "Loading %d images to AI...",
		"enhancing_run":         "Enhancing %d images...",
		"enhancing_fast_run":    "Denoising %d images (Fast CPU)...",
		"enhancing_done":        "Enhancement complete.",
		"enhancing_fail":        "Enhancement failed or skipped.",
		"error_pre_process":     "Error during image pre-processing: %s",
		"error_batch":           "Error during batch enhancement: %s",
		"skip_folder":           "Skipping %s (enhancement failed).",
		"no_images_process":     "No images found to process.",
		"no_subfolders":         "No subfolders with images found!",
		"open_folder_err":       "Could not open folder: %s",
		"path_not_exist":        "Folder path does not exist.",
		"stopping":              "Stopping...",
		"stopped":               "Stopped by user.",
		"webp_nostitch_fallback": "An image is larger than WebP's limit — stitching normally instead.",
		"error_invalid_input":   "Please enter valid numbers for width, height, and quality.",
		"error_watermark_path":  "Please select a valid PNG watermark image.",
		"error_unexpected":      "An unexpected error occurred: %s",
		"status_enhancing":      "AI Enhancing...",
		"status_denoising":      "Denoising...",
		"status_slicing":        "Slicing...",
		"status_stitching":      "Stitching...",
		"status_processing":     "Processing...",
	},
	"fa": {
		"ready":                  "آماده برای شروع",
		"app_window_title":      "فوتو اسلایسر - نسخه " + constants.Version,
		"paused":                "توقف...",
		"resuming":              "در حال ادامه...",
		"idle_done":             "تمام شد! آماده.",
		"error_folder":          "لطفا ابتدا یک پوشه انتخاب کنید.",
		"error_no_images":       "هیچ تصویر یا زیرپوشه‌ای یافت نشد.",
		"error_valid_dir":       "پوشه معتبر انتخاب کنید!",
		"preparing":             "آماده‌سازی: %s...",
		"processing_single":     "پردازش پوشه تکی...",
		"processing_multi":      "پردازش %s - %d/%d...",
		"enhancer_missing":      "فایل هوش مصنوعی یافت نشد! مطمئن شوید 'realesrgan-ncnn-vulkan.exe' در پوشه 'up-model' است.",
		"enhancing_load":        "بارگذاری %d تصویر در هوش مصنوعی...",
		"enhancing_run":         "افزایش کیفیت %d تصویر...",
		"enhancing_fast_run":    "نویزگیری سریع %d تصویر (پردازنده)...",
		"enhancing_done":        "افزایش کیفیت تکمیل شد.",
		"enhancing_fail":        "افزایش کیفیت شکست خورد.",
		"error_pre_process":     "خطا در پیش‌پردازش تصاویر: %s",
		"error_batch":           "خطا در افزایش کیفیت گروهی: %s",
		"skip_folder":           "رد کردن %s (خطا در AI).",
		"no_images_process":     "تصویری برای پردازش یافت نشد.",
		"no_subfolders":         "هیچ زیرپوشه‌ای یافت نشد!",
		"open_folder_err":       "خطا در باز کردن پوشه: %s",
		"path_not_exist":        "مسیر پوشه وجود ندارد.",
		"stopping":              "در حال توقف...",
		"stopped":               "توسط کاربر متوقف شد.",
		"webp_nostitch_fallback": "یک تصویر بزرگ‌تر از حد مجاز WebP است؛ به‌جای حالت بدون چسباندن، به‌صورت عادی چسبانده می‌شود.",
		"error_invalid_input":   "لطفاً برای عرض، ارتفاع و کیفیت عددهای معتبر وارد کنید.",
		"error_watermark_path":  "لطفاً یک تصویر واترمارک PNG معتبر انتخاب کنید.",
		"error_unexpected":      "خطای غیرمنتظره‌ای رخ داد: %s",
		"status_enhancing":      "افزایش کیفیت...",
		"status_denoising":      "نویزگیری...",
		"status_slicing":        "برش...",
		"status_stitching":      "چسباندن...",
		"status_processing":     "در حال پردازش...",
	},
}

func getMsg(key, lang string, args ...interface{}) string {
	dict, ok := translations[lang]
	if !ok {
		dict = translations["en"]
	}
	msg, ok := dict[key]
	if !ok {
		msg = key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

func formatDuration(seconds float64) string {
	s := int(math.Round(seconds))
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	minutes := s / 60
	remSec := s % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %02ds", minutes, remSec)
	}
	hours := minutes / 60
	remMin := minutes % 60
	return fmt.Sprintf("%dh %02dm", hours, remMin)
}

func calculateEta(startTime time.Time, currentPercent float64) string {
	if currentPercent <= 0 || currentPercent >= 100 {
		return ""
	}
	elapsed := time.Since(startTime).Seconds()
	totalEst := elapsed / (currentPercent / 100.0)
	rem := totalEst - elapsed
	return formatDuration(rem)
}

type App struct {
	ctx        context.Context
	settingsMu sync.Mutex
	settings   map[string]interface{}
	stateMu    sync.RWMutex
	controller *pipeline.Controller
	isBusy     int32
	startTime  time.Time
	lastOutput string
}

func (a *App) getController() *pipeline.Controller {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.controller
}

func (a *App) setController(c *pipeline.Controller) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.controller = c
}

func (a *App) getLastOutput() string {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.lastOutput
}

func (a *App) setLastOutput(out string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.lastOutput = out
}

func (a *App) getStartTime() time.Time {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.startTime
}

func (a *App) setStartTime(t time.Time) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.startTime = t
}

func NewApp() *App {
	return &App{
		settings: make(map[string]interface{}),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	wailsRuntime.WindowExecJS(ctx, fmt.Sprintf("window.__APP_VERSION__ = '%s'; if (typeof applyAppVersion === 'function') applyAppVersion('%s');", constants.Version, constants.Version))
	wailsRuntime.OnFileDrop(ctx, func(x, y int, paths []string) {
		if len(paths) > 0 {
			pathsJSON, err := json.Marshal(paths)
			if err == nil {
				wailsRuntime.WindowExecJS(ctx, fmt.Sprintf("if (typeof window.handleDroppedPaths === 'function') window.handleDroppedPaths(%s);", string(pathsJSON)))
			}
		}
	})
}

// GetAppVersion returns the current application version string.
func (a *App) GetAppVersion() string {
	return constants.Version
}

func (a *App) defaultSettings() map[string]interface{} {
	return map[string]interface{}{
		"custom_width_checked": true,
		"width":                800,
		"height_limit":         16000,
		"save_quality":         100,
		"save_format":          "JPG",
		"zip_checked":          false,
		"pdf_checked":          false,
		"cbz_checked":          false,
		"enhance_checked":      false,
		"enhance_engine":       "fast",
		"no_stitch_checked":    false,
		"selected_tab":         "process",
		"theme":                "blue",
		"language":             "fa",
		"save_location":        "",
		"save_next_to_source":  false,
		"play_sound":           true,
		"show_notification":    true,
		"thread_count":         4,
		"output_suffix":        " [Stitched]",
		"filename_pattern":     "[number]",
		"filename_digits":      3,
		"custom_theme_color":   "",
		"watermark_enabled":    false,
		"watermark_path":       "",
		"watermark_count":      1,
		"watermark_edge":       "right",
		"watermark_margin":     0,
		"presets":              []interface{}{},
		"default_preset":       nil,
	}
}

func (a *App) loadSettings() map[string]interface{} {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()

	defaults := a.defaultSettings()
	filePath := constants.GetSettingsFile()
	data, err := os.ReadFile(filePath)
	if err != nil {
		a.settings = defaults
		return a.settings
	}

	var loaded map[string]interface{}
	if err := json.Unmarshal(data, &loaded); err != nil {
		a.settings = defaults
		return a.settings
	}

	for k, v := range defaults {
		if _, exists := loaded[k]; !exists {
			loaded[k] = v
		}
	}

	if sanitizeSettingsPresets(loaded) {
		bytes, err := json.MarshalIndent(loaded, "", "    ")
		if err == nil {
			_ = os.WriteFile(filePath, bytes, 0644)
		}
	}

	a.settings = loaded
	return a.settings
}

func (a *App) saveSettingsToDisk(settings map[string]interface{}) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()

	dir := constants.GetSettingsDir()
	_ = os.MkdirAll(dir, 0755)

	filePath := constants.GetSettingsFile()

	// Retain presets if not provided
	if _, ok := settings["presets"]; !ok {
		if existing, ok := a.settings["presets"]; ok {
			settings["presets"] = existing
		}
	}

	for k, v := range settings {
		a.settings[k] = v
	}

	bytes, err := json.MarshalIndent(a.settings, "", "    ")
	if err != nil {
		return
	}
	tmpFile := fmt.Sprintf("%s.%d.tmp", filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, bytes, 0644); err != nil {
		return
	}
	_ = os.Rename(tmpFile, filePath)
}

func (a *App) execJS(js string) {
	if a.ctx != nil {
		wailsRuntime.WindowExecJS(a.ctx, js)
	}
}

func (a *App) changeProgress(percent float64) {
	pct := fmt.Sprintf("%.1f", percent)
	a.execJS(fmt.Sprintf(`
		if (document.getElementById('pr')) document.getElementById('pr').style.width = '%s%%';
		if (document.getElementById('pr-text')) document.getElementById('pr-text').textContent = '%s%%';
		if (document.getElementById('progress-percent')) document.getElementById('progress-percent').textContent = '%s%%';
	`, pct, pct, pct))
}

func (a *App) changeProgressDetail(current, total int, filename, elapsed, eta string) {
	fileJSON, _ := json.Marshal(filename)
	elapsedJSON, _ := json.Marshal(elapsed)
	etaJSON, _ := json.Marshal(eta)
	a.execJS(fmt.Sprintf(`
		if (typeof updateProgressInfo === 'function') {
			updateProgressInfo(%d, %d, %s, %s, %s);
		}
	`, current, total, string(fileJSON), string(elapsedJSON), string(etaJSON)))
}

func (a *App) updateStep(stepName string) {
	sJSON, _ := json.Marshal(stepName)
	a.execJS(fmt.Sprintf(`
		if (typeof updateStepIndicator === 'function') {
			updateStepIndicator(%s);
		}
	`, string(sJSON)))
}

func (a *App) resetProgressUI() {
	a.execJS(`if (typeof resetProgressUI === 'function') resetProgressUI();`)
}

func (a *App) changeStatusText(text string) {
	tJSON, _ := json.Marshal(text)
	a.execJS(fmt.Sprintf(`
		if (document.getElementById('status')) document.getElementById('status').textContent = %s;
		if (document.getElementById('progress-detail')) document.getElementById('progress-detail').textContent = %s;
	`, string(tJSON), string(tJSON)))
}

func (a *App) changeStatusOnly(text string) {
	tJSON, _ := json.Marshal(text)
	a.execJS(fmt.Sprintf(`
		if (document.getElementById('status')) document.getElementById('status').textContent = %s;
	`, string(tJSON)))
}

func (a *App) showError(text string, force bool) {
	tJSON, _ := json.Marshal(text)
	if force {
		a.execJS(fmt.Sprintf(`if (typeof showError === 'function') showError(%s);`, string(tJSON)))
	} else {
		a.execJS(fmt.Sprintf(`if (document.getElementById('show-notifications')?.checked !== false && typeof showError === 'function') showError(%s);`, string(tJSON)))
	}
}

func (a *App) showSuccess(text string) {
	tJSON, _ := json.Marshal(text)
	a.execJS(fmt.Sprintf(`if (document.getElementById('show-notifications')?.checked !== false && typeof showSuccess === 'function') showSuccess(%s);`, string(tJSON)))
}

func (a *App) setButtonState(state string) {
	sJSON, _ := json.Marshal(state)
	a.execJS(fmt.Sprintf(`if (typeof setButtonState === 'function') setButtonState(%s);`, string(sJSON)))
}

func (a *App) showOpenFolderButton(path string) {
	pJSON, _ := json.Marshal(path)
	a.execJS(fmt.Sprintf(`if (typeof showOpenFolderButton === 'function') showOpenFolderButton(%s);`, string(pJSON)))
}

func (a *App) playAudio(path string) {
	pJSON, _ := json.Marshal(path)
	a.execJS(fmt.Sprintf(`if (typeof playAudio === 'function') playAudio(%s);`, string(pJSON)))
}

func (a *App) clearSourceDirectory() {
	a.execJS(`if (typeof clearDirectory === 'function') clearDirectory(false);`)
}

// AppReady is called when the frontend DOM and Wails runtime are ready.
func (a *App) AppReady() {
	a.execJS(fmt.Sprintf(`if (typeof applyAppVersion === 'function') applyAppVersion('%s');`, constants.Version))
	settings := a.loadSettings()
	lang, _ := settings["language"].(string)
	if lang == "" {
		lang = "fa"
	}

	wailsRuntime.WindowSetTitle(a.ctx, getMsg("app_window_title", lang))
	a.changeStatusText(getMsg("ready", lang))
	a.applySettingsToDOM(settings)
}

func (a *App) applySettingsToDOM(settings map[string]interface{}) {
	// Create an effective copy of settings, overlaying default_preset values if configured (matching Python behavior)
	eff := make(map[string]interface{})
	for k, v := range settings {
		eff[k] = v
	}
	if defPresetName, ok := settings["default_preset"].(string); ok && defPresetName != "" {
		if presetsList, ok := settings["presets"].([]interface{}); ok {
			for _, p := range presetsList {
				if pMap, ok := p.(map[string]interface{}); ok {
					if pMap["name"] == defPresetName {
						if vals, ok := pMap["values"].(map[string]interface{}); ok {
							for k, v := range vals {
								eff[k] = v
							}
						}
						break
					}
				}
			}
		}
	}

	sJSON := marshalJSONAscii(eff)
	presetsJSON := marshalJSONAscii(settings["presets"])
	defaultPresetJSON := marshalJSONAscii(settings["default_preset"])

	js := fmt.Sprintf(`
		(function() {
			var s = %s;
			if (!s) return;

			function setVal(id, val) {
				var el = document.getElementById(id);
				if (el && val !== undefined && val !== null) el.value = val;
			}
			function setChecked(id, val) {
				var el = document.getElementById(id);
				if (el && val !== undefined && val !== null) el.checked = !!val;
			}

			setChecked('custom-width', s.custom_width_checked !== false);
			setVal('width-input', s.width || 800);
			setVal('height-input', s.height_limit || 16000);
			setVal('quality-input', s.save_quality || 100);
			setVal('format-select', (s.save_format || 'JPG').toUpperCase());
			setChecked('is-zip', s.zip_checked);
			setChecked('is-pdf', s.pdf_checked);
			setChecked('is-cbz', s.cbz_checked);
			setChecked('enhance-quality', s.enhance_checked);
			setChecked('no-stitch', s.no_stitch_checked);
			setVal('save-location-input', s.save_location || '');
			setChecked('save-next-to-source', s.save_next_to_source);
			setChecked('play-sound', s.play_sound !== false);
			setChecked('show-notifications', s.show_notification !== false);
			setVal('thread-count', s.thread_count || 4);
			setVal('output-suffix', s.output_suffix || ' [Stitched]');
			setVal('filename-pattern', s.filename_pattern || '[number]');
			setVal('filename-digits', s.filename_digits || 3);
			setVal('custom-theme-color', s.custom_theme_color || '');
			setChecked('watermark-enabled', s.watermark_enabled);
			setVal('watermark-path', s.watermark_path || '');
			setVal('watermark-count', s.watermark_count || 1);
			setVal('watermark-edge', s.watermark_edge || 'right');
			setVal('watermark-margin', s.watermark_margin || 0);
			setVal('enhance-engine-select', s.enhance_engine || 'fast');

			if (typeof setTheme === 'function') setTheme(s.theme || 'blue');
			if (s.custom_theme_color && typeof applyCustomTheme === 'function') applyCustomTheme(s.custom_theme_color);
			if (typeof setLanguage === 'function') setLanguage(s.language || 'fa');

			// Re-affirm select values after setLanguage to prevent any browser option translation reset
			setVal('enhance-engine-select', s.enhance_engine || 'fast');
			setVal('watermark-edge', s.watermark_edge || 'right');

			if (typeof refreshSaveLocationState === 'function') refreshSaveLocationState();
			if (typeof toggleWatermarkOptions === 'function') toggleWatermarkOptions();
			if (typeof refreshFilenamePreview === 'function') refreshFilenamePreview();

			if (typeof showTab === 'function') showTab(s.selected_tab || 'process');
			if (typeof syncFormatDropdown === 'function') syncFormatDropdown();
			if (typeof initPresets === 'function') initPresets(%s, %s);
		})();
	`, sJSON, presetsJSON, defaultPresetJSON)

	a.execJS(js)
}

func (a *App) SelectFolder() string {
	res, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Folder",
	})
	if err != nil {
		return ""
	}
	return res
}

func (a *App) SelectWatermarkFile() string {
	res, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Watermark PNG",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "PNG Image (*.png)", Pattern: "*.png"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return ""
	}
	return res
}

func (a *App) ExportPresets(jsonText string, suggestedFilename string) string {
	if suggestedFilename == "" {
		suggestedFilename = "photoslicer-presets.json"
	}
	res, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Export Presets",
		DefaultFilename: suggestedFilename,
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON File (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil || res == "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(res), ".json") {
		res += ".json"
	}
	if err := os.WriteFile(res, []byte(jsonText), 0644); err != nil {
		return ""
	}
	return res
}

func (a *App) ImportPresets() string {
	res, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Import Presets",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON File (*.json)", Pattern: "*.json"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil || res == "" {
		return ""
	}
	data, err := os.ReadFile(res)
	if err != nil {
		return ""
	}
	return fixPresetsJSONMojibake(string(data))
}

func (a *App) SaveSettings(settings map[string]interface{}) {
	a.saveSettingsToDisk(settings)
	if lang, ok := settings["language"].(string); ok && lang != "" {
		wailsRuntime.WindowSetTitle(a.ctx, getMsg("app_window_title", lang))
	}
}

func (a *App) PauseProcessing() {
	if ctrl := a.getController(); ctrl != nil {
		ctrl.Pause()
		settings := a.loadSettings()
		lang, _ := settings["language"].(string)
		a.changeStatusText(getMsg("paused", lang))
	}
}

func (a *App) ResumeProcessing() {
	if ctrl := a.getController(); ctrl != nil {
		ctrl.Resume()
		settings := a.loadSettings()
		lang, _ := settings["language"].(string)
		a.changeStatusText(getMsg("resuming", lang))
	}
}

func (a *App) StopProcessing() {
	if ctrl := a.getController(); ctrl != nil {
		ctrl.Stop()
		settings := a.loadSettings()
		lang, _ := settings["language"].(string)
		a.changeStatusText(getMsg("stopped", lang))
		a.setButtonState("idle")
	}
}

func (a *App) OpenFileExplorer(path string) {
	if path == "" {
		return
	}
	cleanPath := filepath.Clean(path)
	fi, err := os.Stat(cleanPath)
	if err != nil {
		return
	}

	switch runtime.GOOS {
	case "windows":
		if !fi.IsDir() {
			cmd := exec.Command("explorer", fmt.Sprintf("/select,%s", cleanPath))
			_ = cmd.Start()
			return
		}
		cmd := exec.Command("explorer", cleanPath)
		_ = cmd.Start()
	case "darwin":
		if !fi.IsDir() {
			cmd := exec.Command("open", "-R", cleanPath)
			_ = cmd.Start()
			return
		}
		cmd := exec.Command("open", cleanPath)
		_ = cmd.Start()
	default: // linux, bsd
		targetDir := cleanPath
		if !fi.IsDir() {
			targetDir = filepath.Dir(cleanPath)
		}
		cmd := exec.Command("xdg-open", targetDir)
		_ = cmd.Start()
	}
}

func (a *App) GetClipboardText() string {
	text, err := clipboard.ReadAll()
	if err != nil {
		return ""
	}
	return text
}

func (a *App) MinimizeWindow() {
	wailsRuntime.WindowMinimise(a.ctx)
}

func (a *App) CloseWindow() {
	wailsRuntime.Quit(a.ctx)
}

func (a *App) IsDirectory(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func (a *App) FolderName(path string) string {
	return filepath.Base(path)
}

// InspectDirectory provides fast pre-flight analysis of a directory or archive file.
func (a *App) InspectDirectory(path string) map[string]interface{} {
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return map[string]interface{}{"status": "empty", "mode": "", "item_count": 0, "path": ""}
	}

	fi, err := os.Stat(cleanPath)
	if err != nil {
		return map[string]interface{}{"status": "not_found", "mode": "", "item_count": 0, "path": cleanPath}
	}

	if !fi.IsDir() {
		extLower := strings.ToLower(filepath.Ext(cleanPath))
		if extLower == ".zip" || extLower == ".cbz" {
			r, err := zip.OpenReader(cleanPath)
			if err != nil {
				return map[string]interface{}{"status": "error", "mode": "", "item_count": 0, "path": cleanPath}
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
			mode := "archive_zip"
			if extLower == ".cbz" {
				mode = "archive_cbz"
			}
			if count == 0 {
				return map[string]interface{}{"status": "empty", "mode": mode, "item_count": 0, "path": cleanPath}
			}
			return map[string]interface{}{"status": "ok", "mode": mode, "item_count": count, "path": cleanPath}
		}
		return map[string]interface{}{"status": "error", "mode": "", "item_count": 0, "path": cleanPath}
	}

	// It's a directory
	directImages, _ := sorting.GetAllImagesDirectory(cleanPath)
	if len(directImages) > 0 {
		return map[string]interface{}{
			"status":     "ok",
			"mode":       "single",
			"item_count": len(directImages),
			"path":       cleanPath,
		}
	}

	// Check subfolders and archives without extracting them to disk
	entries, err := os.ReadDir(cleanPath)
	if err == nil && len(entries) > 0 {
		validFolders := 0
		for _, entry := range entries {
			fullPath := filepath.Join(cleanPath, entry.Name())
			if entry.IsDir() {
				imgs, _ := sorting.GetAllImagesDirectory(fullPath)
				if len(imgs) > 0 {
					validFolders++
				}
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext == ".zip" || ext == ".cbz" {
				count, err := archive.CountImagesInArchive(fullPath)
				if err == nil && count > 0 {
					validFolders++
				}
			}
		}
		if validFolders > 0 {
			return map[string]interface{}{
				"status":     "ok",
				"mode":       "batch",
				"item_count": validFolders,
				"path":       cleanPath,
			}
		}
	}

	return map[string]interface{}{"status": "empty", "mode": "single", "item_count": 0, "path": cleanPath}
}

// Start launches the main worker thread for single folder or batch slicing.
func (a *App) Start(params map[string]interface{}) {
	// Re-entrancy guard
	if !atomic.CompareAndSwapInt32(&a.isBusy, 0, 1) {
		return
	}

	go func() {
		defer atomic.StoreInt32(&a.isBusy, 0)
		defer archive.CleanupAllTempDirs()

		a.setLastOutput("")
		a.setStartTime(time.Now())
		a.setController(pipeline.NewController())

		checkState := func() error {
			if c := a.getController(); c != nil {
				return c.CheckState()
			}
			return nil
		}

		settings := a.loadSettings()
		lang, _ := settings["language"].(string)
		if lang == "" {
			lang = "fa"
		}

		a.execJS("resetTimer(); startTimer(); if (typeof resetProgressUI === 'function') resetProgressUI();")
		a.setButtonState("processing")

		dirAddress, _ := params["directory"].(string)
		dirAddress = strings.TrimSpace(dirAddress)

		if dirAddress == "" {
			a.showError(getMsg("error_folder", lang), true)
			a.changeStatusText(getMsg("error_valid_dir", lang))
			a.setButtonState("idle")
			a.execJS("stopTimer();")
			return
		}

		originalInputPath := dirAddress

		// Handle direct ZIP/CBZ/PDF input file
		fi, err := os.Stat(dirAddress)
		if err != nil {
			a.showError(getMsg("path_not_exist", lang), true)
			a.changeStatusText(getMsg("error_valid_dir", lang))
			a.setButtonState("idle")
			a.execJS("stopTimer();")
			return
		}

		originalIsFile := !fi.IsDir()
		var archiveTempDir string
		if originalIsFile {
			extLower := strings.ToLower(filepath.Ext(dirAddress))
			if extLower == ".zip" || extLower == ".cbz" {
				tempRoot, err := os.MkdirTemp("", "photoslicer_extract_")
				if err != nil {
					a.showError(err.Error(), true)
					a.changeStatusText(getMsg("error_valid_dir", lang))
					a.setButtonState("idle")
					a.execJS("stopTimer();")
					return
				}
				archive.RegisterTempDir(tempRoot)
				extracted, err := archive.ExtractImagesFromZip(dirAddress, tempRoot)
				if err != nil || extracted == "" {
					errMsg := getMsg("error_no_images", lang)
					if err != nil {
						errMsg = fmt.Sprintf("%s: %s", errMsg, err.Error())
					}
					a.showError(errMsg, true)
					a.changeStatusText(getMsg("error_valid_dir", lang))
					a.setButtonState("idle")
					a.execJS("stopTimer();")
					return
				}
				dirAddress = extracted
				archiveTempDir = tempRoot
			} else {
				a.showError(getMsg("error_folder", lang), true)
				a.changeStatusText(getMsg("error_valid_dir", lang))
				a.setButtonState("idle")
				a.execJS("stopTimer();")
				return
			}
		}

		// Output base directory
		saveLocation, _ := params["save_location"].(string)
		saveLocation = strings.TrimSpace(saveLocation)
		outputBase := "./Results"
		if saveLocation != "" {
			outputBase = saveLocation
		}

		saveNextSrc, _ := params["save_next_to_source"].(bool)
		outputSuffix, _ := params["output_suffix"].(string)
		outputSuffix = strings.TrimSpace(outputSuffix)
		outputSuffix = strings.ReplaceAll(outputSuffix, "..", "")
		outputSuffix = strings.ReplaceAll(outputSuffix, "/", "")
		outputSuffix = strings.ReplaceAll(outputSuffix, "\\", "")
		outputSuffix = strings.ReplaceAll(outputSuffix, ":", "")
		if outputSuffix == "" {
			outputSuffix = " [Stitched]"
		} else if !strings.HasPrefix(outputSuffix, " ") {
			outputSuffix = " " + outputSuffix
		}

		// Watermark check
		wmEnabled, _ := params["watermark_enabled"].(bool)
		wmPath, _ := params["watermark_path"].(string)
		if wmEnabled {
			if wmPath == "" {
				a.showError(getMsg("error_watermark_path", lang), true)
				a.changeStatusText(getMsg("error_valid_dir", lang))
				a.setButtonState("idle")
				a.execJS("stopTimer();")
				return
			}
			if _, err := os.Stat(wmPath); err != nil {
				a.showError(getMsg("error_watermark_path", lang), true)
				a.changeStatusText(getMsg("error_valid_dir", lang))
				a.setButtonState("idle")
				a.execJS("stopTimer();")
				return
			}
		}

		// Update UI watermark step visibility
		a.execJS(fmt.Sprintf("if (typeof setWatermarkStepVisible === 'function') setWatermarkStepVisible(%t);", wmEnabled))

		// Parse parameters
		isCustomWidth, _ := params["custom_width_checked"].(bool)
		widthVal := int(getFloatOrInt(params["width"], 800))
		heightLimitVal := int(getFloatOrInt(params["height_limit"], 16000))
		saveQualityVal := int(getFloatOrInt(params["save_quality"], 100))
		saveFormat, _ := params["save_format"].(string)
		if saveFormat == "" {
			saveFormat = "JPG"
		}
		isZip, _ := params["zip_checked"].(bool)
		isPdf, _ := params["pdf_checked"].(bool)
		isCbz, _ := params["cbz_checked"].(bool)
		isEnhance, _ := params["enhance_checked"].(bool)
		enhanceEngine, _ := params["enhance_engine"].(string)
		if enhanceEngine == "" {
			enhanceEngine = "fast"
		}
		isNoStitch, _ := params["no_stitch_checked"].(bool)
		wmCount := int(getFloatOrInt(params["watermark_count"], 1))
		wmEdge, _ := params["watermark_edge"].(string)
		if wmEdge == "" {
			wmEdge = "right"
		}
		wmMargin := int(getFloatOrInt(params["watermark_margin"], 0))
		threadCount := int(getFloatOrInt(params["thread_count"], 4))
		filenamePattern, _ := params["filename_pattern"].(string)
		if filenamePattern == "" {
			filenamePattern = "[number]"
		}
		filenameDigits := int(getFloatOrInt(params["filename_digits"], 3))

		// Bounds validation
		if saveQualityVal < 1 {
			saveQualityVal = 1
		} else if saveQualityVal > 100 {
			saveQualityVal = 100
		}
		if widthVal < 10 {
			widthVal = 10
		} else if widthVal > 30000 {
			widthVal = 30000
		}
		if heightLimitVal < 100 {
			heightLimitVal = 100
		} else if heightLimitVal > 100000 {
			heightLimitVal = 100000
		}
		if threadCount < 1 {
			threadCount = 1
		} else if threadCount > 64 {
			threadCount = 64
		}
		switch strings.ToUpper(saveFormat) {
		case "PNG", "WEBP", "PSD":
			saveFormat = strings.ToUpper(saveFormat)
		default:
			saveFormat = "JPG"
		}
		switch strings.ToLower(wmEdge) {
		case "left", "center":
			wmEdge = strings.ToLower(wmEdge)
		default:
			wmEdge = "right"
		}
		if filenameDigits < 1 {
			filenameDigits = 1
		} else if filenameDigits > 10 {
			filenameDigits = 10
		}

		// Detect mode: check if directory contains supported images directly or subfolders
		directImages, _ := sorting.GetAllImagesDirectory(dirAddress)
		isSingleMode := len(directImages) > 0

		stitchedSaveName := filepath.Base(dirAddress)
		if saveNextSrc {
			absSrc, err := filepath.Abs(originalInputPath)
			if err == nil {
				sourceParent := filepath.Dir(absSrc)
				sourceName := filepath.Base(absSrc)
				if originalIsFile {
					sourceName = strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
				}
				stitchedName := fmt.Sprintf("%s%s", sourceName, outputSuffix)
				if isSingleMode {
					outputBase = sourceParent
					stitchedSaveName = stitchedName
				} else {
					outputBase = filepath.Join(sourceParent, stitchedName)
				}
			}
		}
		_ = os.MkdirAll(outputBase, 0755)

		currentDate := time.Now().Format("2006-01-02")

		if isSingleMode {
			a.changeProgress(0)
			a.updateStep("scan")
			a.changeStatusOnly(getMsg("processing_single", lang))
			a.updateStep("process")

			processFolder := dirAddress
			if isEnhance {
				a.updateStep("process")
				if enhanceEngine == "fast" {
					a.changeStatusOnly(getMsg("enhancing_fast_run", lang, len(directImages)))
					enhancedDir, err := enhancer.RunFastEnhancement(dirAddress, threadCount, checkState, func(pct, curr, total int) {
						a.changeProgress(float64(pct))
						st := a.getStartTime()
						elapsed := time.Since(st).Seconds()
						eta := calculateEta(st, float64(pct))
						a.changeProgressDetail(curr, total, getMsg("status_denoising", lang), formatDuration(elapsed), eta)
					})
					if err != nil {
						if checkState() != nil {
							a.changeStatusText(getMsg("stopped", lang))
							a.updateStep("ready")
							a.setButtonState("idle")
							a.execJS("stopTimer();")
							return
						}
						a.showError(fmt.Sprintf("%s: %s", getMsg("enhancing_fail", lang), err.Error()), true)
						a.changeStatusText(getMsg("enhancing_fail", lang))
						a.updateStep("ready")
						a.setButtonState("idle")
						a.execJS("stopTimer();")
						return
					}
					if enhancedDir != "" {
						processFolder = enhancedDir
					}
				} else {
					exePath := enhancer.FindRealEsrganExecutable("")
					if exePath == "" {
						a.showError(getMsg("enhancer_missing", lang), true)
						a.changeStatusText(getMsg("enhancing_fail", lang))
						a.updateStep("ready")
						a.setButtonState("idle")
						a.execJS("stopTimer();")
						return
					}
					a.changeStatusOnly(getMsg("enhancing_run", lang, len(directImages)))
					enhancedDir, err := enhancer.RunRealEsrganAI(exePath, dirAddress, "", checkState, func(pct, curr, total int) {
						a.changeProgress(float64(pct))
						st := a.getStartTime()
						elapsed := time.Since(st).Seconds()
						eta := calculateEta(st, float64(pct))
						a.changeProgressDetail(curr, total, getMsg("status_enhancing", lang), formatDuration(elapsed), eta)
					})
					if err != nil {
						if checkState() != nil {
							a.changeStatusText(getMsg("stopped", lang))
							a.updateStep("ready")
							a.setButtonState("idle")
							a.execJS("stopTimer();")
							return
						}
						a.showError(fmt.Sprintf("%s: %s", getMsg("enhancing_fail", lang), err.Error()), true)
						a.changeStatusText(getMsg("enhancing_fail", lang))
						a.updateStep("ready")
						a.setButtonState("idle")
						a.execJS("stopTimer();")
						return
					}
					if enhancedDir != "" {
						processFolder = enhancedDir
					}
				}
			}

			a.updateStep("process")

			opts := pipeline.PipelineOptions{
				Mode:                  "single",
				NewWidth:              widthVal,
				IsCustomWidth:         isCustomWidth,
				SaveFormat:            saveFormat,
				SaveQuality:           saveQualityVal,
				SaveDirectory:         stitchedSaveName,
				HeightLimit:           heightLimitVal,
				CurrentDate:           currentDate,
				IsZip:                 isZip,
				IsPdf:                 isPdf,
				IsCbz:                 isCbz,
				IsNoStitch:            isNoStitch,
				OutputBase:            outputBase,
				MaxWorkers:            threadCount,
				FilenamePattern:       filenamePattern,
				FilenameDigits:        filenameDigits,
				WatermarkEnabled:      wmEnabled,
				WatermarkPath:         wmPath,
				WatermarkCount:        wmCount,
				WatermarkEdge:         wmEdge,
				WatermarkWidthPercent: 0,
				WatermarkMargin:       wmMargin,
				Controller:            a.getController(),
				ProgressCallback: func(pct float64, curr, total int, item string) {
					a.changeProgress(pct)
					st := a.getStartTime()
					elapsed := time.Since(st).Seconds()
					eta := calculateEta(st, pct)
					displayItem := item
					if displayItem == "" {
						displayItem = filepath.Base(dirAddress)
					} else {
						switch displayItem {
						case "Slicing...":
							displayItem = getMsg("status_slicing", lang)
						case "Processing...":
							displayItem = getMsg("status_processing", lang)
						case "Stitching...":
							displayItem = getMsg("status_stitching", lang)
						}
					}
					a.changeProgressDetail(curr, total, displayItem, formatDuration(elapsed), eta)
				},
				WebpFallbackCallback: func() {
					a.showError(getMsg("webp_nostitch_fallback", lang), false)
				},
			}

			resPath, err := pipeline.MergerImages(processFolder, opts)
			if err != nil {
				if checkState() != nil {
					a.changeStatusText(getMsg("stopped", lang))
					a.updateStep("ready")
					a.setButtonState("idle")
					a.execJS("stopTimer();")
					return
				}
				a.showError(err.Error(), true)
				a.changeStatusText(getMsg("error_unexpected", lang, err.Error()))
				a.updateStep("ready")
				a.setButtonState("idle")
				a.execJS("stopTimer();")
				return
			} else {
				a.updateStep("save")
				a.setLastOutput(resPath)
				a.showOpenFolderButton(resPath)
				a.changeProgress(100)
				a.updateStep("done")
				a.changeStatusText(getMsg("idle_done", lang))
				playSound, _ := params["play_sound"].(bool)
				if playSound {
					a.playAudio("success.wav")
				}
				a.showSuccess(getMsg("idle_done", lang))
				a.clearSourceDirectory()
			}
		} else {
			// Batch mode
			a.changeProgress(0)
			a.updateStep("scan")
			subfolders, _ := archive.FastScanDir(dirAddress)
			var validFolders []string
			for _, sf := range subfolders {
				imgs, _ := sorting.GetAllImagesDirectory(sf)
				if len(imgs) > 0 {
					validFolders = append(validFolders, sf)
				}
			}

			if len(validFolders) == 0 {
				a.showError(getMsg("no_subfolders", lang), true)
				a.changeStatusText(getMsg("error_valid_dir", lang))
				a.updateStep("ready")
				a.setButtonState("idle")
				a.execJS("stopTimer();")
				return
			}

			totalFolders := len(validFolders)
			var aiExePath string
			if isEnhance && enhanceEngine != "fast" {
				aiExePath = enhancer.FindRealEsrganExecutable("")
				if aiExePath == "" {
					a.showError(getMsg("enhancer_missing", lang), true)
					a.changeStatusText(getMsg("enhancing_fail", lang))
					a.updateStep("ready")
					a.setButtonState("idle")
					a.execJS("stopTimer();")
					return
				}
			}

			successCount := 0
			failCount := 0

			for idx, fld := range validFolders {
				if checkState() != nil {
					break
				}

				fldName := filepath.Base(fld)
				a.changeStatusOnly(getMsg("processing_multi", lang, fldName, idx+1, totalFolders))
				a.updateStep("process")

				processFolder := fld
				if isEnhance {
					a.updateStep("process")
					if enhanceEngine == "fast" {
						enhancedDir, err := enhancer.RunFastEnhancement(fld, threadCount, checkState, func(pct, curr, total int) {
							overallPct := (float64(idx)/float64(totalFolders))*100.0 + (float64(pct) / float64(totalFolders))
							a.changeProgress(overallPct)
							st := a.getStartTime()
							elapsed := time.Since(st).Seconds()
							eta := calculateEta(st, overallPct)
							a.changeProgressDetail(idx+1, totalFolders, fldName, formatDuration(elapsed), eta)
						})
						if err != nil {
							if checkState() != nil {
								break
							}
							a.showError(fmt.Sprintf("%s (%s): %s", getMsg("enhancing_fail", lang), fldName, err.Error()), true)
						} else if enhancedDir != "" {
							processFolder = enhancedDir
						}
					} else {
						enhancedDir, err := enhancer.RunRealEsrganAI(aiExePath, fld, "", checkState, func(pct, curr, total int) {
							overallPct := (float64(idx)/float64(totalFolders))*100.0 + (float64(pct) / float64(totalFolders))
							a.changeProgress(overallPct)
							st := a.getStartTime()
							elapsed := time.Since(st).Seconds()
							eta := calculateEta(st, overallPct)
							a.changeProgressDetail(idx+1, totalFolders, fldName, formatDuration(elapsed), eta)
						})
						if err != nil {
							if checkState() != nil {
								break
							}
							a.showError(fmt.Sprintf("%s (%s): %s", getMsg("enhancing_fail", lang), fldName, err.Error()), true)
						} else if enhancedDir != "" {
							processFolder = enhancedDir
						}
					}
				}

				a.updateStep("process")

				opts := pipeline.PipelineOptions{
					Mode:                  "multi",
					NewWidth:              widthVal,
					IsCustomWidth:         isCustomWidth,
					SaveFormat:            saveFormat,
					SaveQuality:           saveQualityVal,
					SaveDirectory:         fldName,
					HeightLimit:           heightLimitVal,
					CurrentDate:           currentDate,
					IsZip:                 isZip,
					IsPdf:                 isPdf,
					IsCbz:                 isCbz,
					IsNoStitch:            isNoStitch,
					OutputBase:            outputBase,
					MaxWorkers:            threadCount,
					FilenamePattern:       filenamePattern,
					FilenameDigits:        filenameDigits,
					WatermarkEnabled:      wmEnabled,
					WatermarkPath:         wmPath,
					WatermarkCount:        wmCount,
					WatermarkEdge:         wmEdge,
					WatermarkWidthPercent: 0,
					WatermarkMargin:       wmMargin,
					Controller:            a.getController(),
					ProgressCallback: func(pct float64, curr, total int, item string) {
						overallPct := (float64(idx)/float64(totalFolders))*100.0 + (pct / float64(totalFolders))
						a.changeProgress(overallPct)
						st := a.getStartTime()
						elapsed := time.Since(st).Seconds()
						eta := calculateEta(st, overallPct)
						a.changeProgressDetail(idx+1, totalFolders, fldName, formatDuration(elapsed), eta)
					},
				}

				resPath, err := pipeline.MergerImages(processFolder, opts)
				if err == nil {
					a.setLastOutput(resPath)
					successCount++
				} else {
					failCount++
					if checkState() != nil {
						break
					}
					a.showError(fmt.Sprintf("%s (%s): %s", getMsg("error_folder", lang), fldName, err.Error()), true)
				}
			}

			if checkState() != nil {
				a.changeStatusText(getMsg("stopped", lang))
				a.updateStep("ready")
				a.setButtonState("idle")
				a.execJS("stopTimer();")
				return
			}

			if successCount == 0 {
				a.changeStatusText(getMsg("no_images_process", lang))
				a.updateStep("ready")
				a.setButtonState("idle")
				a.execJS("stopTimer();")
				return
			}

			a.updateStep("save")
			a.changeProgress(100)
			a.updateStep("done")
			if failCount > 0 {
				a.changeStatusText(fmt.Sprintf("%s (%d/%d)", getMsg("idle_done", lang), successCount, totalFolders))
			} else {
				a.changeStatusText(getMsg("idle_done", lang))
			}

			lastOut := a.getLastOutput()
			if lastOut != "" {
				openTarget := outputBase
				dateTarget := filepath.Join(outputBase, currentDate)
				if fi, err := os.Stat(dateTarget); err == nil && fi.IsDir() {
					openTarget = dateTarget
				}
				if fi, err := os.Stat(openTarget); err == nil && fi.IsDir() {
					a.showOpenFolderButton(openTarget)
				} else {
					a.showOpenFolderButton(lastOut)
				}
			}
			playSound, _ := params["play_sound"].(bool)
			if playSound {
				a.playAudio("success.wav")
			}
			if failCount > 0 {
				a.showError(fmt.Sprintf("%d/%d %s", failCount, totalFolders, getMsg("error_folder", lang)), true)
			} else {
				a.showSuccess(getMsg("idle_done", lang))
			}
			a.clearSourceDirectory()
		}

		a.setButtonState("idle")
		a.execJS("stopTimer();")
		_ = archiveTempDir
	}()
}

func getFloatOrInt(v interface{}, def float64) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return def
	}
}
