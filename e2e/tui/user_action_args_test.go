package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestUserActionAsksAndTakesTypedArgs: an [[actions]] entry with typed args
// and an ask step. Its key asks the step in the palette and runs with the
// answer and the defaults. tuios actions run takes key=value, checks each
// value against its type, asks only for what it was not given, and prints the
// values it ran with as typed JSON. A cancel runs nothing and exits 1.
//
// How it could pass wrongly: an action that ran with stale values would
// still write the file, so the file is removed before each run and must hold
// the values of that run.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Actions that ask").
func TestUserActionAsksAndTakesTypedArgs(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	out := filepath.Join(base, "deploy.out")
	writeConfig(t, base, `[[actions]]
name = "deploy"
label = "Deploy"
key = "prefix+alt+d"
run = ["/bin/sh", "-c", "printf '%s %s %s' \"$1\" \"$2\" \"$3\" > `+out+`", "deploy", "{env}", "{replicas}", "{dry}"]
in = "silent"
args = [{ name = "replicas", type = "number", default = 2 }, { name = "dry", type = "bool", default = false }]
ask = [{ name = "env", kind = "pick", title = "Deploy to?", items = ["stg=Staging", "prod=Production"] }]
`)
	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	term := attachIn(t, base, "S", startOpts{cols: 120, rows: 36})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never started: %v\n%s", err, term.Snapshot())
	}
	ran := func(want, what string) {
		t.Helper()
		got := waitForFileText(t, out, func(s string) bool { return s != "" }, what)
		if got != want {
			t.Errorf("ASSERTION: %s: the action ran with %q, want %q", what, got, want)
		}
		_ = os.Remove(out)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Alt('d')); err != nil {
		t.Fatalf("press the key: %v", err)
	}
	waitPrompt(t, term, "action-ask", "Deploy to?", "Staging", "Production")
	pressAfterSettle(t, term, promptKeyDown, promptKeyEnter)
	ran("prod 2 false", "the key")

	if o, err := tuiosCLI(t, base, "actions", "run", "deploy", "-s", "S", "dry=yes"); err == nil || !strings.Contains(o, "true or false") {
		t.Errorf("ASSERTION: dry=yes ran or did not say why: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "actions", "run", "deploy", "-s", "S", "env=stg", "colour=red"); err == nil || !strings.Contains(o, "no argument colour") {
		t.Errorf("ASSERTION: an unknown argument ran or did not say why: %v\n%s", err, o)
	}

	run := startCLIBackground(t, base, "actions", "run", "deploy", "-s", "S", "replicas=3", "dry=true", "--json")
	waitPrompt(t, term, "action-ask-cli", "Deploy to?", "Prompt from a command")
	pressAfterSettle(t, term, promptKeyEnter)
	r := promptRun(t, term, run)
	var res struct {
		Status string         `json:"status"`
		Args   map[string]any `json:"args"`
	}
	if r.err != nil || json.Unmarshal([]byte(r.out), &res) != nil || res.Status != "ran" ||
		res.Args["env"] != "stg" || res.Args["replicas"] != float64(3) || res.Args["dry"] != true {
		t.Errorf("ASSERTION: actions run --json printed %q (%v), want env stg, replicas 3, dry true", r.out, r.err)
	}
	ran("stg 3 true", "the CLI")

	if o, err := tuiosCLI(t, base, "actions", "run", "deploy", "-s", "S", "env=prod", "replicas=1.5"); err != nil {
		t.Errorf("ASSERTION: actions run with every value failed: %v\n%s", err, o)
	}
	ran("prod 1.5 false", "the CLI with every value")

	run = startCLIBackground(t, base, "actions", "run", "deploy", "-s", "S")
	waitPrompt(t, term, "action-ask-cancel", "Deploy to?")
	pressAfterSettle(t, term, promptKeyEsc)
	if r := promptRun(t, term, run); promptExit(r.err) != 1 {
		t.Errorf("ASSERTION: a cancelled ask exited %d, want 1", promptExit(r.err))
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("ASSERTION: a cancelled ask ran the action")
	}
}
