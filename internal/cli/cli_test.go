package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestJSONFlagRejectedWithYAMLError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"--json"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected --json to be rejected")
	}
	doc := requireYAMLError(t, stdout.String(), stderr.String(), "error.schema.yaml")
	envelope, ok := doc.(map[string]any)["error"].(map[string]any)
	if !ok {
		t.Fatalf("error envelope missing: %#v", doc)
	}
	code, _ := envelope["code"].(string)
	message, _ := envelope["message"].(string)
	if code == "" || !strings.Contains(message, "--json") {
		t.Fatalf("unsupported --json envelope = %#v", envelope)
	}
}

func TestRootHelpHasHumanReadableSections(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "bare", args: nil},
		{name: "help", args: []string{"help"}},
		{name: "long flag", args: []string{"--help"}},
		{name: "short flag", args: []string{"-h"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(tc.args, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if stderr.String() != "" {
				t.Fatalf("stderr = %q; want empty", stderr.String())
			}
			out := stdout.String()
			assertSectionOrder(t, out, []string{"USAGE", "COMMANDS", "FLAGS", "OUTPUT", "EXAMPLES", "SEE ALSO"})
			if strings.Contains(out, "commands:") || strings.Contains(out, "schemas:") {
				t.Fatalf("root help still looks like YAML dump:\n%s", out)
			}
		})
	}
}

func TestRootHelpListsCommandsAndFlagDefaults(t *testing.T) {
	t.Setenv("SPECD_BEARER_TOKEN", "super-secret-token")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"serve             Start the OpenSpec API daemon.",
		"validate-config   Validate daemon configuration.",
		"schemas           List structured YAML output schemas.",
		"--repo-root DIR", "(default: SPECD_REPO_ROOT)",
		"--openspec-bin PATH", "(default: SPECD_OPENSPEC_BIN or openspec)",
		"--bearer-token TOKEN", "(default: SPECD_BEARER_TOKEN)",
		"--host HOST", "(default: SPECD_HOST or 127.0.0.1)",
		"--port PORT", "(default: SPECD_PORT or 2781)",
		"--job-state-dir DIR", "(default: SPECD_JOB_STATE_DIR)",
		"--super-root DIR", "(default: SPECD_SUPER_ROOT or /data/super)",
		"--command-timeout DURATION", "(default: SPECD_COMMAND_TIMEOUT or 60s)",
		"--enable-agent-workflows", "(default: SPECD_ENABLE_AGENT_WORKFLOWS or false)",
		"specd schemas",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("root help missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "super-secret-token") {
		t.Fatalf("root help leaked configured bearer token:\n%s", out)
	}
}

func TestRootHelpIsNotStructuredYAMLDump(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := yaml.Unmarshal([]byte(stdout.String()), &decoded); err != nil {
		return
	}
	mapping, ok := normalizeYAML(decoded).(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"commands", "flags", "schemas"} {
		if _, exists := mapping[key]; exists {
			t.Fatalf("root help parsed as old YAML help map with key %q: %#v", key, mapping)
		}
	}
}

func TestCommandHelpBeforeSideEffects(t *testing.T) {
	commands := []string{"serve", "validate-config", "schemas"}
	for _, command := range commands {
		forms := [][]string{{"help", command}, {command, "--help"}, {command, "-h"}}
		for _, args := range forms {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				if err := Run(args, &stdout, &stderr); err != nil {
					t.Fatalf("Run(%v) error = %v stderr=%s", args, err, stderr.String())
				}
				if stderr.String() != "" {
					t.Fatalf("stderr = %q; want empty", stderr.String())
				}
				out := stdout.String()
				assertSectionOrder(t, out, []string{"USAGE", "OUTPUT", "EXAMPLES"})
				if !strings.Contains(out, "specd "+command) {
					t.Fatalf("command help missing command name %q:\n%s", command, out)
				}
				if strings.Contains(out, "command: "+command) || strings.Contains(out, "status:") {
					t.Fatalf("command help emitted normal command YAML output:\n%s", out)
				}
			})
		}
	}
}

func TestUnknownCommandHelpUsesYAMLError(t *testing.T) {
	for _, args := range [][]string{{"help", "bogus"}, {"bogus", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(args, &stdout, &stderr)
			if err == nil {
				t.Fatal("expected unknown command help error")
			}
			doc := requireYAMLError(t, stdout.String(), stderr.String(), "error.schema.yaml")
			envelope := doc.(map[string]any)["error"].(map[string]any)
			if envelope["code"] != "unknown_command" {
				t.Fatalf("error envelope = %#v", envelope)
			}
			details, _ := envelope["details"].(map[string]any)
			hint, _ := details["hint"].(string)
			if !strings.Contains(hint, "specd help") {
				t.Fatalf("missing help hint in envelope: %#v", envelope)
			}
		})
	}
}

func TestSchemasYAMLDiscoveryOmitsTerminalHelpSchema(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"schemas"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	doc := requireYAMLStdout(t, stdout.String(), stderr.String(), "schemas.schema.yaml")
	requireSchemaDiscoveryPaths(t, doc)
	schemas := doc.(map[string]any)["schemas"].([]any)
	ids := map[string]bool{}
	for _, item := range schemas {
		entry := item.(map[string]any)
		id, _ := entry["id"].(string)
		ids[id] = true
	}
	for _, want := range []string{"schemas", "error", "validate-config", "serve"} {
		if !ids[want] {
			t.Fatalf("schema discovery missing %q: %#v", want, ids)
		}
	}
	if ids["help"] {
		t.Fatalf("schema discovery still advertises terminal help schema: %#v", ids)
	}
}

func TestValidateConfigUsesServeFlags(t *testing.T) {
	root := tempDir(t)
	repoRoot := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, "openspec"), 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "openspec", "project.md"), []byte("# project\n"), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}
	bin := filepath.Join(root, "openspec")
	if err := os.WriteFile(bin, []byte("#!/usr/bin/env bash\nprintf '{}'\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod bin: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := Run([]string{"--repo-root", repoRoot, "--openspec-bin", bin, "--bearer-token", "secret", "--job-state-dir", filepath.Join(root, "jobs"), "validate-config"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run(validate-config) error = %v stderr=%s", err, stderr.String())
	}
	doc := requireYAMLStdout(t, stdout.String(), stderr.String(), "validate-config.schema.yaml")
	if got := doc.(map[string]any)["status"]; got != "ok" {
		t.Fatalf("status=%#v", got)
	}
}

func TestValidateConfigRejectsMissingToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"validate-config"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected missing token error")
	}
	doc := requireYAMLError(t, stdout.String(), stderr.String(), "error.schema.yaml")
	envelope := doc.(map[string]any)["error"].(map[string]any)
	if envelope["code"] != "missing_bearer_token" {
		t.Fatalf("error envelope = %#v", envelope)
	}
}

func TestUnknownCommandYAMLError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected unknown command error")
	}
	doc := requireYAMLError(t, stdout.String(), stderr.String(), "error.schema.yaml")
	envelope := doc.(map[string]any)["error"].(map[string]any)
	if envelope["code"] != "unknown_command" {
		t.Fatalf("error envelope = %#v", envelope)
	}
}

func assertSectionOrder(t *testing.T, text string, sections []string) {
	t.Helper()
	last := -1
	for _, section := range sections {
		idx := strings.Index(text, "\n"+section+"\n")
		if idx == -1 {
			if strings.HasPrefix(text, section+"\n") {
				idx = 0
			} else {
				t.Fatalf("missing section %q in:\n%s", section, text)
			}
		}
		if idx <= last {
			t.Fatalf("section %q appears out of order in:\n%s", section, text)
		}
		last = idx
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("SUPER_TMP_DIR")
	if base == "" {
		base = filepath.Join(yamlTestModuleRoot(t), "tmp", "specd-tests")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir temp base: %v", err)
	}
	dir, err := os.MkdirTemp(base, "cli-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
