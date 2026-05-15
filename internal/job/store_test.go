package job

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistenceCancelSSEPromptAndRecovery(t *testing.T) {
	store, err := NewStore(filepath.Join(tempDir(t), "jobs"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	change := "alpha-change"
	created, err := store.Create("opsx-apply", Target{ChangeName: &change})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Start(created.JobID); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := store.SetPrompt(created.JobID, PromptRequest{ID: "prompt-1", Questions: []PromptQuestion{{Header: "H", Question: "Continue?"}}}); err != nil {
		t.Fatalf("SetPrompt() error = %v", err)
	}
	if _, err := store.ReplyPrompt(created.JobID, "prompt-1", [][]string{{"yes"}}); err != nil {
		t.Fatalf("ReplyPrompt() error = %v", err)
	}
	if _, err := store.Cancel(created.JobID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	events, err := store.EventsAfter(created.JobID, 0)
	if err != nil {
		t.Fatalf("EventsAfter() error = %v", err)
	}
	if len(events) < 4 || events[0].Sequence != 1 {
		t.Fatalf("events = %#v", events)
	}
	reloaded, err := NewStore(store.root)
	if err != nil {
		t.Fatalf("reload NewStore() error = %v", err)
	}
	got, err := reloaded.Get(created.JobID)
	if err != nil || got.Status != StatusFailed || got.RecoveryNote == nil {
		t.Fatalf("recovered job = %#v err=%v", got, err)
	}
}

func TestStoreRetentionCleansTerminalJobs(t *testing.T) {
	store, err := NewStore(filepath.Join(tempDir(t), "jobs"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	created, err := store.Create("archive", Target{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	finished := time.Now().UTC().Add(-31 * 24 * time.Hour)
	created.Status = StatusSucceeded
	created.FinishedAt = &finished
	if err := store.save(created); err != nil {
		t.Fatalf("save() error = %v", err)
	}
	if err := store.Cleanup(time.Now().UTC()); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.jobsDir, created.JobID+".json")); !os.IsNotExist(err) {
		t.Fatalf("terminal job was not pruned: %v", err)
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("SUPER_TMP_DIR")
	if base == "" {
		base = filepath.Join("..", "..", "..", "tmp", "specd-tests")
	}
	_ = os.MkdirAll(base, 0o755)
	dir, err := os.MkdirTemp(base, "job-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
