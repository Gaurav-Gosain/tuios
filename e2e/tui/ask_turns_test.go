package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The asks wait 10 seconds of silence before they guess the agent is done,
// longer than it is silent over a question, so each ask ends on the agent's
// own idle report.

// slowAgentScript is a stand-in agent that takes 3 seconds over every line
// typed into it: it reports working, prints GOT[line], waits, prints
// DONE[line] and reports idle. The terminal does not echo, so the pane shows
// only what the agent printed.
const slowAgentScript = `T="$1"
stty -echo
"$T" set-agent-state idle -w "$TUIOS_PANE_ID" >/dev/null
echo AGENT-READY
while IFS= read -r l; do
  "$T" set-agent-state working -w "$TUIOS_PANE_ID" >/dev/null
  echo "GOT[$l]"
  sleep 3
  echo "DONE[$l]"
  "$T" set-agent-state idle -w "$TUIOS_PANE_ID" >/dev/null
done
`

// askResult is the part of ask-agent --json the test reads.
type askResult struct {
	Reply        string `json:"reply"`
	SettledBy    string `json:"settled_by"`
	QueuedBehind int    `json:"queued_behind"`
	WaitedTurnMS int64  `json:"waited_turn_ms"`
}

// askAsync runs ask-agent in the background and returns a func that waits
// for it and reads its answer.
func askAsync(t *testing.T, base, session, window, text string, extra ...string) func() (askResult, string, error) {
	t.Helper()
	args := append([]string{"ask-agent", "-s", session, "-w", window, "--json", "--settle", "10000", "--timeout", "60000"}, extra...)
	cmd := exec.Command(tuiosBin, append(args, text)...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ask-agent: %v", err)
	}
	var once sync.Once
	var res askResult
	var err error
	return func() (askResult, string, error) {
		once.Do(func() {
			err = cmd.Wait()
			if err == nil {
				err = json.Unmarshal([]byte(out.String()), &res)
			}
		})
		return res, out.String(), err
	}
}

// TestAsksOfOneAgentTakeTurns asks one stand-in agent several questions at
// once, against a real daemon.
//
// How this could pass wrongly, written down first:
//   - A second ask could succeed only because the first finished inside its
//     ready timeout. The second ask is given a ready timeout of 1 second,
//     and the agent takes 3 seconds over the first question.
//   - Two asks could both type at once and still both succeed, each reading
//     the other's answer as its own. Every reply must hold its own DONE line
//     and no other question's.
//   - The order could be right by luck. The pane must show each question's
//     GOT and DONE before the next question's GOT.
//
// The artifact is ask-turns.log in the test's artifact directory: each
// ask's answer and the pane at the end.
//
// Negative control (NEGATIVE_CONTROLS.md): with askAgent taking no turn, the
// two asks sent together are typed into one line and each reads the other's
// answer. With that half taken out, the ask behind a busy agent fails with
// not_ready.
func TestAsksOfOneAgentTakeTurns(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-askturns"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	script := filepath.Join(base, "slow-agent.sh")
	if err := os.WriteFile(script, []byte(slowAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "answerer", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	agent := windowID(t, base, session, "answerer")
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", agent, "sh '"+script+"' '"+tuiosBin+"'\n"); err != nil {
		t.Fatalf("start the stand-in agent: %v\n%s", err, out)
	}
	waitCapture(t, base, session, agent, "AGENT-READY")
	waitAgentState(t, base, session, agent, "idle", "", &stateLog{name: "ask-turns"}, "stand-in agent ready")

	var log strings.Builder
	defer func() {
		log.WriteString("== the pane at the end\n" + capture(t, base, session, agent) + "\n")
		path := filepath.Join(artifactDir(t), "ask-turns.log")
		if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
			t.Errorf("save the artifact: %v", err)
		}
		t.Logf("artifact: %s", path)
	}()
	all := []string{"ASK-ONE", "ASK-TWO", "ASK-THREE", "ASK-FOUR"}
	check := func(q string, wait func() (askResult, string, error)) askResult {
		t.Helper()
		res, out, err := wait()
		fmt.Fprintf(&log, "== %s: err=%v\n%s\n", q, err, out)
		if err != nil {
			t.Fatalf("ask %s failed: %v\n%s", q, err, out)
		}
		if !strings.Contains(res.Reply, "DONE["+q+"]") {
			t.Fatalf("ask %s did not get its own answer: %q", q, res.Reply)
		}
		for _, other := range all {
			if other != q && strings.Contains(res.Reply, other+"]") {
				t.Fatalf("ask %s read the answer to %s as its own: %q", q, other, res.Reply)
			}
		}
		return res
	}

	// Two asks sent together: each types alone and gets its own answer.
	three := askAsync(t, base, session, agent, "ASK-THREE")
	four := askAsync(t, base, session, agent, "ASK-FOUR")
	check("ASK-THREE", three)
	check("ASK-FOUR", four)

	// One ask is being answered; a second comes with a short ready timeout.
	one := askAsync(t, base, session, agent, "ASK-ONE")
	waitCapture(t, base, session, agent, "GOT[ASK-ONE]")
	two := askAsync(t, base, session, agent, "ASK-TWO", "--ready-timeout", "1000")
	check("ASK-ONE", one)
	res := check("ASK-TWO", two)
	if res.QueuedBehind != 1 || res.WaitedTurnMS < 1000 {
		t.Fatalf("ask ASK-TWO says it waited %d ms behind %d asks, want behind 1 for a second or more", res.WaitedTurnMS, res.QueuedBehind)
	}

	// The pane took the questions one at a time, in the order they came.
	pane := strings.ReplaceAll(capture(t, base, session, agent), "\n", "")
	pos := func(s string) int { return strings.Index(pane, s) }
	order := []string{"GOT[ASK-ONE]", "DONE[ASK-ONE]", "GOT[ASK-TWO]", "DONE[ASK-TWO]"}
	// ASK-THREE and ASK-FOUR came first, so ASK-ONE is looked for after them.
	if done := max(pos("DONE[ASK-THREE]"), pos("DONE[ASK-FOUR]")); pos("GOT[ASK-ONE]") < done {
		t.Fatalf("ASK-ONE was typed before the asks ahead of it were answered:\n%s", pane)
	}
	for i := 1; i < len(order); i++ {
		if pos(order[i-1]) < 0 || pos(order[i]) < pos(order[i-1]) {
			t.Fatalf("the pane did not take the asks one at a time, in order (%s before %s):\n%s", order[i-1], order[i], pane)
		}
	}
	first, second := "ASK-THREE", "ASK-FOUR"
	if pos("GOT[ASK-FOUR]") < pos("GOT[ASK-THREE]") {
		first, second = second, first
	}
	if pos("DONE["+first+"]") > pos("GOT["+second+"]") {
		t.Fatalf("the two asks sent together were typed into each other:\n%s", pane)
	}
}
