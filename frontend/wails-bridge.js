// Wails v2 bridge providing 1:1 window.pywebview.api compatibility
(function() {
    function gatherUIParams() {
        return {
            directory: document.getElementById('directory-input')?.value || '',
            custom_width_checked: !!document.getElementById('custom-width')?.checked,
            width: parseInt(document.getElementById('width-input')?.value, 10) || 800,
            height_limit: parseInt(document.getElementById('height-input')?.value, 10) || 16000,
            save_quality: parseInt(document.getElementById('quality-input')?.value, 10) || 100,
            save_format: document.getElementById('format-select')?.value || 'JPG',
            zip_checked: !!document.getElementById('is-zip')?.checked,
            pdf_checked: !!document.getElementById('is-pdf')?.checked,
            cbz_checked: !!document.getElementById('is-cbz')?.checked,
            enhance_checked: !!document.getElementById('enhance-quality')?.checked,
            enhance_engine: document.getElementById('enhance-engine-select')?.value || 'fast',
            no_stitch_checked: !!document.getElementById('no-stitch')?.checked,
            watermark_enabled: !!document.getElementById('watermark-enabled')?.checked,
            watermark_path: document.getElementById('watermark-path')?.value || '',
            watermark_count: parseInt(document.getElementById('watermark-count')?.value, 10) || 1,
            watermark_edge: document.getElementById('watermark-edge')?.value || 'right',
            watermark_margin: parseInt(document.getElementById('watermark-margin')?.value, 10) || 0,
            save_location: document.getElementById('save-location-input')?.value || '',
            save_next_to_source: !!document.getElementById('save-next-to-source')?.checked,
            play_sound: !!document.getElementById('play-sound')?.checked,
            show_notification: !!document.getElementById('show-notifications')?.checked,
            thread_count: parseInt(document.getElementById('thread-count')?.value, 10) || 4,
            output_suffix: document.getElementById('output-suffix')?.value || ' [Stitched]',
            filename_pattern: document.getElementById('filename-pattern')?.value || '[number]',
            filename_digits: parseInt(document.getElementById('filename-digits')?.value, 10) || 3
        };
    }

    const api = {
        app_ready: () => window.go?.main?.App?.AppReady(),
        select_folder: () => window.go?.main?.App?.SelectFolder(),
        select_watermark_file: () => window.go?.main?.App?.SelectWatermarkFile(),
        export_presets: (jsonText, filename) => window.go?.main?.App?.ExportPresets(jsonText, filename),
        import_presets: () => window.go?.main?.App?.ImportPresets(),
        save_settings: (settings) => window.go?.main?.App?.SaveSettings(settings),
        pause_processing: () => window.go?.main?.App?.PauseProcessing(),
        resume_processing: () => window.go?.main?.App?.ResumeProcessing(),
        stop_processing: () => window.go?.main?.App?.StopProcessing(),
        open_file_explorer: (path) => window.go?.main?.App?.OpenFileExplorer(path),
        start: () => window.go?.main?.App?.Start(gatherUIParams()),
        minimize_window: () => window.go?.main?.App?.MinimizeWindow(),
        close_window: () => window.go?.main?.App?.CloseWindow(),
        get_clipboard_text: () => window.go?.main?.App?.GetClipboardText(),
        isDirectory: (path) => window.go?.main?.App?.IsDirectory(path),
        folderName: (path) => window.go?.main?.App?.FolderName(path)
    };

    window.pywebview = { api: api };

    function checkWailsReady() {
        if (window.go && window.go.main && window.go.main.App) {
            window.dispatchEvent(new Event('pywebviewready'));
        } else {
            setTimeout(checkWailsReady, 25);
        }
    }
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', checkWailsReady);
    } else {
        checkWailsReady();
    }
})();
