package harness_test

import (
	"fmt"

	"tuios.dev/harness"
)

// Example names the agent a process is, then reads its state from the last
// lines of its screen.
func Example() {
	reg, errs := harness.Load() // the bundled manifests; pass folders to add a person's own
	if len(errs) > 0 {
		fmt.Println(errs)
		return
	}

	// What the kernel says about the process in a pane.
	id, ok := reg.Identify("claude", []string{"claude", "--model", "opus"}, "/usr/local/bin/claude")
	fmt.Println(id, ok)

	// The last non-empty lines of the pane, as text.
	screen := []string{
		"Bash(go test ./...)",
		"Do you want to proceed?",
		"❯ 1. Yes",
		"  2. Yes, and don't ask again for similar commands",
		"  3. No, and tell Claude what to do differently (esc)",
	}
	state, rule, ok := reg.Classify(id, screen)
	fmt.Println(state, ok)
	fmt.Println(reg.RuleKind(id, rule))
	fmt.Println(reg.RulePrompt(id, rule, screen))
	// Output:
	// claude-code true
	// needs_input true
	// approval
	// Do you want to proceed?
}

// ExampleRegistry_Explain shows why each screen rule matched or did not.
func ExampleRegistry_Explain() {
	reg, _ := harness.Load()
	state, rule, reports := reg.Explain("codex", []string{"› Ask Codex to do anything", "  ? for shortcuts"})
	fmt.Println(state, rule)
	for _, r := range reports {
		if !r.Matched && len(r.Missing) > 0 {
			fmt.Printf("rule %d (%s) refused, missing %q\n", r.Index, r.State, r.Missing)
			break
		}
	}
	// Output:
	// idle 7
	// rule 2 (needs_input) refused, missing ["update available!" "update now"]
}
