package workflow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chatinfra/specd/internal/job"
	"github.com/chatinfra/specd/internal/openspec"
)

func TestSkillCommandMapping(t *testing.T) {
	cases := map[string]string{
		"propose":  "/opsx-propose",
		"continue": "/opsx-continue",
		"ff":       "/opsx-ff",
		"apply":    "/opsx-apply",
		"verify":   "/opsx-verify",
	}
	for operation, want := range cases {
		got, err := SkillCommand(operation)
		if err != nil || got != want {
			t.Fatalf("SkillCommand(%q) = %q, %v; want %q", operation, got, err, want)
		}
	}
	if _, err := SkillCommand("shell"); err == nil {
		t.Fatal("unknown workflow unexpectedly succeeded")
	}
}

func TestSuperdirSetupArgsUseDirtyFlagWhenAllowed(t *testing.T) {
	got := superdirSetupArgs("change-123", true)
	want := []string{"setup", "change-123", "--work-item-kind", "openspec-change", "--json", "--dirty"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("superdirSetupArgs(..., true) = %#v; want %#v", got, want)
	}
}

func TestSuperdirSetupArgsOmitDirtyFlagByDefault(t *testing.T) {
	got := superdirSetupArgs("change-123", false)
	want := []string{"setup", "change-123", "--work-item-kind", "openspec-change", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("superdirSetupArgs(..., false) = %#v; want %#v", got, want)
	}
}

// The submitted setup must reach the superdir CLI with an explicit OpenSpec kind, so a change the
// serve catalog has not seen yet is admitted as that change rather than refused as an undeclared name.
func TestSubmitSuperdirSetupDeclaresOpenSpecKindToTheCLI(t *testing.T) {
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nprintf '{}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "superdir"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := job.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Store: store}

	resource, err := runner.SubmitSuperdirSetup(context.Background(), SuperdirSetupRequest{ChangeName: "new-uncataloged-change"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := store.Get(resource.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == job.StatusSucceeded {
			break
		}
		if got.Status == job.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("setup job did not succeed: %#v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(raw))
	want := []string{"setup", "new-uncataloged-change", "--work-item-kind", "openspec-change", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("superdir invoked with %#v; want %#v", got, want)
	}
}

// An archive or sync of a change carrying canonical text operations must fail with the typed refusal
// before any OpenSpec command runs: only the publisher's `canon-ops apply` applies operations, and a
// native archive here would leave them unapplied. A malformed canon-ops entry is refused the same way.
func TestCanonOperationsChangeIsRefusedBeforeAnyOpenSpecCommand(t *testing.T) {
	for _, operation := range []string{"archive", "sync"} {
		for _, shape := range []string{"directory", "malformed-file"} {
			t.Run(operation+"/"+shape, func(t *testing.T) {
				root := t.TempDir()
				changeDir := filepath.Join(root, "openspec", "changes", "carries-ops")
				if err := os.MkdirAll(changeDir, 0o755); err != nil {
					t.Fatal(err)
				}
				opsPath := filepath.Join(changeDir, "canon-ops")
				if shape == "directory" {
					if err := os.MkdirAll(opsPath, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(opsPath, "cap.md"), []byte("# Canonical text operations: cap\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(opsPath, []byte("not a directory\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				invocations := filepath.Join(t.TempDir(), "invocations")
				stub := filepath.Join(t.TempDir(), "openspec")
				script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + invocations + "\nexit 0\n"
				if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				store, err := job.NewStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				runner := &Runner{Store: store, Repo: openspec.NewRepository(root, stub, 10*time.Second)}

				resource, err := runner.SubmitOpenSpecJob(context.Background(), operation, "carries-ops")
				if err != nil {
					t.Fatal(err)
				}
				got := waitForTerminalJob(t, store, resource.JobID)
				if got.Status != job.StatusFailed {
					t.Fatalf("%s of a change carrying canon-ops/ did not fail: %#v", operation, got)
				}
				if got.Error == nil || got.Error.Code != CanonOperationsRefusedCode {
					t.Fatalf("%s failed without the typed canon-ops refusal: %#v", operation, got.Error)
				}
				if !strings.Contains(got.Error.Message, "carries-ops") {
					t.Fatalf("refusal does not name the change: %q", got.Error.Message)
				}
				if raw, err := os.ReadFile(invocations); err == nil {
					t.Fatalf("the OpenSpec CLI was invoked for a refused %s: %q", operation, string(raw))
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
			})
		}
	}
}

// A change without canon-ops/ still reaches the OpenSpec CLI, so the refusal is scoped to operations.
func TestChangeWithoutCanonOperationsStillRunsOpenSpec(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "openspec", "changes", "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	invocations := filepath.Join(t.TempDir(), "invocations")
	stub := filepath.Join(t.TempDir(), "openspec")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + invocations + "\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := job.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Store: store, Repo: openspec.NewRepository(root, stub, 10*time.Second)}
	resource, err := runner.SubmitOpenSpecJob(context.Background(), "archive", "plain")
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTerminalJob(t, store, resource.JobID); got.Status != job.StatusSucceeded {
		t.Fatalf("archive of a change without canon-ops/ did not succeed: %#v", got)
	}
	raw, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "archive plain" {
		t.Fatalf("OpenSpec invoked with %q; want %q", string(raw), "archive plain")
	}
}

func waitForTerminalJob(t *testing.T, store *job.Store, jobID string) job.Resource {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := store.Get(jobID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == job.StatusSucceeded || got.Status == job.StatusFailed {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not finish: %#v", jobID, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
