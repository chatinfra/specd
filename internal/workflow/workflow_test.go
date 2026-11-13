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
