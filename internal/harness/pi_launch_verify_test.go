package harness_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// Verifies the bundled pi manifest takes a start-agent --model choice,
// the failure the worktree was cut for.
func TestPiLaunchArgsModel(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) > 0 {
		t.Fatalf("registry load errors: %v", errs)
	}
	m := reg.Lookup("pi")
	if m == nil {
		t.Fatal("no pi manifest in bundled registry")
	}
	args, err := harness.LaunchArgs(m, harness.LaunchChoice{Model: "anthropic/claude-opus-5-5"})
	if err != nil {
		t.Fatalf("LaunchArgs: %v", err)
	}
	want := []string{"--model", "anthropic/claude-opus-5-5"}
	if len(args) != len(want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %q, want %q", args, want)
		}
	}
	if _, err := harness.LaunchArgs(m, harness.LaunchChoice{Effort: "high"}); err == nil || !strings.Contains(err.Error(), "takes no effort option") {
		t.Fatalf("pi effort choice: want the refusal naming the option, got %v", err)
	}
}
