package federation

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProbeScriptIsOneSafeLine pins what lets the script cross any login
// shell: it is one line, and it holds none of the characters a shell could
// read inside single quotes. It also has to parse, in sh and in dash, which
// is the shell most remote machines run it in.
func TestProbeScriptIsOneSafeLine(t *testing.T) {
	for _, announce := range []bool{true, false} {
		script := remoteProbeScript(announce)
		for _, bad := range []string{"'", `\`, "\n", "!"} {
			if strings.Contains(script, bad) {
				t.Errorf("ASSERTION: the probe script contains %q, which a remote login shell could read inside single quotes:\n%s", bad, script)
			}
		}
		for _, c := range remoteBinaryCandidates {
			if !strings.Contains(script, `"`+c+`"`) {
				t.Errorf("ASSERTION: the probe script does not test %s", c)
			}
		}
		for _, sh := range []string{"sh", "dash", "bash"} {
			if _, err := exec.LookPath(sh); err != nil {
				continue
			}
			out, err := exec.Command(sh, "-n", "-c", script).CombinedOutput() //nolint:gosec // the script under test, parsed only
			if err != nil {
				t.Errorf("ASSERTION: %s does not parse the probe script: %v\n%s", sh, err, out)
			}
		}
	}
}

func waitReport(t *testing.T, m *Manager, want func(HostReport) bool, why string) HostReport {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var r HostReport
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r = m.Reports(ctx)[0]
		cancel()
		if want(r) {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s. Last report: %s / %s / %s / command %q", why, r.Status, r.Reason, r.Detail, r.Command)
	return r
}

func waitStatus(t *testing.T, m *Manager, want Status) HostReport {
	t.Helper()
	return waitReport(t, m, func(r HostReport) bool { return r.Status == want },
		"the host never reached "+string(want))
}

// TestLinkFindsTuiosAtAKnownPathAndRedialsWithIt is the failure this exists
// for: tuios is in ~/.local/bin, where the install script puts it, and that is
// not on the PATH ssh gives a command. The link has to find it, say so, and
// run it directly on the next dial rather than probe again.
func TestLinkFindsTuiosAtAKnownPathAndRedialsWithIt(t *testing.T) {
	far := newFarMachine(t)

	hang := make(chan struct{})
	far.stub = startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "probe-1", Sessions: 1}, nil
		}
		<-hang
		return nil, &RemoteError{Code: "internal", Message: "test over"}
	})
	t.Cleanup(func() { close(hang) })
	installed := far.install(".local/bin/tuios")

	dial, commands := recordingDialer(SSHDialer(far.ssh))
	opts := testOptions(dial)
	opts.CallTimeout = 400 * time.Millisecond
	m := managerFor(t, opts, Host{Name: "build", Addr: "someone@buildbox"})

	r := waitStatus(t, m, StatusUp)
	if r.Command != installed {
		t.Fatalf("ASSERTION: the link reports running %q, want the tuios it found at %s", r.Command, installed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range maxControlFailures {
		if _, err := m.Call(ctx, "build", "list-sessions", nil); err == nil {
			t.Fatal("the call against the hanging stub succeeded; the redial cannot be forced")
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for len(commands()) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	waitStatus(t, m, StatusUp)
	got := commands()
	if len(got) < 2 {
		t.Fatalf("ASSERTION: the link never redialed: %d dial(s)", len(got))
	}
	if got[0] != "" {
		t.Errorf("ASSERTION: the first dial ran %q rather than probing", got[0])
	}
	if got[1] != installed {
		t.Errorf("ASSERTION: the redial ran %q, want the cached %s", got[1], installed)
	}
}

// TestLinkPrefersTuiosOnThePath pins the first step of the order: a tuios the
// non-interactive shell finds by name wins over every known path, because it
// is what already worked, and the working case must not change.
func TestLinkPrefersTuiosOnThePath(t *testing.T) {
	far := newFarMachine(t)
	onPath := far.install("onpath/tuios")
	far.install(".local/bin/tuios")
	far.addToPath(filepath.Dir(onPath))

	m := managerFor(t, testOptions(SSHDialer(far.ssh)), Host{Name: "build", Addr: "someone@buildbox"})
	r := waitStatus(t, m, StatusUp)
	if r.Command != onPath {
		t.Fatalf("ASSERTION: the link runs %q, want the tuios on the PATH at %s ahead of the known paths", r.Command, onPath)
	}
}

// TestLinkPrefersAKnownPathOverTheLoginShell pins the second step: a tuios at
// a known install path wins over what the login shell would find, so the
// profile is only ever run on a machine where nothing else worked.
func TestLinkPrefersAKnownPathOverTheLoginShell(t *testing.T) {
	far := newFarMachine(t)
	known := far.install(".local/bin/tuios")
	far.install("tools/tuios")
	profile := "PATH=$HOME/tools:$PATH\nexport PATH\n"
	if err := os.WriteFile(filepath.Join(far.home, ".profile"), []byte(profile), 0o600); err != nil {
		t.Fatalf("write .profile: %v", err)
	}

	m := managerFor(t, testOptions(SSHDialer(far.ssh)), Host{Name: "build", Addr: "someone@buildbox"})
	r := waitStatus(t, m, StatusUp)
	if r.Command != known {
		t.Fatalf("ASSERTION: the link runs %q, want the known path %s ahead of what the login shell names", r.Command, known)
	}
}

// TestLinkFindsTuiosThroughTheLoginShell is the last resort: tuios is nowhere
// the fixed list looks, and only the person's profile knows where it is.
func TestLinkFindsTuiosThroughTheLoginShell(t *testing.T) {
	skipIfTuiosIsInstalledSystemWide(t)
	far := newFarMachine(t)
	installed := far.install("tools/tuios")
	profile := "PATH=$HOME/tools:$PATH\nexport PATH\n"
	if err := os.WriteFile(filepath.Join(far.home, ".profile"), []byte(profile), 0o600); err != nil {
		t.Fatalf("write .profile: %v", err)
	}

	m := managerFor(t, testOptions(SSHDialer(far.ssh)), Host{Name: "build", Addr: "someone@buildbox"})
	r := waitStatus(t, m, StatusUp)
	if r.Command != installed {
		t.Fatalf("ASSERTION: the link reports running %q, want %s, which only the login shell's PATH names", r.Command, installed)
	}
}

// TestConfiguredCommandSkipsTheProbe keeps the override an override: with a
// command configured, nothing is looked for, and the report names the command
// as configured.
func TestConfiguredCommandSkipsTheProbe(t *testing.T) {
	far := newFarMachine(t)
	configured := far.install("elsewhere/my-tuios")
	far.install(".local/bin/tuios")

	dial, commands := recordingDialer(SSHDialer(far.ssh))
	m := managerFor(t, testOptions(dial), Host{Name: "build", Addr: "someone@buildbox", Command: configured})
	r := waitStatus(t, m, StatusUp)
	if r.Command != configured {
		t.Errorf("ASSERTION: the link reports running %q, want the configured %s", r.Command, configured)
	}
	if got := commands(); len(got) == 0 || got[0] != configured {
		t.Errorf("ASSERTION: the dial was not asked to run the configured command: %q", got)
	}
}

// TestStalePathIsProbedAgain is the cache going wrong: the binary the link
// found has moved. The dial that used the old path reaches the machine and
// runs nothing, so the next dial probes again and finds the new place.
func TestStalePathIsProbedAgain(t *testing.T) {
	far := newFarMachine(t)
	old := far.install(".local/bin/tuios")
	dial, commands := recordingDialer(SSHDialer(far.ssh))
	opts := testOptions(dial)
	m := managerFor(t, opts, Host{Name: "build", Addr: "someone@buildbox"})
	if r := waitStatus(t, m, StatusUp); r.Command != old {
		t.Fatalf("the link reports %q, want %s", r.Command, old)
	}

	moved := far.install("go/bin/tuios")
	if err := os.Remove(old); err != nil {
		t.Fatalf("remove: %v", err)
	}
	tearDownLink(t, m, "build")

	r := waitCommand(t, m, moved)
	if r.Status != StatusUp {
		t.Fatalf("ASSERTION: the link did not come back up after the binary moved: %s / %s", r.Status, r.Reason)
	}
	got := commands()
	if len(got) < 3 || got[1] != old || got[2] != "" {
		t.Errorf("ASSERTION: the dials after the binary moved were %q, want the stale %s and then a fresh probe", got, old)
	}
}

// TestKnownPathSurvivesAnUnreachableMachine is the cache going right: the
// machine is asleep for a while. ssh's own failure says nothing about the
// binary, so the path is kept and the dial that reaches the machine again
// runs it directly.
func TestKnownPathSurvivesAnUnreachableMachine(t *testing.T) {
	far := newFarMachine(t)
	installed := far.install(".local/bin/tuios")
	dial, commands := recordingDialer(SSHDialer(far.ssh))
	m := managerFor(t, testOptions(dial), Host{Name: "build", Addr: "someone@buildbox"})
	waitStatus(t, m, StatusUp)

	if err := os.WriteFile(far.down, nil, 0o600); err != nil {
		t.Fatalf("mark the machine down: %v", err)
	}
	tearDownLink(t, m, "build")
	waitReport(t, m, func(r HostReport) bool {
		return r.Status == StatusUnreachable && strings.Contains(r.Detail, "No route to host")
	}, "the dial against the down machine never reported ssh's refusal")
	if err := os.Remove(far.down); err != nil {
		t.Fatalf("wake the machine: %v", err)
	}
	waitStatus(t, m, StatusUp)

	got := commands()
	if len(got) < 3 {
		t.Fatalf("ASSERTION: only %d dial(s) were made: %q", len(got), got)
	}
	for _, c := range got[1:] {
		if c != installed {
			t.Errorf("ASSERTION: a dial after the path was known ran %q rather than the known %s: all dials %q", c, installed, got)
		}
	}
}

func newFarMachine(t *testing.T) *farMachine {
	t.Helper()
	base := t.TempDir()
	f := &farMachine{
		t:         t,
		home:      filepath.Join(base, "home"),
		down:      filepath.Join(base, "down"),
		extraPath: filepath.Join(base, "extra-path"),
		ssh:       filepath.Join(base, "fake-ssh"),
		stub:      startStubDaemon(t, helloOK("probe-1", 1)),
	}
	empty := filepath.Join(base, "sh-only-path")
	for _, dir := range []string{f.home, empty} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.Symlink("/bin/sh", filepath.Join(empty, "sh")); err != nil {
		t.Fatalf("symlink sh: %v", err)
	}
	script := "#!/bin/sh\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in -o) shift 2 ;; -T|-t) shift ;; --) shift; break ;; *) break ;; esac; done\n" +
		"shift\n" +
		"if [ -e " + f.down + " ]; then echo 'ssh: connect to host buildbox port 22: No route to host' >&2; exit 255; fi\n" +
		"extra=; if [ -f " + f.extraPath + " ]; then extra=$(cat " + f.extraPath + "):; fi\n" +
		"export HOME=" + f.home + " PATH=$extra" + empty + " SHELL=/bin/sh\n" +
		"exec /bin/sh -c \"$*\"\n"
	if err := os.WriteFile(f.ssh, []byte(script), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return f
}

type farMachine struct {
	t         *testing.T
	home      string
	down      string
	extraPath string
	ssh       string
	stub      *stubDaemon
}

func (f *farMachine) install(rel string) string {
	f.t.Helper()
	self, err := os.Executable()
	if err != nil {
		f.t.Skipf("no test executable path: %v", err)
	}
	path := filepath.Join(f.home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatalf("mkdir: %v", err)
	}
	script := "#!/bin/sh\nexec " + self + " -test.run=TestHelperStdioProxy -fed.proxysock=" + f.stub.path + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // the tuios stand-in this test runs
		f.t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

func (f *farMachine) addToPath(dir string) {
	f.t.Helper()
	if err := os.WriteFile(f.extraPath, []byte(dir), 0o600); err != nil {
		f.t.Fatalf("write the extra PATH entry: %v", err)
	}
}

func recordingDialer(inner Dialer) (Dialer, func() []string) {
	var mu sync.Mutex
	var commands []string
	dial := func(ctx context.Context, h Host) (Transport, error) {
		mu.Lock()
		commands = append(commands, h.Command)
		mu.Unlock()
		return inner(ctx, h)
	}
	return dial, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), commands...)
	}
}

func skipIfTuiosIsInstalledSystemWide(t *testing.T) {
	t.Helper()
	for _, c := range remoteBinaryCandidates {
		if strings.HasPrefix(c, "$") {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			t.Skipf("tuios is installed at %s on this machine, so the probe would find it", c)
		}
	}
}

func tearDownLink(t *testing.T, m *Manager, name string) {
	t.Helper()
	l := m.link(name)
	if l == nil {
		t.Fatalf("no link for %s", name)
	}
	l.mu.Lock()
	down := l.tearDown
	l.mu.Unlock()
	if down == nil {
		t.Fatalf("the link to %s is not up", name)
	}
	down()
}

func waitCommand(t *testing.T, m *Manager, want string) HostReport {
	t.Helper()
	return waitReport(t, m, func(r HostReport) bool { return r.Status == StatusUp && r.Command == want },
		"the host never reported running "+want)
}
