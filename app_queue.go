package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"photoslicer/engine/archive"
	"photoslicer/engine/queue"
)

func (a *App) queueFilePath() string {
	return filepath.Join(filepath.Dir(a.getSettingsFilePath()), "queue.json")
}

// getJobs returns the job queue, loading it from disk on first use. A queue
// file that cannot be read is treated as empty rather than blocking the app.
func (a *App) getJobs() *queue.Queue {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	if a.jobs == nil {
		q, _ := queue.Load(a.queueFilePath())
		a.jobs = q
	}
	return a.jobs
}

func (a *App) queueState() map[string]interface{} {
	return map[string]interface{}{
		"jobs":   a.getJobs().Snapshot(),
		"active": atomic.LoadInt32(&a.queueActive) == 1,
	}
}

// pushQueue sends the current queue to the frontend.
func (a *App) pushQueue() {
	data, err := json.Marshal(a.queueState())
	if err != nil {
		return
	}
	a.execJS(fmt.Sprintf(`if (typeof onQueueChanged === 'function') onQueueChanged(%s);`, string(data)))
}

// GetQueue returns the queued jobs and whether a queue run is in progress.
func (a *App) GetQueue() map[string]interface{} {
	return a.queueState()
}

// EnqueueJob validates the source in params and adds it to the queue together
// with a snapshot of every setting in params. It reports whether the job was
// added; on failure the reason has already been shown to the user.
func (a *App) EnqueueJob(params map[string]interface{}) map[string]interface{} {
	settings := a.loadSettings()
	lang, _ := settings["language"].(string)
	if lang == "" {
		lang = "fa"
	}
	rejected := func(key string) map[string]interface{} {
		a.showError(getMsg(key, lang), true)
		return map[string]interface{}{"ok": false}
	}

	input, _ := params["directory"].(string)
	input = strings.TrimSpace(input)
	if input == "" {
		return rejected("error_folder")
	}
	info := a.InspectDirectory(input)
	switch info["status"] {
	case "ok":
	case "not_found":
		return rejected("path_not_exist")
	default:
		return rejected("error_no_images")
	}

	if wmEnabled, _ := params["watermark_enabled"].(bool); wmEnabled {
		wmPath, _ := params["watermark_path"].(string)
		if wmPath == "" {
			return rejected("error_watermark_path")
		}
		if _, err := os.Stat(wmPath); err != nil {
			return rejected("error_watermark_path")
		}
	}

	snapshot := make(map[string]interface{}, len(params))
	for k, v := range params {
		snapshot[k] = v
	}
	snapshot["directory"] = input

	name := filepath.Base(input)
	if fi, err := os.Stat(input); err == nil && !fi.IsDir() {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	mode, _ := info["mode"].(string)
	items, _ := info["item_count"].(int)

	// Results of earlier operations are only kept until the next one begins
	a.getJobs().ClearFinished()
	job := a.getJobs().Add(queue.Job{
		Name:   name,
		Input:  input,
		Mode:   mode,
		Items:  items,
		Params: snapshot,
	})
	a.pushQueue()
	a.clearSourceDirectory()
	return map[string]interface{}{"ok": true, "name": job.Name}
}

// RemoveQueueJob deletes a job that is not currently running.
func (a *App) RemoveQueueJob(id string) {
	_ = a.getJobs().Remove(id)
	a.pushQueue()
}

// MoveQueueJob moves a waiting job one place earlier (direction < 0) or later.
func (a *App) MoveQueueJob(id string, direction int) {
	_ = a.getJobs().Move(id, direction)
	a.pushQueue()
}

// RetryQueueJob puts a stopped, failed or partial job back in the queue.
func (a *App) RetryQueueJob(id string) {
	_ = a.getJobs().Retry(id)
	a.pushQueue()
}

// ClearFinishedJobs drops every job that is no longer waiting or running.
func (a *App) ClearFinishedJobs() {
	a.getJobs().ClearFinished()
	a.pushQueue()
}

// StartQueue runs the waiting jobs one after another. It does nothing while a
// job is already running, or when no job is waiting.
func (a *App) StartQueue() {
	if !a.getJobs().HasQueued() {
		return
	}
	if !atomic.CompareAndSwapInt32(&a.isBusy, 0, 1) {
		return
	}
	atomic.StoreInt32(&a.queueAbort, 0)
	atomic.StoreInt32(&a.queueActive, 1)

	go func() {
		defer atomic.StoreInt32(&a.isBusy, 0)
		a.runQueue()
	}()
}

// runQueue works through the waiting jobs in order. Jobs run one at a time
// because the AI enhancer and the encoders already saturate the GPU and RAM.
// Stopping ends the current job and leaves the rest waiting.
func (a *App) runQueue() {
	q := a.getJobs()
	ran, succeeded := 0, 0

	// A new run: drop the results of the previous one. The jobs this run
	// finishes stay in the list until the next operation.
	q.ClearFinished()
	a.pushQueue()

	for atomic.LoadInt32(&a.queueAbort) == 0 {
		job := q.Next()
		if job == nil {
			break
		}
		a.pushQueue()

		a.beginRun()
		res := a.runJob(job.Params, true)
		archive.CleanupAllTempDirs()

		q.Finish(job.ID, res)
		ran++
		if res.Status == queue.StatusDone {
			succeeded++
		}
		a.pushQueue()

		if res.Status == queue.StatusStopped {
			break
		}
	}

	atomic.StoreInt32(&a.queueActive, 0)
	a.endRun()
	a.pushQueue()

	if ran == 0 || atomic.LoadInt32(&a.queueAbort) == 1 {
		return
	}
	settings := a.loadSettings()
	lang, _ := settings["language"].(string)
	if lang == "" {
		lang = "fa"
	}
	if playSound, ok := settings["play_sound"].(bool); !ok || playSound {
		a.playAudio("success.wav")
	}
	msg := getMsg("queue_done", lang, succeeded, ran)
	if succeeded == ran {
		a.showSuccess(msg)
	} else {
		a.showError(msg, true)
	}
}
