# UI/UX Enhancements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Pre-flight Inspection Badge, Contextual Control Dimming, and Glassmorphic Tooltips with micro-interactions in PhotoSlicer Go.

**Architecture:** 
- A lightweight Go backend method `InspectDirectory(path string)` analyzes folder/archive contents in <10ms and returns classification + item counts.
- `wails-bridge.js` exposes this to the frontend.
- `script.js` coordinates live inspection, contextual dimming of dependent inputs, and bilingual tooltip rendering in `styles.css` and `index.html`.

**Tech Stack:** Go 1.25+, Wails v2, Vanilla JS, CSS3 Glassmorphism.

## Global Constraints

- **CRITICAL:** Do NOT use `go build` to compile the app. Always use `wails build` (or `wails dev`). Standard `go test` and `go test -race` are used for unit tests.
- Support both Persian (`fa`, RTL) and English (`en`, LTR) without layout breaking or flipped numbers.
- Grid layout must remain stable during control dimming (no jarring shifts).
- All strings passed from Go backend to JS runtime must be escaped safely.

---

### Task 1: Backend Pre-flight Inspection API

**Files:**
- Modify: `app.go:590-605`
- Test: `app_test.go` (new)

**Interfaces:**
- Consumes: `sorting.GetAllImagesDirectory`, `archive.FastScanDir`, `constants.SupportedExtensions`
- Produces: `(a *App) InspectDirectory(path string) map[string]interface{}` returning:
  `{"status": "ok"|"empty"|"not_found"|"error", "mode": "single"|"batch"|"archive_zip"|"archive_cbz", "item_count": int, "path": string}`

- [ ] **Step 1: Write failing test in `app_test.go`**

```go
package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDirectory(t *testing.T) {
	app := NewApp()

	// 1. Not found
	res := app.InspectDirectory("non_existent_folder_xyz_123")
	if res["status"] != "not_found" {
		t.Errorf("expected not_found, got %v", res["status"])
	}

	// 2. Single folder with image
	tempDir := t.TempDir()
	img1 := filepath.Join(tempDir, "01.jpg")
	_ = os.WriteFile(img1, []byte("fake"), 0644)

	res = app.InspectDirectory(tempDir)
	if res["status"] != "ok" || res["mode"] != "single" || res["item_count"] != 1 {
		t.Errorf("expected ok single 1, got %v", res)
	}

	// 3. Batch folder with subfolders
	batchDir := t.TempDir()
	sub1 := filepath.Join(batchDir, "Chapter 1")
	_ = os.MkdirAll(sub1, 0755)
	_ = os.WriteFile(filepath.Join(sub1, "01.jpg"), []byte("fake"), 0644)
	sub2 := filepath.Join(batchDir, "Chapter 2")
	_ = os.MkdirAll(sub2, 0755)
	_ = os.WriteFile(filepath.Join(sub2, "02.jpg"), []byte("fake"), 0644)

	res = app.InspectDirectory(batchDir)
	if res["status"] != "ok" || res["mode"] != "batch" || res["item_count"] != 2 {
		t.Errorf("expected ok batch 2, got %v", res)
	}

	// 4. Archive file (.cbz)
	cbzPath := filepath.Join(tempDir, "comic.cbz")
	zf, _ := os.Create(cbzPath)
	w := zip.NewWriter(zf)
	f, _ := w.Create("page01.jpg")
	_, _ = f.Write([]byte("fake"))
	_ = w.Close()
	_ = zf.Close()

	res = app.InspectDirectory(cbzPath)
	if res["status"] != "ok" || res["mode"] != "archive_cbz" || res["item_count"] != 1 {
		t.Errorf("expected ok archive_cbz 1, got %v", res)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./... -run TestInspectDirectory`  
Expected: FAIL (`app.InspectDirectory undefined`)

- [ ] **Step 3: Implement `InspectDirectory` in `app.go`**

```go
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
				ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(f.Name), "."))
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

	// Check subfolders
	subfolders, err := archive.FastScanDir(cleanPath)
	if err == nil && len(subfolders) > 0 {
		validFolders := 0
		for _, sf := range subfolders {
			imgs, _ := sorting.GetAllImagesDirectory(sf)
			if len(imgs) > 0 {
				validFolders++
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./... -run TestInspectDirectory`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add app.go app_test.go
git commit -m "feat(backend): add fast directory pre-flight inspection API"
```

---

### Task 2: Wails Bridge & Frontend Pre-flight Inspection Badge

**Files:**
- Modify: `frontend/wails-bridge.js`
- Modify: `frontend/index.html`
- Modify: `frontend/styles.css`
- Modify: `frontend/script.js`

**Interfaces:**
- Consumes: `App.InspectDirectory` from Task 1
- Produces: `#dir-inspection-badge` element, `updateDirectoryInspection(path)` in `script.js`

- [ ] **Step 1: Expose `inspect_directory` in `frontend/wails-bridge.js`**

Add inside `api`:
```javascript
inspect_directory: (path) => window.go?.main?.App?.InspectDirectory(path),
```

- [ ] **Step 2: Add badge HTML in `frontend/index.html`**

Directly under `<div class="input-section full-width">...</div>`:
```html
<div class="dir-inspection-badge" id="dir-inspection-badge" style="display: none;">
    <div class="badge-icon" id="badge-icon"></div>
    <span class="badge-text" id="badge-text"></span>
</div>
```

- [ ] **Step 3: Add badge styles in `frontend/styles.css`**

Add glassmorphic pill badge styling:
```css
.dir-inspection-badge {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin-top: 8px;
    padding: 6px 14px;
    border-radius: 20px;
    background: var(--bg-card-glass, rgba(30, 41, 59, 0.6));
    backdrop-filter: blur(10px);
    border: 1px solid var(--border-glass, rgba(255, 255, 255, 0.08));
    font-size: 0.82rem;
    color: var(--text-primary, #f8fafc);
    animation: badgeSlideDown 0.25s cubic-bezier(0.4, 0, 0.2, 1) forwards;
    transition: all 0.25s ease;
}

@keyframes badgeSlideDown {
    from {
        opacity: 0;
        transform: translateY(-6px);
    }
    to {
        opacity: 1;
        transform: translateY(0);
    }
}

.dir-inspection-badge.badge-single {
    border-color: rgba(14, 165, 233, 0.35);
    box-shadow: 0 4px 12px rgba(14, 165, 233, 0.12);
}
.dir-inspection-badge.badge-batch {
    border-color: rgba(168, 85, 247, 0.35);
    box-shadow: 0 4px 12px rgba(168, 85, 247, 0.12);
}
.dir-inspection-badge.badge-archive {
    border-color: rgba(16, 185, 129, 0.35);
    box-shadow: 0 4px 12px rgba(16, 185, 129, 0.12);
}
.dir-inspection-badge.badge-empty {
    border-color: rgba(239, 68, 68, 0.35);
    box-shadow: 0 4px 12px rgba(239, 68, 68, 0.12);
}
```

- [ ] **Step 4: Implement `updateDirectoryInspection` in `frontend/script.js`**

Add function and call it on folder selection, paste, drop, and path clear:
```javascript
async function updateDirectoryInspection(path) {
    const badge = document.getElementById('dir-inspection-badge');
    if (!badge) return;

    if (!path || !path.trim()) {
        badge.style.display = 'none';
        badge.className = 'dir-inspection-badge';
        return;
    }

    if (!window.pywebview?.api?.inspect_directory) return;

    try {
        const res = await window.pywebview.api.inspect_directory(path.trim());
        if (!res) return;

        const iconEl = document.getElementById('badge-icon');
        const textEl = document.getElementById('badge-text');
        const isFa = (currentLang === 'fa');

        badge.className = 'dir-inspection-badge';

        if (res.status === 'ok') {
            if (res.mode === 'single') {
                badge.classList.add('badge-single');
                iconEl.textContent = '🖼️';
                textEl.textContent = isFa 
                    ? `${res.item_count} تصویر معتبر (پوشه تکی)` 
                    : `${res.item_count} valid images found (Single Folder)`;
            } else if (res.mode === 'batch') {
                badge.classList.add('badge-batch');
                iconEl.textContent = '📚';
                textEl.textContent = isFa 
                    ? `${res.item_count} چپتر شناسایی شد (پردازش گروهی)` 
                    : `${res.item_count} chapters detected (Batch Mode)`;
            } else if (res.mode === 'archive_zip' || res.mode === 'archive_cbz') {
                badge.classList.add('badge-archive');
                iconEl.textContent = '📦';
                const ext = res.mode === 'archive_cbz' ? 'CBZ' : 'ZIP';
                textEl.textContent = isFa 
                    ? `${res.item_count} تصویر در آرشیو ${ext}` 
                    : `${res.item_count} images in ${ext} archive`;
            }
            badge.style.display = 'inline-flex';
        } else if (res.status === 'empty') {
            badge.classList.add('badge-empty');
            iconEl.textContent = '⚠️';
            textEl.textContent = isFa 
                ? 'هیچ تصویر یا زیرپوشه‌ای یافت نشد' 
                : 'No supported images found in directory';
            badge.style.display = 'inline-flex';
        } else {
            badge.style.display = 'none';
        }
    } catch (e) {
        console.error('Inspect directory failed', e);
    }
}
```

- [ ] **Step 5: Run tests and verify**

Run: `go test ./...`  
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add frontend/wails-bridge.js frontend/index.html frontend/styles.css frontend/script.js
git commit -m "feat(ui): add live pre-flight inspection badge for folders and archives"
```

---

### Task 3: Contextual Control Dimming

**Files:**
- Modify: `frontend/styles.css`
- Modify: `frontend/script.js`

**Interfaces:**
- Consumes: `#custom-width`, `#no-stitch`, `#width-input`, `#height-input`
- Produces: `syncControlStates()` in `script.js` with `.card-disabled` class in `styles.css`

- [ ] **Step 1: Add `.card-disabled` styling in `frontend/styles.css`**

```css
.control-card {
    transition: opacity 0.25s cubic-bezier(0.4, 0, 0.2, 1), filter 0.25s ease, border-color 0.25s ease;
}

.control-card.card-disabled {
    opacity: 0.42;
    filter: grayscale(40%);
    pointer-events: none;
    border-color: rgba(255, 255, 255, 0.04);
}

.control-card.card-disabled input {
    cursor: not-allowed;
}

.disabled-pill-note {
    font-size: 0.68rem;
    padding: 1px 6px;
    border-radius: 4px;
    background: rgba(255, 255, 255, 0.08);
    color: var(--text-muted, #94a3b8);
    margin-left: 6px;
    display: none;
}

[dir="rtl"] .disabled-pill-note {
    margin-left: 0;
    margin-right: 6px;
}

.control-card.card-disabled .disabled-pill-note {
    display: inline-block;
}
```

- [ ] **Step 2: Add disabled note element into Height card in `frontend/index.html`**

In Height card header:
```html
<span class="disabled-pill-note" data-i18n="inactiveNoStitch">No Stitch</span>
```

- [ ] **Step 3: Implement `syncControlStates()` in `frontend/script.js`**

```javascript
function syncControlStates() {
    const customWidthChecked = document.getElementById('custom-width')?.checked !== false;
    const widthCard = document.getElementById('width-input')?.closest('.control-card');
    const widthInput = document.getElementById('width-input');
    if (widthCard && widthInput) {
        if (customWidthChecked) {
            widthCard.classList.remove('card-disabled');
            widthInput.disabled = false;
        } else {
            widthCard.classList.add('card-disabled');
            widthInput.disabled = true;
        }
    }

    const noStitchChecked = !!document.getElementById('no-stitch')?.checked;
    const heightCard = document.getElementById('height-input')?.closest('.control-card');
    const heightInput = document.getElementById('height-input');
    if (heightCard && heightInput) {
        if (noStitchChecked) {
            heightCard.classList.add('card-disabled');
            heightInput.disabled = true;
        } else {
            heightCard.classList.remove('card-disabled');
            heightInput.disabled = false;
        }
    }
}
```

- [ ] **Step 4: Attach listeners in `frontend/script.js`**

Bind `change` events on `#custom-width` and `#no-stitch`, and invoke in `applySettingsToDOM`.

- [ ] **Step 5: Run tests and verify**

Run: `go test ./...`  
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add frontend/index.html frontend/styles.css frontend/script.js
git commit -m "feat(ui): add contextual animated dimming for dependent control cards"
```

---

### Task 4: Glassmorphic Tooltip System & Micro-Interactions

**Files:**
- Modify: `frontend/index.html`
- Modify: `frontend/styles.css`
- Modify: `frontend/script.js`

**Interfaces:**
- Consumes: Translations object in `script.js`
- Produces: CSS tooltips for `.info-tooltip-btn` and glowing ready pulse on `#start-button`

- [ ] **Step 1: Add info tooltip triggers in `frontend/index.html`**

Add `<span class="info-tooltip-btn" data-tooltip-key="widthTip">ⓘ</span>` to Width, Height, Quality, Format, AI Enhance, No Stitch, and Archive formats.

- [ ] **Step 2: Add glassmorphic tooltip styles in `frontend/styles.css`**

```css
.info-tooltip-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    font-size: 0.68rem;
    font-weight: bold;
    color: var(--text-muted, #94a3b8);
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.1);
    cursor: help;
    transition: all 0.2s ease;
    position: relative;
    user-select: none;
}

.info-tooltip-btn:hover {
    color: #fff;
    background: var(--primary-accent, #0ea5e9);
    border-color: var(--primary-accent, #0ea5e9);
}

.custom-tooltip-popup {
    position: fixed;
    z-index: 10000;
    max-width: 240px;
    padding: 8px 12px;
    border-radius: 8px;
    background: rgba(15, 23, 42, 0.92);
    backdrop-filter: blur(14px);
    border: 1px solid rgba(255, 255, 255, 0.14);
    box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.5);
    font-size: 0.78rem;
    line-height: 1.4;
    color: #f1f5f9;
    pointer-events: none;
    opacity: 0;
    transition: opacity 0.2s ease, transform 0.2s ease;
    transform: translateY(4px);
}

.custom-tooltip-popup.show {
    opacity: 1;
    transform: translateY(0);
}

/* Button pulse ready animation */
.start-button.btn-ready {
    animation: startPulse 2s infinite ease-in-out;
}

@keyframes startPulse {
    0%, 100% {
        box-shadow: 0 4px 15px var(--glow-accent, rgba(14, 165, 233, 0.3));
    }
    50% {
        box-shadow: 0 4px 28px var(--glow-accent, rgba(14, 165, 233, 0.65));
        transform: translateY(-1px);
    }
}
```

- [ ] **Step 3: Add tooltip translations in `frontend/script.js`**

Add bilingual text entries for all keys in `translations.fa` and `translations.en`.

- [ ] **Step 4: Add hover event handler for `.info-tooltip-btn` and toggle `btn-ready` on `#start-button`**

- [ ] **Step 5: Run tests and verify**

Run: `go test ./...`  
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add frontend/index.html frontend/styles.css frontend/script.js
git commit -m "feat(ui): add glassmorphic tooltips and button ready pulse animation"
```

---

### Task 5: End-to-End Build & Final Verification

**Files:**
- Repository build output: `build/bin/PhotoSlicer.exe`

- [ ] **Step 1: Run full unit tests**

Run: `go test -v ./...`  
Expected: All packages PASS

- [ ] **Step 2: Run race detector tests**

Run: `go test -race ./...`  
Expected: All packages PASS without race or link errors

- [ ] **Step 3: Run Wails production build**

Run: `wails build -clean -s`  
Expected: Build successful, generates `build/bin/PhotoSlicer.exe`

- [ ] **Step 4: Final verification commit**

```bash
git status
```
