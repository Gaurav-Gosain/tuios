package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests hold --server and TUIOS_SERVER to what they promise: a named
// server is a daemon of its own, with its own socket and its own saved state,
// and a tuios run without a name reaches the default daemon exactly as it
// always has.
//
// Negative controls (NEGATIVE_CONTROLS.md, "tuios --server"):
//   - the Apply call cut from the root command's PersistentPreRunE: --server
//     reaches the default daemon, and every isolation assertion fails.
//   - the TUIOS_SERVER Apply cut from main: the env-named server lands on the
//     default daemon, and a pane's tuios lists the default daemon's sessions.
//   - sockpath.Dir returning BaseDir: every server shares one socket.

// serverCLI runs tuios under base on the named server, by flag.
func serverCLI(t *testing.T, base, server string, args ...string) (string, error) {
	t.Helper()
	return tuiosCLIInDir(t, base, workDirIn(t, base), nil, append([]string{"--server", server}, args...)...)
}

// envServerCLI runs tuios under base on the named server, by TUIOS_SERVER.
func envServerCLI(t *testing.T, base, server string, args ...string) (string, error) {
	t.Helper()
	return tuiosCLIInDir(t, base, workDirIn(t, base), []string{"TUIOS_SERVER=" + server}, args...)
}

// stopServerAtEnd stops a named server's daemon when the test ends, as
// killDaemon does for the default one.
func stopServerAtEnd(t *testing.T, base, server string) {
	t.Helper()
	t.Cleanup(func() {
		if out, err := serverCLI(t, base, server, "kill-server"); err != nil {
			t.Logf("kill-server --server %s (best effort): %v: %s", server, err, strings.TrimSpace(out))
		}
	})
}

// serverRow is a row of 'tuios ls --json' with the server it names.
type serverRow struct {
	Name   string `json:"name"`
	Saved  bool   `json:"saved"`
	Server string `json:"server"`
}

// listOn runs 'ls --json' on a server ("" for the default one, run with no
// name at all) and returns the raw output, the rows and the exit status.
func listOn(t *testing.T, base, server string) (string, []serverRow, int) {
	t.Helper()
	var out string
	var err error
	if server == "" {
		out, err = tuiosCLI(t, base, "ls", "--json")
	} else {
		out, err = serverCLI(t, base, server, "ls", "--json")
	}
	var rows []serverRow
	if jsonErr := json.Unmarshal([]byte(out), &rows); jsonErr != nil {
		t.Fatalf("ls --json on server %q is not JSON: %v\n%s", server, jsonErr, out)
	}
	return out, rows, exitCode(t, err)
}

func rowNames(rows []serverRow) []string {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return names
}

// waitForFileWith polls until path exists and holds want, and returns what it
// last read.
func waitForFileWith(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), want) || !time.Now().Before(deadline) {
			return string(data)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// envOf parses `env` output into a map.
func envOf(dump string) map[string]string {
	vars := map[string]string{}
	for line := range strings.SplitSeq(dump, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			vars[k] = v
		}
	}
	return vars
}

func mustNew(t *testing.T, what string, out string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("create %s: %v: %s", what, err, out)
	}
}

// TestNamedServersAreIsolated runs the default daemon and two named ones side
// by side. Each lists only its own session, keeps its socket and its state in
// folders of its own, and the default one shows nothing of the others.
func TestNamedServersAreIsolated(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	stopServerAtEnd(t, base, "foo")
	stopServerAtEnd(t, base, "bar")

	out, err := tuiosCLI(t, base, "new", "s-def", "--detach")
	mustNew(t, "the default server's session", out, err)
	out, err = serverCLI(t, base, "foo", "new", "s-foo", "--detach")
	mustNew(t, "foo's session", out, err)
	out, err = envServerCLI(t, base, "bar", "new", "s-bar", "--detach")
	mustNew(t, "bar's session", out, err)

	// Each server lists its own session and only that one.
	for _, c := range []struct{ server, session string }{{"", "s-def"}, {"foo", "s-foo"}, {"bar", "s-bar"}} {
		raw, rows, code := listOn(t, base, c.server)
		if code != 0 {
			t.Errorf("ASSERTION: ls on server %q exited %d, want 0 from a running daemon", c.server, code)
		}
		if got := rowNames(rows); !slices.Equal(got, []string{c.session}) {
			t.Errorf("ASSERTION: ls on server %q listed %v, want only %s", c.server, got, c.session)
		}
		for _, r := range rows {
			if r.Server != c.server {
				t.Errorf("ASSERTION: ls on server %q gave a row with server %q", c.server, r.Server)
			}
		}
		// The default listing carries no server field at all, so a script
		// reading it sees the shape it always did.
		if c.server == "" && strings.Contains(raw, `"server"`) {
			t.Errorf("ASSERTION: the default server's ls --json has a server field:\n%s", raw)
		}
	}
	// TUIOS_SERVER reaches the same daemon the flag does.
	out, _ = envServerCLI(t, base, "foo", "ls", "--json")
	var envRows []serverRow
	_ = json.Unmarshal([]byte(out), &envRows)
	if !slices.Equal(rowNames(envRows), []string{"s-foo"}) {
		t.Errorf("ASSERTION: TUIOS_SERVER=foo ls listed %v, want s-foo:\n%s", rowNames(envRows), out)
	}

	// The tmux front takes no flags of its own, so cobra hands it the root's:
	// --server, in either form, must still choose the daemon.
	for _, args := range [][]string{{"--server", "foo"}, {"--server=foo"}} {
		out, err = tuiosCLIInDir(t, base, workDirIn(t, base), nil, append(args, "tmux", "list-sessions")...)
		if err != nil || !strings.Contains(out, "s-foo") || strings.Contains(out, "s-def") {
			t.Errorf("ASSERTION: tuios %s tmux list-sessions did not list only foo's session (err %v):\n%s", strings.Join(args, " "), err, out)
		}
	}

	// The plain listing names a named server and says nothing for the default.
	out, err = serverCLI(t, base, "foo", "ls")
	if err != nil || !strings.Contains(out, "Server: foo (") {
		t.Errorf("ASSERTION: ls --server foo did not name the server (err %v):\n%s", err, out)
	}
	t.Logf("ls --server foo:\n%s", out)
	out, err = tuiosCLI(t, base, "ls")
	if err != nil || strings.Contains(out, "Server:") {
		t.Errorf("ASSERTION: the default server's ls changed (err %v):\n%s", err, out)
	}

	// Three sockets, one per daemon.
	runtimeDir := xdgDir(base, "XDG_RUNTIME_DIR")
	for _, sock := range []string{
		filepath.Join(runtimeDir, "tuios", "tuios.sock"),
		filepath.Join(runtimeDir, "tuios", "servers", "foo", "tuios.sock"),
		filepath.Join(runtimeDir, "tuios", "servers", "bar", "tuios.sock"),
	} {
		if st, err := os.Stat(sock); err != nil || st.Mode()&os.ModeSocket == 0 {
			t.Errorf("ASSERTION: no daemon socket at %s: %v", sock, err)
		}
	}

	// A pane of a named server carries the name, so a tuios run in it reaches
	// its own server, and its XDG folders are the ones the daemon was started
	// with: the scope never moves the directories other programs read.
	dump := filepath.Join(base, "foo-pane.env")
	listed := filepath.Join(base, "foo-pane.ls")
	if out, err := serverCLI(t, base, "foo", "send-text", "-s", "s-foo",
		"env > "+dump+"; "+tuiosBin+" ls --json > "+listed+".tmp && mv "+listed+".tmp "+listed+"\n"); err != nil {
		t.Fatalf("send-text to s-foo: %v: %s", err, out)
	}
	env := envOf(waitForFileWith(t, dump, "TUIOS_SERVER="))
	if env["TUIOS_SERVER"] != "foo" {
		t.Errorf("ASSERTION: foo's pane has TUIOS_SERVER=%q, want foo", env["TUIOS_SERVER"])
	}
	for _, key := range []string{"XDG_RUNTIME_DIR", "XDG_STATE_HOME"} {
		if want := xdgDir(base, key); env[key] != want {
			t.Errorf("ASSERTION: foo's pane has %s=%q, want the unscoped %q", key, env[key], want)
		}
	}
	var paneRows []serverRow
	if err := json.Unmarshal([]byte(waitForFileWith(t, listed, "s-foo")), &paneRows); err != nil {
		t.Errorf("ASSERTION: tuios ls in foo's pane printed no JSON: %v", err)
	}
	if got := rowNames(paneRows); !slices.Equal(got, []string{"s-foo"}) {
		t.Errorf("ASSERTION: tuios ls in foo's pane listed %v, want foo's s-foo", got)
	}

	// The positive half: the default server's pane has no name at all.
	defDump := filepath.Join(base, "def-pane.env")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "s-def", "env > "+defDump+".tmp && mv "+defDump+".tmp "+defDump+"\n"); err != nil {
		t.Fatalf("send-text to s-def: %v: %s", err, out)
	}
	defEnv := envOf(waitForFileWith(t, defDump, "XDG_STATE_HOME="))
	if _, ok := defEnv["TUIOS_SERVER"]; ok {
		t.Errorf("ASSERTION: the default server's pane has TUIOS_SERVER=%q, want it unset", defEnv["TUIOS_SERVER"])
	}
	if len(defEnv) == 0 {
		t.Errorf("ASSERTION: the default server's pane never wrote its environment")
	}

	// Each server keeps its state apart: its own daemon log, and its own saved
	// session once kill-server has written it.
	for _, server := range []string{"foo", "bar"} {
		if out, err := serverCLI(t, base, server, "kill-server"); err != nil {
			t.Fatalf("kill-server --server %s: %v: %s", server, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	stateDir := xdgDir(base, "XDG_STATE_HOME")
	for _, c := range []struct{ dir, session string }{
		{filepath.Join(stateDir, "tuios"), "s-def"},
		{filepath.Join(stateDir, "tuios-servers", "foo", "tuios"), "s-foo"},
		{filepath.Join(stateDir, "tuios-servers", "bar", "tuios"), "s-bar"},
	} {
		for _, file := range []string{"daemon.log", filepath.Join("sessions", c.session+".json")} {
			if _, err := os.Stat(filepath.Join(c.dir, file)); err != nil {
				t.Errorf("ASSERTION: %s: %v", filepath.Join(c.dir, file), err)
			}
		}
		entries, _ := os.ReadDir(filepath.Join(c.dir, "sessions"))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") && e.Name() != c.session+".json" {
				t.Errorf("ASSERTION: %s holds another server's session %s", c.dir, e.Name())
			}
		}
	}
}

// TestKillServerStopsOnlyTheNamedDaemon stops one named server while the
// default daemon and another named one keep running.
func TestKillServerStopsOnlyTheNamedDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	stopServerAtEnd(t, base, "foo")
	stopServerAtEnd(t, base, "bar")

	out, err := tuiosCLI(t, base, "new", "s-def", "--detach")
	mustNew(t, "the default server's session", out, err)
	out, err = serverCLI(t, base, "foo", "new", "s-foo", "--detach")
	mustNew(t, "foo's session", out, err)
	out, err = serverCLI(t, base, "bar", "new", "s-bar", "--detach")
	mustNew(t, "bar's session", out, err)

	out, err = serverCLI(t, base, "foo", "kill-server")
	if err != nil {
		t.Fatalf("kill-server --server foo: %v: %s", err, out)
	}
	if !strings.Contains(out, "Server: foo (") {
		t.Errorf("ASSERTION: kill-server --server foo did not name the server it stopped:\n%s", out)
	}

	_, rows, code := listOn(t, base, "foo")
	if code != noDaemonExit {
		t.Errorf("ASSERTION: foo still answers after its kill-server: ls exited %d, want %d", code, noDaemonExit)
	}
	if len(rows) != 1 || rows[0].Name != "s-foo" || !rows[0].Saved {
		t.Errorf("ASSERTION: foo's session was not saved by its kill-server: %+v", rows)
	}
	for _, c := range []struct{ server, session string }{{"", "s-def"}, {"bar", "s-bar"}} {
		_, rows, code := listOn(t, base, c.server)
		if code != 0 || !slices.Equal(rowNames(rows), []string{c.session}) || rows[0].Saved {
			t.Errorf("ASSERTION: server %q was touched by foo's kill-server: exit %d, rows %+v", c.server, code, rows)
		}
	}

	// The default kill-server says nothing about servers and stops only the
	// default daemon.
	out, err = tuiosCLI(t, base, "kill-server")
	if err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	if strings.Contains(out, "Server:") {
		t.Errorf("ASSERTION: the default kill-server output changed:\n%s", out)
	}
	if _, rows, code := listOn(t, base, "bar"); code != 0 || !slices.Equal(rowNames(rows), []string{"s-bar"}) {
		t.Errorf("ASSERTION: bar was stopped by the default kill-server: exit %d, rows %+v", code, rows)
	}
}

// TestServerNamesAreChecked refuses a name that is not a plain folder name,
// before anything is created, and accepts the plain one beside it.
func TestServerNamesAreChecked(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	servers := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "servers")

	for _, bad := range []string{"../escape", "a/b", "has space", strings.Repeat("x", 25)} {
		if out, err := serverCLI(t, base, bad, "ls"); err == nil || !strings.Contains(out, "server name") {
			t.Errorf("ASSERTION: --server %q was not refused (err %v):\n%s", bad, err, out)
		}
		if out, err := envServerCLI(t, base, bad, "ls"); err == nil || !strings.Contains(out, "TUIOS_SERVER") {
			t.Errorf("ASSERTION: TUIOS_SERVER=%q was not refused (err %v):\n%s", bad, err, out)
		} else if !strings.Contains(out, "Unset TUIOS_SERVER or set it to a valid name") {
			t.Errorf("ASSERTION: the refusal of TUIOS_SERVER=%q does not say what to do:\n%s", bad, out)
		}
	}
	if _, err := os.Stat(servers); !os.IsNotExist(err) {
		t.Errorf("ASSERTION: a refused name left %s behind: %v", servers, err)
	}

	// The positive half: a plain name gets its folder.
	if _, _, code := listOn(t, base, "ok_name-1"); code != noDaemonExit {
		t.Errorf("ASSERTION: ls --server ok_name-1 exited %d, want %d with no daemon", code, noDaemonExit)
	}
	if _, err := os.Stat(filepath.Join(servers, "ok_name-1")); err != nil {
		t.Errorf("ASSERTION: a plain name did not get its socket folder: %v", err)
	}
	// With no daemon, the hint names a command that reaches this server, not
	// the default one.
	out, _ := serverCLI(t, base, "ok_name-1", "ls")
	if !strings.Contains(out, "'tuios --server ok_name-1 new'") || strings.Contains(out, "'tuios new'") {
		t.Errorf("ASSERTION: the no-daemon hint on server ok_name-1 does not carry --server:\n%s", out)
	}
}

// TestTuiosWebServesTheNamedServer: tuios-web started with TUIOS_SERVER set,
// as it is from a pane of a named server, serves that server's daemon and not
// the default one. tuios-web runs its daemon itself, so nothing else applies
// the name for it.
//
// The ways this can fail, written down first:
//   - tuios-web ignores TUIOS_SERVER and starts the default daemon, whose
//     panes would still inherit TUIOS_SERVER and send a tuios run in them to
//     another daemon;
//   - it starts both.
func TestTuiosWebServesTheNamedServer(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	stopServerAtEnd(t, base, "web1")
	startTuiosWebEnv(t, base, []string{"TUIOS_SERVER=web1"})

	runtimeDir := xdgDir(base, "XDG_RUNTIME_DIR")
	named := filepath.Join(runtimeDir, "tuios", "servers", "web1", "tuios.sock")
	if err := waitForPath(named, bootTimeout); err != nil {
		t.Errorf("ASSERTION: tuios-web with TUIOS_SERVER=web1 started no daemon at %s: %v", named, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "tuios", "tuios.sock")); err == nil {
		t.Errorf("ASSERTION: tuios-web with TUIOS_SERVER=web1 started the default daemon")
	}
}

// waitForPath waits for path to exist.
func waitForPath(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := os.Stat(path)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestANamedServerOverSSHReachesTheSameDaemon is the /run/user step for a
// named server. A command over ssh, with no XDG_RUNTIME_DIR, reaches the
// named server the desktop started, in /run/user/<uid>/tuios/servers/<name>.
// It never takes a daemon in /tmp/tuios-<uid> for it: no older tuios started
// a named server, so the /tmp fallback is the default server's alone.
//
// The ways this can fail, written down first:
//   - the named folder is built on the /tmp folder rather than on the one
//     the /run/user step found, so ssh starts a second foo;
//   - the /tmp fallback looks at the default server's folder for a named
//     server, so ssh foo lands on a /tmp daemon of another server;
//   - the fallback takes a named server's daemon in /tmp, so ssh bar joins
//     a bar no current tuios would start there.
func TestANamedServerOverSSHReachesTheSameDaemon(t *testing.T) {
	m := newOneMachine(t)
	t.Cleanup(func() {
		for _, env := range [][]string{m.desktopEnv(), m.oldLinkEnv()} {
			for _, name := range []string{"foo", "bar"} {
				_, _ = m.cli(t, env, "--server", name, "kill-server")
			}
		}
	})
	uid := strconv.Itoa(os.Getuid())

	m.mustCLI(t, m.desktopEnv(), "--server", "foo", "new", "fdesk", "--detach")
	if out, _ := m.cli(t, m.sshEnv(), "--server", "foo", "ls"); !strings.Contains(out, "fdesk") {
		t.Errorf("ASSERTION: without XDG_RUNTIME_DIR, ls --server foo does not list the desktop's foo session:\n%s", out)
	}
	m.mustCLI(t, m.sshEnv(), "--server", "foo", "new", "fssh", "--detach")
	if out, _ := m.cli(t, m.desktopEnv(), "--server", "foo", "ls"); !strings.Contains(out, "fssh") {
		t.Errorf("ASSERTION: the foo session made over ssh is not on the desktop's foo daemon:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(m.sys, "tuios", "servers", "foo", "tuios.sock")); err != nil {
		t.Errorf("ASSERTION: foo's socket is not in the runtime folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.tmp, "tuios-"+uid, "servers", "foo", "tuios.sock")); err == nil {
		t.Errorf("ASSERTION: a second foo daemon started in /tmp")
	}

	// A default daemon in /tmp, as an older tuios left one, is not foo's.
	m.mustCLI(t, m.oldLinkEnv(), "new", "old", "--detach")
	if out, _ := m.cli(t, m.sshEnv(), "--server", "foo", "ls"); strings.Contains(out, "old") {
		t.Errorf("ASSERTION: ls --server foo over ssh listed the default /tmp daemon's session:\n%s", out)
	}

	// A named daemon in /tmp is not joined either: ssh starts bar in the
	// runtime folder.
	m.mustCLI(t, m.oldLinkEnv(), "--server", "bar", "new", "oldbar", "--detach")
	m.mustCLI(t, m.sshEnv(), "--server", "bar", "new", "sshbar", "--detach")
	if out, _ := m.cli(t, m.sshEnv(), "--server", "bar", "ls"); strings.Contains(out, "oldbar") || !strings.Contains(out, "sshbar") {
		t.Errorf("ASSERTION: ls --server bar over ssh should list only sshbar:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(m.sys, "tuios", "servers", "bar", "tuios.sock")); err != nil {
		t.Errorf("ASSERTION: bar's ssh daemon is not in the runtime folder: %v", err)
	}
}
