package tmuxcompat

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Tests for the commands tools that drive tmux from outside it use: the
// ones Collie's tmux adapter (bridge/mux/tmux in github.com/AltanS/collie)
// sends, argv for argv.

// collieSep is the field separator Collie's formats use, U+001F.
const collieSep = "\x1f"

// collieListing is Collie's snapshot call (LISTING_ARGS in protocol.ts).
func collieListing() []string {
	j := func(f ...string) string { return strings.Join(f, collieSep) }
	return []string{
		"list-sessions", "-F", j("S", "#{session_id}", "#{session_windows}", "#{session_activity}", "#{session_path}", "#{session_name}"), ";",
		"list-windows", "-a", "-F", j("W", "#{window_id}", "#{session_id}", "#{window_index}", "#{window_active}", "#{window_panes}", "#{automatic-rename}", "#{window_name}"), ";",
		"list-panes", "-a", "-F", j("P", "#{pane_id}", "#{window_id}", "#{session_id}", "#{pane_dead}", "#{pane_active}", "#{window_active}", "#{pane_height}", "#{history_size}", "#{host}", "#{pane_current_path}", "#{pane_current_command}", "#{pane_title}"), ";",
		"list-clients", "-F", j("C", "#{client_session}", "#{client_control_mode}", "#{client_activity}", "#{client_tty}"),
	}
}

// records splits listing output into its tagged records.
func records(out string) map[string][][]string {
	got := map[string][][]string{}
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		f := strings.Split(line, collieSep)
		got[f[0]] = append(got[f[0]], f[1:])
	}
	return got
}

// TestCollieListingFillsEveryField runs Collie's one snapshot call and checks
// every field it reads comes back filled, in a pane and outside one.
func TestCollieListingFillsEveryField(t *testing.T) {
	h := newHarness(t)
	h.fake.windows[0].cmd = "claude"
	h.fake.windows[0].history = 312
	code, out := h.run(collieListing()...)
	if code != 0 {
		t.Fatalf("listing failed: %s", h.err)
	}
	rec := records(out)
	if got := rec["S"]; len(got) != 1 || !slices.Equal(got[0], []string{"$0", "1", "1700000500", "/src", "work"}) {
		t.Errorf("session record = %q", got)
	}
	if got := rec["W"]; len(got) != 1 || !slices.Equal(got[0], []string{"@1", "$0", "1", "1", "1", "1", "claude"}) {
		t.Errorf("window record = %q", got)
	}
	p := rec["P"]
	if len(p) != 1 {
		t.Fatalf("pane records = %q", p)
	}
	want := []string{PaneID("leader-0001"), "@1", "$0", "0", "1", "1", "40", "312"}
	if !slices.Equal(p[0][:8], want) || p[0][9] != "/src" || p[0][10] != "claude" || p[0][11] != "claude" {
		t.Errorf("pane record = %q, want %q then host, /src, claude, claude", p[0], want)
	}
	if got := rec["C"]; len(got) != 1 || !slices.Equal(got[0], []string{"work", "0", "1700000500", ""}) {
		t.Errorf("client record = %q", got)
	}
	for _, e := range h.logEntries(t) {
		if e.Outcome != OutcomeOK {
			t.Errorf("the listing was logged as %s: %v", e.Outcome, e.Detail)
		}
	}

	// At a shell prompt the daemon names no command: the shell is the
	// pane's command, as in tmux.
	h.fake.windows[0].cmd = ""
	h.shim.Shell = "/usr/bin/zsh"
	_, out = h.run("display-message", "-p", "#{pane_current_command}")
	if out != "zsh\n" {
		t.Errorf("pane_current_command at a prompt = %q, want zsh", out)
	}
}

// TestCollieListingEverySession serves every session, as for a tool outside
// tuios: each session is a tmux session with ids of its own, and window ids
// stay unique on the server.
func TestCollieListingEverySession(t *testing.T) {
	h := newHarness(t)
	m := newMultiFake(t, "work", "api")
	h.shim.Caller = m
	h.shim.AllSessions = true
	h.shim.Session, h.shim.Window, h.shim.TmuxPane = "", "", ""
	code, out := h.run(collieListing()...)
	if code != 0 {
		t.Fatalf("listing failed: %s", h.err)
	}
	rec := records(out)
	if len(rec["S"]) != 2 || len(rec["W"]) != 2 || len(rec["P"]) != 2 {
		t.Fatalf("records = %q, want two sessions, two windows, two panes", rec)
	}
	ids := map[string]bool{}
	for _, s := range rec["S"] {
		ids[s[0]] = true
		if s[0] == "$0" || !strings.HasPrefix(s[0], "$") {
			t.Errorf("session id %q, want $N other than $0", s[0])
		}
	}
	for _, w := range rec["W"] {
		if !ids[w[1]] {
			t.Errorf("window %v names session %s, not listed", w, w[1])
		}
	}
	if rec["W"][0][0] == rec["W"][1][0] {
		t.Errorf("both workspaces 1 have the id %s", rec["W"][0][0])
	}
	// A window id reaches its own session: new-window -t $N lands there,
	// and select-window -t @N shows that session's workspace.
	apiID := rec["S"][1][0]
	if rec["S"][1][4] != "api" {
		apiID = rec["S"][0][0]
	}
	code, out = h.run("new-window", "-d", "-t", apiID, "-P", "-F", "#{pane_id}"+collieSep+"#{window_id}"+collieSep+"#{session_id}"+collieSep+"#{session_name}", "-n", "logs")
	if code != 0 {
		t.Fatalf("new-window -t %s failed: %s", apiID, h.err)
	}
	f := strings.Split(strings.TrimSpace(out), collieSep)
	if len(f) != 4 || f[2] != apiID || f[3] != "api" {
		t.Fatalf("new-window printed %q, want the api session", out)
	}
	api := m.session("api")
	if len(api.windows) != 2 || api.windows[1].ws != 2 || api.wsNames[2] != "logs" {
		t.Errorf("api session after new-window: %d windows, names %v", len(api.windows), api.wsNames)
	}
	if code, _ := h.run("select-window", "-t", f[1]); code != 0 || api.current != 2 {
		t.Errorf("select-window -t %s = %d, api current %d: %s", f[1], code, api.current, h.err)
	}
	if m.session("work").current != 1 {
		t.Error("select-window moved the work session")
	}
}

// TestNewSessionEverySession maps new-session to a tuios session when the
// shim serves every session, and refuses it for a caller in a pane.
func TestNewSessionEverySession(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run("new-session", "-d", "-s", "x"); code != 0 || !strings.Contains(h.err.String(), "in a pane does not start sessions") {
		// The error names the refusal; the exit status is tmux's 1.
		if code == 0 {
			t.Fatal("new-session from a pane succeeded")
		}
	}
	m := newMultiFake(t, "work")
	h.shim.Caller = m
	h.shim.AllSessions = true
	h.shim.Session, h.shim.Window, h.shim.TmuxPane = "", "", ""
	code, out := h.run("new-session", "-d", "-P", "-F", "#{session_name}"+collieSep+"#{pane_current_path}", "-c", "/srv/app", "-s", "app")
	if code != 0 {
		t.Fatalf("new-session failed: %s", h.err)
	}
	if out != "app"+collieSep+"/srv/app\n" {
		t.Errorf("new-session -P printed %q", out)
	}
	if code, _ := h.run("new-session", "-s", "other"); code == 0 || !strings.Contains(h.err.String(), "add -d") {
		t.Errorf("new-session without -d = %d %q, want a refusal asking for -d", code, h.err)
	}
	if code, _ := h.run("new-session", "-d", "-s", "app"); code == 0 || !strings.Contains(h.err.String(), "duplicate session: app") {
		t.Errorf("a second app = %d %q, want duplicate session", code, h.err)
	}
	if code, _ := h.run("has-session", "-t", "app"); code != 0 {
		t.Errorf("has-session -t app failed: %s", h.err)
	}
}

// TestCollieReplyThroughPasteBuffer sends a reply the way Collie's typeText
// does: the text on stdin into a named buffer, pasted and deleted in the same
// call, with line feeds kept.
func TestCollieReplyThroughPasteBuffer(t *testing.T) {
	h := newHarness(t)
	h.shim.Dir = t.TempDir()
	h.shim.Stdin = strings.NewReader("yes;\nsecond line\x1b[201~rm -rf /\x07")
	leader := PaneID("leader-0001")
	code, _ := h.run("load-buffer", "-b", "collie-type", "-", ";", "paste-buffer", "-d", "-b", "collie-type", "-t", leader, "-s", "\n")
	if code != 0 {
		t.Fatalf("load-buffer ; paste-buffer failed: %s", h.err)
	}
	st := h.fake.last("send-text")
	if st["text"] != "yes;\nsecond line[201~rm -rf /" || st["window"] != "leader-0001" {
		t.Errorf("send-text = %v, want the text without its control characters", st)
	}
	if _, ok := st["paste"]; ok {
		t.Errorf("paste-buffer without -p asked for a bracketed paste: %v", st)
	}
	if left, _ := os.ReadDir(filepath.Join(h.shim.Dir, "buffers")); len(left) != 0 {
		t.Errorf("paste-buffer -d left %d buffers", len(left))
	}
}

// TestPasteBufferAcrossCalls keeps a buffer between two calls, as tmux's
// server does, and pastes the newest when -b names none.
func TestPasteBufferAcrossCalls(t *testing.T) {
	h := newHarness(t)
	h.shim.Dir = t.TempDir()
	h.shim.Cwd = t.TempDir()
	if err := os.WriteFile(filepath.Join(h.shim.Cwd, "note.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := h.run("load-buffer", "note.txt"); code != 0 {
		t.Fatalf("load-buffer note.txt failed: %s", h.err)
	}
	if code, _ := h.run("paste-buffer", "-p"); code != 0 {
		t.Fatalf("paste-buffer failed: %s", h.err)
	}
	st := h.fake.last("send-text")
	if st["text"] != "one\rtwo\r" || st["paste"] != true {
		t.Errorf("send-text = %v, want line feeds as carriage returns, as a bracketed paste", st)
	}
	if code, _ := h.run("paste-buffer", "-r", "-b", "buffer0000"); code != 0 {
		t.Fatalf("paste-buffer -r failed: %s", h.err)
	}
	if st := h.fake.last("send-text"); st["text"] != "one\ntwo\n" {
		t.Errorf("paste-buffer -r typed %q, want the line feeds kept", st["text"])
	}
	if code, _ := h.run("set-buffer", "-b", "b2", "hi"); code != 0 {
		t.Fatal(h.err)
	}
	if code, _ := h.run("set-buffer", "-a", "-b", "b2", " there"); code != 0 {
		t.Fatal(h.err)
	}
	if code, _ := h.run("paste-buffer", "-d"); code != 0 {
		t.Fatal(h.err)
	}
	if st := h.fake.last("send-text"); st["text"] != "hi there" {
		t.Errorf("the newest buffer typed %q, want b2", st["text"])
	}
	if code, _ := h.run("paste-buffer", "-b", "b2"); code == 0 || !strings.Contains(h.err.String(), "no buffer b2") {
		t.Errorf("b2 after -d = %d %q, want no buffer b2", code, h.err)
	}
	if code, _ := h.run("delete-buffer", "-b", "buffer0000"); code != 0 {
		t.Fatal(h.err)
	}
	before := len(h.fake.calls)
	if code, _ := h.run("paste-buffer"); code != 0 || len(h.fake.calls) != before {
		t.Errorf("paste-buffer with no buffers = %d and %d calls, want a quiet no-op", code, len(h.fake.calls)-before)
	}
}

// TestPasteBufferHeldToGrants checks a paste goes through send-text, the verb
// the daemon holds to the caller's pane grants, and that a refusal types
// nothing and keeps the buffer.
func TestPasteBufferHeldToGrants(t *testing.T) {
	h := newHarness(t)
	h.shim.Dir = t.TempDir()
	h.fake.refuseText = "forbidden"
	h.shim.Stdin = strings.NewReader("rm -rf ~\n")
	code, _ := h.run("load-buffer", "-b", "x", "-", ";", "paste-buffer", "-d", "-b", "x", "-t", PaneID("leader-0001"))
	if code != 1 || !strings.Contains(h.err.String(), "forbidden") {
		t.Fatalf("a refused paste = %d %q, want the daemon's refusal", code, h.err)
	}
	for _, c := range h.fake.calls {
		if c.verb == "send-keys" || c.verb == "run" || c.verb == "ask-agent" {
			t.Errorf("the paste tried %s after send-text was refused", c.verb)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(h.shim.Dir, "buffers")); len(left) != 1 {
		t.Errorf("the refused paste left %d buffers, want the one kept", len(left))
	}
}

// TestShowOptions answers the options tools ask about, the way tmux does.
func TestShowOptions(t *testing.T) {
	h := newHarness(t)
	h.shim.Shell = "/bin/zsh"
	for _, c := range []struct {
		args []string
		out  string
	}{
		{[]string{"show-options", "-gv", "window-size"}, "latest\n"},
		{[]string{"show", "-g", "base-index"}, "base-index 1\n"},
		{[]string{"show-options", "-gv", "history-limit"}, "5000\n"},
		{[]string{"showw", "-gv", "pane-base-index"}, "0\n"},
		{[]string{"show-options", "-gqv", "@collie"}, ""},
		{[]string{"show-options", "-gv", "default-shell"}, "/bin/zsh\n"},
	} {
		code, out := h.run(c.args...)
		if code != 0 || out != c.out {
			t.Errorf("%v = %d %q, want %q (%s)", c.args, code, out, c.out, h.err)
		}
	}
	if code, _ := h.run("show-options", "-gv", "nonsense"); code != 1 || !strings.Contains(h.err.String(), "invalid option: nonsense") {
		t.Errorf("an unknown option = %d %q", code, h.err)
	}
	if code, out := h.run("show-window-options", "-g"); code != 0 || !strings.Contains(out, "window-size latest\n") {
		t.Errorf("show-window-options -g = %d %q", code, out)
	}
}

// TestListClientsDetached lists no client for a session nobody is looking at.
func TestListClientsDetached(t *testing.T) {
	h := newHarness(t)
	m := newMultiFake(t, "work", "api")
	h.shim.Caller = m
	h.shim.AllSessions = true
	code, out := h.run("list-clients", "-F", "#{client_session} #{client_control_mode}")
	if code != 0 || out != "work 0\n" {
		t.Errorf("list-clients = %d %q, want only the attached work session", code, out)
	}
}

// TestPaneWithoutAdminLists runs the listing from a pane without admin, which
// may not list every session. The shim reads its own session with
// session-info, a read, and answers in full but for the times.
func TestPaneWithoutAdminLists(t *testing.T) {
	h := newHarness(t)
	h.fake.noAdmin = true
	code, out := h.run("list-sessions", "-F", "#{session_id} #{session_name} #{session_attached}")
	if code != 0 || out != "$0 work 1\n" {
		t.Errorf("list-sessions = %d %q (%s)", code, out, h.err)
	}
	if code, _ := h.run("list-panes", "-a", "-F", "#{pane_id}"); code != 0 {
		t.Errorf("list-panes failed: %s", h.err)
	}
}

// TestScratchWindowsAreNotPanes leaves the scratch terminal (workspace 1000
// and up) out of the listing. Its workspace number would collide with the
// window ids of every session (session*1000+workspace).
func TestScratchWindowsAreNotPanes(t *testing.T) {
	h := newHarness(t)
	m := newMultiFake(t, "work", "api")
	m.session("work").windows = append(m.session("work").windows, &fakeWindow{id: "scratch-0001", ws: 1000, scratch: true, w: 80, h: 24})
	h.shim.Caller = m
	h.shim.AllSessions = true
	h.shim.Session, h.shim.Window, h.shim.TmuxPane = "", "", ""
	code, out := h.run("list-windows", "-a", "-F", "#{window_id} #{window_index}", ";", "list-panes", "-a", "-F", "#{pane_id}")
	if code != 0 {
		t.Fatalf("listing failed: %s", h.err)
	}
	if strings.Contains(out, " 1000\n") || strings.Contains(out, PaneID("scratch-0001")) {
		t.Errorf("the scratch terminal is listed:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n != 4 {
		t.Errorf("listing has %d lines, want two windows and two panes:\n%s", n, out)
	}
}
