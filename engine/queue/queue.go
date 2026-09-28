// Package queue keeps the ordered list of processing jobs that the user has
// lined up, and persists it so pending work survives an app restart.
//
// A Job is a snapshot of the input path together with every setting the
// pipeline needs, taken at the moment it was added. That lets each job run
// with its own width, format, watermark and so on, regardless of what the
// Workspace form looks like when its turn comes.
package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusPartial Status = "partial" // batch job where some folders failed
	StatusFailed  Status = "failed"
	StatusStopped Status = "stopped"
)

// ErrInterrupted is stored in Job.Error for a job that was still running when
// the app closed. The frontend localises it.
const ErrInterrupted = "interrupted"

var (
	ErrNotFound     = errors.New("job not found")
	ErrRunning      = errors.New("job is running")
	ErrNotRetryable = errors.New("only stopped, failed or partial jobs can be retried")
)

// Job is one entry in the queue.
type Job struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Input      string                 `json:"input"`
	Mode       string                 `json:"mode,omitempty"`  // single, batch or archive_*
	Items      int                    `json:"items,omitempty"` // images, chapters or pages found when added
	Params     map[string]interface{} `json:"params"`
	Status     Status                 `json:"status"`
	Error      string                 `json:"error,omitempty"`
	OutputPath string                 `json:"output_path,omitempty"`
	OK         int                    `json:"ok"`
	Failed     int                    `json:"failed"`
	Total      int                    `json:"total"`
	AddedAt    time.Time              `json:"added_at"`
	StartedAt  time.Time              `json:"started_at,omitempty"`
	FinishedAt time.Time              `json:"finished_at,omitempty"`
}

// Result is what a finished run reports back to the queue.
type Result struct {
	Status     Status
	Error      string
	OutputPath string
	OK         int
	Failed     int
	Total      int
}

// Finished reports whether the job has left the queue's active states.
func (j Job) Finished() bool {
	switch j.Status {
	case StatusDone, StatusPartial, StatusFailed, StatusStopped:
		return true
	}
	return false
}

// Queue is a mutex-guarded, file-backed list of jobs.
type Queue struct {
	mu   sync.Mutex
	path string
	jobs []*Job
	seq  uint64
}

// Load reads the queue stored at path. A missing file yields an empty queue.
// A file that cannot be parsed also yields an empty queue, together with the
// parse error, so the caller can carry on and simply lose the broken state.
// Jobs that were running when the app last closed are marked stopped.
func Load(path string) (*Queue, error) {
	q := &Queue{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return q, nil
		}
		return q, err
	}
	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return q, err
	}
	for _, j := range jobs {
		if j == nil || j.ID == "" {
			continue
		}
		if j.Params == nil {
			j.Params = map[string]interface{}{}
		}
		if j.Status == StatusRunning {
			j.Status = StatusStopped
			j.Error = ErrInterrupted
		}
		q.jobs = append(q.jobs, j)
	}
	return q, nil
}

func (q *Queue) newID() string {
	q.seq++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), q.seq)
}

func copyParams(p map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// save writes the queue atomically. The queue is only a convenience, so a
// failed write is not surfaced; the in-memory state stays authoritative.
// The caller must hold q.mu.
func (q *Queue) save() {
	if q.path == "" {
		return
	}
	data, err := json.MarshalIndent(q.jobs, "", "    ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(q.path), 0755); err != nil {
		return
	}
	tmp := fmt.Sprintf("%s.%d.tmp", q.path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	if err := os.Rename(tmp, q.path); err != nil {
		_ = os.Remove(tmp)
	}
}

func (q *Queue) find(id string) int {
	for i, j := range q.jobs {
		if j.ID == id {
			return i
		}
	}
	return -1
}

// Add appends j to the queue as a waiting job and returns a copy of it. The
// caller supplies Name, Input, Params and the optional Mode and Items; the
// queue assigns the ID, status and timestamp.
func (q *Queue) Add(j Job) Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.ID = q.newID()
	j.Params = copyParams(j.Params)
	j.Status = StatusQueued
	j.AddedAt = time.Now()
	j.Error, j.OutputPath = "", ""
	j.OK, j.Failed, j.Total = 0, 0, 0
	j.StartedAt, j.FinishedAt = time.Time{}, time.Time{}
	q.jobs = append(q.jobs, &j)
	q.save()
	return snapshotJob(&j)
}

// Remove deletes a job. A running job cannot be removed.
func (q *Queue) Remove(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.find(id)
	if i < 0 {
		return ErrNotFound
	}
	if q.jobs[i].Status == StatusRunning {
		return ErrRunning
	}
	q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
	q.save()
	return nil
}

// Move shifts a queued job one place toward the front (direction < 0) or the
// back (direction > 0) of the waiting jobs. Finished and running jobs are
// never jumped over, so the order of what has already run is preserved.
func (q *Queue) Move(id string, direction int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.find(id)
	if i < 0 {
		return ErrNotFound
	}
	if q.jobs[i].Status != StatusQueued || direction == 0 {
		return nil
	}
	step := 1
	if direction < 0 {
		step = -1
	}
	for k := i + step; k >= 0 && k < len(q.jobs); k += step {
		if q.jobs[k].Status == StatusQueued {
			q.jobs[i], q.jobs[k] = q.jobs[k], q.jobs[i]
			q.save()
			return nil
		}
	}
	return nil
}

// Retry puts a stopped, failed or partially failed job back at the end of the
// waiting jobs so it runs again with the settings it was added with.
func (q *Queue) Retry(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.find(id)
	if i < 0 {
		return ErrNotFound
	}
	j := q.jobs[i]
	switch j.Status {
	case StatusStopped, StatusFailed, StatusPartial:
	default:
		return ErrNotRetryable
	}
	j.Status = StatusQueued
	j.Error = ""
	j.OutputPath = ""
	j.OK, j.Failed, j.Total = 0, 0, 0
	j.StartedAt, j.FinishedAt = time.Time{}, time.Time{}
	q.jobs = append(append(q.jobs[:i], q.jobs[i+1:]...), j)
	q.save()
	return nil
}

// ClearFinished removes every job that is done, failed, partial or stopped and
// returns how many were removed.
func (q *Queue) ClearFinished() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.jobs[:0]
	removed := 0
	for _, j := range q.jobs {
		if j.Finished() {
			removed++
			continue
		}
		kept = append(kept, j)
	}
	// Drop the dangling tail so removed jobs can be collected.
	for k := len(kept); k < len(q.jobs); k++ {
		q.jobs[k] = nil
	}
	q.jobs = kept
	if removed > 0 {
		q.save()
	}
	return removed
}

// HasQueued reports whether at least one job is waiting to run.
func (q *Queue) HasQueued() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.Status == StatusQueued {
			return true
		}
	}
	return false
}

// Next marks the first waiting job as running and returns a copy of it, or
// nil when nothing is waiting.
func (q *Queue) Next() *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range q.jobs {
		if j.Status == StatusQueued {
			j.Status = StatusRunning
			j.StartedAt = time.Now()
			q.save()
			c := snapshotJob(j)
			return &c
		}
	}
	return nil
}

// Finish records the outcome of a running job.
func (q *Queue) Finish(id string, r Result) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.find(id)
	if i < 0 {
		return
	}
	j := q.jobs[i]
	j.Status = r.Status
	j.Error = r.Error
	j.OutputPath = r.OutputPath
	j.OK, j.Failed, j.Total = r.OK, r.Failed, r.Total
	j.FinishedAt = time.Now()
	q.save()
}

// Snapshot returns a deep copy of every job in order.
func (q *Queue) Snapshot() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, len(q.jobs))
	for i, j := range q.jobs {
		out[i] = snapshotJob(j)
	}
	return out
}

func snapshotJob(j *Job) Job {
	c := *j
	c.Params = copyParams(j.Params)
	return c
}
