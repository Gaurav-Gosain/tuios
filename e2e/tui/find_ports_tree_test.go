package tuie2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// search-panes, command-history, list-ports and started_by, the four things
// the GUI's find bar, command palette, ports list and pane tree read, against
// a real daemon and a real bash that marks its commands with OSC 133.
//
// How each could pass wrongly, written down first:
//
//   - search-panes could find the marker in the typed command line rather
//     than in the output. The commands print it with printf and the marker
//     split in two, so only the output holds it whole, and the count of
//     matches is checked exactly.
//   - Its row could count from the top of the screen and not from the top of
//     the history. The first marker is printed before 80 filler lines, so it
//     sits in the history, and every row is checked against the same row of
//     a capture of history and screen.
//   - Smart case could always ignore case: a query with a capital must miss
//     the lower-case match, and case sensitive must miss it too.
//   - The guards could be missing: a pattern over 512 bytes and a pattern
//     that matches an empty line must be refused, not run.
//   - command-history could read the screen rather than the marks, or keep
//     every run. The command run twice must come back once, the failing one
//     must carry exit 1, the cwd must be the folder bash reported over OSC 7
//     when the command started (which a cd changes), and the prompt row must
//     be the row search-panes finds the prompt on.
//   - list-ports could list every listening socket on the machine. The test
//     opens a listener of its own, outside every pane, which must not be
//     listed, and the pane's port must name the pane's window and python.
//     It could read only the pane's own process: python runs under sh, so
//     the socket is held by a child of the pane's process.
//   - started_by could be set on every window, or read from the focus. The
//     window the test opens from outside every pane must have none, and the
//     one the pane's shell opens must name that pane, in list-windows and in
//     list-agents.

// findRC is the bash setup: OSC 133 A, B, C and D around every command, and
// OSC 7 with the folder before every prompt.
const findRC = `PS1='$ \[\e]133;B\a\]'
PS0='\e]133;C\a'
PROMPT_COMMAND='printf "\e]133;D;%s\a\e]7;file://%s\a\e]133;A\a" "$?" "$PWD"'
`

type findMatch struct {
	Session   string `json:"session"`
	Window    string `json:"window"`
	Name      string `json:"name"`
	Workspace int    `json:"workspace"`
	Row       int    `json:"row"`
	Text      string `json:"text"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	AgeRows   int    `json:"age_rows"`
}

type findResult struct {
	Matches       []findMatch `json:"matches"`
	PanesSearched int         `json:"panes_searched"`
	Truncated     bool        `json:"truncated"`
}

type historyCommand struct {
	Session    string `json:"session"`
	Window     string `json:"window"`
	Name       string `json:"name"`
	Seq        int    `json:"seq"`
	Cmdline    string `json:"cmdline"`
	Exit       *int   `json:"exit"`
	At         int64  `json:"at"`
	DurationMS int64  `json:"duration_ms"`
	Cwd        string `json:"cwd"`
	Row        int    `json:"row"`
}

type portEntry struct {
	Session  string `json:"session"`
	Window   string `json:"window"`
	Port     int    `json:"port"`
	Addr     string `json:"addr"`
	PID      int    `json:"pid"`
	Comm     string `json:"comm"`
	Worktree string `json:"worktree"`
}

type paneRef struct {
	Session string `json:"session"`
	Window  string `json:"window"`
}

// TestFindPortsAndStartedBy drives the four features through a detached
// daemon and writes every answer it checked to find_ports_tree.json in the
// artifact directory.
func TestFindPortsAndStartedBy(t *testing.T) {
	for _, prog := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(prog); err != nil {
			t.Skip("needs " + prog)
		}
	}
	const session = "find"
	base := t.TempDir()
	killDaemon(t, base)
	work := workDirIn(t, base)
	sub := filepath.Join(work, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(base, "findrc")
	if err := os.WriteFile(rc, []byte(findRC+"TUIOS_BIN="+tuiosBin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(out)
	}
	cli("new", session, "--detach")
	shID := cli("new-window", "-s", session, "--print-id", "sh", "--", "bash", "--noprofile", "--rcfile", rc, "-i")
	c := dialFileVerbs(t, base)
	evidence := map[string]any{}

	run := func(command string) map[string]any {
		t.Helper()
		var out map[string]any
		c.must("run", map[string]any{"session": session, "window": "sh", "command": command}, &out)
		return out
	}
	run(`printf 'FIND%s\n' ME-one`)
	run(`for i in $(seq 1 80); do echo filler $i; done`)
	if out := run(`printf 'FIND%s\n' ME-two; false`); out["exit_code"] != float64(1) {
		t.Fatalf("the failing command ran with %v, want exit 1", out)
	}
	run(`printf 'FIND%s\n' ME-one`)
	run(`cd sub`)
	run(`pwd`)

	// search-panes.
	search := func(params map[string]any) findResult {
		t.Helper()
		var r findResult
		c.must("search-panes", params, &r)
		return r
	}
	all := search(map[string]any{"query": "findme-"})
	evidence["search_findme"] = all
	if len(all.Matches) != 3 || all.Truncated {
		t.Fatalf("search for findme- found %d matches (truncated %v), want the 3 output lines: %+v", len(all.Matches), all.Truncated, all.Matches)
	}
	if all.PanesSearched < 2 {
		t.Errorf("panes_searched = %d, want every pane of the daemon", all.PanesSearched)
	}
	var capture struct {
		Content     string `json:"content"`
		HistoryRows int    `json:"history_rows"`
	}
	c.must("capture-pane", map[string]any{"session": session, "window": "sh", "source": "recent"}, &capture)
	lines := strings.Split(capture.Content, "\n")
	wantTexts := []string{"FINDME-one", "FINDME-two", "FINDME-one"}
	for i, m := range all.Matches {
		if m.Window != shID || m.Name != "sh" || m.Session != session {
			t.Errorf("match %d is in %s/%s (%s), want sh: %+v", i, m.Session, m.Window, m.Name, m)
		}
		if m.Text != wantTexts[i] || m.Start != 0 || m.End != len("FINDME-") {
			t.Errorf("match %d = %q [%d,%d), want %q [0,7)", i, m.Text, m.Start, m.End, wantTexts[i])
		}
		if i > 0 && m.Row >= all.Matches[i-1].Row {
			t.Errorf("matches are not newest first: row %d after row %d", m.Row, all.Matches[i-1].Row)
		}
		if m.Row+m.AgeRows != all.Matches[0].Row+all.Matches[0].AgeRows {
			t.Errorf("match %d: row %d plus age_rows %d does not name the same bottom row as match 0", i, m.Row, m.AgeRows)
		}
		if m.Row >= len(lines) || strings.TrimRight(lines[m.Row], " ") != m.Text {
			got := "(past the capture)"
			if m.Row < len(lines) {
				got = lines[m.Row]
			}
			t.Errorf("match %d says row %d holds %q, and the capture's row %d is %q", i, m.Row, m.Text, m.Row, got)
		}
	}
	if oldest := all.Matches[2]; oldest.Row >= capture.HistoryRows {
		t.Errorf("the first marker is at row %d, but the pane has %d history rows above the screen: rows do not count from the top of the history", oldest.Row, capture.HistoryRows)
	}
	if r := search(map[string]any{"query": "FINDME-ONE"}); len(r.Matches) != 0 {
		t.Errorf("smart case: a capital query matched %+v", r.Matches)
	}
	if r := search(map[string]any{"query": "findme-one", "case": "sensitive"}); len(r.Matches) != 0 {
		t.Errorf("case sensitive matched %+v", r.Matches)
	}
	if r := search(map[string]any{"query": "FINDME-(one|two)", "regex": true, "sessions": []string{session}}); len(r.Matches) != 3 {
		t.Errorf("regex search found %d matches, want 3", len(r.Matches))
	}
	if r := search(map[string]any{"query": "findme-", "per_pane": 1}); len(r.Matches) != 1 || !r.Truncated {
		t.Errorf("per_pane 1: %d matches, truncated %v; want 1 and true", len(r.Matches), r.Truncated)
	}
	for _, bad := range []map[string]any{
		{"query": strings.Repeat("a", 513)},
		{"query": "x*", "regex": true},
		{"query": "(", "regex": true},
	} {
		_, err := c.call("search-panes", bad)
		var vf *verbFailure
		if !errors.As(err, &vf) || vf.Code != "invalid_params" {
			t.Errorf("search-panes %v answered %v, want invalid_params", bad["query"], err)
		}
	}

	// command-history.
	var hist struct {
		Commands []historyCommand `json:"commands"`
	}
	c.must("command-history", map[string]any{"session": session, "window": "sh"}, &hist)
	evidence["command_history"] = hist
	var got []string
	for _, h := range hist.Commands {
		got = append(got, h.Cmdline)
	}
	want := []string{`pwd`, `cd sub`, `printf 'FIND%s\n' ME-one`, `printf 'FIND%s\n' ME-two; false`, `for i in $(seq 1 80); do echo filler $i; done`}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("command-history lists\n  %q\nwant\n  %q", got, want)
	}
	byCmd := map[string]historyCommand{}
	for _, h := range hist.Commands {
		byCmd[h.Cmdline] = h
	}
	if h := byCmd[want[3]]; h.Exit == nil || *h.Exit != 1 {
		t.Errorf("the failing command has exit %v, want 1", h.Exit)
	}
	if h := byCmd["pwd"]; h.Exit == nil || *h.Exit != 0 || h.Cwd != sub {
		t.Errorf("pwd: exit %v, cwd %q; want 0 and %q", h.Exit, h.Cwd, sub)
	}
	if h := byCmd["cd sub"]; h.Cwd != work {
		t.Errorf("cd sub started in %q, want %q", h.Cwd, work)
	}
	if h := byCmd[want[2]]; h.Seq != 4 {
		t.Errorf("the repeated command has seq %d, want 4, its newer run", h.Seq)
	}
	for _, h := range hist.Commands {
		if h.Window != shID || h.Name != "sh" || h.At <= 0 {
			t.Errorf("history row %+v does not name sh or has no start time", h)
		}
	}
	prompt := search(map[string]any{"query": "$ pwd", "case": "sensitive", "sessions": []string{session}})
	if len(prompt.Matches) != 1 || prompt.Matches[0].Row != byCmd["pwd"].Row {
		t.Errorf("pwd's prompt row is %d in command-history, and search-panes finds the prompt at %+v", byCmd["pwd"].Row, prompt.Matches)
	}
	var filtered struct {
		Commands []historyCommand `json:"commands"`
	}
	c.must("command-history", map[string]any{"query": "FIND", "limit": 1}, &filtered)
	if len(filtered.Commands) != 1 || filtered.Commands[0].Cmdline != want[2] {
		t.Errorf("command-history query FIND limit 1 = %+v, want the newest printf", filtered.Commands)
	}

	// list-ports. The test's own listener is outside every pane.
	outside, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outside.Close() }()
	outsidePort := outside.Addr().(*net.TCPAddr).Port
	srvID := cli("new-window", "-s", session, "--print-id", "srv", "--", "sh", "-c", "python3 -m http.server 0 --bind 127.0.0.1; true")
	served := waitCapture(t, base, session, "srv", "Serving HTTP")
	m := regexp.MustCompile(`port (\d+)`).FindStringSubmatch(served)
	if m == nil {
		t.Fatalf("python printed no port:\n%s", served)
	}
	port, _ := strconv.Atoi(m[1])
	var ports struct {
		Supported bool        `json:"supported"`
		Ports     []portEntry `json:"ports"`
	}
	deadline := time.Now().Add(uiTimeout)
	var hit *portEntry
	for hit == nil {
		c.must("list-ports", map[string]any{"session": session}, &ports)
		for i := range ports.Ports {
			if ports.Ports[i].Port == port {
				hit = &ports.Ports[i]
			}
		}
		if hit == nil {
			if time.Now().After(deadline) {
				t.Fatalf("list-ports never listed python's port %d: %+v", port, ports)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	evidence["list_ports"] = ports
	if !ports.Supported {
		t.Errorf("list-ports says unsupported on linux")
	}
	if hit.Window != srvID || hit.Session != session || hit.Addr != "127.0.0.1" || !strings.HasPrefix(hit.Comm, "python") || hit.PID <= 0 || hit.Worktree != "" {
		t.Errorf("python's port row is %+v, want window %s, addr 127.0.0.1, a python pid and no worktree", *hit, srvID)
	}
	for _, p := range ports.Ports {
		if p.Port == outsidePort {
			t.Errorf("list-ports lists the test's own listener on %d, which is in no pane: %+v", outsidePort, p)
		}
	}
	var noSession struct {
		Ports []portEntry `json:"ports"`
	}
	c.must("list-ports", nil, &noSession)
	if len(noSession.Ports) != len(ports.Ports) {
		t.Errorf("list-ports with no session lists %d rows, and for the only session %d", len(noSession.Ports), len(ports.Ports))
	}

	// started_by. The shell in sh opens a window; the test opened sh and srv
	// from outside every pane.
	out := run(`"$TUIOS_BIN" new-window --print-id child -- sleep 600`)
	childID := strings.TrimSpace(out["output"].(string))
	if childID == "" {
		t.Fatalf("new-window from the pane printed no id: %v", out)
	}
	var windows struct {
		Windows []struct {
			ID        string   `json:"window_id"`
			Name      string   `json:"display_name"`
			StartedBy *paneRef `json:"started_by"`
		} `json:"windows"`
	}
	c.must("list-windows", map[string]any{"session": session}, &windows)
	evidence["list_windows"] = windows
	seenChild := false
	for _, w := range windows.Windows {
		switch w.ID {
		case childID:
			seenChild = true
			if w.StartedBy == nil || *w.StartedBy != (paneRef{Session: session, Window: shID}) {
				t.Errorf("child's started_by is %+v, want %s/%s", w.StartedBy, session, shID)
			}
		default:
			if w.StartedBy != nil {
				t.Errorf("window %s (%s) was opened from outside every pane and has started_by %+v", w.Name, w.ID, *w.StartedBy)
			}
		}
	}
	if !seenChild {
		t.Fatalf("list-windows does not list the child %s", childID)
	}
	var agents struct {
		Agents []struct {
			Window    string   `json:"window_id"`
			StartedBy *paneRef `json:"started_by"`
		} `json:"agents"`
	}
	c.must("list-agents", map[string]any{"session": session, "all": true}, &agents)
	for _, a := range agents.Agents {
		if (a.Window == childID) != (a.StartedBy != nil) {
			t.Errorf("list-agents row %s has started_by %+v", a.Window, a.StartedBy)
		}
	}

	b, _ := json.MarshalIndent(evidence, "", "  ")
	path := filepath.Join(artifactDir(t), "find_ports_tree.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Errorf("write the artifact: %v", err)
	}
	t.Logf("answers written to %s", path)
}

// TestSearchPanesKeepsToItsBudget searches four panes of 150,000 history lines
// each for text none of them holds. Each pane gets 50 ms, read from the bottom
// up in chunks with the pane's lock let go between them, so an answer comes in
// well under a second and says truncated. The search copied every pane's
// whole history under its lock before it looked at the clock: 134 ms and
// 5.7 MB for each 10,000 lines, so seconds here.
func TestSearchPanesKeepsToItsBudget(t *testing.T) {
	const session = "big"
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance]\nscrollback_lines = 150000\n")
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(out)
	}
	cli("new", session, "--detach")
	for i := range 4 {
		cli("new-window", "-s", session, "--print-id", fmt.Sprintf("hist%d", i), "--", "sh", "-c", "seq -f 'history line %g' 1 150000; exec sleep 600")
	}
	c := dialFileVerbs(t, base)
	deadline := time.Now().Add(60 * time.Second)
	for {
		var r findResult
		c.must("search-panes", map[string]any{"query": "history line 150000", "sessions": []string{session}}, &r)
		if len(r.Matches) >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the four panes never printed their history: %d matches", len(r.Matches))
		}
		time.Sleep(200 * time.Millisecond)
	}
	var times []time.Duration
	var last findResult
	for range 5 {
		start := time.Now()
		c.must("search-panes", map[string]any{"query": "NOT-IN-ANY-PANE", "sessions": []string{session}}, &last)
		times = append(times, time.Since(start))
	}
	slices.Sort(times)
	median := times[len(times)/2]
	t.Logf("search over four panes of 150,000 lines: %v (median of %v), truncated %v", median, times, last.Truncated)
	saveTransferArtifact(t, "search-panes-budget", map[string]any{"median_ms": median.Milliseconds(), "truncated": last.Truncated, "panes": last.PanesSearched})
	if median > 500*time.Millisecond {
		t.Errorf("ASSERTION: a search of four 150,000-line panes took %v, budget 50 ms a pane", median)
	}
	if !last.Truncated || last.PanesSearched != 5 && last.PanesSearched != 4 {
		t.Errorf("ASSERTION: the search did not say it was cut short: truncated %v over %d panes", last.Truncated, last.PanesSearched)
	}
}
