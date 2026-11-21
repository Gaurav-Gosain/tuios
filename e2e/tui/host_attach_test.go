package tuie2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

func writeFakeSSHTo(t *testing.T, dir, remoteBase string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh-remote")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("while [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    -t) shift ;;\n    --) shift; break ;;\n    *) break ;;\n  esac\ndone\n")
	b.WriteString("shift\n")
	for _, key := range xdgKeys {
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(xdgDir(remoteBase, key))
		b.WriteString("\n")
	}
	b.WriteString(recordFakeSSHPid(remoteBase))
	b.WriteString("exec /bin/sh -c \"$*\"\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

func fakeSSHPids(remoteBase string) string {
	return filepath.Join(remoteBase, "fake-ssh.pids")
}

func recordFakeSSHPid(remoteBase string) string {
	return "echo $$ >> " + posixQuote(fakeSSHPids(remoteBase)) + "\n"
}

func remoteMachine(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for _, key := range xdgKeys {
		xdgDir(base, key)
	}
	killDaemon(t, base)
	remotesOfTests.add(t, base)
	return base
}

var remotesOfTests = &remoteRegistry{byTest: map[string][]string{}}

type remoteRegistry struct {
	mu     sync.Mutex
	byTest map[string][]string
}

func topTestName(t *testing.T) string {
	name, _, _ := strings.Cut(t.Name(), "/")
	return name
}

func (r *remoteRegistry) add(t *testing.T, base string) {
	name := topTestName(t)
	r.mu.Lock()
	r.byTest[name] = append(r.byTest[name], base)
	r.mu.Unlock()
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.byTest[name] = slices.DeleteFunc(r.byTest[name], func(b string) bool { return b == base })
		if len(r.byTest[name]) == 0 {
			delete(r.byTest, name)
		}
	})
}

func testProxies(t *testing.T) []int {
	t.Helper()
	remotesOfTests.mu.Lock()
	remotes := slices.Clone(remotesOfTests.byTest[topTestName(t)])
	remotesOfTests.mu.Unlock()
	var pids []int
	for _, remote := range remotes {
		pids = append(pids, farProxies(remote)...)
	}
	return pids
}

func writeOneHostConfig(t *testing.T, base, tuiosPath string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[hosts.build]\n" +
		"addr = \"someone@buildbox\"\n" +
		"command = \"" + tuiosPath + "\"\n" +
		"connect_timeout = 5\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func remoteSessionsListed(t *testing.T, remoteBase string) string {
	t.Helper()
	out, _ := tuiosCLI(t, remoteBase, "ls")
	return out
}

func noNestedClient(t *testing.T, session string) {
	t.Helper()
	for _, line := range commandLinesContaining(t, tuiosBin) {
		if strings.Contains(line, " attach "+session) && !strings.Contains(line, "--host") {
			t.Fatalf("ASSERTION: a nested client is running for %s: %s", session, line)
		}
	}
}

// TestAttachOnAHostIsDrawnByThisClient is the frame the whole feature is for:
// a session that exists only on the other machine, on screen in this client.
func TestAttachOnAHostIsDrawnByThisClient(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far-shell"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
	}, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the client never drew the far session: %v\n%s", err, term.Snapshot())
	}
	noNestedClient(t, "far-shell")

	if err := term.SendKeys("echo REMOTE-$((6*7))\r"); err != nil {
		t.Fatalf("type into the far shell: %v", err)
	}
	if err := term.WaitForText("REMOTE-42", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the far shell's output never reached this client: %v\n%s", err, term.Snapshot())
	}

	toggleSidebarViaPalette(t, term)
	railShows(t, term, hostOpen+" build")
	t.Logf("a session on build, drawn by this client:\n%s", term.Snapshot())

	if out, _ := tuiosCLI(t, base, "ls"); strings.Contains(out, "far-shell") {
		t.Fatalf("ASSERTION: this machine's daemon holds far-shell, so the session was not attached across the link:\n%s", out)
	}
	if out := remoteSessionsListed(t, remote); !strings.Contains(out, "far-shell") {
		t.Fatalf("the far daemon lost its session:\n%s", out)
	}
	alive(t, term, "after attaching a session on a host")
}

func railCurrentIs(s tuitest.Screen, want string) bool {
	r := railRowOf(s, want)
	if r < 0 {
		return false
	}
	return strings.HasPrefix(s.Line(r), "▎")
}

func waitRailCurrent(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railCurrentIs(s, want) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never marked %q as the attached session: %v\n%s", want, err, term.Snapshot())
	}
}

func railRows(s tuitest.Screen, wants ...string) map[string]int {
	out := make(map[string]int, len(wants))
	for _, w := range wants {
		out[w] = railRowOf(s, w)
	}
	return out
}

// TestRailAttachesARemoteSessionInThisClient is the on-screen proof for the
// rail: a click on a session under a host switches this client onto it, with
// no pane opened and no nested client, and not one row of the rail moves. The
// machine groups keep their order, this machine first, and the attached
// session is marked current in place under the machine that holds it.
//
// What would pass a weaker test and fail this one: the rail that used to be
// drawn, with build hoisted to the top of the section and this machine's
// sessions pushed down into a group of their own, so the clicked row moved out
// from under the pointer.
//
// Then next_host and prev_host, bound to alt+. and alt+, step between the two
// machines. The step onto build lands on far-shell, the session this client
// last showed there, while another client is attached to far-other there. A
// build that does not remember the session lands on far-other.
func TestRailAttachesARemoteSessionInThisClient(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	cfg := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	body = append(body, "\n[keybindings.window_management]\nnext_host = [\"alt+.\"]\nprev_host = [\"alt+,\"]\n"...)
	if err := os.WriteFile(cfg, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	rows := []string{hostOpen + " local", "home", hostOpen + " build", "far-shell"}
	for _, want := range rows {
		railShows(t, term, want)
	}
	waitRailCurrent(t, term, "home")
	panesBefore := -1
	if wl, err := daemonWindows(base, "home"); err == nil {
		panesBefore = len(wl.Windows)
	}

	before := railRows(term.Screen(), rows...)
	if !(before[rows[0]] < before[rows[1]] && before[rows[1]] < before[rows[2]] && before[rows[2]] < before[rows[3]]) {
		t.Fatalf("ASSERTION: the rail is not laid out local first (%v):\n%s", before, term.Snapshot())
	}
	t.Logf("before the click:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-switch-before")

	mouseClick(t, term, 3, before["far-shell"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "far-shell")
	t.Logf("after the click:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-switch-after")

	after := railRows(term.Screen(), rows...)
	for _, want := range rows {
		if after[want] != before[want] {
			t.Errorf("ASSERTION: the row %q moved from screen row %d to %d on the switch:\n%s",
				want, before[want], after[want], term.Snapshot())
		}
	}
	if railCurrentIs(term.Screen(), "home") {
		t.Errorf("ASSERTION: this machine's session is still marked current after the switch:\n%s", term.Snapshot())
	}
	if railRowOf(term.Screen(), "@ ") >= 0 {
		t.Errorf("ASSERTION: the rail still marks the attached machine with @:\n%s", term.Snapshot())
	}

	if wl, err := daemonWindows(base, "home"); err == nil && len(wl.Windows) != panesBefore {
		t.Fatalf("ASSERTION: the switch opened a pane in the local session: %d windows, had %d", len(wl.Windows), panesBefore)
	}
	noNestedClient(t, "far-shell")

	mouseClick(t, term, 3, after[hostOpen+" build"], tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, hostShut+" build")
		return r >= 0 && railRowOf(s, "far-shell") < 0 && strings.Contains(s.Line(r), "1")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the click on the header did not fold the group: %v\n%s", err, term.Snapshot())
	}
	if railRowOf(term.Screen(), "home") != before["home"] {
		t.Errorf("ASSERTION: folding build moved this machine's rows:\n%s", term.Snapshot())
	}
	t.Logf("with build folded:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-host-folded")

	mouseClick(t, term, 3, after[hostOpen+" build"], tuitest.MouseLeft, 0)
	railShows(t, term, "far-shell")

	mouseClick(t, term, 3, before["home"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "home")
	back := railRows(term.Screen(), rows...)
	for _, want := range rows {
		if back[want] != before[want] {
			t.Errorf("ASSERTION: the row %q is on screen row %d after coming back, was %d:\n%s",
				want, back[want], before[want], term.Snapshot())
		}
	}
	alive(t, term, "after coming back from build")

	if out, err := tuiosCLI(t, remote, "new", "far-other", "--detach"); err != nil {
		t.Fatalf("create far-other: %v\n%s", err, out)
	}
	attachIn(t, remote, "far-other", startOpts{cols: 80, rows: 24})
	if out, err := tuiosCLI(t, remote, "new", "far-mark", "--detach"); err != nil {
		t.Fatalf("create far-mark: %v\n%s", err, out)
	}
	railShows(t, term, "far-mark")

	if err := term.SendKeys(tuitest.Alt('.')); err != nil {
		t.Fatalf("press alt+.: %v", err)
	}
	waitRailCurrent(t, term, "far-shell")
	saveFrame(t, term, "next-host")
	if err := term.SendKeys(tuitest.Alt(',')); err != nil {
		t.Fatalf("press alt+,: %v", err)
	}
	waitRailCurrent(t, term, "home")
	noNestedClient(t, "far-shell")
	alive(t, term, "after stepping between machines")
}
