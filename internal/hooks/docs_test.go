package hooks

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestHooksDocNamesEveryVariableAndEvent holds docs/HOOKS.md to what a hook
// really gets. The page used to send readers to the website for the variable
// list, so a variable added here had nowhere in the repository to be checked
// against. It runs a hook that writes its environment, and requires every
// TUIOS_ variable it saw, and every event, to be named on the page.
func TestHooksDocNamesEveryVariableAndEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hooks run through sh")
	}
	doc, err := os.ReadFile("../../docs/HOOKS.md")
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "env")
	res := executeHook("env > "+out, Context{EventType: AfterAgentState})
	if res.err != nil || res.exitCode != 0 {
		t.Fatalf("hook failed: %v, exit %d, %s", res.err, res.exitCode, res.stderr)
	}
	env, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	names := regexp.MustCompile(`(?m)^(TUIOS_[A-Z_]+)=`).FindAllStringSubmatch(string(env), -1)
	if len(names) < 10 {
		t.Fatalf("the hook saw %d TUIOS_ variables, want the whole context", len(names))
	}
	for _, m := range names {
		// A hook inherits the process environment too; only what the hook
		// context adds is the contract.
		if _, inherited := os.LookupEnv(m[1]); inherited {
			continue
		}
		if !strings.Contains(string(doc), "`"+m[1]+"`") {
			t.Errorf("docs/HOOKS.md does not name %s, which every hook gets", m[1])
		}
	}
	for _, ev := range AllEvents() {
		if !strings.Contains(string(doc), "`"+string(ev)+"`") {
			t.Errorf("docs/HOOKS.md does not name the %s event", ev)
		}
	}
}
