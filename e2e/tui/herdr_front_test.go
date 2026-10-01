package tuie2e

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The herdr front: herdr's CLI, as tools built for herdr run it through
// $HERDR_BIN_PATH inside a pane. Each test types a shell script into a real
// pane of a real daemon with a client attached. The script runs one plugin's
// command sequence through "$HERDR_BIN_PATH" and writes each command's
// stdout, stderr and exit code to files the test reads.

// herdrFrontClient is crushClient with tiling on, as the plugins expect: a
// split in herdr is a tile.
func herdrFrontClient(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	term, base := crushClient(t)
	if out, err := tuiosCLI(t, base, "run-command", "-s", crushSession, "EnableTiling"); err != nil {
		t.Fatalf("EnableTiling: %v\n%s", err, out)
	}
	return term, base
}

// herdrStep is what one command of a sequence printed and its exit code.
type herdrStep struct {
	out, err string
	code     int
}

// json decodes the step's stdout as one JSON object.
func (s herdrStep) json(t *testing.T, name string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s.out), &m); err != nil {
		t.Fatalf("%s printed no JSON object (exit %d): %v\nstdout: %s\nstderr: %s", name, s.code, err, s.out, s.err)
	}
	return m
}

// runHerdrSteps runs body in pane window as a POSIX shell script and returns
// the steps it ran. In body, `step NAME command...` runs one command and
// records it, $H is "$HERDR_BIN_PATH", and $D is the directory of the
// records.
func runHerdrSteps(t *testing.T, base, window, name, body string) map[string]herdrStep {
	t.Helper()
	dir := filepath.Join(base, "steps-"+name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(base, "steps-"+name+".sh")
	text := "D=" + dir + "\nH=\"$HERDR_BIN_PATH\"\n" +
		`step() { n=$1; shift; "$@" >"$D/$n.out" 2>"$D/$n.err"; echo $? >"$D/$n.code"; }` + "\n" +
		`newpane() { sed -n 's/.*"pane_id":"\([^"]*\)".*/\1/p' "$D/$1.out" | head -1; }` + "\n" +
		body + "\necho done >\"$D/DONE\"\n"
	if err := os.WriteFile(script, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	typeIn(t, base, window, "sh "+script)
	deadline := time.Now().Add(2 * uiTimeout)
	for {
		if _, err := os.Stat(filepath.Join(dir, "DONE")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			out, _ := tuiosCLI(t, base, "capture-pane", "-s", crushSession, "-w", window)
			t.Fatalf("the %s sequence did not finish:\n%s", name, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	steps := map[string]herdrStep{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n, ok := strings.CutSuffix(e.Name(), ".code")
		if !ok {
			continue
		}
		read := func(ext string) string {
			b, _ := os.ReadFile(filepath.Join(dir, n+ext))
			return string(b)
		}
		code, _ := strconv.Atoi(strings.TrimSpace(read(".code")))
		steps[n] = herdrStep{out: read(".out"), err: read(".err"), code: code}
	}
	return steps
}

// ok fails the test unless step name exited 0.
func (s herdrStep) ok(t *testing.T, name string) {
	t.Helper()
	if s.code != 0 {
		t.Fatalf("%s exited %d\nstdout: %s\nstderr: %s", name, s.code, s.out, s.err)
	}
}

// dig reads a nested field of a decoded JSON object.
func dig(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[p]
	}
	return v
}

// windowPrefix is the first 8 hex digits of the window a herdr pane id
// names, which tuios takes as a window id prefix.
func windowPrefix(paneID string) string {
	_, win, _ := strings.Cut(paneID, ":p")
	return win[:min(8, len(win))]
}

// herdrLayoutOf is the layout of the tab that pane is in, read from outside
// every pane: pane id to its x, and whether the tab is zoomed.
func herdrLayoutOf(t *testing.T, base, pane string) (map[string]float64, string, bool) {
	t.Helper()
	l := herdrCall(t, base, "pane.layout", map[string]any{"pane_id": pane})["layout"].(map[string]any)
	xs := map[string]float64{}
	for _, p := range l["panes"].([]any) {
		m := p.(map[string]any)
		xs[m["pane_id"].(string)] = m["rect"].(map[string]any)["x"].(float64)
	}
	focused, _ := l["focused_pane_id"].(string)
	zoomed, _ := l["zoomed"].(bool)
	return xs, focused, zoomed
}

// TestHerdrFrontTerminalBrowserSplit runs terminal-browser's herdr adapter
// for `terminal-browser open URL --split right`, call for call
// (pixel/src/terminal/terminals/herdr.ts): it reloads herdr's config, lists
// the panes and their processes, splits the caller's pane with --pane and
// --focus, finds the new pane as the caller's right neighbour, reads the new
// pane's tab against HERDR_TAB_ID, and runs the browser's command in it.
//
// Negative controls: with the daemon handing panes the tuios binary as
// HERDR_BIN_PATH instead of the herdr link (bin = link cut in daemon.go),
// server reload-config answers tuios's unknown command error instead of
// herdr's unsupported one. With HERDR_TAB_ID taken out of HerdrEnv, the
// environment check fails.
func TestHerdrFrontTerminalBrowserSplit(t *testing.T) {
	term, base := herdrFrontClient(t)
	crushPanes(t, base, "caller")
	caller := herdrPaneByLabel(t, base, "caller")
	callerID := caller["pane_id"].(string)

	steps := runHerdrSteps(t, base, "caller", "browser", `
echo "$HERDR_PANE_ID $HERDR_TAB_ID" >"$D/env"
step reload "$H" server reload-config
step list "$H" pane list
step proc "$H" pane process-info --pane "$HERDR_PANE_ID"
step split "$H" pane split --pane "$HERDR_PANE_ID" --direction right --focus --right-click pane
NEW=$(newpane split)
step run "$H" pane run "$NEW" "echo browser-pane-ran"
step neighbor "$H" pane neighbor --pane "$HERDR_PANE_ID" --direction right
step get "$H" pane get "$NEW"
`)
	env, _ := os.ReadFile(filepath.Join(base, "steps-browser", "env"))
	if got := strings.Fields(string(env)); len(got) != 2 || got[0] != callerID || got[1] != caller["tab_id"] {
		t.Fatalf("the pane's HERDR_PANE_ID and HERDR_TAB_ID are %q, want %s %v", env, callerID, caller["tab_id"])
	}

	// terminal-browser ignores a failed reload. herdr's error shape and exit
	// code let it.
	if r := steps["reload"]; r.code != 1 || !strings.Contains(r.err, `"code":"unsupported"`) {
		t.Fatalf("server reload-config: exit %d, stderr %q, want herdr's unsupported error and exit 1", r.code, r.err)
	}
	steps["list"].ok(t, "pane list")
	found := false
	for _, p := range dig(steps["list"].json(t, "pane list"), "result", "panes").([]any) {
		found = found || p.(map[string]any)["pane_id"] == callerID
	}
	if !found {
		t.Fatalf("pane list does not hold the caller %s:\n%s", callerID, steps["list"].out)
	}
	steps["proc"].ok(t, "pane process-info")
	proc := steps["proc"].json(t, "pane process-info")
	if pid, _ := dig(proc, "result", "process_info", "shell_pid").(float64); pid <= 0 || !strings.Contains(steps["proc"].out, "steps-browser.sh") {
		t.Fatalf("pane process-info names no shell, or not the script in the foreground:\n%s", steps["proc"].out)
	}

	steps["split"].ok(t, "pane split")
	newID, _ := dig(steps["split"].json(t, "pane split"), "result", "pane", "pane_id").(string)
	if newID == "" || newID == callerID {
		t.Fatalf("pane split gave no new pane:\n%s", steps["split"].out)
	}
	if r := steps["run"]; r.code != 0 || r.out != "" {
		t.Fatalf("pane run: exit %d, stdout %q, want exit 0 and nothing printed", r.code, r.out)
	}
	steps["neighbor"].ok(t, "pane neighbor")
	if got := dig(steps["neighbor"].json(t, "pane neighbor"), "result", "neighbor", "neighbor_pane_id"); got != newID {
		t.Fatalf("the caller's right neighbour is %v, want the new pane %s:\n%s", got, newID, steps["neighbor"].out)
	}
	steps["get"].ok(t, "pane get")
	if got := dig(steps["get"].json(t, "pane get"), "result", "pane", "tab_id"); got != caller["tab_id"] {
		t.Fatalf("the new pane is on tab %v, want the caller's %v", got, caller["tab_id"])
	}

	// The command reached the new pane, and the new pane holds the focus.
	waitJoined(t, base, windowPrefix(newID), "browser-pane-ran")
	if _, focused, _ := herdrLayoutOf(t, base, newID); focused != newID {
		t.Fatalf("the focused pane is %s, want the new pane %s (pane split --focus)", focused, newID)
	}
	saveFrame(t, term, "herdr-front-terminal-browser")
	alive(t, term, "after terminal-browser's herdr sequence")
}

// TestHerdrFrontSplitLeftSwaps runs the sequence terminal-browser and
// terminal-code use for --split left: herdr splits only right or down, so
// the adapter splits right and swaps the new pane to the left, then runs the
// command in it. The new pane must end on the caller's left, as drawn and as
// the daemon reports it, with the caller as its right neighbour.
//
// Negative control: with the swap_windows case taken out of the client's
// routed commands (internal/app/update.go), pane swap fails and the new
// pane stays on the right.
func TestHerdrFrontSplitLeftSwaps(t *testing.T) {
	term, base := herdrFrontClient(t)
	crushPanes(t, base, "caller")
	callerID := herdrPaneByLabel(t, base, "caller")["pane_id"].(string)

	steps := runHerdrSteps(t, base, "caller", "left", `
step split "$H" pane split --pane "$HERDR_PANE_ID" --direction right --focus --right-click pane
NEW=$(newpane split)
step swap "$H" pane swap --pane "$NEW" --direction left
step run "$H" pane run "$NEW" "echo code-pane-ran"
step neighbor "$H" pane neighbor --pane "$NEW" --direction right
`)
	steps["split"].ok(t, "pane split")
	newID, _ := dig(steps["split"].json(t, "pane split"), "result", "pane", "pane_id").(string)
	steps["swap"].ok(t, "pane swap")
	swap := steps["swap"].json(t, "pane swap")
	if dig(swap, "result", "swap", "changed") != true || dig(swap, "result", "swap", "target_pane_id") != callerID {
		t.Fatalf("pane swap did not swap the new pane with the caller:\n%s", steps["swap"].out)
	}
	steps["run"].ok(t, "pane run")
	steps["neighbor"].ok(t, "pane neighbor")
	if got := dig(steps["neighbor"].json(t, "pane neighbor"), "result", "neighbor", "neighbor_pane_id"); got != callerID {
		t.Fatalf("the new pane's right neighbour is %v, want the caller %s", got, callerID)
	}
	xs, focused, _ := herdrLayoutOf(t, base, callerID)
	if len(xs) != 3 {
		t.Fatalf("the tab holds %d panes after one split, want 3: %v", len(xs), xs)
	}
	if xs[newID] >= xs[callerID] || focused != newID {
		t.Fatalf("after the swap the new pane is at x %v and the caller at %v, focus on %s; want the new pane left of the caller, focused", xs[newID], xs[callerID], focused)
	}
	waitJoined(t, base, windowPrefix(newID), "code-pane-ran")
	// The client draws the same order: on the row with the caller's command
	// line, the command typed into the new pane sits to its left. Both are
	// matched by their first characters, since a narrow pane wraps them.
	drawnLeft := func(s tuitest.Screen) bool {
		for line := range strings.SplitSeq(s.Text(), "\n") {
			ran, cmd := strings.Index(line, "echo code-pan"), strings.Index(line, "$ sh /")
			if ran >= 0 && cmd >= 0 && ran < cmd {
				return true
			}
		}
		return false
	}
	if err := term.WaitFor(drawnLeft, uiTimeout); err != nil {
		t.Fatalf("the screen does not draw the new pane left of the caller: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "herdr-front-split-left")
	alive(t, term, "after the split left sequence")
}

// TestHerdrFrontVimNavigation runs what the Vim navigation plugins
// (vim-herdr-navigation, herdr-nvim, herdr-splits.nvim) run when Ctrl+h
// reaches the edge of Vim: read the pane's process and edges, then move the
// focus across to the next pane. Then a zoom on and off, which
// herdr-splits.nvim binds.
//
// Negative control: with pane.focus_direction, pane.edges and pane.zoom
// taken out of herdrMethods, the steps answer unsupported and exit 1.
func TestHerdrFrontVimNavigation(t *testing.T) {
	term, base := herdrFrontClient(t)
	crushPanes(t, base, "nav")
	navID := herdrPaneByLabel(t, base, "nav")["pane_id"].(string)
	xs, _, _ := herdrLayoutOf(t, base, navID)
	leftID := ""
	for id := range xs {
		if id != navID {
			leftID = id
		}
	}
	if leftID == "" || xs[leftID] >= xs[navID] {
		t.Fatalf("the layout is not two panes side by side with nav on the right: %v", xs)
	}

	steps := runHerdrSteps(t, base, "nav", "vim", `
step proc "$H" pane process-info --current
step edges "$H" pane edges --current
step zoomon "$H" pane zoom "$HERDR_PANE_ID" --on
`)
	steps["proc"].ok(t, "pane process-info")
	if !strings.Contains(steps["proc"].out, `"pane_id":"`+navID+`"`) {
		t.Fatalf("pane process-info --current is not about the caller:\n%s", steps["proc"].out)
	}
	steps["edges"].ok(t, "pane edges")
	edges := steps["edges"].json(t, "pane edges")
	if dig(edges, "result", "edges", "left") != false || dig(edges, "result", "edges", "right") != true {
		t.Fatalf("pane edges for the right pane: %s", steps["edges"].out)
	}
	steps["zoomon"].ok(t, "pane zoom --on")
	if z := steps["zoomon"].json(t, "pane zoom --on"); dig(z, "result", "zoom", "zoomed") != true || dig(z, "result", "zoom", "zoom_changed") != true || dig(z, "result", "zoom", "focused_pane_id") != navID {
		t.Fatalf("pane zoom --on: %s", steps["zoomon"].out)
	}
	// The pane that asked is the one zoomed: the screen draws one pane, and
	// its bottom border carries nav's title. The pane focused before the call was the other one.
	zoomedOnNav := func(s tuitest.Screen) bool {
		text := s.Text()
		if strings.Count(text, "╰") != 1 {
			return false
		}
		for line := range strings.SplitSeq(text, "\n") {
			if strings.Contains(line, "╰") {
				return strings.Contains(line, " nav ")
			}
		}
		return false
	}
	if err := term.WaitFor(zoomedOnNav, uiTimeout); err != nil {
		t.Fatalf("pane zoom --on from nav did not zoom nav: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "herdr-front-vim-zoomed")

	more := runHerdrSteps(t, base, "nav", "vim2", `
step zoomoff "$H" pane zoom "$HERDR_PANE_ID" --off
step focus "$H" pane focus --direction left --pane "$HERDR_PANE_ID"
step edge "$H" pane focus --direction right --pane "$HERDR_PANE_ID"
`)
	maps.Copy(steps, more)
	steps["zoomoff"].ok(t, "pane zoom --off")
	if z := steps["zoomoff"].json(t, "pane zoom --off"); dig(z, "result", "zoom", "zoomed") != false {
		t.Fatalf("pane zoom --off: %s", steps["zoomoff"].out)
	}
	steps["focus"].ok(t, "pane focus --direction left")
	f := steps["focus"].json(t, "pane focus")
	if dig(f, "result", "focus", "changed") != true || dig(f, "result", "focus", "focused_pane_id") != leftID {
		t.Fatalf("pane focus --direction left did not move to %s:\n%s", leftID, steps["focus"].out)
	}
	steps["edge"].ok(t, "pane focus --direction right")
	if e := steps["edge"].json(t, "pane focus at the edge"); dig(e, "result", "focus", "reason") != "no_neighbor" {
		t.Fatalf("a step off the right edge: %s", steps["edge"].out)
	}
	if _, focused, zoomed := herdrLayoutOf(t, base, navID); focused != leftID || zoomed {
		t.Fatalf("after the sequence the focus is on %s, zoomed %v; want %s, not zoomed", focused, zoomed, leftID)
	}
	saveFrame(t, term, "herdr-front-vim-navigation")
	alive(t, term, "after the Vim navigation sequence")
}

// TestHerdrFrontHoldsThePaneToItsGrants gives a pane the read grant alone
// and runs terminal-browser's split sequence and the navigation calls from
// it. A split, a swap, a focus and a zoom change the layout and need admin:
// each is refused with herdr's forbidden error and exit 1, and the layout
// does not change. A read (pane neighbor, pane edges) is still answered.
//
// Negative control: with the herdrAdmit call taken out of herdrPaneSwap,
// the swap step exits 0 and the layout changes. A focus and a zoom stay
// refused without their herdrAdmit, because focus-window and run-command
// check the same grant.
func TestHerdrFrontHoldsThePaneToItsGrants(t *testing.T) {
	term, base := herdrFrontClient(t)
	ids := crushPanes(t, base, "held")
	heldID := herdrPaneByLabel(t, base, "held")["pane_id"].(string)
	before, focusBefore, _ := herdrLayoutOf(t, base, heldID)
	otherID := ""
	for id := range before {
		if id != heldID {
			otherID = id
		}
	}
	// The other pane runs a program whose arguments stand for a secret.
	typeIn(t, base, windowPrefix(otherID), "sleep 7654321")
	waitForProcInfo := func() map[string]any {
		for deadline := time.Now().Add(uiTimeout); ; time.Sleep(100 * time.Millisecond) {
			r := herdrCall(t, base, "pane.process_info", map[string]any{"pane_id": otherID})
			if b, _ := json.Marshal(r); strings.Contains(string(b), "7654321") || time.Now().After(deadline) {
				return r
			}
		}
	}
	if b, _ := json.Marshal(waitForProcInfo()); !strings.Contains(string(b), `"cmdline":"sleep 7654321"`) {
		t.Fatalf("the person does not see the other pane's command line: %s", b)
	}
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", crushSession, "-w", ids["held"], "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}

	steps := runHerdrSteps(t, base, "held", "grants", `
step neighbor "$H" pane neighbor --pane "$HERDR_PANE_ID" --direction left
step edges "$H" pane edges --current
step procother "$H" pane process-info --pane `+otherID+`
step procown "$H" pane process-info --pane "$HERDR_PANE_ID"
step split "$H" pane split --pane "$HERDR_PANE_ID" --direction right --focus
step swap "$H" pane swap --pane "$HERDR_PANE_ID" --direction left
step focus "$H" pane focus --direction left --pane "$HERDR_PANE_ID"
step zoom "$H" pane zoom --on --pane "$HERDR_PANE_ID"
`)
	// The positive half: reads are answered.
	steps["neighbor"].ok(t, "pane neighbor")
	if dig(steps["neighbor"].json(t, "pane neighbor"), "result", "neighbor", "neighbor_pane_id") == nil {
		t.Fatalf("pane neighbor from a read-only pane found no neighbour:\n%s", steps["neighbor"].out)
	}
	steps["edges"].ok(t, "pane edges")
	// A read-only pane sees another pane's processes by pid and name, and
	// not their arguments or directories. Its own pane it sees whole.
	steps["procother"].ok(t, "pane process-info of another pane")
	if o := steps["procother"].out; strings.Contains(o, "7654321") || strings.Contains(o, `"cwd"`) || !strings.Contains(o, `"name":"sleep"`) {
		t.Errorf("pane process-info of another pane from a read-only pane: %s", o)
	}
	steps["procown"].ok(t, "pane process-info of its own pane")
	if !strings.Contains(steps["procown"].out, "steps-grants.sh") {
		t.Errorf("pane process-info of its own pane lacks its command line: %s", steps["procown"].out)
	}
	for _, name := range []string{"split", "swap", "focus", "zoom"} {
		s := steps[name]
		var resp map[string]any
		_ = json.Unmarshal([]byte(s.err), &resp)
		if s.code != 1 || dig(resp, "error", "code") != "forbidden" || s.out != "" {
			t.Errorf("%s from a read-only pane: exit %d, stdout %q, stderr %q; want exit 1 and herdr's forbidden error", name, s.code, s.out, s.err)
		}
	}
	after, focusAfter, zoomed := herdrLayoutOf(t, base, heldID)
	if fmt.Sprint(after) != fmt.Sprint(before) || focusAfter != focusBefore || zoomed {
		t.Fatalf("a refused call changed the layout: %v focus %s zoomed %v, was %v focus %s", after, focusAfter, zoomed, before, focusBefore)
	}
	saveFrame(t, term, "herdr-front-grants")
	alive(t, term, "after the refused calls")
}

// TestHerdrFrontStartsAnAgentAndShowsAWorkspace runs what the Telegram and
// phone bridges (herdr-telegram-agents, herdr-mobile-relay) run: list the
// workspaces, start a named agent in a pane at its shell prompt and wait for
// it, find it by its name, then make a workspace and show it on the client.
// The agent is a stand-in named claude that reports itself at rest through
// herdr's own report command, as an agent with herdr support does.
//
// Negative controls: with agent.start taken out of herdrMethods, agent start
// exits 1. With workspace.focus taken out, workspace focus exits 1.
func TestHerdrFrontStartsAnAgentAndShowsAWorkspace(t *testing.T) {
	term, base := herdrFrontClient(t)
	ids := crushPanes(t, base, "bridge", "target")
	stubs := filepath.Join(base, "stubs")
	if err := os.MkdirAll(stubs, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\necho STUB-AGENT-UP \"$@\"\n\"$HERDR_BIN_PATH\" pane report-agent \"$HERDR_PANE_ID\" --source stub --agent claude --state idle --seq 1\nsleep 300\n"
	if err := os.WriteFile(filepath.Join(stubs, "claude"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	typeIn(t, base, "target", "PATH="+stubs+":$PATH; export PATH; echo TARGET-READY")
	waitJoined(t, base, "target", "TARGET-READY")

	steps := runHerdrSteps(t, base, "bridge", "bridge", `
step wslist "$H" workspace list
step badkind "$H" agent start nobody --kind nope --pane `+ids["target"]+`
step badopt "$H" --foo
step start "$H" agent start helper --kind claude --pane `+ids["target"]+` --timeout 20000 -- --model test
step get "$H" agent get helper
step create "$H" workspace create --label phone-ws
WS=$(sed -n 's/.*"workspace_id":"\([^"]*\)".*/\1/p' "$D/create.out" | head -1)
step focus "$H" workspace focus "$WS"
`)
	steps["wslist"].ok(t, "workspace list")
	// herdr refuses an agent kind it does not know, and an option it does
	// not have, as usage errors: exit 2, the message on stderr.
	if b := steps["badkind"]; b.code != 2 || strings.TrimSpace(b.err) != "unsupported interactive agent kind: nope" {
		t.Errorf("agent start --kind nope: exit %d, stderr %q", b.code, b.err)
	}
	if b := steps["badopt"]; b.code != 2 || !strings.HasPrefix(b.err, "unknown option: --foo") {
		t.Errorf("herdr --foo: exit %d, stderr %q", b.code, b.err)
	}
	steps["start"].ok(t, "agent start")
	start := steps["start"].json(t, "agent start")
	if dig(start, "result", "type") != "agent_started" || dig(start, "result", "agent", "agent_status") != "idle" {
		t.Fatalf("agent start did not answer a ready agent:\n%s", steps["start"].out)
	}
	if argv, _ := dig(start, "result", "argv").([]any); len(argv) != 3 || argv[0] != "claude" || argv[2] != "test" {
		t.Fatalf("agent start argv %v, want claude --model test", dig(start, "result", "argv"))
	}
	waitJoined(t, base, "target", "STUB-AGENT-UP --model test")
	steps["get"].ok(t, "agent get")
	if get := steps["get"].json(t, "agent get"); dig(get, "result", "agent", "name") != "helper" || dig(get, "result", "agent", "agent") != "claude" {
		t.Fatalf("agent get helper: %s", steps["get"].out)
	}

	steps["create"].ok(t, "workspace create")
	ws, _ := dig(steps["create"].json(t, "workspace create"), "result", "workspace", "workspace_id").(string)
	steps["focus"].ok(t, "workspace focus")
	if dig(steps["focus"].json(t, "workspace focus"), "result", "workspace", "focused") != true {
		t.Fatalf("workspace focus did not show %s:\n%s", ws, steps["focus"].out)
	}
	if got := dig(herdrCall(t, base, "workspace.get", map[string]any{"workspace_id": ws}), "workspace", "focused"); got != true {
		t.Fatalf("the client does not show the new workspace: focused %v", got)
	}
	saveFrame(t, term, "herdr-front-bridge")
	alive(t, term, "after the bridge sequence")
}

// shq quotes s for the POSIX sh the step scripts run in.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// TestHerdrFrontAgentStartQuotesForTheShell starts an agent through
// "$HERDR_BIN_PATH" agent start in a sh pane and in a fish pane, with
// arguments built to break out of a shell line: quotes, backslashes, command
// substitution in both shells' forms, globs, braces and an argument that ends
// in a backslash. The agent, a stand-in named claude, writes the arguments it
// got to a file. Each must arrive as it was sent, one word each, and none of
// the touch commands in them may run.
//
// Negative control: with herdrShellQuote quoting fish the POSIX way, the
// fish pane runs the touch in the first argument and the arguments arrive
// changed.
func TestHerdrFrontAgentStartQuotesForTheShell(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	term, base := herdrFrontClient(t)
	ids := crushPanes(t, base, "bridge", "shpane")
	if out, err := tuiosCLI(t, base, "new-window", "fishpane", "-s", crushSession, "--no-focus", "--", fish, "--no-config"); err != nil {
		t.Fatalf("new-window fish: %v\n%s", err, out)
	}
	// fish sets its title to its directory, so the pane is found by the
	// name it was given.
	out, _ := tuiosCLI(t, base, "list-windows", "--json", "-s", crushSession)
	var listing struct {
		Windows []struct {
			ID         string `json:"window_id"`
			CustomName string `json:"custom_name"`
		} `json:"windows"`
	}
	_ = json.Unmarshal([]byte(out), &listing)
	for _, w := range listing.Windows {
		if w.CustomName == "fishpane" {
			ids["fishpane"] = w.ID
		}
	}
	if ids["fishpane"] == "" {
		t.Fatalf("no fishpane window:\n%s", out)
	}
	stubs := filepath.Join(base, "stubs")
	if err := os.MkdirAll(stubs, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$ARGS_OUT\"\necho STUB-ARGS-WRITTEN\n\"$HERDR_BIN_PATH\" pane report-agent \"$HERDR_PANE_ID\" --source stub --agent claude --state idle --seq 1\nsleep 300\n"
	if err := os.WriteFile(filepath.Join(stubs, "claude"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	pwned := filepath.Join(base, "PWNED")
	corpus := []string{
		`x\'; touch ` + pwned + `; #`,
		`x\\'; touch ` + pwned + `6; #`,
		`a'b`,
		`$(touch ` + pwned + `2)`,
		"`touch " + pwned + "3`",
		`(touch ` + pwned + `4)`,
		`;touch ` + pwned + `5`,
		`"dq" and spaces`,
		`*`, `~`, `{a,b}`, `\\`, `=ls`, `%self`, `$HOME`, `-n`, ``,
		`back\`,
	}
	typeIn(t, base, "shpane", "PATH="+stubs+":$PATH; export PATH; ARGS_OUT="+filepath.Join(base, "args-sh")+"; export ARGS_OUT; echo SH-READY")
	waitJoined(t, base, "shpane", "SH-READY")
	typeIn(t, base, "fishpane", "set -gx PATH "+stubs+" $PATH; set -gx ARGS_OUT "+filepath.Join(base, "args-fish")+"; echo FISH-READY")
	waitJoined(t, base, "fishpane", "FISH-READY")

	var args []string
	for _, a := range corpus {
		args = append(args, shq(a))
	}
	all := strings.Join(args, " ")
	steps := runHerdrSteps(t, base, "bridge", "quote", `
step sh "$H" agent start helper-sh --kind claude --pane `+ids["shpane"]+` --timeout 20000 -- `+all+`
step fish "$H" agent start helper-fish --kind claude --pane `+ids["fishpane"]+` --timeout 20000 -- `+all+`
`)
	want := strings.Join(corpus, "\n") + "\n"
	for _, shell := range []string{"sh", "fish"} {
		steps[shell].ok(t, "agent start in the "+shell+" pane")
		got, _ := os.ReadFile(filepath.Join(base, "args-"+shell))
		if string(got) != want {
			t.Errorf("the agent in the %s pane got the arguments\n%q\nwant\n%q", shell, got, want)
		}
	}
	matches, _ := filepath.Glob(pwned + "*")
	if len(matches) > 0 {
		t.Fatalf("an argument ran a command: %v", matches)
	}
	saveFrame(t, term, "herdr-front-agent-start-quoting")
	alive(t, term, "after the hostile arguments")
}
