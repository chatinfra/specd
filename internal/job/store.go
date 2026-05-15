package job

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/chatinfra/specd/internal/apierr"
)

const RetentionDays = 30

type Store struct {
	root        string
	jobsDir     string
	eventsDir   string
	mu          sync.Mutex
	subscribers map[string][]chan Event
}

func NewStore(root string) (*Store, error) {
	if root == "" {
		return nil, apierr.New(http.StatusBadRequest, "missing_job_state_dir", "job-state directory is required")
	}
	store := &Store{root: root, jobsDir: filepath.Join(root, "jobs"), eventsDir: filepath.Join(root, "events"), subscribers: map[string][]chan Event{}}
	if err := os.MkdirAll(store.jobsDir, 0o755); err != nil {
		return nil, apierr.Wrap(http.StatusBadRequest, "job_state_unavailable", "failed to create job snapshot directory", err)
	}
	if err := os.MkdirAll(store.eventsDir, 0o755); err != nil {
		return nil, apierr.Wrap(http.StatusBadRequest, "job_state_unavailable", "failed to create job event directory", err)
	}
	if err := store.ReloadAndRecover(time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := store.Cleanup(time.Now().UTC()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Create(operation string, target Target) (Resource, error) {
	now := time.Now().UTC()
	job := Resource{JobID: newID(), Operation: operation, Status: StatusQueued, Target: target, CreatedAt: now, CancellationRequested: false}
	if err := s.save(job); err != nil {
		return Resource{}, err
	}
	_ = s.Append(job.JobID, "job.created", "job accepted", map[string]any{"operation": operation})
	return job, nil
}

func (s *Store) List() ([]Resource, error) {
	entries, err := os.ReadDir(s.jobsDir)
	if err != nil {
		return nil, apierr.Wrap(http.StatusInternalServerError, "job_list_failed", "failed to list jobs", err)
	}
	jobs := make([]Resource, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		job, err := s.read(filepath.Join(s.jobsDir, entry.Name()))
		if err == nil {
			jobs = append(jobs, job)
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs, nil
}

func (s *Store) Get(id string) (Resource, error) {
	if id == "" {
		return Resource{}, apierr.New(http.StatusBadRequest, "missing_job_id", "job id is required")
	}
	job, err := s.read(filepath.Join(s.jobsDir, id+".json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Resource{}, apierr.New(http.StatusNotFound, "job_not_found", "job was not found")
		}
		return Resource{}, apierr.Wrap(http.StatusInternalServerError, "job_read_failed", "failed to read job", err)
	}
	return job, nil
}

func (s *Store) Start(id string) (Resource, error) {
	job, err := s.Get(id)
	if err != nil {
		return Resource{}, err
	}
	now := time.Now().UTC()
	job.Status = StatusRunning
	job.StartedAt = &now
	if err := s.save(job); err != nil {
		return Resource{}, err
	}
	_ = s.Append(id, "job.started", "job started", nil)
	return job, nil
}

func (s *Store) Succeed(id, summary string, result map[string]any) (Resource, error) {
	return s.finish(id, StatusSucceeded, summary, result, nil)
}

func (s *Store) Fail(id string, errInfo ErrorResponse) (Resource, error) {
	return s.finish(id, StatusFailed, errInfo.Message, nil, &errInfo)
}

func (s *Store) Cancel(id string) (Resource, error) {
	job, err := s.Get(id)
	if err != nil {
		return Resource{}, err
	}
	if Terminal(job.Status) {
		return Resource{}, apierr.New(http.StatusConflict, "job_terminal", "terminal jobs cannot be cancelled")
	}
	job.CancellationRequested = true
	job.Status = StatusCancelling
	if err := s.save(job); err != nil {
		return Resource{}, err
	}
	_ = s.Append(id, "job.cancel_requested", "cancellation requested", nil)
	return job, nil
}

func (s *Store) SetPrompt(id string, prompt PromptRequest) (Resource, error) {
	job, err := s.Get(id)
	if err != nil {
		return Resource{}, err
	}
	if Terminal(job.Status) {
		return Resource{}, apierr.New(http.StatusConflict, "job_terminal", "terminal jobs cannot receive prompts")
	}
	job.Status = StatusAwaitingInput
	job.PendingPrompt = &prompt
	if err := s.save(job); err != nil {
		return Resource{}, err
	}
	_ = s.Append(id, "job.prompt", "prompt requested", map[string]any{"promptId": prompt.ID})
	return job, nil
}

func (s *Store) ReplyPrompt(id, requestID string, answers [][]string) (Resource, error) {
	job, err := s.Get(id)
	if err != nil {
		return Resource{}, err
	}
	if job.PendingPrompt == nil || job.PendingPrompt.ID != requestID {
		return Resource{}, apierr.New(http.StatusConflict, "prompt_not_pending", "prompt request is not pending for this job")
	}
	job.PendingPrompt = nil
	job.Status = StatusRunning
	if err := s.save(job); err != nil {
		return Resource{}, err
	}
	_ = s.Append(id, "job.prompt_reply", "prompt reply accepted", map[string]any{"requestId": requestID, "answers": answers})
	return job, nil
}

func (s *Store) RejectPrompt(id, requestID string) (Resource, error) {
	job, err := s.Get(id)
	if err != nil {
		return Resource{}, err
	}
	if job.PendingPrompt == nil || job.PendingPrompt.ID != requestID {
		return Resource{}, apierr.New(http.StatusConflict, "prompt_not_pending", "prompt request is not pending for this job")
	}
	job.PendingPrompt = nil
	job.Status = StatusRunning
	if err := s.save(job); err != nil {
		return Resource{}, err
	}
	_ = s.Append(id, "job.prompt_reject", "prompt rejection accepted", map[string]any{"requestId": requestID})
	return job, nil
}

func (s *Store) EventsAfter(id string, after int64) ([]Event, error) {
	if _, err := s.Get(id); err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(s.eventsDir, id+".jsonl"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Event{}, nil
		}
		return nil, apierr.Wrap(http.StatusInternalServerError, "job_events_read_failed", "failed to read job events", err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err == nil && event.Sequence > after {
			events = append(events, event)
		}
	}
	return events, scanner.Err()
}

func (s *Store) Subscribe(jobID string) (<-chan Event, func()) {
	ch := make(chan Event, 16)
	s.mu.Lock()
	s.subscribers[jobID] = append(s.subscribers[jobID], ch)
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.subscribers[jobID]
		for i, sub := range subs {
			if sub == ch {
				s.subscribers[jobID] = append(subs[:i], subs[i+1:]...)
				close(ch)
				break
			}
		}
	}
}

func (s *Store) Append(jobID, eventType, message string, payload map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sequence, err := s.nextSequenceLocked(jobID)
	if err != nil {
		return err
	}
	msg := message
	event := Event{Sequence: sequence, Timestamp: time.Now().UTC(), Type: eventType, JobID: jobID, Message: &msg, Payload: payload}
	path := filepath.Join(s.eventsDir, jobID+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return apierr.Wrap(http.StatusInternalServerError, "job_event_write_failed", "failed to append job event", err)
	}
	defer file.Close()
	data, _ := json.Marshal(event)
	if _, err := file.Write(append(data, '\n')); err != nil {
		return apierr.Wrap(http.StatusInternalServerError, "job_event_write_failed", "failed to append job event", err)
	}
	for _, sub := range s.subscribers[jobID] {
		select {
		case sub <- event:
		default:
		}
	}
	return nil
}

func (s *Store) ReloadAndRecover(now time.Time) error {
	jobs, err := s.List()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, item := range jobs {
		if Terminal(item.Status) {
			continue
		}
		note := "job was not running after specd restart and was classified as failed"
		item.Status = StatusFailed
		item.FinishedAt = &now
		item.RecoveryNote = &note
		item.Error = &ErrorResponse{Code: "recovery_classified_failed", Message: note}
		if err := s.save(item); err != nil {
			return err
		}
		_ = s.Append(item.JobID, "job.failed", note, map[string]any{"recovery": true})
	}
	return nil
}

func (s *Store) Cleanup(now time.Time) error {
	jobs, err := s.List()
	if err != nil {
		return err
	}
	cutoff := now.Add(-RetentionDays * 24 * time.Hour)
	for _, item := range jobs {
		if !Terminal(item.Status) || item.FinishedAt == nil || item.FinishedAt.After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(s.jobsDir, item.JobID+".json"))
		_ = os.Remove(filepath.Join(s.eventsDir, item.JobID+".jsonl"))
	}
	return nil
}

func (s *Store) finish(id, status, summary string, result map[string]any, errInfo *ErrorResponse) (Resource, error) {
	job, err := s.Get(id)
	if err != nil {
		return Resource{}, err
	}
	now := time.Now().UTC()
	job.Status = status
	job.FinishedAt = &now
	job.PendingPrompt = nil
	job.Result = result
	job.Error = errInfo
	if summary != "" {
		job.Summary = &summary
	}
	if err := s.save(job); err != nil {
		return Resource{}, err
	}
	eventType := "job." + status
	_ = s.Append(id, eventType, summary, result)
	return job, nil
}

func (s *Store) save(job Resource) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return apierr.Wrap(http.StatusInternalServerError, "job_encode_failed", "failed to encode job", err)
	}
	path := filepath.Join(s.jobsDir, job.JobID+".json")
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (s *Store) read(path string) (Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Resource{}, err
	}
	var job Resource
	if err := json.Unmarshal(data, &job); err != nil {
		return Resource{}, err
	}
	return job, nil
}

func (s *Store) nextSequenceLocked(jobID string) (int64, error) {
	path := filepath.Join(s.eventsDir, jobID+".jsonl")
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 1, nil
		}
		return 0, err
	}
	defer file.Close()
	var last int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err == nil && event.Sequence > last {
			last = event.Sequence
		}
	}
	return last + 1, scanner.Err()
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
