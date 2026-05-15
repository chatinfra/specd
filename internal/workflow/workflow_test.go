package workflow

import "testing"

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
