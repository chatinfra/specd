package job

import "time"

const (
	StatusQueued        = "queued"
	StatusRunning       = "running"
	StatusAwaitingInput = "awaiting_input"
	StatusCancelling    = "cancelling"
	StatusSucceeded     = "succeeded"
	StatusFailed        = "failed"
	StatusCancelled     = "cancelled"
)

type ErrorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type Target struct {
	ChangeName *string `json:"changeName,omitempty"`
	EntryKind  *string `json:"entryKind,omitempty"`
	SuperdirID *string `json:"superdirId,omitempty"`
}

type Resource struct {
	JobID                 string         `json:"jobId"`
	Operation             string         `json:"operation"`
	Status                string         `json:"status"`
	Target                Target         `json:"target"`
	CreatedAt             time.Time      `json:"createdAt"`
	StartedAt             *time.Time     `json:"startedAt,omitempty"`
	FinishedAt            *time.Time     `json:"finishedAt,omitempty"`
	Summary               *string        `json:"summary,omitempty"`
	Result                map[string]any `json:"result,omitempty"`
	Error                 *ErrorResponse `json:"error,omitempty"`
	PendingPrompt         *PromptRequest `json:"pendingPrompt,omitempty"`
	CancellationRequested bool           `json:"cancellationRequested"`
	RecoveryNote          *string        `json:"recoveryNote,omitempty"`
}

type ListResponse struct {
	Jobs []Resource `json:"jobs"`
}

type Event struct {
	Sequence  int64          `json:"sequence"`
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"`
	JobID     string         `json:"jobId"`
	Message   *string        `json:"message,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type PromptRequest struct {
	ID        string           `json:"id"`
	Questions []PromptQuestion `json:"questions"`
}

type PromptQuestion struct {
	Header   string         `json:"header"`
	Question string         `json:"question"`
	Options  []PromptOption `json:"options"`
	Multiple bool           `json:"multiple"`
	Custom   bool           `json:"custom"`
}

type PromptOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type PromptReplyRequest struct {
	Answers [][]string `json:"answers"`
}

func Terminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}
