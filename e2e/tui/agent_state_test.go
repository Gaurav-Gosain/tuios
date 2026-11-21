package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const needsInputGlyph = "▲"

// TestAgentStateIndicatorRenders drives a real daemon: it sets a window's agent
// state through the set-agent-state verb (via the tuios CLI, exactly as a pane's
// state-reporting shim would) and asserts the attached client renders the
// matching indicator in the window title.
//
// It is the end-to-end proof that the state model, the verb, the state sync, and
// the renderer are wired together: nothing in this test touches tuios internals,
// only the CLI a shim uses and the screen a user sees.
//
// Negative control: against a binary without the feature, `tuios set-agent-state`
// is an unknown subcommand (the CLI errors and this fails at the set step), and
// even if the call were a no-op no indicator would ever render, so the final wait
// would time out. Verified per NEGATIVE_CONTROLS.md by pointing TUIOS_E2E_BIN at
// a pre-feature build.
func TestAgentStateIndicatorRenders(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e"}})
	killDaemon(t, base)

	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")

	renameWindow(t, term, "AGENTPANE")

	if strings.Contains(term.Screen().Text(), needsInputGlyph) {
		t.Fatalf("the needs_input indicator was on screen before the state was set\n%s", term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "e2e", "-w", "AGENTPANE"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, needsInputGlyph) && strings.Contains(text, "AGENTPANE")
	}, uiTimeout); err != nil {
		t.Fatalf("the needs_input indicator never rendered on the window title: %v\n%s",
			err, term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "none", "-s", "e2e", "-w", "AGENTPANE"); err != nil {
		t.Fatalf("set-agent-state none failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), needsInputGlyph)
	}, uiTimeout); err != nil {
		t.Fatalf("the indicator never cleared after the state was reset to none: %v\n%s",
			err, term.Snapshot())
	}
}
