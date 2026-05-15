package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestHelpAndSchemasYAML(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	requireYAMLStdout(t, stdout.String(), stderr.String(), "help.schema.yaml")

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"schemas"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	requireYAMLStdout(t, stdout.String(), stderr.String(), "schemas.schema.yaml")
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
