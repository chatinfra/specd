package openspec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/chatinfra/specd/internal/apierr"
)

func ValidateOpenSpecBinary(bin string) error {
	if strings.TrimSpace(bin) == "" {
		return apierr.New(http.StatusBadRequest, "missing_openspec_bin", "OpenSpec binary is required")
	}
	if strings.ContainsAny(bin, `/\`) {
		if _, err := exec.LookPath(bin); err == nil {
			return nil
		}
		if _, err := exec.LookPath(strings.TrimSpace(bin)); err != nil {
			return apierr.Wrap(http.StatusBadRequest, "openspec_unavailable", "OpenSpec binary is not executable", err)
		}
		return nil
	}
	if _, err := exec.LookPath(bin); err != nil {
		return apierr.Wrap(http.StatusBadRequest, "openspec_unavailable", "OpenSpec binary was not found in PATH", err)
	}
	return nil
}

func (r *Repository) Status(ctx context.Context, changeName string) (map[string]any, error) {
	return r.runJSON(ctx, "status", "--change", changeName, "--json")
}

func (r *Repository) Instructions(ctx context.Context, action, changeName string) (map[string]any, error) {
	if strings.TrimSpace(action) == "" {
		action = "apply"
	}
	return r.runJSON(ctx, "instructions", action, "--change", changeName, "--json")
}

func (r *Repository) Validate(ctx context.Context, targetType, name string) (ValidateResponse, error) {
	if targetType != "change" && targetType != "spec" {
		return ValidateResponse{}, apierr.New(http.StatusBadRequest, "invalid_validate_target", "targetType must be change or spec")
	}
	stdout, stderr, err := r.run(ctx, "validate", "--type", targetType, name)
	output := strings.TrimSpace(stdout + stderr)
	if err != nil {
		return ValidateResponse{OK: false, Output: output, Errors: []string{errorMessage(err)}}, nil
	}
	return ValidateResponse{OK: true, Output: output}, nil
}

func (r *Repository) RunCommand(ctx context.Context, args ...string) (string, string, error) {
	return r.run(ctx, args...)
}

func (r *Repository) runJSON(ctx context.Context, args ...string) (map[string]any, error) {
	stdout, stderr, err := r.run(ctx, args...)
	if err != nil {
		return nil, translateCLIError(err, stderr)
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, apierr.WithDetails(apierr.Wrap(http.StatusInternalServerError, "openspec_invalid_json", "OpenSpec CLI returned invalid JSON", err), map[string]any{"stdout": stdout, "stderr": stderr})
	}
	return payload, nil
}

func (r *Repository) run(ctx context.Context, args ...string) (string, string, error) {
	if err := ValidateOpenSpecBinary(r.OpenSpecBin); err != nil {
		return "", "", err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.OpenSpecBin, args...)
	cmd.Dir = r.Root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), apierr.New(http.StatusGatewayTimeout, "cli_timeout", "OpenSpec CLI operation timed out")
	}
	return stdout.String(), stderr.String(), err
}

func translateCLIError(err error, stderr string) error {
	if err == nil {
		return nil
	}
	var typed *apierr.Error
	if errors.As(err, &typed) {
		return typed
	}
	message := strings.TrimSpace(stderr)
	if message == "" {
		message = err.Error()
	}
	return apierr.WithDetails(apierr.New(http.StatusUnprocessableEntity, "openspec_cli_failed", message), map[string]any{"stderr": stderr})
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	var typed *apierr.Error
	if errors.As(err, &typed) {
		return typed.Message
	}
	return err.Error()
}
