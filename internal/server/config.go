package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chatinfra/specd/internal/apierr"
	"github.com/chatinfra/specd/internal/job"
	"github.com/chatinfra/specd/internal/openspec"
)

type Config struct {
	RepoRoot             string
	OpenSpecBin          string
	BearerToken          string
	Host                 string
	Port                 int
	JobStateDir          string
	EnableAgentWorkflows bool
	SuperRoot            string
	CommandTimeout       time.Duration
}

func DefaultConfig(getenv func(string) string) Config {
	if getenv == nil {
		getenv = os.Getenv
	}
	port := intFromEnv(getenv("SPECD_PORT"), 2781)
	timeout := durationFromEnv(getenv("SPECD_COMMAND_TIMEOUT"), 60*time.Second)
	return Config{
		RepoRoot:             getenv("SPECD_REPO_ROOT"),
		OpenSpecBin:          valueOr(getenv("SPECD_OPENSPEC_BIN"), "openspec"),
		BearerToken:          getenv("SPECD_BEARER_TOKEN"),
		Host:                 valueOr(getenv("SPECD_HOST"), "127.0.0.1"),
		Port:                 port,
		JobStateDir:          getenv("SPECD_JOB_STATE_DIR"),
		EnableAgentWorkflows: boolFromEnv(getenv("SPECD_ENABLE_AGENT_WORKFLOWS")),
		SuperRoot:            valueOr(getenv("SPECD_SUPER_ROOT"), "/data/super"),
		CommandTimeout:       timeout,
	}
}

func (c Config) Address() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.BearerToken) == "" {
		return apierr.New(http.StatusBadRequest, "missing_bearer_token", "bearer token is required")
	}
	if strings.TrimSpace(c.Host) == "" {
		return apierr.New(http.StatusBadRequest, "missing_host", "bind host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return apierr.New(http.StatusBadRequest, "invalid_port", "bind port must be between 1 and 65535")
	}
	if err := openspec.ValidateLayout(c.RepoRoot); err != nil {
		return err
	}
	if err := openspec.ValidateOpenSpecBinary(c.OpenSpecBin); err != nil {
		return err
	}
	if strings.TrimSpace(c.JobStateDir) == "" {
		return apierr.New(http.StatusBadRequest, "missing_job_state_dir", "job-state directory is required")
	}
	if err := os.MkdirAll(c.JobStateDir, 0o755); err != nil {
		return apierr.Wrap(http.StatusBadRequest, "job_state_unavailable", "job-state directory is not writable", err)
	}
	return nil
}

func (c Config) NewDependencies() (*openspec.Repository, *job.Store, error) {
	repo := openspec.NewRepository(c.RepoRoot, c.OpenSpecBin, c.CommandTimeout)
	store, err := job.NewStore(c.JobStateDir)
	if err != nil {
		return nil, nil, err
	}
	return repo, store, nil
}

func (c Config) Metadata() map[string]any {
	return map[string]any{
		"repositoryRoot": filepath.Clean(c.RepoRoot),
		"openspecBinary": c.OpenSpecBin,
		"jobStateDir":    filepath.Clean(c.JobStateDir),
		"capabilities": map[string]any{
			"artifactUpdate":          true,
			"canonicalSpecs":          true,
			"deterministicOperations": true,
			"superdirContext":         true,
			"agentWorkflows":          c.EnableAgentWorkflows,
		},
		"retentionDays": job.RetentionDays,
	}
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func intFromEnv(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolFromEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func durationFromEnv(value string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err == nil {
		return parsed
	}
	seconds, err := strconv.Atoi(value)
	if err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func (c Config) String() string {
	return fmt.Sprintf("specd %s repo=%s", c.Address(), c.RepoRoot)
}
