package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoredShellKeepsTerminalIdentity: a client whose host draws kitty
// graphics attaches, so the session's shells are told TERM_PROGRAM=ghostty.
// After a restart no client is attached when the restore spawns the shells
// and resume_agents = "auto" types the agent's resume into one. The shell
// must still be told ghostty.
//
// Claude Code turns on the kitty keyboard protocol, which is what carries
// Shift+Enter as its own key, straight from TERM_PROGRAM=ghostty. Told TUIOS
// it asks the terminal instead and gives the answer two seconds. A resume
// that loads a long conversation misses that, so Shift+Enter reached the
// resumed agent as a bare Enter, while a fresh one, which answers in time,
// still had it.
//
// How this could pass wrongly, written down first:
//   - the variable might come from the test's own environment: the probe
//     prints the one its process got, and the test sets TERM_PROGRAM to
//     something else;
//   - the shell might be one a client started after the restart: no client is
//     attached from the restart until the probe has printed.
//
// Negative control: with the graphics not put back in restoreSessionOffers
// (daemon_resurrect.go), the resumed probe prints TUIOS.
func TestRestoredShellKeepsTerminalIdentity(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeClaude := "#!/bin/sh\nif [ \"$1\" = --resume ]; then echo \"resumed-as-$TERM_PROGRAM\"; fi\nsleep 600\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+agentFreePath("/usr/bin", "/bin"))
	t.Setenv("TERM_PROGRAM", "e2e-outer")
	writeConfig(t, base, "[daemon]\nresume_agents = 'auto'\n")

	const name = "e2e-resumeident"
	mustCLI(t, base, "new", name, "--detach")
	term := attachIn(t, base, name, startOpts{cols: 120, rows: 30, env: []string{"TUIOS_KITTY_GRAPHICS=1"}})
	// The attach is what records the host's graphics on the session.
	enterTerminalMode(t, term)
	_ = term.Close()

	mustCLI(t, base, "set-agent-state", "-s", name, "working", "--harness", "claude-code", "--agent-session-id", "f00d-cafe")
	mustCLI(t, base, "restart-server", "--yes")
	waitUnwrapped(t, base, name, "resumed-as-")
	out := mustCLI(t, base, "capture-pane", "--unwrap", "-s", name)
	artifact := filepath.Join(artifactDir(t), "resumed-identity.txt")
	_ = os.WriteFile(artifact, []byte(out), 0o644)
	if !strings.Contains(out, "resumed-as-ghostty") {
		t.Errorf("the resumed agent was not told TERM_PROGRAM=ghostty (pane saved to %s):\n%s", artifact, out)
	}
}

// TestUnattachedSessionTakesTheLastClientsIdentity: a session no client has
// attached to, such as one an agent made with new --detach while the person's
// client shows another session, tells its shells the TERM_PROGRAM of the
// daemon's most recent client. It told them TUIOS, and Claude Code started
// there had to win a two second race for Shift+Enter (see
// TestRestoredShellKeepsTerminalIdentity).
//
// How this could pass wrongly, written down first:
//   - the variable might come from the test's own environment: the test sets
//     TERM_PROGRAM to something else;
//   - the pane might be one the client's own session made: it is a new window
//     of the other session, opened from the CLI after the client attached.
//
// Negative control: with the HostGraphics fallback cut from
// Session.GraphicsCapabilities, the pane prints TUIOS.
func TestUnattachedSessionTakesTheLastClientsIdentity(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Setenv("TERM_PROGRAM", "e2e-outer")
	mustCLI(t, base, "new", "e2e-shown", "--detach")
	mustCLI(t, base, "new", "e2e-unshown", "--detach")
	term := attachIn(t, base, "e2e-shown", startOpts{cols: 120, rows: 30, env: []string{"TUIOS_KITTY_GRAPHICS=1"}})
	enterTerminalMode(t, term)

	mustCLI(t, base, "new-window", "-s", "e2e-unshown", "--no-focus", "probe", "--", "sh", "-c", "echo made-as-$TERM_PROGRAM; sleep 600")
	out := waitCapture(t, base, "e2e-unshown", "probe", "made-as-")
	artifact := filepath.Join(artifactDir(t), "unattached-identity.txt")
	_ = os.WriteFile(artifact, []byte(out), 0o644)
	if !strings.Contains(out, "made-as-ghostty") {
		t.Errorf("a pane of a session no client attached to was not told TERM_PROGRAM=ghostty (pane saved to %s):\n%s", artifact, out)
	}
}
