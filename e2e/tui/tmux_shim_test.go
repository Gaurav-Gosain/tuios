package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTmuxShimOpensATeammatePane drives the tmux shim the way Claude Code
// agent teams drive tmux: a command started under tuios tmux-shim asks tmux
// for its pane and window, splits off a pane running the cat placeholder,
// names it, and respawns it with the teammate's command. The attached client
// must draw the second pane, the pane must carry the name, and the respawned
// command must run in it with TMUX_PANE naming it, and be detected as the
// agent it is.
//
// Negative controls: with the "tmux-pane" argv in paneCommand replaced by a
// plain command, respawn-pane finds no holder and the script fails. With the
// holder not handing its command the terminal's foreground, the teammate is
// never detected as an agent.
func TestTmuxShimOpensATeammatePane(t *testing.T) {
	term, base := attachClientBase(t)

	out := filepath.Join(base, "shim-out")
	script := filepath.Join(base, "teams.sh")
	// A stand-in for the teammate: a program named claude, which the agent
	// detector knows, that stays up.
	bin := filepath.Join(base, "bin")
	mustMkdir(bin)
	teammateBin := filepath.Join(bin, "claude")
	if err := os.WriteFile(teammateBin, []byte("#!/bin/sh\necho TEAMMATE_UP $TMUX_PANE\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"set -e",
		"tmux display-message -p '#{pane_id} #{window_id}' > " + out,
		"P=$(tmux split-window -d -h -l 70% -P -F '#{pane_id}' -- cat)",
		"echo $P >> " + out,
		"tmux select-pane -t $P -T mate-pane",
		"tmux respawn-pane -k -t $P -- 'cd " + base + " && " + teammateBin + "'",
		"tmux list-panes -F '#{pane_id}' | wc -l | tr -d ' ' >> " + out,
		"echo SHIM_DONE",
	}, "\n") + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// The quotes keep the typed line itself from matching the marker.
	line := tuiosBin + " tmux-shim -- sh " + script + ` || echo SHIM_"FAILED"` + "\n"
	if o, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, o)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SHIM_DONE") || strings.Contains(s.Text(), "SHIM_FAILED")
	}, shellTimeout); err != nil {
		t.Fatalf("the script under the shim never finished: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "SHIM_FAILED") {
		raw, _ := os.ReadFile(out)
		t.Fatalf("the script under the shim failed; it wrote %q\n%s", raw, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "after split-window through the shim")

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(raw))
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "%") || lines[1] != "@1" || !strings.HasPrefix(lines[2], "%") || lines[3] != "2" {
		t.Fatalf("the script wrote %q, want the caller's pane, @1, the new pane and a count of 2", raw)
	}
	teammate := lines[2]

	panes, err := tuiosCLI(t, base, "list-windows", "-s", "e2e-ctrlp", "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, panes)
	}
	if !strings.Contains(panes, "mate-pane") {
		t.Errorf("no window is named mate-pane after select-pane -T: %s", panes)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "TEAMMATE_UP "+teammate)
	}, shellTimeout); err != nil {
		t.Fatalf("the respawned teammate command never drew in its pane: %v\n%s", err, term.Snapshot())
	}

	// The teammate is found as an agent behind the pane holder, so it shows
	// on the rail like any agent pane.
	deadline := time.Now().Add(shellTimeout)
	var agents string
	for time.Now().Before(deadline) {
		agents, _ = tuiosCLI(t, base, "list-agents", "-s", "e2e-ctrlp", "--json")
		if strings.Contains(agents, `"claude-code"`) && strings.Contains(agents, "mate-pane") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(agents, `"claude-code"`) || !strings.Contains(agents, "mate-pane") {
		t.Errorf("the teammate pane was never detected as a claude-code agent: %s", agents)
	}
	saveFrame(t, term, "tmux-shim-teammate")
	alive(t, term, "after the tmux shim opened a pane")
}
