package queue

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestQueue(t *testing.T) (*Queue, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return q, path
}

func names(jobs []Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNextRunsJobsInOrderAndMarksRunning(t *testing.T) {
	q, _ := newTestQueue(t)
	q.Add(Job{Name: "a", Input: "/in/a"})
	q.Add(Job{Name: "b", Input: "/in/b"})

	first := q.Next()
	if first == nil || first.Name != "a" || first.Status != StatusRunning {
		t.Fatalf("first job = %+v, want running a", first)
	}
	second := q.Next()
	if second == nil || second.Name != "b" {
		t.Fatalf("second job = %+v, want b", second)
	}
	if q.Next() != nil {
		t.Fatal("Next should return nil when nothing is waiting")
	}
	if q.HasQueued() {
		t.Fatal("HasQueued should be false once every job has started")
	}
}

func TestParamsAreSnapshotted(t *testing.T) {
	q, _ := newTestQueue(t)
	params := map[string]interface{}{"width": 800.0, "save_format": "JPG"}
	added := q.Add(Job{Name: "a", Input: "/in/a", Params: params})

	params["width"] = 1200.0
	added.Params["save_format"] = "PNG"

	job := q.Next()
	if job.Params["width"] != 800.0 || job.Params["save_format"] != "JPG" {
		t.Fatalf("job params changed after Add: %v", job.Params)
	}
	job.Params["width"] = 1.0
	if got := q.Snapshot()[0].Params["width"]; got != 800.0 {
		t.Fatalf("mutating a returned copy leaked into the queue: width = %v", got)
	}
}

func TestMoveSkipsNonWaitingJobs(t *testing.T) {
	q, _ := newTestQueue(t)
	a := q.Add(Job{Name: "a", Input: "/in/a"})
	q.Add(Job{Name: "b", Input: "/in/b"})
	c := q.Add(Job{Name: "c", Input: "/in/c"})
	d := q.Add(Job{Name: "d", Input: "/in/d"})

	// a runs and finishes, so it sits at the front as a finished job.
	q.Next()
	q.Finish(a.ID, Result{Status: StatusDone})

	if err := q.Move(d.ID, -1); err != nil {
		t.Fatal(err)
	}
	if got := names(q.Snapshot()); !equal(got, []string{"a", "b", "d", "c"}) {
		t.Fatalf("after moving d up: %v", got)
	}

	// b is the first waiting job; moving it up must not jump over finished a.
	b := q.Snapshot()[1]
	if err := q.Move(b.ID, -1); err != nil {
		t.Fatal(err)
	}
	if got := names(q.Snapshot()); !equal(got, []string{"a", "b", "d", "c"}) {
		t.Fatalf("moving the first waiting job up changed the order: %v", got)
	}

	if err := q.Move(c.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := names(q.Snapshot()); !equal(got, []string{"a", "b", "d", "c"}) {
		t.Fatalf("moving the last waiting job down changed the order: %v", got)
	}

	if err := q.Move("missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Move on unknown id = %v, want ErrNotFound", err)
	}
}

func TestRemove(t *testing.T) {
	q, _ := newTestQueue(t)
	a := q.Add(Job{Name: "a", Input: "/in/a"})
	b := q.Add(Job{Name: "b", Input: "/in/b"})

	running := q.Next()
	if running.ID != a.ID {
		t.Fatalf("expected a to run first")
	}
	if err := q.Remove(a.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("Remove running job = %v, want ErrRunning", err)
	}
	if err := q.Remove(b.ID); err != nil {
		t.Fatalf("Remove waiting job: %v", err)
	}
	if err := q.Remove(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove twice = %v, want ErrNotFound", err)
	}
	if got := names(q.Snapshot()); !equal(got, []string{"a"}) {
		t.Fatalf("remaining jobs: %v", got)
	}
}

func TestFinishRecordsResult(t *testing.T) {
	q, _ := newTestQueue(t)
	q.Add(Job{Name: "a", Input: "/in/a"})
	job := q.Next()

	q.Finish(job.ID, Result{
		Status:     StatusPartial,
		Error:      "ch-3: boom",
		OutputPath: "/out/a",
		OK:         2,
		Failed:     1,
		Total:      3,
	})

	got := q.Snapshot()[0]
	if got.Status != StatusPartial || got.Error != "ch-3: boom" || got.OutputPath != "/out/a" ||
		got.OK != 2 || got.Failed != 1 || got.Total != 3 || got.FinishedAt.IsZero() {
		t.Fatalf("unexpected finished job: %+v", got)
	}
	if !got.Finished() {
		t.Fatal("partial job should count as finished")
	}
}

func TestRetryRequeuesAtTheEnd(t *testing.T) {
	q, _ := newTestQueue(t)
	a := q.Add(Job{Name: "a", Input: "/in/a"})
	q.Add(Job{Name: "b", Input: "/in/b"})

	q.Next()
	q.Finish(a.ID, Result{Status: StatusFailed, Error: "boom", OK: 0, Failed: 1, Total: 1})

	if err := q.Retry(a.ID); err != nil {
		t.Fatal(err)
	}
	snap := q.Snapshot()
	if !equal(names(snap), []string{"b", "a"}) {
		t.Fatalf("retried job should move to the end: %v", names(snap))
	}
	if snap[1].Status != StatusQueued || snap[1].Error != "" || snap[1].Total != 0 {
		t.Fatalf("retried job was not reset: %+v", snap[1])
	}

	if err := q.Retry(snap[0].ID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("Retry on a waiting job = %v, want ErrNotRetryable", err)
	}
	if err := q.Retry("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Retry on unknown id = %v, want ErrNotFound", err)
	}
}

func TestClearFinished(t *testing.T) {
	q, _ := newTestQueue(t)
	a := q.Add(Job{Name: "a", Input: "/in/a"})
	b := q.Add(Job{Name: "b", Input: "/in/b"})
	c := q.Add(Job{Name: "c", Input: "/in/c"})
	q.Add(Job{Name: "d", Input: "/in/d"})

	q.Next()
	q.Finish(a.ID, Result{Status: StatusDone})
	q.Next()
	q.Finish(b.ID, Result{Status: StatusFailed, Error: "x"})
	q.Next() // c is left running

	if got := q.ClearFinished(); got != 2 {
		t.Fatalf("ClearFinished removed %d, want 2", got)
	}
	snap := q.Snapshot()
	if !equal(names(snap), []string{"c", "d"}) {
		t.Fatalf("remaining jobs: %v", names(snap))
	}
	if snap[0].ID != c.ID || snap[0].Status != StatusRunning {
		t.Fatalf("running job must survive ClearFinished: %+v", snap[0])
	}
	if got := q.ClearFinished(); got != 0 {
		t.Fatalf("second ClearFinished removed %d, want 0", got)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	q, path := newTestQueue(t)
	q.Add(Job{Name: "a", Input: "/in/a", Params: map[string]interface{}{"width": 800.0, "watermark_enabled": true}})
	q.Add(Job{Name: "b", Input: "/in/b", Params: map[string]interface{}{"save_format": "WEBP"}})

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap := loaded.Snapshot()
	if !equal(names(snap), []string{"a", "b"}) {
		t.Fatalf("loaded jobs: %v", names(snap))
	}
	if snap[0].Params["width"] != 800.0 || snap[0].Params["watermark_enabled"] != true {
		t.Fatalf("params lost in round trip: %v", snap[0].Params)
	}
	if snap[0].Status != StatusQueued || snap[0].Input != "/in/a" {
		t.Fatalf("unexpected loaded job: %+v", snap[0])
	}

	// IDs handed out after a reload must not collide with loaded ones.
	added := loaded.Add(Job{Name: "c", Input: "/in/c"})
	for _, j := range snap {
		if j.ID == added.ID {
			t.Fatalf("duplicate id %q after reload", added.ID)
		}
	}
}

func TestLoadMarksRunningJobsInterrupted(t *testing.T) {
	q, path := newTestQueue(t)
	q.Add(Job{Name: "a", Input: "/in/a"})
	q.Add(Job{Name: "b", Input: "/in/b"})
	q.Next() // a is running when the "app closes"

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap := loaded.Snapshot()
	if snap[0].Status != StatusStopped || snap[0].Error != ErrInterrupted {
		t.Fatalf("interrupted job = %+v", snap[0])
	}
	if snap[1].Status != StatusQueued {
		t.Fatalf("waiting job should stay queued: %+v", snap[1])
	}
	if err := loaded.Retry(snap[0].ID); err != nil {
		t.Fatalf("an interrupted job should be retryable: %v", err)
	}
}

func TestLoadMissingAndCorruptFiles(t *testing.T) {
	dir := t.TempDir()

	q, err := Load(filepath.Join(dir, "absent.json"))
	if err != nil || len(q.Snapshot()) != 0 {
		t.Fatalf("missing file: err=%v jobs=%d", err, len(q.Snapshot()))
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	q, err = Load(bad)
	if err == nil {
		t.Fatal("corrupt file should report an error")
	}
	if q == nil || len(q.Snapshot()) != 0 {
		t.Fatal("corrupt file should still yield a usable, empty queue")
	}
	q.Add(Job{Name: "a", Input: "/in/a"})
	if got := len(q.Snapshot()); got != 1 {
		t.Fatalf("queue from a corrupt file should accept jobs, has %d", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	q, _ := newTestQueue(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			q.Add(Job{Name: "x", Input: "/in/x", Params: map[string]interface{}{"n": 1}})
		}()
		go func() {
			defer wg.Done()
			_ = q.Snapshot()
			_ = q.HasQueued()
		}()
		go func() {
			defer wg.Done()
			if j := q.Next(); j != nil {
				q.Finish(j.ID, Result{Status: StatusDone})
			}
		}()
	}
	wg.Wait()

	ids := map[string]bool{}
	for _, j := range q.Snapshot() {
		if ids[j.ID] {
			t.Fatalf("duplicate job id %q", j.ID)
		}
		ids[j.ID] = true
	}
}

func TestAddIgnoresStateSuppliedByTheCaller(t *testing.T) {
	q, _ := newTestQueue(t)
	added := q.Add(Job{
		ID:         "chosen-by-caller",
		Name:       "a",
		Input:      "/in/a",
		Status:     StatusDone,
		Error:      "stale",
		OutputPath: "/out/stale",
		OK:         5,
		Total:      5,
	})
	if added.ID == "chosen-by-caller" || added.Status != StatusQueued || added.Error != "" ||
		added.OutputPath != "" || added.OK != 0 || added.Total != 0 || added.AddedAt.IsZero() {
		t.Fatalf("Add kept caller-supplied state: %+v", added)
	}
	if q.Next() == nil {
		t.Fatal("a freshly added job must be picked up by Next")
	}
}
