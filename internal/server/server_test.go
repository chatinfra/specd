package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chatinfra/specd/internal/job"
	"github.com/chatinfra/specd/internal/workflow"
)

func TestServerAuthHealthMetadataAndEntries(t *testing.T) {
	repoRoot, openspecBin, jobDir := testRepo(t)
	superRoot := filepath.Join(repoRoot, "superdirs")
	mustWrite(t, filepath.Join(superRoot, "alpha-change-1234567890", ".env"), "CHANGE_NAME=alpha-change\n")
	mustWrite(t, filepath.Join(superRoot, "typo-change-1234567890", ".env"), "CHANGE_NAME=typo-change\n")
	mustMkdir(t, filepath.Join(superRoot, "merge-train"))
	mustWrite(t, filepath.Join(superRoot, "archive-train", ".env"), "CHANGE_NAME=alpha-change\nOPENCODE_URL=http://127.0.0.1:2777\n")
	mustWrite(t, filepath.Join(superRoot, "super-master", ".env"), "SUPER_CHANGE=alpha-change\n")
	cfg := Config{RepoRoot: repoRoot, OpenSpecBin: openspecBin, BearerToken: "secret", Host: "127.0.0.1", Port: 2781, JobStateDir: jobDir, SuperRoot: superRoot, CommandTimeout: time.Second}
	repo, store, err := cfg.NewDependencies()
	if err != nil {
		t.Fatalf("NewDependencies() error = %v", err)
	}
	handler := New(cfg, repo, store, workflow.NewRunner(store, repo, false)).Handler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/health", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", unauthorized.Code)
	}

	health := requestJSON(t, handler, http.MethodGet, "/health", nil, "secret")
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), "repositoryReady") {
		t.Fatalf("health response = %d %s", health.Code, health.Body.String())
	}
	metadata := requestJSON(t, handler, http.MethodGet, "/server", nil, "secret")
	if metadata.Code != http.StatusOK || strings.Contains(metadata.Body.String(), "secret") {
		t.Fatalf("metadata leaked secret or failed: %d %s", metadata.Code, metadata.Body.String())
	}
	entries := requestJSON(t, handler, http.MethodGet, "/entries?kind=active", nil, "secret")
	if entries.Code != http.StatusOK || !strings.Contains(entries.Body.String(), "alpha-change") {
		t.Fatalf("entries response = %d %s", entries.Code, entries.Body.String())
	}
	superdirs := requestJSON(t, handler, http.MethodGet, "/superdirs", nil, "secret")
	if superdirs.Code != http.StatusOK || !strings.Contains(superdirs.Body.String(), "typo-change") {
		t.Fatalf("superdirs response = %d %s", superdirs.Code, superdirs.Body.String())
	}
	var superdirsBody struct {
		Superdirs []struct {
			DirectoryName   string `json:"directoryName"`
			TargetName      string `json:"targetName"`
			AssociationKind string `json:"associationKind"`
		} `json:"superdirs"`
	}
	if err := json.Unmarshal(superdirs.Body.Bytes(), &superdirsBody); err != nil {
		t.Fatalf("superdirs JSON invalid: %v", err)
	}
	foundUnmatched := false
	foundInfrastructure := map[string]bool{}
	for _, item := range superdirsBody.Superdirs {
		if item.TargetName == "typo-change" && item.AssociationKind == "unmatched" {
			foundUnmatched = true
		}
		if item.AssociationKind == "infrastructure" {
			foundInfrastructure[item.DirectoryName] = true
		}
	}
	if !foundUnmatched {
		t.Fatalf("superdirs did not include unmatched metadata: %#v", superdirsBody.Superdirs)
	}
	for _, dirName := range []string{"merge-train", "archive-train", "super-master"} {
		if !foundInfrastructure[dirName] {
			t.Fatalf("superdirs did not include infrastructure metadata for %s: %#v", dirName, superdirsBody.Superdirs)
		}
	}
	detail := requestJSON(t, handler, http.MethodGet, "/entries/active/alpha-change", nil, "secret")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail response = %d %s", detail.Code, detail.Body.String())
	}
	var detailBody struct {
		AssociatedSuperdirs []struct {
			TargetName        string `json:"targetName"`
			AssociationKind   string `json:"associationKind"`
			AssociationSource string `json:"associationSource"`
		} `json:"associatedSuperdirs"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("detail JSON invalid: %v", err)
	}
	if len(detailBody.AssociatedSuperdirs) != 1 || detailBody.AssociatedSuperdirs[0].TargetName != "alpha-change" {
		t.Fatalf("associated superdirs not scoped: %#v", detailBody.AssociatedSuperdirs)
	}
}

func TestServerArtifactRevisionConflictAndAgentGate(t *testing.T) {
	repoRoot, openspecBin, jobDir := testRepo(t)
	cfg := Config{RepoRoot: repoRoot, OpenSpecBin: openspecBin, BearerToken: "secret", Host: "127.0.0.1", Port: 2781, JobStateDir: jobDir, CommandTimeout: time.Second, EnableAgentWorkflows: false}
	repo, store, err := cfg.NewDependencies()
	if err != nil {
		t.Fatalf("NewDependencies() error = %v", err)
	}
	handler := New(cfg, repo, store, workflow.NewRunner(store, repo, false)).Handler()

	read := requestJSON(t, handler, http.MethodGet, "/artifacts/content?entryKind=active&entryId=alpha-change&path=openspec/changes/alpha-change/tasks.md", nil, "secret")
	if read.Code != http.StatusOK {
		t.Fatalf("read artifact = %d %s", read.Code, read.Body.String())
	}
	var content struct {
		RevisionToken string `json:"revisionToken"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &content); err != nil || content.RevisionToken == "" {
		t.Fatalf("artifact JSON missing token: %v %s", err, read.Body.String())
	}
	update := map[string]string{"entryKind": "active", "entryId": "alpha-change", "path": "openspec/changes/alpha-change/tasks.md", "content": "- [x] done\n", "expectedRevisionToken": content.RevisionToken}
	updated := requestJSON(t, handler, http.MethodPut, "/artifacts", update, "secret")
	if updated.Code != http.StatusOK {
		t.Fatalf("update artifact = %d %s", updated.Code, updated.Body.String())
	}
	stale := requestJSON(t, handler, http.MethodPut, "/artifacts", update, "secret")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale artifact = %d %s", stale.Code, stale.Body.String())
	}
	disabled := requestJSON(t, handler, http.MethodPost, "/opsx/apply", map[string]string{"changeName": "alpha-change"}, "secret")
	if disabled.Code != http.StatusForbidden || !strings.Contains(disabled.Body.String(), "agent_workflows_disabled") {
		t.Fatalf("agent disabled = %d %s", disabled.Code, disabled.Body.String())
	}
}

func TestServerAgentWorkflowAcceptedWhenEnabled(t *testing.T) {
	repoRoot, openspecBin, jobDir := testRepo(t)
	cfg := Config{RepoRoot: repoRoot, OpenSpecBin: openspecBin, BearerToken: "secret", Host: "127.0.0.1", Port: 2781, JobStateDir: jobDir, CommandTimeout: time.Second, EnableAgentWorkflows: true}
	repo, store, err := cfg.NewDependencies()
	if err != nil {
		t.Fatalf("NewDependencies() error = %v", err)
	}
	handler := New(cfg, repo, store, workflow.NewRunner(store, repo, true)).Handler()
	accepted := requestJSON(t, handler, http.MethodPost, "/opsx/ff", map[string]string{"changeName": "alpha-change"}, "secret")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("agent accepted = %d %s", accepted.Code, accepted.Body.String())
	}
	var resource job.Resource
	if err := json.Unmarshal(accepted.Body.Bytes(), &resource); err != nil {
		t.Fatalf("job JSON invalid: %v", err)
	}
	for i := 0; i < 20; i++ {
		got := requestJSON(t, handler, http.MethodGet, "/jobs/"+resource.JobID, nil, "secret")
		var current job.Resource
		_ = json.Unmarshal(got.Body.Bytes(), &current)
		if current.Status == job.StatusSucceeded {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agent job did not succeed")
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func testRepo(t *testing.T) (string, string, string) {
	t.Helper()
	root := tempDir(t)
	repoRoot := filepath.Join(root, "repo")
	mustMkdir(t, filepath.Join(repoRoot, "openspec", "changes", "alpha-change", "specs", "alpha"))
	mustMkdir(t, filepath.Join(repoRoot, "openspec", "specs", "canonical-alpha"))
	mustWrite(t, filepath.Join(repoRoot, "openspec", "project.md"), "# project\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "changes", "alpha-change", "proposal.md"), "## Why\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "changes", "alpha-change", "tasks.md"), "- [ ] do it\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "changes", "alpha-change", "specs", "alpha", "spec.md"), "## ADDED Requirements\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "specs", "canonical-alpha", "spec.md"), "## Requirements\n")
	bin := filepath.Join(root, "openspec")
	mustWrite(t, bin, "#!/usr/bin/env bash\nif [[ \"$1\" == \"status\" ]]; then printf '{\"changeName\":\"alpha-change\",\"schemaName\":\"spec-driven\",\"isComplete\":false,\"artifacts\":[]}'; exit 0; fi\nif [[ \"$1\" == \"instructions\" ]]; then printf '{\"changeName\":\"alpha-change\",\"schemaName\":\"spec-driven\",\"state\":\"ready\",\"instruction\":\"go\"}'; exit 0; fi\nif [[ \"$1\" == \"validate\" ]]; then printf 'valid'; exit 0; fi\nprintf 'ok'; exit 0\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod fake openspec: %v", err)
	}
	return repoRoot, bin, filepath.Join(root, "jobs")
}

func tempDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("SUPER_TMP_DIR")
	if base == "" {
		base = filepath.Join("..", "..", "..", "tmp", "specd-tests")
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		t.Fatalf("abs temp base: %v", err)
	}
	if err := os.MkdirAll(absBase, 0o755); err != nil {
		t.Fatalf("mkdir temp base: %v", err)
	}
	dir, err := os.MkdirTemp(absBase, "specd-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
