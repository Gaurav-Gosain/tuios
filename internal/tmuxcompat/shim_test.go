package tmuxcompat

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestClaudeCodeTeammateSequence replays the tmux calls Claude Code 2.1.280's
// TmuxBackend makes to open, name, lay out, start and close one teammate pane
// from inside tmux, argv for argv, and checks each is answered the way the
// backend needs: the ids it parses come back in tmux's shape, the pane lands
// in the leader's workspace without taking the focus, the placeholder is
// respawned with the teammate's command, and nothing was logged as
// unsupported.
func TestClaudeCodeTeammateSequence(t *testing.T) {
	h := newHarness(t)
	sock := SocketPath(h.shim.Dir)
	leader := PaneID("leader-0001")

	code, out := h.run("-S", sock, "display-message", "-t", leader, "-p", "#{window_id}")
	if code != 0 || out != "@1\n" {
		t.Fatalf("display-message #{window_id} = %d %q, want 0 %q (stderr %q)", code, out, "@1\n", h.err)
	}
	code, out = h.run("-S", sock, "list-panes", "-t", "@1", "-F", "#{pane_id}")
	if code != 0 || out != leader+"\n" {
		t.Fatalf("list-panes = %d %q, want the leader alone", code, out)
	}

	code, out = h.run("-S", sock, "split-window", "-d", "-t", leader, "-h", "-l", "70%", "-P", "-F", "#{pane_id}", "--", "cat")
	if code != 0 {
		t.Fatalf("split-window failed: %s", h.err)
	}
	teammate := PaneID("new-0001")
	if out != teammate+"\n" {
		t.Fatalf("split-window -P printed %q, want %q", out, teammate+"\n")
	}
	nw := h.fake.last("new-window")
	if nw["workspace"] != float64(1) || nw["focus"] != false || nw["cwd"] != "/src" {
		t.Errorf("new-window params = %v, want workspace 1, focus false, cwd /src", nw)
	}
	wantCmd := []any{"/opt/tuios", "tmux-pane", "--dir", "/run/tuios/tmux", "--", "cat"}
	if !reflect.DeepEqual(nw["command"], wantCmd) {
		t.Errorf("new-window command = %v, want %v", nw["command"], wantCmd)
	}

	for _, args := range [][]string{
		{"set-option", "-p", "-t", teammate, "window-style", "bg=default,fg=blue"},
		{"set-option", "-p", "-t", teammate, "pane-border-style", "fg=blue"},
		{"set-option", "-p", "-t", teammate, "pane-active-border-style", "fg=blue"},
	} {
		if code, _ := h.run(append([]string{"-S", sock}, args...)...); code != 0 {
			t.Fatalf("%v failed: %s", args, h.err)
		}
	}
	focusBefore := h.fake.focused
	if code, _ := h.run("-S", sock, "select-pane", "-t", teammate, "-T", "researcher"); code != 0 {
		t.Fatalf("select-pane -T failed: %s", h.err)
	}
	if sw := h.fake.last("set-window"); sw["window"] != "new-0001" || sw["name"] != "researcher" {
		t.Errorf("set-window = %v, want the teammate named researcher", sw)
	}
	if h.fake.called("focus-window") || h.fake.focused != focusBefore {
		t.Error("select-pane -T moved the focus; naming a pane must not")
	}
	for _, args := range [][]string{
		{"set-option", "-p", "-t", teammate, "pane-border-format", "#[fg=blue,bold] #{pane_title} #[default]"},
		{"select-layout", "-t", "@1", "main-vertical"},
		{"resize-pane", "-t", leader, "-x", "30%"},
		{"set-option", "-p", "-t", teammate, "remain-on-exit", "failed"},
	} {
		if code, _ := h.run(append([]string{"-S", sock}, args...)...); code != 0 {
			t.Fatalf("%v failed: %s", args, h.err)
		}
	}
	code, out = h.run("-S", sock, "list-panes", "-t", "@1", "-F", "#{pane_id}")
	if code != 0 || out != leader+"\n"+teammate+"\n" {
		t.Fatalf("list-panes after the split = %q, want leader then teammate", out)
	}

	teammateCmd := "cd /src && env CLAUDECODE=1 claude --agent-id researcher@team"
	if code, _ := h.run("-S", sock, "respawn-pane", "-k", "-t", teammate, "--", teammateCmd); code != 0 {
		t.Fatalf("respawn-pane failed: %s", h.err)
	}
	if len(h.respawns) != 1 || !reflect.DeepEqual(h.respawns[0].Command, []string{teammateCmd}) {
		t.Fatalf("respawn requests = %+v, want one with the teammate's command", h.respawns)
	}

	if code, _ := h.run("-S", sock, "kill-pane", "-t", teammate); code != 0 {
		t.Fatalf("kill-pane failed: %s", h.err)
	}
	if cw := h.fake.last("close-window"); cw["window"] != "new-0001" {
		t.Errorf("close-window = %v, want the teammate", cw)
	}
	if len(h.fake.foreign) > 0 {
		t.Errorf("calls named other sessions: %v", h.fake.foreign)
	}
	if got := h.logEntries(t); len(got) != 0 {
		t.Errorf("the default log recorded %v; every call in the sequence is supported", got)
	}
}

// TestDisplayMessagePaneID covers the call Claude makes when TMUX_PANE is
// unset.
func TestDisplayMessagePaneID(t *testing.T) {
	h := newHarness(t)
	h.shim.TmuxPane = ""
	code, out := h.run("display-message", "-p", "#{pane_id}")
	if code != 0 || out != PaneID("leader-0001")+"\n" {
		t.Fatalf("display-message = %d %q", code, out)
	}
}

// TestTargetsStayInTheCallersSession names another session every way a
// target can, and checks each is refused without a verb call reaching it.
func TestTargetsStayInTheCallersSession(t *testing.T) {
	for _, args := range [][]string{
		{"has-session", "-t", "other"},
		{"list-panes", "-t", "other:1"},
		{"send-keys", "-t", "other:1.0", "rm -rf ~", "Enter"},
		{"kill-pane", "-t", "other:1.0"},
		{"kill-window", "-t", "other:1"},
		{"split-window", "-t", "other:"},
		{"new-window", "-t", "other:"},
		{"capture-pane", "-p", "-t", "other:1.0"},
		{"respawn-pane", "-k", "-t", "other:1.0"},
		{"list-windows", "-t", "other"},
		{"kill-session", "-t", "work"},
		{"kill-server"},
		{"new-session", "-d", "-s", "x"},
	} {
		h := newHarness(t)
		code, _ := h.run(args...)
		if code == 0 {
			t.Errorf("%v succeeded, want it refused", args)
		}
		if len(h.fake.foreign) > 0 {
			t.Errorf("%v called verbs on sessions %v", args, h.fake.foreign)
		}
		for _, v := range []string{"send-text", "close-window", "new-window"} {
			if h.fake.called(v) {
				t.Errorf("%v reached %s", args, v)
			}
		}
	}
	h := newHarness(t)
	for _, name := range []string{"work", "=work", "$0", "work:1"} {
		if code, _ := h.run("has-session", "-t", name); code != 0 {
			t.Errorf("has-session -t %s = %d, want 0", name, code)
		}
	}
}

// TestUnsupportedCallsAreLogged checks the default log records what the shim
// could not answer, and only that.
func TestUnsupportedCallsAreLogged(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run("wait-for", "-S", "done"); code != 1 {
		t.Error("wait-for succeeded")
	}
	if !strings.Contains(h.err.String(), "unknown command: wait-for") {
		t.Errorf("stderr = %q", h.err)
	}
	if code, _ := h.run("split-window", "-Q"); code != 1 {
		t.Error("split-window -Q succeeded")
	}
	if code, out := h.run("display-message", "-p", "#{pane_current_command}|#{pane_id}"); code != 0 || !strings.HasPrefix(out, "|%") {
		t.Errorf("display-message with an unknown variable = %d %q", code, out)
	}
	h.run("list-panes")
	h.run("set-option", "-g", "mouse", "on")

	got := h.logEntries(t)
	if len(got) != 3 {
		t.Fatalf("log = %+v, want three entries", got)
	}
	want := []struct {
		cmd, outcome, detail string
	}{
		{"wait-for", OutcomeUnsupported, "unknown command: wait-for"},
		{"split-window", OutcomeUnsupported, "unknown flag -Q"},
		{"display-message", OutcomePartial, "no value for pane_current_command"},
	}
	for i, w := range want {
		e := got[i]
		if e.Argv[1] != w.cmd || e.Outcome != w.outcome || !strings.Contains(strings.Join(e.Detail, ";"), w.detail) {
			t.Errorf("entry %d = %+v, want %s %s mentioning %q", i, e, w.cmd, w.outcome, w.detail)
		}
	}

	h2 := newHarness(t)
	h2.shim.Log.All = true
	h2.run("list-panes")
	h2.run("set-option", "-g", "mouse", "on")
	all := h2.logEntries(t)
	if len(all) != 2 || all[0].Outcome != OutcomeOK || all[1].Outcome != OutcomeIgnored {
		t.Errorf("log-all = %+v, want an ok and an ignored entry", all)
	}
}

// TestLogRedactsText checks the log keeps what a call was (command, flags,
// how many arguments) and drops what it said: keys typed and command lines
// can carry secrets.
func TestLogRedactsText(t *testing.T) {
	h := newHarness(t)
	h.shim.Log.All = true
	h.run("send-keys", "-t", PaneID("leader-0001"), "export KEY=hunter2", "Enter")
	h.run("split-window", "-d", "-e", "TOKEN=hunter2", "--", "echo hunter2")
	h.run("respawn-pane", "-k", "-t", PaneID("new-0001"), "claude --api-key hunter2")
	h.run("send-keys", "-Q", "hunter2")
	h.run("display-message", "-p", "#{pane_id}")
	data, err := os.ReadFile(h.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Errorf("the log holds typed text:\n%s", data)
	}
	got := h.logEntries(t)
	if len(got) != 5 {
		t.Fatalf("log = %+v", got)
	}
	want := [][]string{
		{"tmux", "send-keys", "-t", PaneID("leader-0001"), "<2 redacted>"},
		{"tmux", "split-window", "-d", "-e", "<redacted>", "--", "<1 redacted>"},
		{"tmux", "respawn-pane", "-k", "-t", PaneID("new-0001"), "<1 redacted>"},
		{"tmux", "send-keys", "-Q", "<redacted>"},
		{"tmux", "display-message", "-p", "#{pane_id}"},
	}
	for i, w := range want {
		if !reflect.DeepEqual(got[i].Argv, w) {
			t.Errorf("entry %d argv = %q, want %q", i, got[i].Argv, w)
		}
	}
}

// TestLogRedactsFailedCalls checks the calls the default log records, the
// ones that fail, keep nothing typed or run either: a global flag that does
// not parse, a word a ';' split into command position, a command the shim
// does not answer, and an argument quoted in an error.
func TestLogRedactsFailedCalls(t *testing.T) {
	h := newHarness(t)
	calls := []struct {
		args   []string
		argv   []string
		detail string
	}{
		{[]string{"-c", "export TOKEN=hunter2"}, []string{"tmux", "-c", "<redacted>"}, "-c is not supported"},
		{[]string{"-x", "send-keys", "hunter2"}, []string{"tmux", "-x", "<redacted>", "<redacted>"}, "unknown option: -x"},
		{[]string{"-S"}, []string{"tmux", "-S"}, "option requires an argument"},
		{[]string{"send-keys", "-t", PaneID("leader-0001"), "a;", "hunter2", "x"},
			[]string{"tmux", "send-keys", "-t", PaneID("leader-0001"), "<1 redacted>", ";", "<unknown command>", "<1 redacted>"}, "unknown command"},
		{[]string{"wait-for", "-S", "hunter2"}, []string{"tmux", "wait-for", "<2 redacted>"}, "unknown command: wait-for"},
		{[]string{"kill-server", "hunter2"}, []string{"tmux", "kill-server", "<1 redacted>"}, "kill-server: refused"},
		{[]string{"send-keys", "-H", "-t", PaneID("leader-0001"), "hunter2"},
			[]string{"tmux", "send-keys", "-H", "-t", PaneID("leader-0001"), "<1 redacted>"}, "invalid hex key"},
	}
	for i, c := range calls {
		if code, _ := h.run(c.args...); code != 1 {
			t.Errorf("%q succeeded", c.args)
		}
		// stderr is the caller's own, and still names the unknown word.
		if i == 3 && !strings.Contains(h.err.String(), "unknown command: hunter2") {
			t.Errorf("stderr = %q, want it to name the unknown command", h.err)
		}
	}
	data, err := os.ReadFile(h.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Errorf("the log holds typed text:\n%s", data)
	}
	got := h.logEntries(t)
	if len(got) != len(calls) {
		t.Fatalf("log = %+v, want %d entries", got, len(calls))
	}
	for i, c := range calls {
		if !reflect.DeepEqual(got[i].Argv, c.argv) {
			t.Errorf("entry %d argv = %q, want %q", i, got[i].Argv, c.argv)
		}
		if !strings.Contains(strings.Join(got[i].Detail, ";"), c.detail) {
			t.Errorf("entry %d detail = %q, want it to mention %q", i, got[i].Detail, c.detail)
		}
	}
}

func TestSendKeys(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"echo hi", "Enter"}, "echo hi\r"},
		{[]string{"-l", "Enter"}, "Enter"},
		{[]string{"C-c"}, "\x03"},
		{[]string{"^D"}, "\x04"},
		{[]string{"M-x"}, "\x1bx"},
		{[]string{"Escape", "Up", "BSpace", "Tab", "Space"}, "\x1b\x1b[A\x7f\t "},
		{[]string{"-H", "41", "0a"}, "A\n"},
		{[]string{"-N", "3", "x"}, "xxx"},
	}
	for _, c := range cases {
		h := newHarness(t)
		args := append([]string{"send-keys", "-t", PaneID("leader-0001")}, c.args...)
		if code, _ := h.run(args...); code != 0 {
			t.Errorf("%v failed: %s", c.args, h.err)
			continue
		}
		st := h.fake.last("send-text")
		if st["text"] != c.want || st["window"] != "leader-0001" {
			t.Errorf("send-keys %v sent %q to %v, want %q", c.args, st["text"], st["window"], c.want)
		}
	}
}

func TestCapturePane(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "a\nb\n"},
		{[]string{"-S", "-2"}, "h3\nh4\na\nb\n"},
		{[]string{"-S", "-"}, "h1\nh2\nh3\nh4\na\nb\n"},
		{[]string{"-E", "0"}, "a\n"},
		{[]string{"-S", "-1", "-E", "-1"}, "h4\n"},
		{[]string{"-S", "-100"}, "h1\nh2\nh3\nh4\na\nb\n"},
		{[]string{"-J", "-N"}, "a\nb\n"},
	}
	for _, c := range cases {
		h := newHarness(t)
		h.fake.windows[0].visible = "a\nb\n"
		h.fake.windows[0].recent = "h1\nh2\nh3\nh4\na\nb\n"
		code, out := h.run(append([]string{"capture-pane", "-p"}, c.args...)...)
		if code != 0 || out != c.want {
			t.Errorf("capture-pane -p %v = %d %q, want %q (stderr %q)", c.args, code, out, c.want, h.err)
		}
	}
	h := newHarness(t)
	if code, _ := h.run("capture-pane"); code != 1 {
		t.Error("capture-pane without -p succeeded; the shim keeps no buffers")
	}
	h.run("capture-pane", "-p", "-e")
	if h.fake.last("capture-pane")["styled"] != true {
		t.Error("capture-pane -e did not ask for a styled capture")
	}
}

func TestNewWindow(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("new-window", "-d", "-n", "swarm", "-P", "-F", "#{window_id} #{window_name} #{pane_id}")
	if code != 0 {
		t.Fatalf("new-window failed: %s", h.err)
	}
	if want := "@2 swarm " + PaneID("new-0001") + "\n"; out != want {
		t.Errorf("new-window -P = %q, want %q", out, want)
	}
	if nw := h.fake.last("new-window"); nw["workspace"] != float64(2) || nw["focus"] != false {
		t.Errorf("new-window params = %v, want workspace 2 unfocused", nw)
	}
	if code, _ := h.run("new-window", "-t", "work:1"); code != 1 || !strings.Contains(h.err.String(), "index 1 in use") {
		t.Errorf("new-window on an occupied workspace = %d %q", code, h.err)
	}
	code, out = h.run("list-windows", "-F", "#{window_index}:#{window_panes}:#{window_active}")
	if code != 0 || out != "1:1:1\n2:1:0\n" {
		t.Errorf("list-windows = %q", out)
	}
}

func TestSelectPaneAndWindow(t *testing.T) {
	h := newHarness(t)
	h.run("split-window", "-d")
	if code, _ := h.run("select-pane", "-t", PaneID("new-0001")); code != 0 {
		t.Fatal(h.err)
	}
	if fw := h.fake.last("focus-window"); fw["window"] != "new-0001" {
		t.Errorf("focus-window = %v", fw)
	}
	h.run("select-pane", "-L")
	if fw := h.fake.last("focus-window"); fw["direction"] != "left" {
		t.Errorf("select-pane -L sent %v", fw)
	}
	h.run("rename-window", "-t", "@1", "team")
	if sw := h.fake.last("set-workspace-name"); sw["workspace"] != float64(1) || sw["name"] != "team" {
		t.Errorf("rename-window sent %v", sw)
	}
	h.run("select-window", "-t", "team")
	if sw := h.fake.last("select-workspace"); sw["workspace"] != float64(1) {
		t.Errorf("select-window by name sent %v", sw)
	}
}

func TestRespawnNeedsKillAndAHolder(t *testing.T) {
	h := newHarness(t)
	h.run("split-window", "-d", "--", "cat")
	if code, _ := h.run("respawn-pane", "-t", PaneID("new-0001"), "true"); code != 1 || !strings.Contains(h.err.String(), "still active") {
		t.Errorf("respawn-pane without -k = %d %q", code, h.err)
	}
	h.fake.windows = append(h.fake.windows, &fakeWindow{id: "plain-0009", ws: 1})
	if code, _ := h.run("respawn-pane", "-k", "-t", PaneID("plain-0009"), "true"); code != 1 {
		t.Error("respawn-pane of a pane the shim did not open succeeded")
	}
	if len(h.respawns) != 0 {
		t.Errorf("respawns = %v", h.respawns)
	}
}

func TestSplitWindowWithoutAHolder(t *testing.T) {
	h := newHarness(t)
	h.shim.Exe = ""
	h.run("split-window", "-d", "top -b")
	if got := h.fake.last("new-window")["command"]; !reflect.DeepEqual(got, []any{"/bin/sh", "-c", "top -b"}) {
		t.Errorf("command = %v", got)
	}
	h.run("split-window", "-d")
	if _, ok := h.fake.last("new-window")["command"]; ok {
		t.Error("a split with no command sent one; the daemon's shell is the default")
	}
}

func TestChainedCommands(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("display-message", "-p", "one", ";", "display-message", "-p", "two;")
	if code != 0 || out != "one\ntwo\n" {
		t.Errorf("chained = %d %q", code, out)
	}
	if code, _ := h.run("-V"); code != 0 || h.out.String() != "tmux "+Version+"\n" {
		t.Errorf("-V = %q", h.out)
	}
}

func TestOtherServerIsRefused(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run("-L", "claude-swarm-1", "has-session", "-t", "claude-swarm"); code != 1 {
		t.Error("-L reached the shim")
	}
	if code, _ := h.run("-S", "/tmp/tmux-501/default", "list-panes"); code != 1 {
		t.Error("-S naming another socket reached the shim")
	}
	if len(h.fake.calls) != 0 {
		t.Errorf("calls = %v", h.fake.verbs())
	}
}

func TestForShim(t *testing.T) {
	dir := "/run/tuios/tmux"
	ours := TmuxValue(dir, 7)
	cases := []struct {
		g    Global
		tmux string
		want bool
	}{
		{Global{}, ours, true},
		{Global{}, "/tmp/tmux-501/default,1,0", false},
		{Global{}, "", false},
		{Global{Socket: SocketPath(dir)}, "", true},
		{Global{Socket: "/tmp/x"}, ours, false},
		{Global{Name: "swarm"}, ours, false},
	}
	for _, c := range cases {
		if got := ForShim(c.g, c.tmux, dir); got != c.want {
			t.Errorf("ForShim(%+v, %q) = %v", c.g, c.tmux, got)
		}
	}
	if ForShim(Global{}, ours, "") {
		t.Error("an empty dir matched")
	}
}

func TestLauncherEnv(t *testing.T) {
	dir := "/run/tuios/tmux"
	base := []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/tmux-1/default,5,0", "HOME=/h"}
	env := LauncherEnv(base, dir, "leader-0001", "/l/shim.log", true)
	get := func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return ""
	}
	if got := get("PATH"); got != BinDir(dir)+string(os.PathListSeparator)+"/usr/bin:/bin" {
		t.Errorf("PATH = %q", got)
	}
	if got := SocketFromTmux(get("TMUX")); got != SocketPath(dir) {
		t.Errorf("TMUX = %q", get("TMUX"))
	}
	if get("TMUX_PANE") != PaneID("leader-0001") || get("HOME") != "/h" {
		t.Errorf("env = %v", env)
	}
	if get(EnvLog) != "/l/shim.log" || get(EnvLogAll) != "1" {
		t.Errorf("log env = %v", env)
	}
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("TMUX set %d times", n)
	}
	// A shell started under the shim that starts it again keeps one copy of
	// the bin directory on PATH.
	again := LauncherEnv(env, dir, "leader-0001", "", false)
	for _, kv := range again {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok && strings.Count(v, BinDir(dir)) != 1 {
			t.Errorf("PATH after a second launch = %q", v)
		}
	}
}

func TestFindRealTmux(t *testing.T) {
	root := t.TempDir()
	self := filepath.Join(root, "tuios")
	shimDir := filepath.Join(root, "shim")
	other := filepath.Join(root, "other")
	realDir := filepath.Join(root, "real")
	for _, d := range []string{BinDir(shimDir), other, realDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "tmux"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(BinDir(shimDir), "tmux")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(self, filepath.Join(other, "tmux")); err != nil {
		t.Fatal(err)
	}
	path := strings.Join([]string{BinDir(shimDir), other, realDir}, string(os.PathListSeparator))
	got, err := FindRealTmux(path, shimDir, self)
	if err != nil || got != filepath.Join(realDir, "tmux") {
		t.Errorf("FindRealTmux = %q %v, want the real one", got, err)
	}
	if _, err := FindRealTmux(strings.Join([]string{BinDir(shimDir), other}, string(os.PathListSeparator)), shimDir, self); err == nil {
		t.Error("found a tmux where only links to tuios are")
	}
}

func TestPaneIDsAreStable(t *testing.T) {
	a, b := PaneID("6f1c2e"), PaneID("6f1c2e")
	if a != b || !strings.HasPrefix(a, "%") {
		t.Fatalf("PaneID = %q then %q", a, b)
	}
	if PaneID("6f1c2e") == PaneID("6f1c2f") {
		t.Error("neighbouring ids share a pane id")
	}
	h := newHarness(t)
	for _, target := range []string{PaneID("leader-0001"), "%leader-0001", "%lead", "leader-0001", "work:1.0", ":1.0", "@1.0", "1"} {
		code, out := h.run("display-message", "-t", target, "-p", "#{tuios_window_id}")
		if code != 0 || out != "leader-0001\n" {
			t.Errorf("target %q = %d %q %q", target, code, out, h.err)
		}
	}
	for _, target := range []string{"%99", "work:7.0", "@1.5"} {
		if code, _ := h.run("display-message", "-t", target, "-p", "x"); code == 0 {
			t.Errorf("target %q resolved", target)
		}
	}
}

func TestIgnoredCommandsAcceptAnyArguments(t *testing.T) {
	h := newHarness(t)
	for _, name := range append(slices.Clone(ignoredCommands), "set", "setw", "selectl", "resizep") {
		if code, _ := h.run(name, "-Q", "--weird", "x"); code != 0 {
			t.Errorf("%s failed: %s", name, h.err)
		}
	}
	if len(h.fake.calls) != 0 {
		t.Errorf("ignored commands called %v", h.fake.verbs())
	}
}
