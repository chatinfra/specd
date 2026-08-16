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
	if len(args) == 0 || (len(args) == 1 && args[0] == "help") || args[0] == "--help" || args[0] == "-h" {
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
	if handled, err := handleHelpRequest(rest, stdout, stderr); handled {
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
		err := unknownCommandError(rest[0])
		emitError(stderr, err)
		return err
	}
}

func rejectJSONFlag(args []string) error {
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			return &commandError{
				code:    "unsupported_flag",
				message: "--json is not supported; specd emits terminal help and YAML command output",
				details: map[string]any{"argument": "--json", "hint": "Remove --json; use specd schemas for structured YAML output discovery."},
			}
		}
	}
	return nil
}

func schemaDiscovery() map[string]any {
	ids := []string{"schemas", "error", "validate-config", "serve"}
	return map[string]any{"tool": "specd", "schemas": schemaEntries(ids...)}
}

func schemaEntries(ids ...string) []map[string]string {
	schemas := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		schemas = append(schemas, map[string]string{"id": id, "path": "spec/outputs/" + id + ".schema.yaml"})
	}
	return schemas
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

func handleHelpRequest(rest []string, stdout, stderr io.Writer) (bool, error) {
	if len(rest) == 0 {
		return false, nil
	}
	if rest[0] == "help" {
		if len(rest) == 1 || rest[1] == "--help" || rest[1] == "-h" {
			return true, printUsage(stdout)
		}
		if ok, err := printCommandUsage(stdout, rest[1]); ok {
			return true, err
		}
		err := unknownCommandError(rest[1])
		emitError(stderr, err)
		return true, err
	}
	if hasHelpFlag(rest[1:]) {
		if ok, err := printCommandUsage(stdout, rest[0]); ok {
			return true, err
		}
		err := unknownCommandError(rest[0])
		emitError(stderr, err)
		return true, err
	}
	return false, nil
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func unknownCommandError(command string) *commandError {
	return &commandError{
		code:    "unknown_command",
		message: fmt.Sprintf("unknown command %q", command),
		details: map[string]any{
			"command": command,
			"hint":    "Run \"specd help\" to list commands.",
		},
	}
}

func printUsage(out io.Writer) error {
	_, err := io.WriteString(out, rootHelp)
	return err
}

func printCommandUsage(out io.Writer, command string) (bool, error) {
	help, ok := commandHelpText(command)
	if !ok {
		return false, nil
	}
	_, err := io.WriteString(out, help)
	return true, err
}

func commandHelpText(command string) (string, bool) {
	switch command {
	case "serve":
		return serveHelp, true
	case "validate-config":
		return validateConfigHelp, true
	case "schemas", "schema":
		return schemasHelp, true
	default:
		return "", false
	}
}

const rootHelp = `specd — trusted OpenSpec remote API daemon

USAGE
  specd [global flags] <command>
  specd help <command>
  specd <command> --help

COMMANDS
  serve             Start the OpenSpec API daemon.
  validate-config   Validate daemon configuration.
  schemas           List structured YAML output schemas.

FLAGS
  --repo-root DIR                  OpenSpec repository root (default: SPECD_REPO_ROOT)
  --openspec-bin PATH              OpenSpec CLI binary (default: SPECD_OPENSPEC_BIN or openspec)
  --bearer-token TOKEN             Opaque bearer token (default: SPECD_BEARER_TOKEN)
  --host HOST                      Bind host (default: SPECD_HOST or 127.0.0.1)
  --port PORT                      Bind port (default: SPECD_PORT or 2781)
  --job-state-dir DIR              Durable job-state directory (default: SPECD_JOB_STATE_DIR)
  --super-root DIR                 Superdir worktree root (default: SPECD_SUPER_ROOT or /data/super)
  --command-timeout DURATION       OpenSpec CLI command timeout (default: SPECD_COMMAND_TIMEOUT or 60s)
  --enable-agent-workflows         Enable agent-backed /opsx-* workflow endpoints (default: SPECD_ENABLE_AGENT_WORKFLOWS or false)

OUTPUT
  stdout: help is terminal text; command results are YAML for serve, validate-config, and schemas.
  stderr: failures are YAML error envelopes; secret values are never printed.
  structured discovery: run "specd schemas" for supported YAML output schemas.

EXAMPLES
  specd --repo-root /path/to/super --bearer-token "$SPECD_BEARER_TOKEN" --job-state-dir ./tmp/specd-jobs serve
  specd --repo-root /path/to/super --bearer-token "$SPECD_BEARER_TOKEN" --job-state-dir ./tmp/specd-jobs validate-config
  specd schemas

SEE ALSO
  specd help serve
  specd help validate-config
  specd schemas
`

const serveHelp = `specd serve — start the OpenSpec API daemon

USAGE
  specd [global flags] serve

FLAGS
  --repo-root DIR                  OpenSpec repository root (default: SPECD_REPO_ROOT)
  --openspec-bin PATH              OpenSpec CLI binary (default: SPECD_OPENSPEC_BIN or openspec)
  --bearer-token TOKEN             Opaque bearer token (default: SPECD_BEARER_TOKEN)
  --host HOST                      Bind host (default: SPECD_HOST or 127.0.0.1)
  --port PORT                      Bind port (default: SPECD_PORT or 2781)
  --job-state-dir DIR              Durable job-state directory (default: SPECD_JOB_STATE_DIR)
  --super-root DIR                 Superdir worktree root (default: SPECD_SUPER_ROOT or /data/super)
  --command-timeout DURATION       OpenSpec CLI command timeout (default: SPECD_COMMAND_TIMEOUT or 60s)
  --enable-agent-workflows         Enable agent-backed /opsx-* workflow endpoints (default: SPECD_ENABLE_AGENT_WORKFLOWS or false)

OUTPUT
  stdout: YAML startup status with command, status, and address before the server runs.
  stderr: YAML error envelope when configuration validation or server startup fails.
  structured discovery: run "specd schemas" and inspect schema id "serve".

EXAMPLES
  specd --repo-root /path/to/super --bearer-token "$SPECD_BEARER_TOKEN" --job-state-dir ./tmp/specd-jobs serve
  specd --host 127.0.0.1 --port 2781 --enable-agent-workflows serve
`

const validateConfigHelp = `specd validate-config — validate daemon configuration

USAGE
  specd [global flags] validate-config

FLAGS
  --repo-root DIR                  OpenSpec repository root (default: SPECD_REPO_ROOT)
  --openspec-bin PATH              OpenSpec CLI binary (default: SPECD_OPENSPEC_BIN or openspec)
  --bearer-token TOKEN             Opaque bearer token (default: SPECD_BEARER_TOKEN)
  --host HOST                      Bind host (default: SPECD_HOST or 127.0.0.1)
  --port PORT                      Bind port (default: SPECD_PORT or 2781)
  --job-state-dir DIR              Durable job-state directory (default: SPECD_JOB_STATE_DIR)
  --super-root DIR                 Superdir worktree root (default: SPECD_SUPER_ROOT or /data/super)
  --command-timeout DURATION       OpenSpec CLI command timeout (default: SPECD_COMMAND_TIMEOUT or 60s)
  --enable-agent-workflows         Enable agent-backed /opsx-* workflow endpoints (default: SPECD_ENABLE_AGENT_WORKFLOWS or false)

OUTPUT
  stdout: YAML status with command, status "ok", and address when configuration is valid.
  stderr: YAML error envelope when configuration is invalid.
  structured discovery: run "specd schemas" and inspect schema id "validate-config".

EXAMPLES
  specd --repo-root /path/to/super --bearer-token "$SPECD_BEARER_TOKEN" --job-state-dir ./tmp/specd-jobs validate-config
`

const schemasHelp = `specd schemas — list structured YAML output schemas

USAGE
  specd schemas

OUTPUT
  stdout: YAML document listing supported structured output schema ids and repo-relative schema paths.
  stderr: YAML error envelope when schema discovery fails.

EXAMPLES
  specd schemas
`

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
