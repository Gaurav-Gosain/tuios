package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
)

// approverManifest is a user manifest whose one rule reads a prompt line that
// starts with "approve ", the way a dialog rule's message does. It is not a
// dialog rule.
const approverManifest = `schema_version = 1
id             = "tuiosapprover"
display_name   = "Approver"

[detect]
comm  = ["tuiosapprover"]
argv0 = ["tuiosapprover"]

[screen]
enabled = true
lines   = 10

[[screen.rule]]
state    = "needs_input"
priority = 100
kind     = "approval"
all      = ["approve deploy: prod"]
`

// approverScript draws the prompt line, draws it again once the detector has
// had time to name the pane's harness, and waits.
const approverScript = `#!/bin/sh
printf 'approve deploy: prod now? [y/n]\n'
sleep 3
printf '\033[2J\033[Happrove deploy: prod now? [y/n]\n'
sleep 600
`

// TestScreenMessageKeepsItsKindOutsideDialogs: a screen rule that is not a
// dialog rule reads a line that starts with "approve ". Its message keeps the
// kind in front, "approval: approve deploy: prod now? [y/n]", as every screen
// rule's message did before dialog rules. Only a dialog rule's message, which
// tuios builds as "approve <tool>: <what>", goes without it.
//
// Negative control: with the RuleShowsDialog condition taken out of
// screenRuleMessage, the message loses its "approval: " and the wait fails.
func TestScreenMessageKeepsItsKindOutsideDialogs(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "harnesses")
	mustMkdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "tuiosapprover.toml"), []byte(approverManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "tuiosapprover")
	if err := os.WriteFile(bin, []byte(approverScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", crushSession, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	win := crushHomeWindow(t, base)
	typeIn(t, base, win, bin)
	log := &stateLog{name: "screen-message-kind"}
	waitAgentState(t, base, crushSession, win, "needs_input", "tuiosapprover", log, "prompt on the screen")
	waitAgentMeta(t, base, win, `"message": "approval: approve deploy: prod now? [y/n]"`)
	log.save(t)
}
