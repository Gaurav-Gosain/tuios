package tuie2e

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOmpPluginPaneFollowsItsProcess is issue #2160. The omp integration v2
// reports from inside omp, over the daemon's socket, where v1 started a
// tuios agent-hook child for each event. The daemon worked out the harness
// process the same way for both: the first ancestor of the caller that is not
// a shell. For v1 the caller is the hook, and that ancestor is omp. For v2 the
// caller is omp, and that ancestor is whatever is above the pane's shell. So a
// v2 pane recorded a process that is not omp as its harness, and the
// liveness checks that read the record watched the wrong process.
//
// The installed extension runs under the plugin driver, as node named omp, so
// the detector takes the pane for omp while the extension reports. The pane
// claims a card and runs a turn. Then omp is killed with no agent_end, as a
// crash. The pane is at its shell, its harness is gone, and the pane has to
// go back to none and let go of the card.
//
// How this could pass wrongly, written down first:
//   - The extension could fail to report, so nothing would hold the pane. The
//     test waits for working and done from the extension first.
//   - A none during the turn would also release the card, and the test would
//     read the release at the end as right. The test reads every agent-state
//     and task event, and fails on a none or a release before the kill.
//
// NEGATIVE CONTROL: with peerHookIO back to integration.ProcessOf for a
// plugin, the pane stays working after the kill and the test fails there.
func TestOmpPluginPaneFollowsItsProcess(t *testing.T) {
	_, base := agentSessions(t)
	const sess = "e2e-agent"
	if out, err := tuiosCLI(t, base, "new-window", "omp", "-s", sess, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	win := windowID(t, base, sess, "omp")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sub := exec.CommandContext(ctx, tuiosBin, "subscribe", "-s", sess, "--types", "agent-state,task-changed")
	sub.Dir = workDirIn(t, base)
	sub.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		sub.Env = append(sub.Env, key+"="+xdgDir(base, key))
	}
	pipe, err := sub.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = sub.Wait() })
	var mu sync.Mutex
	var lines []string
	go func() {
		s := bufio.NewScanner(pipe)
		s.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for s.Scan() {
			mu.Lock()
			lines = append(lines, s.Text())
			mu.Unlock()
		}
		if err := s.Err(); err != nil {
			mu.Lock()
			lines = append(lines, "the event stream failed: "+err.Error())
			mu.Unlock()
		}
	}()
	waitUntil(t, "the subscription is up", uiTimeout, func() (bool, string) {
		mu.Lock()
		defer mu.Unlock()
		return len(lines) > 0, "no line yet"
	})
	events := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
	defer func() {
		if err := os.WriteFile(filepath.Join(artifactDir(t), "omp-events.ndjson"), []byte(strings.Join(events(), "\n")+"\n"), 0o644); err != nil {
			t.Errorf("save the events: %v", err)
		}
	}()
	// released reports whether an event let go of t1, and noneAt the events
	// that put the pane at none.
	released := func(evs []string) bool {
		for _, line := range evs {
			var ev struct {
				Type   string `json:"type"`
				Action string `json:"action"`
				Task   struct {
					ID string `json:"id"`
				} `json:"task"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "task-changed" && ev.Action == "released" && ev.Task.ID == "t1" {
				return true
			}
		}
		return false
	}
	noneAt := func(evs []string) []string {
		var out []string
		for _, line := range evs {
			var ev struct {
				Type   string `json:"type"`
				Window string `json:"window"`
				State  string `json:"state"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "agent-state" && ev.Window == win && ev.State == "none" {
				out = append(out, line)
			}
		}
		return out
	}

	log := &stateLog{name: "omp-pane-follows"}
	defer log.save(t)
	d := startPluginAs(t, base, "omp", sess, win, pluginRun{name: "omp"}, nil)
	waitUntil(t, "the detector takes the pane for omp", uiTimeout, func() (bool, string) {
		out, _ := tuiosCLI(t, base, "explain-agent-detect", "-s", sess, "-w", win, "--json")
		return strings.Contains(out, `"matched_harness": "omp"`), out
	})
	d.emit(t, map[string]any{"type": "session_start", "idle": true})
	waitAgentState(t, base, sess, win, "idle", "omp", log, "session_start")
	mustCLI(t, base, "task", "add", "-s", sess, "-p", "do the card", "card")
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", win)
	d.emit(t, map[string]any{"type": "agent_start", "idle": false})
	waitAgentState(t, base, sess, win, "working", "omp", log, "agent_start")
	d.emit(t, map[string]any{"type": "agent_end", "event": map[string]any{"willContinue": false}})
	waitAgentState(t, base, sess, win, "done", "omp", log, "agent_end")
	d.emit(t, map[string]any{"type": "agent_start", "idle": false})
	waitAgentState(t, base, sess, win, "working", "omp", log, "second agent_start")
	if evs := events(); released(evs) || len(noneAt(evs)) > 0 {
		t.Fatalf("ASSERTION: the pane went to none or let go of t1 while omp ran: %v", noneAt(evs))
	}
	if cards, raw := lifeCards(t, base, sess); cards["t1"].Holder == nil || cards["t1"].Holder.Window != win {
		t.Fatalf("ASSERTION: the pane does not hold t1 while omp runs: %s", raw)
	}

	// omp crashes mid-turn: no agent_end, and the shell has the prompt.
	driverDir := filepath.Join(base, "plugin-driver-omp")
	if out, err := exec.Command("pkill", "-KILL", "-f", filepath.Join(driverDir, "omp")).CombinedOutput(); err != nil {
		t.Fatalf("kill omp: %v\n%s", err, out)
	}
	waitUntil(t, "the pane goes back to none once omp is gone", 20*time.Second, func() (bool, string) {
		st := readAgentState(t, base, sess, win)
		return st.State == "none", "state " + st.State + " source " + st.Source
	})
	log.add("after the kill: none")
	waitUntil(t, "the card is let go with the agent", uiTimeout, func() (bool, string) {
		return released(events()), "not released yet"
	})
}
