package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
//   - Each client says the window moved. The source's client shows "Moved
//     mover to the session beta." and the destination's "Moved mover here
//     from the session alpha.", for both moves. Each is watched from before
//     the move, and fails when it shows "Window closed" or "Window created"
//     first, so a client that reads the move as a close or a new window
//     fails.
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
//   - with windowMoveNotice never taken in the StateSyncMsg case, or with
//     recordWindowMove cut from Session.releaseWindow, it fails at "the
//     source client's notice: showed "Window closed"".
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

	// The move, by the verb. Both clients are watched from before the call,
	// because the notice they show lasts two seconds.
	alphaNote := watchMoveNotice(alpha, "alpha-notice-move", "Moved mover to the session beta.", "Window closed")
	betaNote := watchMoveNotice(beta, "beta-notice-move", "Moved mover here from the session alpha.", "Window created")
	moved := must("move-window", map[string]any{"session": "alpha", "window": win, "to_session": "beta", "follow": true})
	if moved["type"] != "window_moved" || moved["window_id"] != win || moved["session"] != "beta" || moved["from_session"] != "alpha" {
		t.Fatalf("ASSERTION: move-window result = %v, want window_moved of %s from alpha to beta", moved, win)
	}
	if msg := <-alphaNote; msg != "" {
		t.Fatalf("ASSERTION: the source client's notice: %s", msg)
	}
	if msg := <-betaNote; msg != "" {
		t.Fatalf("ASSERTION: the destination client's notice: %s", msg)
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
	alphaNote = watchMoveNotice(alpha, "alpha-notice-cli-move", "Moved mover here from the session beta.", "Window created")
	betaNote = watchMoveNotice(beta, "beta-notice-cli-move", "Moved mover to the session alpha.", "Window closed")
	out, err := tuiosCLI(t, base, "move-window", "-s", "beta", "-w", win, "--to-session", "alpha", "--follow")
	art.add("cli", map[string]any{"args": "move-window -s beta -w " + win + " --to-session alpha --follow"}, nil, out)
	if err != nil || !strings.Contains(out, "from session beta to session alpha") {
		t.Fatalf("ASSERTION: tuios move-window --to-session = %v\n%s", err, out)
	}
	if row("beta") != nil || row("alpha") == nil {
		t.Fatal("ASSERTION: the CLI move did not put the window back in alpha")
	}
	if msg := <-alphaNote; msg != "" {
		t.Fatalf("ASSERTION: after the CLI move, the destination client's notice: %s", msg)
	}
	if msg := <-betaNote; msg != "" {
		t.Fatalf("ASSERTION: after the CLI move, the source client's notice: %s", msg)
	}
	if _, err := call("send-text", map[string]any{"session": "alpha", "window": win, "text": "back\n"}); err != nil {
		t.Fatalf("send-text after the CLI move: %v", err)
	}
	// The same pid: the CLI move did not start the program again.
	waitMoveCapture(t, call, "alpha", win, "got-back "+pid)
	waitForAll(t, alpha, 20*time.Second, "the source client to draw the pane again", "MOVED-PANE-MARK", "got-back")
	art.frame(t, alpha, "alpha-after-cli-move")
}

// watchMoveNotice watches term until it shows want, and reports on the
// channel what went wrong: "" when want showed and no deny text showed
// before it. It runs in its own goroutine, so a watch on each client starts
// before the move and neither misses a notice while the other is read. The
// screen that showed want is saved as TestMoveWindowToSession-<frame>.txt
// under $TUIOS_E2E_FRAMES.
func watchMoveNotice(term *tuitest.Terminal, frame, want string, deny ...string) <-chan string {
	ch := make(chan string, 1)
	go func() {
		var bad, shown string
		err := term.WaitFor(func(s tuitest.Screen) bool {
			for _, d := range deny {
				if bad == "" && screenHas(s, d) {
					bad = d
				}
			}
			if screenHas(s, want) {
				shown = s.Text()
				return true
			}
			return false
		}, 20*time.Second)
		if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" && shown != "" {
			_ = os.WriteFile(filepath.Join(dir, "TestMoveWindowToSession-"+frame+".txt"), []byte(shown), 0o644)
		}
		switch {
		case err != nil && bad != "":
			ch <- fmt.Sprintf("showed %q and never %q:\n%s", bad, want, term.Snapshot())
		case err != nil:
			ch <- fmt.Sprintf("never showed %q:\n%s", want, term.Snapshot())
		case bad != "":
			ch <- fmt.Sprintf("showed %q before %q:\n%s", bad, want, term.Snapshot())
		default:
			ch <- ""
		}
	}()
	return ch
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

// moveSetup makes the sessions alpha and beta in a fresh daemon, and a shell
// window named name in alpha. It returns the base dir, a verb caller and the
// window's id.
func moveSetup(t *testing.T, art *moveArtifact, name string) (string, func(string, map[string]any) map[string]any, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	for _, s := range []string{"alpha", "beta"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	must := func(verb string, params map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(params)
		out, err := tuiosCLI(t, base, "call", verb, string(raw))
		var res map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &res)
		art.add(verb, params, res, out)
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", verb, params, err, out)
		}
		return res
	}
	created := must("new-window", map[string]any{"session": "alpha", "name": name})
	win, _ := created["window_id"].(string)
	if win == "" {
		t.Fatalf("new-window gave no window_id: %v", created)
	}
	return base, must, win
}

// paneDims reads "rows cols" as two numbers.
func paneDims(t *testing.T, size string) (int, int) {
	t.Helper()
	var rows, cols int
	if _, err := fmt.Sscanf(size, "%d %d", &rows, &cols); err != nil {
		t.Fatalf("the pane's size %q is not rows and cols: %v", size, err)
	}
	return rows, cols
}

// A pane moved to another session takes that session's size, though a
// pane-view viewer of it holds a fit lease.
//
// alpha has no client, so the viewer's fit lease decides the pane's size
// there: 45 rows by 150 columns, checked before the move. beta has a client
// in an 80 by 24 terminal. After the move the pane must fit inside that
// client, so a pane still held at the viewer's size fails. The size is read
// before the viewer renews its lease, so a pane sized only by a renewal
// fails. It is read again after a renewal (every 10 seconds), so a renewal
// that says no client shows the pane, from the session it left, fails too.
// The viewer must still show the pane after the move.
//
// NEGATIVE CONTROLS (NEGATIVE_CONTROLS.md, "Move a window to another
// session"):
//   - with the setNoClients call cut from Session.adoptWindow, it fails at
//     "after the move the pane kept the viewer's size".
//   - with paneStream.setLease reading the clients of the session the stream
//     opened in, it fails at "after the viewer renewed its lease".
func TestMoveWindowSessionViewerSize(t *testing.T) {
	art := &moveArtifact{}
	defer art.save(t)
	base, must, win := moveSetup(t, art, "viewed")

	beta := attachIn(t, base, "beta", startOpts{cols: 80, rows: 24})
	viewerStart := time.Now()
	ft := startFakeTerm(t, base, 150, 45, fakeTermOpts{bg: "rgb:0000/0000/0000"}, "pane-view", "-s", "alpha", "-w", win)
	if !pvReady(ft, t, base, "alpha", win) {
		t.Fatalf("pane-view never showed the pane: %q", ft.output())
	}
	waitPaneSize(t, base, "alpha", win, "45 150", "the viewer sizes the pane in a session no client shows")

	// The viewer renews its lease 10 seconds after it opened its stream, and
	// every 10 seconds after that. A renewal sizes the pane again, so the
	// move is made just after the first one and the size is read before the
	// second: the move alone must size the pane.
	time.Sleep(time.Until(viewerStart.Add(11500 * time.Millisecond)))
	must("move-window", map[string]any{"session": "alpha", "window": win, "to_session": "beta", "follow": true})
	movedAt := time.Now()

	fits := func(why string, wait time.Duration) string {
		t.Helper()
		got := ""
		deadline := time.Now().Add(wait)
		for {
			got = pvPaneSize(t, base, "beta", win)
			if rows, cols := paneDims(t, got); rows < 24 && cols < 80 {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s: the pane is %q, want it inside the 80x24 client of beta\n%s", why, got, beta.Snapshot())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	afterMove := fits("after the move the pane kept the viewer's size", 4*time.Second)
	if since := time.Since(movedAt); since > 7*time.Second {
		t.Fatalf("the size after the move was read %s after it, past the viewer's next renewal", since)
	}
	art.add("pane-size", map[string]any{"when": "after the move"}, map[string]any{"size": afterMove}, "")

	// Past the viewer's next renewal.
	time.Sleep(time.Until(movedAt.Add(12 * time.Second)))
	afterRenew := fits("after the viewer renewed its lease", 0)
	art.add("pane-size", map[string]any{"when": "after a lease renewal"}, map[string]any{"size": afterRenew}, "")

	pvSend(t, base, "beta", win, "echo AFTER-MOVE-$((6*7))")
	if _, ok := ft.waitFor(regexp.MustCompile(`AFTER-MOVE-42`), shellTimeout); !ok {
		t.Fatalf("ASSERTION: the viewer stopped showing the pane after the move: %q", ft.output())
	}
	waitForAll(t, beta, 20*time.Second, "the beta client to draw the moved pane", "AFTER-MOVE-42")
	art.frame(t, beta, "beta")
}
