package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestAgentsMaxDepthRefusesADeeperStart sets agents.max_depth = 1 and runs
// the chain of fake agents of the parent link tests: gen2, opened from
// outside every pane, is at depth 0 and starts gen1 and sib1 at depth 1. gen1
// then starts gen0, which would be at depth 2, and is refused with nothing
// made. The person naming gen1 as the parent is refused the same way, so the
// cap holds the new pane, not the caller. A shell pane in between does not
// start the count again: a shell that a depth 0 pane opened is at depth 1,
// and its start-agent and fan are refused. Once the file says 2, the same
// start from the person passes, so a change to the file applies at once.
//
// Negative control: with depthRefusal returning nil, gen0 opens and
// START_EXIT=0 appears in the shell.
func TestAgentsMaxDepthRefusesADeeperStart(t *testing.T) {
	base := t.TempDir()
	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[agents]\nmax_depth = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	startHandoffDaemon(t, base)
	const sess = "e2e-depth"
	agent := writeChainAgent(t, base)
	newCommandWindow(t, base, sess, "gen2", false, "sh", agent, "2")
	evidence := map[string]any{"config": "[agents]\nmax_depth = 1"}

	// gen1 starts gen0 from its own pane, at depth 2.
	refused := waitFileHas(t, agent+".start1", "gen1's start-agent to end", 60*time.Second, func(s string) bool {
		return strings.TrimSpace(s) != ""
	})
	evidence["gen1_starts_gen0"] = refused
	if !strings.Contains(refused, "depth 2") || !strings.Contains(refused, "agents.max_depth is 1") {
		t.Errorf("ASSERTION: gen1's start of gen0 at depth 2 was not refused for agents.max_depth:\n%s", refused)
	}
	waitFileHas(t, agent+".sib", "gen2's second start-agent to end", 60*time.Second, func(s string) bool {
		return strings.TrimSpace(s) != ""
	})
	rows := linkRows(t, base, map[string]any{"session": sess, "all": true})
	evidence["rows"] = rows
	for _, name := range []string{"gen1", "sib1"} {
		if r, ok := rows[name]; !ok || r.Depth != 1 {
			t.Errorf("ASSERTION: %s at depth 1 did not start under a cap of 1: %+v", name, rows)
		}
	}
	if _, ok := rows["gen0"]; ok {
		t.Errorf("ASSERTION: the refused start at depth 2 still opened gen0")
	}
	gen1 := rows["gen1"].Window

	// The person names gen1 as the parent: the child would still be at
	// depth 2.
	out, err := handoffCLI(t, base, 60*time.Second, "start-agent", "-s", sess, agent+" 0", "--name", "byperson", "--parent", gen1)
	evidence["person_names_gen1"] = out
	if err == nil || !strings.Contains(out, "agents.max_depth is 1") {
		t.Errorf("ASSERTION: a start under gen1 from outside every pane was not refused: %v\n%s", err, out)
	}

	// A shell between does not reset the count. lead is at depth 0 and
	// opens the shell hop, at depth 1.
	repo := testutil.GitRepo(t)
	lead := newCommandWindow(t, base, sess, "lead", false, "sh")
	if out, err := handoffCLI(t, base, 30*time.Second, "send-text", "-s", sess, "-w", lead,
		"'"+tuiosBin+"' new-window -s "+sess+" hop --no-focus; echo HOP_EXIT=$?\n"); err != nil {
		t.Fatalf("send-text into lead: %v\n%s", err, out)
	}
	waitUntil(t, "lead to open the shell hop", 30*time.Second, func() (bool, string) {
		pane, _ := handoffCLI(t, base, 30*time.Second, "capture-pane", "-s", sess, "-w", lead)
		return strings.Contains(pane, "HOP_EXIT=0"), pane
	})
	line := "cd '" + repo + "' && '" + tuiosBin + "' start-agent -s " + sess + " sh --name fromhop --no-wait; echo START_EXIT=$?; " +
		"'" + tuiosBin + "' fan 1 --agent sh hello; echo FAN_EXIT=$?\n"
	if out, err := handoffCLI(t, base, 30*time.Second, "send-text", "-s", sess, "-w", "hop", line); err != nil {
		t.Fatalf("send-text into hop: %v\n%s", err, out)
	}
	// The typed line echoes the names, so only a digit after one is its
	// exit status.
	startExit, fanExit := regexp.MustCompile(`START_EXIT=\d`), regexp.MustCompile(`FAN_EXIT=\d`)
	var hop string
	waitUntil(t, "the shell hop to be refused its start-agent and fan", 60*time.Second, func() (bool, string) {
		hop, _ = handoffCLI(t, base, 30*time.Second, "capture-pane", "-s", sess, "-w", "hop")
		return startExit.MatchString(hop) && fanExit.MatchString(hop), hop
	})
	evidence["shell_hop"] = hop
	if strings.Contains(hop, "START_EXIT=0") || strings.Contains(hop, "FAN_EXIT=0") || strings.Count(hop, "agents.max_depth is 1") < 2 {
		t.Errorf("ASSERTION: the shell at depth 1 was not refused both starts at depth 2:\n%s", hop)
	}
	if _, ok := linkRows(t, base, map[string]any{"session": sess, "all": true})["fromhop"]; ok {
		t.Errorf("ASSERTION: the refused start from the shell hop still opened a pane")
	}

	// The file says 2 now, and the same start from the person passes.
	saveConfigLikeAnEditor(t, base, "[agents]\nmax_depth = 2\n")
	waitUntil(t, "a start at depth 2 to pass once agents.max_depth is 2", configWatchTimeout, func() (bool, string) {
		out, err := handoffCLI(t, base, 60*time.Second, "start-agent", "-s", sess, agent+" 0", "--name", "byperson", "--parent", gen1, "--no-wait")
		return err == nil, out
	})
	if r := linkRows(t, base, map[string]any{"session": sess, "all": true})["byperson"]; r.Depth != 2 {
		t.Errorf("ASSERTION: the start under gen1 is at depth %d, want 2", r.Depth)
	}
	writeHandoffArtifact(t, evidence)
}
