# UI/UX Enhancements Design Specification

**Date:** 2026-09-04  
**Project:** PhotoSlicer Go v5.3+  
**Status:** Approved by user, ready for planning & implementation  

---

## 1. Overview & Objectives

This specification defines three complementary UI/UX enhancements designed to transform PhotoSlicer from a functional desktop utility into a polished, responsive, and intuitive tool:

1. **Pre-flight Inspection Badge:** Immediate, live feedback displaying detected image or chapter counts when a directory or archive is selected, pasted, or dropped.
2. **Contextual Control Dimming:** Interactive, animated dimming and disabling of controls when dependent options are toggled (e.g., `Custom Width` toggle dims Width card; `No Stitch` checkbox dims Height Limit card).
3. **Glassmorphic Tooltip System & Micro-Interactions:** Modern, bilingual (FA/EN) frosted-glass tooltips on technical settings and subtle interactive polish (e.g. ready-to-process glow on the Initiate button).

---

## 2. Component Specifications

### 2.1 Pre-flight Inspection Badge

#### Backend: Go Bridge (`app.go`)
Add an exported method on `App`:
```go
type InspectionResult struct {
    Status    string `json:"status"`     // "ok", "empty", "not_found", "error"
    Mode      string `json:"mode"`       // "single", "batch", "archive_zip", "archive_cbz"
    ItemCount int    `json:"item_count"` // number of images or number of chapters
    Path      string `json:"path"`
}

func (a *App) InspectDirectory(path string) InspectionResult
```

- **Execution:**
  - Fast stat check on `path`: checks if it exists.
  - If `.zip` or `.cbz`: opens reader, counts valid images in archive root/subfolders using `constants.SupportedExtensions` without extracting bytes.
  - If directory:
    - Calls `sorting.GetAllImagesDirectory(path)` to check for direct images (Mode `single`).
    - If direct images == 0, scans child directories with `archive.FastScanDir(path)` and counts valid folders with images (Mode `batch`).
    - If both 0, returns `Status: "empty"`.
  - Typical response latency: < 15ms.

#### Frontend Bridge (`frontend/wails-bridge.js`)
Expose:
```javascript
inspect_directory: (path) => window.go?.main?.App?.InspectDirectory(path)
```

#### Frontend UI (`frontend/index.html`, `script.js`, `styles.css`)
- Element: `#dir-inspection-badge` placed immediately below `.folder-wrapper`.
- Styles:
  - Frosted glass container (`background: var(--bg-card-glass)`, `backdrop-filter: blur(8px)`, `border: 1px solid var(--border-glass)`).
  - Animation: slide-down + fade-in on detection; fade-out on clear.
  - Color accents:
    - **Single mode:** Cyber blue / theme primary accent with image icon `🖼️`.
    - **Batch mode:** Purple / electric accent with chapters icon `📚`.
    - **Archive mode:** Emerald / teal accent with package icon `📦`.
    - **Empty / Error mode:** Ruby red / warning accent with alert icon `⚠️`.
- Internationalization:
  - FA:
    - Single: `[X] تصویر معتبر شناسایی شد (پوشه تکی)`
    - Batch: `[X] چپتر شناسایی شد (پردازش گروهی)`
    - Archive: `[X] تصویر در فایل فشرده [EXT]`
    - Empty: `هیچ تصویری در این پوشه یافت نشد`
  - EN:
    - Single: `[X] valid images found (Single Folder)`
    - Batch: `[X] chapters detected (Batch Mode)`
    - Archive: `[X] images in [EXT] archive`
    - Empty: `No supported images found in directory`

---

### 2.2 Contextual Control Dimming

#### Rules:
1. **Custom Width Toggle (`#custom-width`):**
   - When checked (`true`): Width card (`#width-input` parent) has standard opacity `1.0`, pointer events active, and input `disabled = false`.
   - When unchecked (`false`): Width card receives `.card-disabled` class (`opacity: 0.45`, `pointer-events: none`, input `disabled = true`).
2. **No Stitch Checkbox (`#no-stitch`):**
   - When unchecked (`false`): Height Limit card operates normally.
   - When checked (`true`): Height Limit card receives `.card-disabled` class (`opacity: 0.45`, `pointer-events: none`, input `disabled = true`). A subtle badge `[Inactive in No Stitch]` appears on the card header.
3. **Format PSD vs PDF:**
   - When PDF is selected as export format (`#is-pdf`), if output format dropdown is set to PSD, the dropdown displays a small warning or switches to JPG automatically as PSD cannot be embedded in PDF.

#### Transition & Layout Stability:
- CSS transition: `opacity 0.25s cubic-bezier(0.4, 0, 0.2, 1)`.
- Grid positions remain fixed (no shifting or collapsing) to maintain clean visual balance.

---

### 2.3 Glassmorphic Tooltip System & Micro-Interactions

#### Tooltip Component:
- Custom lightweight CSS-driven tooltips with `data-tooltip` attribute or `.tooltip-trigger` buttons with SVG info icon `ⓘ`.
- Glassmorphic styling:
  - `background: rgba(15, 23, 42, 0.88)`
  - `backdrop-filter: blur(14px)`
  - `border: 1px solid rgba(255, 255, 255, 0.12)`
  - `box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.5)`
  - `border-radius: 8px`
  - `padding: 8px 12px`
  - Font size: `12px`
  - Direction aware: RTL for Persian, LTR for English.

#### Tooltip Targets & Content:
1. **Custom Width:**
   - FA: `تغییر عرض تمام صفحات به یک اندازه مشخص با حفظ نسبت ابعاد`
   - EN: `Resize all pages to a uniform width while maintaining aspect ratio`
2. **Height Limit:**
   - FA: `حداکثر ارتفاع هر برش (پیش‌فرض ۱۶۰۰۰ پیکسل برای ایمنی WebP و روان بودن مرورگر)`
   - EN: `Maximum vertical height per slice (16000px safe limit for WebP and browser canvas)`
3. **Quality:**
   - FA: `کیفیت فشرده‌سازی خروجی از ۱ تا ۱۰۰ (۱۰۰ برای بالاترین وضوح)`
   - EN: `Output compression quality from 1 to 100 (100 is lossless/highest)`
4. **Format:**
   - FA: `انتخاب فرمت خروجی تصویر (JPG, PNG, WebP فشرده، یا PSD لایه‌باز)`
   - EN: `Output image format (JPG, PNG, compressed WebP, or layered PSD)`
5. **AI Enhance:**
   - FA: `ارتقای هوشمند کیفیت با دو موتور: پردازنده سریع (CPU) یا هوش مصنوعی Real-ESRGAN (GPU)`
   - EN: `Quality enhancement via Fast CPU denoiser or Real-ESRGAN GPU upscaler`
6. **No Stitch:**
   - FA: `پردازش، ریسایز و واترمارک مجزای فایل‌ها بدون چسباندن آن‌ها در یک نوار بلند وب‌تون`
   - EN: `Process, resize, and watermark files individually without stitching into a tall strip`
7. **ZIP / PDF / CBZ:**
   - FA: `بسته‌بندی خودکار خروجی در قالب فایل فشرده، سند پی‌دی‌اف یا کتاب الکترونیک کمیک`
   - EN: `Automatically bundle results into a ZIP archive, multi-page PDF, or CBZ comic book`

#### Micro-Interactions:
- **Initiate Button Ready Pulse:** When a valid directory/archive is inspected and ready, `#start-button` gains a subtle glowing breathing pulse animation (`box-shadow` pulse in theme color).
- **Interactive Icon Feedback:** Hover lift and rotation easing on `.icon-btn` and action buttons.

---

## 3. Implementation Plan Overview
1. Backend: Implement `InspectDirectory` in `app.go`.
2. Frontend Bridge: Bind `inspect_directory` in `frontend/wails-bridge.js`.
3. HTML: Add `#dir-inspection-badge` and tooltip triggers in `frontend/index.html`.
4. CSS: Add styles for badge, `.card-disabled`, tooltips, and pulse animations in `frontend/styles.css`.
5. JS: Wire folder inspection events, contextual dimming listeners, and bilingual tooltip translations in `frontend/script.js`.
6. Testing: Verify with `go test ./...`, `go test -race ./...`, and `wails build -s`.
