package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// move-window with to_session moves a pane to another session with its
// program still running.
//
// What the move must do, and how each check could pass wrongly:
//   - The window leaves its session and joins the other. list-windows of
//     both is read, so a move that only added the window (a copy) or only
//     removed it fails.
//   - The window keeps its id and its process. The pane's PTY id is read
//     before and after, and the program prints its own pid with every line
//     it answers. A move that respawned the program answers with a new pid,
//     and one that made a new window gets a new id.
//   - The program keeps running and keeps its output. The marker it printed
//     before the move is in the destination's capture, and a line typed into
//     the destination after the move is answered by the same read loop, so a
//     pane whose input still goes to the old session fails.
//   - An attached client sees it. A real client attached to each session
//     shows the marker in the source before the move (the positive half) and
//     not after, and the destination's client draws it after the move.
//   - The agent state, its message and its metadata go with it.
//   - A hook that still names the old session in TUIOS_SESSION reaches the
//     pane: set-agent-state with the old session and the pane's id lands in
//     the destination.
//   - Refusals: the window's own session (same_session) and a session that
//     does not exist (session_not_found). The same call with a real other
//     session succeeds in the same fixture.
//   - The CLI, tuios move-window --to-session, moves it back and prints both
//     sessions.
//
// The test writes its calls and replies to
// $TUIOS_E2E_FRAMES/TestMoveWindowToSession.json and the clients' screens
// beside it, when that directory is set.
//
// NEGATIVE CONTROLS (NEGATIVE_CONTROLS.md, "Move a window to another
// session"):
//   - with the line that puts the PTY in the destination's PTY map cut from
//     Session.adoptWindow, it fails at "in session beta never showed".
//   - with the to_session branch cut from verbMoveWindow, it fails at "a move
//     to the window's own session".
//   - with the followMovedWindow call cut from admitVerb, it fails at "a
//     report naming the old session did not reach the moved pane".
func TestMoveWindowToSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	art := &moveArtifact{}
	defer art.save(t)

	for _, name := range []string{"alpha", "beta"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}

	call := func(verb string, params map[string]any) (map[string]any, error) {
		t.Helper()
		raw, _ := json.Marshal(params)
		out, err := tuiosCLI(t, base, "call", verb, string(raw))
		var res map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &res)
		art.add(verb, params, res, out)
		return res, err
	}
	must := func(verb string, params map[string]any) map[string]any {
		t.Helper()
		res, err := call(verb, params)
		if err != nil {
			t.Fatalf("%s %v: %v\n%v", verb, params, err, res)
		}
		return res
	}

	// The program prints a marker once, then answers every line it reads.
	// It is one process for its whole life, so its pid says whether it was
	// started again.
	created := must("new-window", map[string]any{
		"session": "alpha",
		"name":    "mover",
		"command": []string{"sh", "-c", `echo MOVED-PANE-MARK pid-$$; while read l; do echo "got-$l pid-$$"; done`},
	})
	win, _ := created["window_id"].(string)
	if win == "" {
		t.Fatalf("new-window gave no window_id: %v", created)
	}
	first := waitMoveCapture(t, call, "alpha", win, "MOVED-PANE-MARK")
	pid := ""
	for _, f := range strings.Fields(first[strings.Index(first, "MOVED-PANE-MARK"):]) {
		if strings.HasPrefix(f, "pid-") {
			pid = f
			break
		}
	}
	if pid == "" {
		t.Fatalf("the program printed no pid: %q", first)
	}

	row := func(session string) map[string]any {
		t.Helper()
		res := must("list-windows", map[string]any{"session": session})
		ws, _ := res["windows"].([]any)
		for _, w := range ws {
			m, _ := w.(map[string]any)
			if m["window_id"] == win {
				return m
			}
		}
		return nil
	}
	before := row("alpha")
	if before == nil {
		t.Fatal("the new window is not in alpha")
	}
	agentState := func(session string) map[string]any {
		t.Helper()
		return must("get-agent-state", map[string]any{"session": session, "window": win})
	}

	must("set-agent-state", map[string]any{"session": "alpha", "window": win, "state": "needs_input", "message": "approve the move"})
	must("set-agent-meta", map[string]any{"session": "alpha", "window": win, "tokens": map[string]any{"model": "move-model"}})

	// A real client on each session. The source's draws the marker now, so
	// its absence after the move means the move took it away.
	alpha := attachIn(t, base, "alpha", startOpts{})
	waitForAll(t, alpha, 20*time.Second, "the source client to draw the pane", "MOVED-PANE-MARK")
	beta := attachIn(t, base, "beta", startOpts{})
	if screenHas(beta.Screen(), "MOVED-PANE-MARK") {
		t.Fatalf("ASSERTION: the destination client draws the pane before the move:\n%s", beta.Snapshot())
	}

	// Refusals first, on the window as it is.
	if res, err := call("move-window", map[string]any{"session": "alpha", "window": win, "to_session": "alpha"}); err == nil || errCode(res) != "same_session" {
		t.Fatalf("ASSERTION: a move to the window's own session = %v (err %v), want same_session", res, err)
	}
	if res, err := call("move-window", map[string]any{"session": "alpha", "window": win, "to_session": "no-such-session"}); err == nil || errCode(res) != "session_not_found" {
		t.Fatalf("ASSERTION: a move to a session that does not exist = %v (err %v), want session_not_found", res, err)
	}
	if row("alpha") == nil {
		t.Fatal("ASSERTION: a refused move took the window out of alpha")
	}

	// The move, by the verb.
	moved := must("move-window", map[string]any{"session": "alpha", "window": win, "to_session": "beta", "follow": true})
	if moved["type"] != "window_moved" || moved["window_id"] != win || moved["session"] != "beta" || moved["from_session"] != "alpha" {
		t.Fatalf("ASSERTION: move-window result = %v, want window_moved of %s from alpha to beta", moved, win)
	}
	if row("alpha") != nil {
		t.Fatal("ASSERTION: alpha still lists the moved window")
	}
	after := row("beta")
	if after == nil {
		t.Fatal("ASSERTION: beta does not list the moved window")
	}
	if after["pty_id"] != before["pty_id"] {
		t.Fatalf("ASSERTION: the pane was not carried over: pty %v before, %v after", before["pty_id"], after["pty_id"])
	}
	if after["agent_state"] != "needs_input" || after["agent_message"] != "approve the move" {
		t.Fatalf("ASSERTION: the agent state did not move with the pane: %v", after)
	}
	if meta, _ := json.Marshal(agentState("beta")["meta"]); !strings.Contains(string(meta), "move-model") {
		t.Fatalf("ASSERTION: the agent metadata did not move with the pane: %s", meta)
	}

	// The output from before the move, and the same process answering a
	// line typed into the destination.
	waitMoveCapture(t, call, "beta", win, "MOVED-PANE-MARK")
	if _, err := call("send-text", map[string]any{"session": "beta", "window": win, "text": "ping\n"}); err != nil {
		t.Fatalf("ASSERTION: the destination cannot type into the moved pane: %v", err)
	}
	waitMoveCapture(t, call, "beta", win, "got-ping "+pid)

	// Both clients follow.
	waitForAll(t, beta, 20*time.Second, "the destination client to draw the moved pane", "MOVED-PANE-MARK", "got-ping")
	if err := alpha.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "MOVED-PANE-MARK") }, 20*time.Second); err != nil {
		t.Fatalf("ASSERTION: the source client still draws the moved pane:\n%s", alpha.Snapshot())
	}
	art.frame(t, beta, "beta-after-move")
	art.frame(t, alpha, "alpha-after-move")

	// A hook in the pane still says alpha in TUIOS_SESSION.
	if _, err := call("set-agent-state", map[string]any{"session": "alpha", "window": win, "state": "working"}); err != nil {
		t.Fatalf("ASSERTION: a report naming the old session did not reach the moved pane: %v", err)
	}
	if r := row("beta"); r == nil || r["agent_state"] != "working" {
		t.Fatalf("ASSERTION: the report naming the old session did not land in beta: %v", r)
	}

	// The CLI moves it back.
	out, err := tuiosCLI(t, base, "move-window", "-s", "beta", "-w", win, "--to-session", "alpha", "--follow")
	art.add("cli", map[string]any{"args": "move-window -s beta -w " + win + " --to-session alpha --follow"}, nil, out)
	if err != nil || !strings.Contains(out, "from session beta to session alpha") {
		t.Fatalf("ASSERTION: tuios move-window --to-session = %v\n%s", err, out)
	}
	if row("beta") != nil || row("alpha") == nil {
		t.Fatal("ASSERTION: the CLI move did not put the window back in alpha")
	}
	if _, err := call("send-text", map[string]any{"session": "alpha", "window": win, "text": "back\n"}); err != nil {
		t.Fatalf("send-text after the CLI move: %v", err)
	}
	// The same pid: the CLI move did not start the program again.
	waitMoveCapture(t, call, "alpha", win, "got-back "+pid)
	waitForAll(t, alpha, 20*time.Second, "the source client to draw the pane again", "MOVED-PANE-MARK", "got-back")
	art.frame(t, alpha, "alpha-after-cli-move")
}

// waitMoveCapture waits until the pane's capture holds want, and returns it.
func waitMoveCapture(t *testing.T, call func(string, map[string]any) (map[string]any, error), session, window, want string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		res, err := call("capture-pane", map[string]any{"session": session, "window": window, "source": "recent"})
		last = res
		if err == nil {
			if s, _ := res["content"].(string); strings.Contains(s, want) {
				return s
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s in session %s never showed %q: %v", window, session, want, last)
	return ""
}

// errCode is the error code of a verb reply printed by tuios call.
func errCode(res map[string]any) string {
	e, _ := res["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

// moveArtifact is the record TestMoveWindowToSession leaves behind.
type moveArtifact struct {
	Calls []moveCall `json:"calls"`
}

type moveCall struct {
	Verb   string         `json:"verb"`
	Params map[string]any `json:"params"`
	Result map[string]any `json:"result,omitempty"`
	Output string         `json:"output,omitempty"`
}

func (a *moveArtifact) add(verb string, params, res map[string]any, out string) {
	c := moveCall{Verb: verb, Params: params, Result: res}
	if res == nil {
		c.Output = out
	}
	a.Calls = append(a.Calls, c)
}

func (a *moveArtifact) frame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveFrame(t, term, t.Name()+"-"+name)
}

func (a *moveArtifact) save(t *testing.T) {
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	path := filepath.Join(dir, t.Name()+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Logf("could not save %s: %v", path, err)
		return
	}
	t.Logf("artifact: %s", path)
}
