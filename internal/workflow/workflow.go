package workflow

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/chatinfra/specd/internal/apierr"
	"github.com/chatinfra/specd/internal/job"
	"github.com/chatinfra/specd/internal/openspec"
)

type OpsxRequest struct {
	ChangeName *string `json:"changeName,omitempty"`
	Prompt     *string `json:"prompt,omitempty"`
	DryRun     bool    `json:"dryRun"`
}

type SuperdirSetupRequest struct {
	ChangeName string `json:"changeName"`
	AllowDirty bool   `json:"allowDirty"`
}

type AgentExecutor interface {
	Run(ctx context.Context, operation string, skillCommand string, request OpsxRequest) (map[string]any, error)
}

type RecordingAgentExecutor struct{}

func (RecordingAgentExecutor) Run(ctx context.Context, operation string, skillCommand string, request OpsxRequest) (map[string]any, error) {
	result := map[string]any{"kind": operation, "skillCommand": skillCommand, "dispatched": true, "dryRun": request.DryRun}
	if request.ChangeName != nil {
		result["changeName"] = *request.ChangeName
	}
	if request.Prompt != nil {
		result["prompt"] = *request.Prompt
	}
	return result, nil
}

type Runner struct {
	Store                 *job.Store
	Repo                  *openspec.Repository
	AgentWorkflowsEnabled bool
	AgentExecutor         AgentExecutor
}

func NewRunner(store *job.Store, repo *openspec.Repository, agentEnabled bool) *Runner {
	return &Runner{Store: store, Repo: repo, AgentWorkflowsEnabled: agentEnabled, AgentExecutor: RecordingAgentExecutor{}}
}

func (r *Runner) SubmitOpenSpecJob(ctx context.Context, operation, changeName string) (job.Resource, error) {
	changeName = strings.TrimSpace(changeName)
	if changeName == "" {
		return job.Resource{}, apierr.New(http.StatusBadRequest, "missing_change_name", "changeName is required")
	}
	target := job.Target{ChangeName: &changeName}
	resource, err := r.Store.Create(operation, target)
	if err != nil {
		return job.Resource{}, err
	}
	go r.runOpenSpecJob(context.Background(), resource.JobID, operation, changeName)
	return resource, nil
}

func (r *Runner) SubmitSuperdirSetup(ctx context.Context, req SuperdirSetupRequest) (job.Resource, error) {
	changeName := strings.TrimSpace(req.ChangeName)
	if changeName == "" {
		return job.Resource{}, apierr.New(http.StatusBadRequest, "missing_change_name", "changeName is required")
	}
	resource, err := r.Store.Create("superdir-setup", job.Target{ChangeName: &changeName})
	if err != nil {
		return job.Resource{}, err
	}
	go func() {
		_, _ = r.Store.Start(resource.JobID)
		_ = r.Store.Append(resource.JobID, "job.progress", "running superdir setup", map[string]any{"changeName": changeName, "allowDirty": req.AllowDirty})
		args := superdirSetupArgs(changeName, req.AllowDirty)
		cmd := exec.CommandContext(context.Background(), "superdir", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			_, _ = r.Store.Fail(resource.JobID, job.ErrorResponse{Code: "superdir_setup_failed", Message: strings.TrimSpace(stderr.String() + "\n" + err.Error())})
			return
		}
		_, _ = r.Store.Succeed(resource.JobID, "Superdir setup completed", map[string]any{"kind": "superdir-setup", "changeName": changeName, "allowDirty": req.AllowDirty, "stdout": stdout.String(), "stderr": stderr.String()})
	}()
	return resource, nil
}

func (r *Runner) SubmitAgent(ctx context.Context, operation string, req OpsxRequest) (job.Resource, error) {
	if !r.AgentWorkflowsEnabled {
		return job.Resource{}, apierr.New(http.StatusForbidden, "agent_workflows_disabled", "agent-backed workflows are disabled by server configuration")
	}
	skill, err := SkillCommand(operation)
	if err != nil {
		return job.Resource{}, err
	}
	target := job.Target{}
	if req.ChangeName != nil && strings.TrimSpace(*req.ChangeName) != "" {
		name := strings.TrimSpace(*req.ChangeName)
		target.ChangeName = &name
	}
	resource, err := r.Store.Create("opsx-"+operation, target)
	if err != nil {
		return job.Resource{}, err
	}
	go r.runAgentJob(context.Background(), resource.JobID, operation, skill, req)
	return resource, nil
}

func superdirSetupArgs(changeName string, allowDirty bool) []string {
	args := []string{"setup", changeName, "--json"}
	if allowDirty {
		args = append(args, "--dirty")
	}
	return args
}

func SkillCommand(operation string) (string, error) {
	switch operation {
	case "propose":
		return "/opsx-propose", nil
	case "continue":
		return "/opsx-continue", nil
	case "ff":
		return "/opsx-ff", nil
	case "apply":
		return "/opsx-apply", nil
	case "verify":
		return "/opsx-verify", nil
	default:
		return "", apierr.Errorf(http.StatusBadRequest, "unknown_agent_workflow", "unknown agent workflow %q", operation)
	}
}

func (r *Runner) runOpenSpecJob(ctx context.Context, jobID, operation, changeName string) {
	_, _ = r.Store.Start(jobID)
	_ = r.Store.Append(jobID, "job.progress", "running OpenSpec CLI operation", map[string]any{"operation": operation, "changeName": changeName})
	var stdout, stderr string
	var err error
	switch operation {
	case "sync":
		stdout, stderr, err = r.Repo.RunCommand(ctx, "sync", "--change", changeName)
	case "archive":
		stdout, stderr, err = r.Repo.RunCommand(ctx, "archive", changeName)
	default:
		err = fmt.Errorf("unsupported OpenSpec job operation %s", operation)
	}
	if err != nil {
		_, _ = r.Store.Fail(jobID, job.ErrorResponse{Code: "openspec_cli_failed", Message: strings.TrimSpace(stderr + "\n" + err.Error())})
		return
	}
	_, _ = r.Store.Succeed(jobID, "OpenSpec operation completed", map[string]any{"kind": operation, "changeName": changeName, "stdout": stdout, "stderr": stderr})
}

func (r *Runner) runAgentJob(ctx context.Context, jobID, operation, skill string, req OpsxRequest) {
	_, _ = r.Store.Start(jobID)
	_ = r.Store.Append(jobID, "job.progress", "dispatching slash-command skill", map[string]any{"operation": operation, "skillCommand": skill})
	executor := r.AgentExecutor
	if executor == nil {
		executor = RecordingAgentExecutor{}
	}
	result, err := executor.Run(ctx, operation, skill, req)
	if err != nil {
		_, _ = r.Store.Fail(jobID, job.ErrorResponse{Code: "agent_workflow_failed", Message: err.Error()})
		return
	}
	_, _ = r.Store.Succeed(jobID, "agent workflow completed", result)
}
