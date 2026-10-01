package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
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
step zoomon "$H" pane zoom --on
step zoomoff "$H" pane zoom "$HERDR_PANE_ID" --off
step focus "$H" pane focus --direction left --pane "$HERDR_PANE_ID"
step edge "$H" pane focus --direction right --pane "$HERDR_PANE_ID"
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
	if z := steps["zoomon"].json(t, "pane zoom --on"); dig(z, "result", "zoom", "zoomed") != true || dig(z, "result", "zoom", "zoom_changed") != true {
		t.Fatalf("pane zoom --on: %s", steps["zoomon"].out)
	}
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
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", crushSession, "-w", ids["held"], "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}

	steps := runHerdrSteps(t, base, "held", "grants", `
step neighbor "$H" pane neighbor --pane "$HERDR_PANE_ID" --direction left
step edges "$H" pane edges --current
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
step start "$H" agent start helper --kind claude --pane `+ids["target"]+` --timeout 20000 -- --model test
step get "$H" agent get helper
step create "$H" workspace create --label phone-ws
WS=$(sed -n 's/.*"workspace_id":"\([^"]*\)".*/\1/p' "$D/create.out" | head -1)
step focus "$H" workspace focus "$WS"
`)
	steps["wslist"].ok(t, "workspace list")
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
