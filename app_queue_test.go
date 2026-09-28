package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"photoslicer/engine/queue"
)

// newQueueTestApp returns an App whose settings and queue live in a temp dir.
func newQueueTestApp(t *testing.T) *App {
	t.Helper()
	app := NewApp()
	app.settingsPathOverride = filepath.Join(t.TempDir(), "settings.json")
	return app
}

// writeChapter creates a folder holding a few small PNG pages.
func writeChapter(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 60, 80))
		for y := 0; y < 80; y++ {
			for x := 0; x < 60; x++ {
				img.Set(x, y, color.RGBA{R: uint8(40 * i), G: uint8(x * 4), B: uint8(y * 3), A: 255})
			}
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%02d.png", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	return dir
}

func queueParams(dir, out string) map[string]interface{} {
	return map[string]interface{}{
		"directory":            dir,
		"save_location":        out,
		"custom_width_checked": false,
		"height_limit":         float64(16000),
		"save_quality":         float64(90),
		"save_format":          "PNG",
		"thread_count":         float64(1),
		"filename_pattern":     "[number]",
		"filename_digits":      float64(3),
		"language":             "en",
	}
}

func TestEnqueueJobRejectsBadInput(t *testing.T) {
	app := newQueueTestApp(t)
	root := t.TempDir()

	cases := map[string]map[string]interface{}{
		"empty path":    {"directory": "  "},
		"missing path":  {"directory": filepath.Join(root, "nope")},
		"empty folder":  {"directory": t.TempDir()},
		"missing mark":  {"directory": writeChapter(t, root, "c1"), "watermark_enabled": true, "watermark_path": filepath.Join(root, "wm.png")},
		"unset mark":    {"directory": filepath.Join(root, "c1"), "watermark_enabled": true},
		"unsupported":   {"directory": func() string { p := filepath.Join(root, "notes.txt"); _ = os.WriteFile(p, []byte("x"), 0644); return p }()},
		"nil directory": {},
	}
	for name, params := range cases {
		if res := app.EnqueueJob(params); res["ok"] != false {
			t.Errorf("%s: EnqueueJob = %v, want ok=false", name, res)
		}
	}
	if got := len(app.getJobs().Snapshot()); got != 0 {
		t.Fatalf("rejected jobs must not be queued, queue has %d", got)
	}
}

func TestEnqueueJobSnapshotsSettingsAndPersists(t *testing.T) {
	app := newQueueTestApp(t)
	root := t.TempDir()
	chapter := writeChapter(t, root, "Chapter 1")

	params := queueParams("  "+chapter+"  ", filepath.Join(root, "out"))
	params["width"] = float64(800)
	res := app.EnqueueJob(params)
	if res["ok"] != true || res["name"] != "Chapter 1" {
		t.Fatalf("EnqueueJob = %v", res)
	}

	// Changing the caller's map afterwards must not touch the queued job.
	params["width"] = float64(1200)
	params["directory"] = "elsewhere"

	jobs := app.getJobs().Snapshot()
	if len(jobs) != 1 {
		t.Fatalf("queue has %d jobs, want 1", len(jobs))
	}
	job := jobs[0]
	if job.Input != chapter || job.Params["directory"] != chapter {
		t.Errorf("input not trimmed/kept: %q / %v", job.Input, job.Params["directory"])
	}
	if job.Params["width"] != float64(800) {
		t.Errorf("width = %v, want the value at enqueue time (800)", job.Params["width"])
	}
	if job.Mode != "single" || job.Items != 3 || job.Status != queue.StatusQueued {
		t.Errorf("unexpected job info: %+v", job)
	}

	// A fresh App pointed at the same settings dir must find the job again.
	restarted := NewApp()
	restarted.settingsPathOverride = app.settingsPathOverride
	reloaded := restarted.getJobs().Snapshot()
	if len(reloaded) != 1 || reloaded[0].ID != job.ID || reloaded[0].Params["width"] != float64(800) {
		t.Fatalf("queue not restored after restart: %+v", reloaded)
	}
}

func TestRunQueueRunsJobsInOrderAndContinuesAfterFailure(t *testing.T) {
	app := newQueueTestApp(t)
	root := t.TempDir()
	out := filepath.Join(root, "out")

	first := writeChapter(t, root, "first")
	doomed := writeChapter(t, root, "doomed")
	last := writeChapter(t, root, "last")
	for _, dir := range []string{first, doomed, last} {
		if res := app.EnqueueJob(queueParams(dir, out)); res["ok"] != true {
			t.Fatalf("EnqueueJob(%s) = %v", dir, res)
		}
	}
	// The middle job's source disappears before its turn comes.
	if err := os.RemoveAll(doomed); err != nil {
		t.Fatal(err)
	}

	app.runQueue()

	jobs := app.getJobs().Snapshot()
	if len(jobs) != 3 {
		t.Fatalf("queue has %d jobs, want 3", len(jobs))
	}
	want := []queue.Status{queue.StatusDone, queue.StatusFailed, queue.StatusDone}
	for i, job := range jobs {
		if job.Status != want[i] {
			t.Errorf("job %d (%s) status = %s, want %s (error %q)", i, job.Name, job.Status, want[i], job.Error)
		}
	}
	if jobs[1].Error == "" {
		t.Error("failed job should carry an error message")
	}
	for _, i := range []int{0, 2} {
		if jobs[i].OutputPath == "" {
			t.Errorf("job %s has no output path", jobs[i].Name)
			continue
		}
		if _, err := os.Stat(jobs[i].OutputPath); err != nil {
			t.Errorf("output of %s missing: %v", jobs[i].Name, err)
		}
	}
	if atomic.LoadInt32(&app.queueActive) != 0 {
		t.Error("queue should not stay marked active after it finishes")
	}
}

func TestRunQueueStopsWhenAborted(t *testing.T) {
	app := newQueueTestApp(t)
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		dir := writeChapter(t, root, name)
		if res := app.EnqueueJob(queueParams(dir, filepath.Join(root, "out"))); res["ok"] != true {
			t.Fatalf("EnqueueJob = %v", res)
		}
	}

	atomic.StoreInt32(&app.queueAbort, 1)
	app.runQueue()

	for _, job := range app.getJobs().Snapshot() {
		if job.Status != queue.StatusQueued {
			t.Errorf("job %s ran despite the abort flag: %s", job.Name, job.Status)
		}
	}
}

func TestStartQueueIgnoredWhileBusy(t *testing.T) {
	app := newQueueTestApp(t)
	root := t.TempDir()
	dir := writeChapter(t, root, "a")
	if res := app.EnqueueJob(queueParams(dir, filepath.Join(root, "out"))); res["ok"] != true {
		t.Fatalf("EnqueueJob = %v", res)
	}

	atomic.StoreInt32(&app.isBusy, 1)
	app.StartQueue()
	if got := app.getJobs().Snapshot()[0].Status; got != queue.StatusQueued {
		t.Fatalf("StartQueue must not run while another job is active, status = %s", got)
	}
	if atomic.LoadInt32(&app.queueActive) != 0 {
		t.Fatal("queue must not be marked active when StartQueue was refused")
	}
}

func TestStopProcessingAbortsOnlyAnActiveQueue(t *testing.T) {
	app := newQueueTestApp(t)

	app.StopProcessing()
	if atomic.LoadInt32(&app.queueAbort) != 0 {
		t.Fatal("stopping a one-off job must not raise the queue abort flag")
	}

	atomic.StoreInt32(&app.queueActive, 1)
	app.StopProcessing()
	if atomic.LoadInt32(&app.queueAbort) != 1 {
		t.Fatal("stopping during a queue run must abort the rest of the queue")
	}
}

func TestRunJobReportsStoppedWhenControllerIsStopped(t *testing.T) {
	app := newQueueTestApp(t)
	root := t.TempDir()
	dir := writeChapter(t, root, "a")

	app.beginRun()
	app.getController().Stop()
	res := app.runJob(queueParams(dir, filepath.Join(root, "out")), true)

	if res.Status != queue.StatusStopped {
		t.Fatalf("status = %s (error %q), want stopped", res.Status, res.Error)
	}
}

func TestRunJobBatchReportsPartialFailure(t *testing.T) {
	app := newQueueTestApp(t)
	root := t.TempDir()
	series := filepath.Join(root, "series")
	writeChapter(t, series, "ch-1")
	badDir := filepath.Join(series, "ch-2")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "01.png"), []byte("not an image"), 0644); err != nil {
		t.Fatal(err)
	}

	app.beginRun()
	res := app.runJob(queueParams(series, filepath.Join(root, "out")), true)

	if res.Total != 2 || res.OK != 1 || res.Failed != 1 {
		t.Fatalf("counts ok/failed/total = %d/%d/%d, want 1/1/2 (status %s, error %q)", res.OK, res.Failed, res.Total, res.Status, res.Error)
	}
	if res.Status != queue.StatusPartial {
		t.Fatalf("status = %s, want partial", res.Status)
	}
	if res.Error == "" {
		t.Fatal("a partial result should say which chapter failed")
	}
}
