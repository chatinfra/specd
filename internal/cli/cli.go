package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
	"github.com/chatinfra/specd/internal/apierr"
	"github.com/chatinfra/specd/internal/server"
)

type options struct {
	repoRoot             string
	openspecBin          string
	bearerToken          string
	host                 string
	port                 int
	jobStateDir          string
	enableAgentWorkflows bool
	superRoot            string
	commandTimeout       time.Duration
}

type errorEnvelope struct {
	Error cliError `json:"error"`
}

type cliError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type commandError struct {
	code    string
	message string
	details map[string]any
}

func (e *commandError) Error() string { return e.message }

func Run(args []string, stdout, stderr io.Writer) error {
	if err := rejectJSONFlag(args); err != nil {
		emitError(stderr, err)
		return err
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return printUsage(stdout)
	}
	opts, rest, err := parse(args, os.Getenv)
	if err != nil {
		emitError(stderr, err)
		return err
	}
	if len(rest) == 0 {
		err := errors.New("missing command")
		emitError(stderr, err)
		return err
	}
	switch rest[0] {
	case "schemas", "schema":
		return writeYAML(stdout, schemaDiscovery())
	case "serve":
		cfg := configFromOptions(opts, os.Getenv)
		if err := writeYAML(stdout, statusPayload("serve", "starting", cfg)); err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := server.Serve(ctx, cfg); err != nil {
			emitError(stderr, err)
			return err
		}
		return nil
	case "validate-config":
		cfg := configFromOptions(opts, os.Getenv)
		if err := cfg.Validate(); err != nil {
			emitError(stderr, err)
			return err
		}
		return writeYAML(stdout, statusPayload("validate-config", "ok", cfg))
	default:
		err := &commandError{code: "unknown_command", message: fmt.Sprintf("unknown command %q", rest[0]), details: map[string]any{"command": rest[0]}}
		emitError(stderr, err)
		return err
	}
}

func rejectJSONFlag(args []string) error {
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			return &commandError{
				code:    "unsupported_flag",
				message: "--json is not supported; specd emits YAML output by default",
				details: map[string]any{"argument": "--json", "hint": "Remove --json and parse stdout/stderr as YAML."},
			}
		}
	}
	return nil
}

func schemaDiscovery() map[string]any {
	ids := []string{"help", "schemas", "error", "validate-config", "serve"}
	schemas := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		schemas = append(schemas, map[string]string{"id": id, "path": "spec/outputs/" + id + ".schema.yaml"})
	}
	return map[string]any{"tool": "specd", "schemas": schemas}
}

func statusPayload(command, status string, cfg server.Config) map[string]any {
	return map[string]any{
		"command": command,
		"status":  status,
		"address": cfg.Address(),
	}
}

func parse(args []string, getenv func(string) string) (options, []string, error) {
	defaults := server.DefaultConfig(getenv)
	opts := options{
		repoRoot:             defaults.RepoRoot,
		openspecBin:          defaults.OpenSpecBin,
		bearerToken:          defaults.BearerToken,
		host:                 defaults.Host,
		port:                 defaults.Port,
		jobStateDir:          defaults.JobStateDir,
		enableAgentWorkflows: defaults.EnableAgentWorkflows,
		superRoot:            defaults.SuperRoot,
		commandTimeout:       defaults.CommandTimeout,
	}
	normalized, err := normalizeGlobalFlags(args)
	if err != nil {
		return opts, nil, err
	}
	fs := flag.NewFlagSet("specd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.repoRoot, "repo-root", opts.repoRoot, "OpenSpec repository root; default SPECD_REPO_ROOT")
	fs.StringVar(&opts.openspecBin, "openspec-bin", opts.openspecBin, "OpenSpec CLI binary; default SPECD_OPENSPEC_BIN or openspec")
	fs.StringVar(&opts.bearerToken, "bearer-token", opts.bearerToken, "opaque bearer token; default SPECD_BEARER_TOKEN")
	fs.StringVar(&opts.host, "host", opts.host, "bind host; default SPECD_HOST or 127.0.0.1")
	fs.IntVar(&opts.port, "port", opts.port, "bind port; default SPECD_PORT or 2781")
	fs.StringVar(&opts.jobStateDir, "job-state-dir", opts.jobStateDir, "durable job-state directory; default SPECD_JOB_STATE_DIR")
	fs.BoolVar(&opts.enableAgentWorkflows, "enable-agent-workflows", opts.enableAgentWorkflows, "enable agent-backed /opsx-* workflow endpoints")
	fs.StringVar(&opts.superRoot, "super-root", opts.superRoot, "Superdir worktree root; default SPECD_SUPER_ROOT or /data/super")
	fs.DurationVar(&opts.commandTimeout, "command-timeout", opts.commandTimeout, "OpenSpec CLI command timeout")
	if err := fs.Parse(normalized); err != nil {
		return opts, nil, err
	}
	return opts, fs.Args(), nil
}

func normalizeGlobalFlags(args []string) ([]string, error) {
	boolFlags := map[string]bool{"enable-agent-workflows": true}
	valueFlags := map[string]bool{"repo-root": true, "openspec-bin": true, "bearer-token": true, "host": true, "port": true, "job-state-dir": true, "super-root": true, "command-timeout": true}
	globals := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			rest = append(rest, arg)
			continue
		}
		nameValue := strings.TrimPrefix(arg, "--")
		name, value, hasValue := strings.Cut(nameValue, "=")
		if boolFlags[name] {
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return nil, fmt.Errorf("flag --%s expects a boolean", name)
				}
				globals = append(globals, "--"+name+"="+strconv.FormatBool(parsed))
			} else {
				globals = append(globals, "--"+name)
			}
			continue
		}
		if valueFlags[name] {
			globals = append(globals, "--"+name)
			if hasValue {
				globals = append(globals, value)
				continue
			}
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag --%s requires a value", name)
			}
			i++
			globals = append(globals, args[i])
			continue
		}
		rest = append(rest, arg)
	}
	return append(globals, rest...), nil
}

func configFromOptions(opts options, getenv func(string) string) server.Config {
	return server.Config{
		RepoRoot:             opts.repoRoot,
		OpenSpecBin:          opts.openspecBin,
		BearerToken:          opts.bearerToken,
		Host:                 opts.host,
		Port:                 opts.port,
		JobStateDir:          opts.jobStateDir,
		EnableAgentWorkflows: opts.enableAgentWorkflows,
		SuperRoot:            opts.superRoot,
		CommandTimeout:       opts.commandTimeout,
	}
}

func emitError(stderr io.Writer, err error) {
	_ = writeYAML(stderr, errorEnvelope{Error: errorDetails(err)})
}

func errorDetails(err error) cliError {
	var commandErr *commandError
	if errors.As(err, &commandErr) && commandErr != nil {
		return cliError{Code: commandErr.code, Message: commandErr.message, Details: commandErr.details}
	}
	var apiError *apierr.Error
	if errors.As(err, &apiError) && apiError != nil {
		return cliError{Code: apiError.Code, Message: apiError.Message, Details: apiError.Details}
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	return cliError{Code: "internal_error", Message: message}
}

func printUsage(out io.Writer) error {
	return writeYAML(out, map[string]any{
		"tool":    "specd",
		"summary": "Trusted OpenSpec remote API daemon",
		"usage":   "specd [global flags] <command>",
		"flags": []map[string]string{
			{"name": "--repo-root DIR", "summary": "OpenSpec repository root; env SPECD_REPO_ROOT"},
			{"name": "--openspec-bin PATH", "summary": "OpenSpec CLI binary; env SPECD_OPENSPEC_BIN"},
			{"name": "--bearer-token TOKEN", "summary": "required bearer token; env SPECD_BEARER_TOKEN"},
			{"name": "--host HOST", "summary": "bind host; default 127.0.0.1"},
			{"name": "--port PORT", "summary": "bind port; default 2781"},
			{"name": "--job-state-dir DIR", "summary": "durable job-state directory; env SPECD_JOB_STATE_DIR"},
			{"name": "--super-root DIR", "summary": "Superdir worktree root; env SPECD_SUPER_ROOT or /data/super"},
			{"name": "--command-timeout DURATION", "summary": "OpenSpec CLI command timeout"},
			{"name": "--enable-agent-workflows", "summary": "enable agent-backed /opsx-* workflow endpoints"},
		},
		"commands": []map[string]string{
			{"name": "serve", "summary": "start the OpenSpec API daemon", "schema": "serve"},
			{"name": "validate-config", "summary": "validate daemon configuration", "schema": "validate-config"},
			{"name": "schemas", "summary": "list output schemas", "schema": "schemas"},
		},
		"schemas": []string{"help", "schemas", "error", "serve", "validate-config"},
	})
}

func writeYAML(out io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	data, err = yaml.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}
