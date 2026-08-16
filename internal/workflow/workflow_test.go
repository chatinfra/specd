package workflow

import (
	"reflect"
	"testing"
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
	want := []string{"setup", "change-123", "--json", "--dirty"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("superdirSetupArgs(..., true) = %#v; want %#v", got, want)
	}
}

func TestSuperdirSetupArgsOmitDirtyFlagByDefault(t *testing.T) {
	got := superdirSetupArgs("change-123", false)
	want := []string{"setup", "change-123", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("superdirSetupArgs(..., false) = %#v; want %#v", got, want)
	}
}
