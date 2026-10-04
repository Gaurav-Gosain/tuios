package tuie2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// tuios hosts sync, against fake hosts.
//
// Each host is a real isolation root with its own HOME and its own daemon,
// reached through an ssh stand-in that routes by address the way the
// federation tests do. Everything on the far side is real: the probe and
// install scripts run in a real sh, the binary is renamed into the host's
// ~/.local/bin, and the daemon is read over a real link. Nothing touches the
// developer's own tuios: every host names its binary with command = in the
// config, so the search the link does without one never runs, and the PATH
// on the far side starts with the host's own ~/.local/bin.
//
// The "new" version is a second build of this checkout stamped
// e2e-sync-next, so it differs from tuiosBin, which the old hosts and their
// daemons run.

const syncNextVersion = "e2e-sync-next"

var (
	syncNextOnce sync.Once
	syncNextBin  string
	syncNextErr  error
)

// syncNextBinary builds the "new" version once per run.
func syncNextBinary(t *testing.T) string {
	t.Helper()
	syncNextOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tuios-e2e-sync-next")
		if err != nil {
			syncNextErr = err
			return
		}
		syncNextBin = filepath.Join(dir, "tuios")
		build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version="+syncNextVersion, "-o", syncNextBin, "./cmd/tuios")
		build.Dir = "../.."
		out, err := build.CombinedOutput()
		if err != nil {
			syncNextErr = err
			t.Logf("build: %s", out)
		}
	})
	if syncNextErr != nil {
		t.Fatalf("build the new version: %v", syncNextErr)
	}
	return syncNextBin
}

// syncHost is one fake host.
type syncHost struct {
	name, base, home, bin string
}

// syncFleet is this machine and its fake hosts.
type syncFleet struct {
	here  string
	env   []string
	hosts map[string]*syncHost
}

// newSyncFleet makes one root per name and an ssh stand-in that reaches each
// at someone@NAMEbox. A name in down has no machine behind it.
func newSyncFleet(t *testing.T, names []string, down []string) *syncFleet {
	t.Helper()
	f := &syncFleet{here: remoteMachine(t), hosts: map[string]*syncHost{}}
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString("while [ $# -gt 0 ]; do case \"$1\" in -o) shift 2 ;; -T|-t) shift ;; --) shift; break ;; *) break ;; esac; done\n")
	script.WriteString("addr=\"$1\"\nshift\ncase \"$addr\" in\n")
	var cfg strings.Builder
	for _, n := range names {
		h := &syncHost{name: n, base: remoteMachine(t)}
		h.home = xdgDir(h.base, "HOME")
		h.bin = filepath.Join(h.home, ".local", "bin", "tuios")
		f.hosts[n] = h
		script.WriteString("  someone@" + n + "box)\n")
		for _, key := range xdgKeys {
			script.WriteString("    export " + key + "=" + xdgDir(h.base, key) + "\n")
		}
		script.WriteString("    export PATH=" + filepath.Dir(h.bin) + ":/usr/bin:/bin SHELL=/bin/sh\n    ;;\n")
	}
	script.WriteString("  *) echo \"ssh: Could not resolve hostname $addr\" >&2; exit 255 ;;\nesac\n")
	script.WriteString("exec /bin/sh -c \"$*\"\n")
	ssh := filepath.Join(f.here, "fake-ssh-sync")
	if err := os.WriteFile(ssh, []byte(script.String()), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	f.env = []string{"TUIOS_SSH=" + ssh}
	for _, n := range append(slices.Clone(names), down...) {
		bin := filepath.Join("/nonexistent", n, "tuios")
		if h, ok := f.hosts[n]; ok {
			bin = h.bin
		}
		cfg.WriteString("[hosts." + n + "]\naddr = \"someone@" + n + "box\"\ncommand = \"" + bin + "\"\nconnect_timeout = 5\n\n")
	}
	dir := filepath.Join(f.here, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return f
}

// install copies bin to the host's ~/.local/bin/tuios.
func (h *syncHost) install(t *testing.T, bin string) {
	t.Helper()
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read %s: %v", bin, err)
	}
	if err := os.MkdirAll(filepath.Dir(h.bin), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(h.bin, data, 0o755); err != nil { //nolint:gosec // a binary the test runs
		t.Fatalf("install on %s: %v", h.name, err)
	}
}

// run runs the host's own tuios with the host's environment.
func (h *syncHost) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, h.base)
	cmd := exec.Command(h.bin, args...)
	cmd.Dir = workDirIn(t, h.base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(h.base, key))
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// sha is the sha256 of the host's binary, or "" when it has none.
func (h *syncHost) sha(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(h.bin)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", h.bin, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// syncResultRow is the part of one JSON row the tests read.
type syncResultRow struct {
	Host          string `json:"host"`
	Before        string `json:"before"`
	After         string `json:"after"`
	InstallPath   string `json:"install_path"`
	Installed     bool   `json:"installed"`
	Action        string `json:"action"`
	RestartNeeded bool   `json:"restart_needed"`
	Error         string `json:"error"`
	Daemon        struct {
		State        string `json:"state"`
		Version      string `json:"version"`
		PID          int    `json:"pid"`
		SessionCount int    `json:"session_count"`
		Restarted    bool   `json:"restarted"`
		Sessions     []struct {
			Name string `json:"name"`
			Busy []struct {
				Program string `json:"program"`
			} `json:"busy"`
		} `json:"sessions"`
	} `json:"daemon"`
}

// syncJSON runs sync with --json and reads the rows by host.
func (f *syncFleet) syncJSON(t *testing.T, args ...string) (map[string]syncResultRow, string, error) {
	t.Helper()
	out, err := tuiosCLIEnv(t, f.here, f.env, append([]string{"hosts", "sync", "--json"}, args...)...)
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("sync printed no JSON: %v\n%s", err, out)
	}
	var rep struct {
		Hosts []syncResultRow `json:"hosts"`
	}
	dec := json.NewDecoder(strings.NewReader(out[start:]))
	if derr := dec.Decode(&rep); derr != nil {
		t.Fatalf("read the JSON: %v\n%s", derr, out)
	}
	rows := map[string]syncResultRow{}
	for _, r := range rep.Hosts {
		rows[r.Host] = r
	}
	return rows, out, err
}

// busyPrograms lists the programs a row says a restart would end.
func busyPrograms(r syncResultRow) []string {
	var busy []string
	for _, s := range r.Daemon.Sessions {
		for _, b := range s.Busy {
			busy = append(busy, b.Program)
		}
	}
	return busy
}

// saveSyncArtifact keeps one run's output for a person to read.
func saveSyncArtifact(t *testing.T, name, out string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(artifactDir(t), name), []byte(out), 0o644); err != nil { //nolint:gosec // a test artifact
		t.Logf("save %s: %v", name, err)
	}
}

// startSyncFleet sets up the four hosts the main test uses:
//
//	fresh  no tuios at all
//	same   the new version, with a daemon of the new version
//	old    the old version, with a daemon holding a session and a busy pane
//	gone   in the config, with no machine behind the address
func startSyncFleet(t *testing.T) (*syncFleet, string) {
	t.Helper()
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"fresh", "same", "old"}, []string{"gone"})
	f.hosts["same"].install(t, next)
	f.hosts["old"].install(t, tuiosBin)
	if out, err := f.hosts["same"].run(t, "new", "work", "--detach"); err != nil {
		t.Fatalf("start the daemon on same: %v\n%s", err, out)
	}
	old := f.hosts["old"]
	if out, err := old.run(t, "new", "work", "--detach"); err != nil {
		t.Fatalf("start the daemon on old: %v\n%s", err, out)
	}
	if out, err := old.run(t, "new-window", "sleeper", "-s", "work", "--", "sleep", "600"); err != nil {
		t.Fatalf("start a program on old: %v\n%s", err, out)
	}
	return f, next
}

// TestHostsSync installs where tuios is missing, leaves a matching host
// alone, updates an old one without touching its daemon, refuses --restart
// without --yes off a terminal, changes nothing on --dry-run, and carries on
// past a host that cannot be reached.
func TestHostsSync(t *testing.T) {
	f, next := startSyncFleet(t)
	fresh, same, old := f.hosts["fresh"], f.hosts["same"], f.hosts["old"]
	sameSHA, oldSHA := same.sha(t), old.sha(t)

	// The dry run: the plan, and no change anywhere. The pane that runs
	// sleep is read from ps, and a pane just made can still be between fork
	// and exec, so the plan is read again until it settles.
	var plan map[string]syncResultRow
	var out string
	var err error
	for deadline := time.Now().Add(20 * time.Second); ; {
		plan, out, err = f.syncJSON(t, "--binary", next, "--dry-run")
		if slices.Equal(busyPrograms(plan["old"]), []string{"sleep"}) || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	saveSyncArtifact(t, "dry-run.json", out)
	if err == nil {
		t.Errorf("ASSERTION: the dry run exited 0 with a host that cannot be reached:\n%s", out)
	}
	if got := plan["fresh"].Action; got != "would install" {
		t.Errorf("ASSERTION: the plan for fresh is %q, want \"would install\"\n%s", got, out)
	}
	if d := plan["fresh"].Daemon; d.State != "stopped" {
		t.Errorf("ASSERTION: the daemon on fresh, which has no tuios, is %q, want \"stopped\"\n%s", d.State, out)
	}
	if got := plan["same"].Action; got != "up to date" {
		t.Errorf("ASSERTION: the plan for same is %q, want \"up to date\"\n%s", got, out)
	}
	if got := plan["old"].Action; !strings.HasPrefix(got, "would update") || !plan["old"].RestartNeeded {
		t.Errorf("ASSERTION: the plan for old is %q (restart needed %v), want an update and a restart needed\n%s", got, plan["old"].RestartNeeded, out)
	}
	if plan["gone"].Error == "" {
		t.Errorf("ASSERTION: the host with no machine behind it has no error\n%s", out)
	}
	oldPID := plan["old"].Daemon.PID
	if oldPID == 0 || plan["old"].Daemon.SessionCount != 1 {
		t.Fatalf("ASSERTION: the daemon on old was not read: %+v\n%s", plan["old"].Daemon, out)
	}
	busy := busyPrograms(plan["old"])
	if !slices.Equal(busy, []string{"sleep"}) {
		t.Errorf("ASSERTION: the busy panes on old are %v, want only the pane that runs sleep\n%s", busy, out)
	}
	if fresh.sha(t) != "" || same.sha(t) != sameSHA || old.sha(t) != oldSHA {
		t.Fatalf("ASSERTION: the dry run changed a binary on a host")
	}

	// --restart off a terminal and without --yes: refused before anything
	// moves, and the refusal names what a restart would end.
	out, err = tuiosCLIEnv(t, f.here, f.env, "hosts", "sync", "--binary", next, "--restart")
	saveSyncArtifact(t, "restart-refused.txt", out)
	if err == nil {
		t.Errorf("ASSERTION: --restart without --yes and without a terminal exited 0:\n%s", out)
	}
	if !strings.Contains(out, "--yes") || !strings.Contains(out, "sleep") {
		t.Errorf("ASSERTION: the refusal does not name --yes and the program a restart ends:\n%s", out)
	}
	if fresh.sha(t) != "" || old.sha(t) != oldSHA {
		t.Fatalf("ASSERTION: the refused restart changed a binary on a host")
	}

	// The real run.
	rows, out, err := f.syncJSON(t, "--binary", next)
	saveSyncArtifact(t, "sync.json", out)
	if err == nil {
		t.Errorf("ASSERTION: sync exited 0 with a host that cannot be reached:\n%s", out)
	}
	if r := rows["fresh"]; !r.Installed || r.After != syncNextVersion || r.InstallPath != fresh.bin {
		t.Errorf("ASSERTION: fresh was not installed at %s: %+v\n%s", fresh.bin, r, out)
	}
	if v, err := fresh.run(t, "--version"); err != nil || !strings.Contains(v, syncNextVersion) {
		t.Errorf("ASSERTION: the binary installed on fresh does not run as the new version: %v\n%s", err, v)
	}
	if r := rows["same"]; r.Installed || r.Action != "up to date" || same.sha(t) != sameSHA {
		t.Errorf("ASSERTION: same was touched: %+v\n%s", r, out)
	}
	r := rows["old"]
	if !r.Installed || r.After != syncNextVersion || r.Daemon.Restarted || !r.RestartNeeded {
		t.Errorf("ASSERTION: old was not updated with its daemon left alone: %+v\n%s", r, out)
	}
	if v, err := old.run(t, "--version"); err != nil || !strings.Contains(v, syncNextVersion) {
		t.Errorf("ASSERTION: the binary on old does not run as the new version: %v\n%s", err, v)
	}
	if rows["gone"].Error == "" {
		t.Errorf("ASSERTION: the host with no machine behind it has no error\n%s", out)
	}
	if entries, _ := os.ReadDir(filepath.Dir(old.bin)); len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("ASSERTION: the install left files beside the binary: %v", names)
	}

	// The daemon on old is the one from before, with its session.
	after, out, _ := f.syncJSON(t, "--binary", next, "--dry-run")
	saveSyncArtifact(t, "after.json", out)
	if d := after["old"].Daemon; d.PID != oldPID || d.Version == syncNextVersion || d.SessionCount != 1 {
		t.Errorf("ASSERTION: the daemon on old changed without --restart: before pid %d, now %+v\n%s", oldPID, d, out)
	}
	if got := after["old"].Action; !strings.HasPrefix(got, "up to date") {
		t.Errorf("ASSERTION: after the sync, old plans %q, want \"up to date\"\n%s", got, out)
	}
}

// TestHostsSyncRestartWithYes restarts a daemon of the old version with
// --restart --yes, and checks that the new daemon runs the new version and
// brings the session back.
func TestHostsSyncRestartWithYes(t *testing.T) {
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"old"}, nil)
	old := f.hosts["old"]
	old.install(t, tuiosBin)
	if out, err := old.run(t, "new", "work", "--detach"); err != nil {
		t.Fatalf("start the daemon on old: %v\n%s", err, out)
	}
	before, out, err := f.syncJSON(t, "--binary", next, "--dry-run")
	if err != nil || before["old"].Daemon.PID == 0 {
		t.Fatalf("read old: %v\n%s", err, out)
	}

	rows, out, err := f.syncJSON(t, "--binary", next, "--restart", "--yes")
	saveSyncArtifact(t, "restart.json", out)
	if err != nil {
		t.Fatalf("ASSERTION: sync --restart --yes failed: %v\n%s", err, out)
	}
	if r := rows["old"]; !r.Installed || !r.Daemon.Restarted || r.RestartNeeded {
		t.Errorf("ASSERTION: old was not installed and restarted: %+v\n%s", r, out)
	}

	deadline := time.Now().Add(20 * time.Second)
	var now map[string]syncResultRow
	for time.Now().Before(deadline) {
		now, out, _ = f.syncJSON(t, "--binary", next, "--dry-run")
		if d := now["old"].Daemon; d.Version == syncNextVersion && d.SessionCount == 1 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	saveSyncArtifact(t, "after-restart.json", out)
	d := now["old"].Daemon
	if d.Version != syncNextVersion || d.PID == before["old"].Daemon.PID {
		t.Errorf("ASSERTION: the daemon on old does not run the new version: %+v\n%s", d, out)
	}
	if d.SessionCount != 1 || len(d.Sessions) != 1 || d.Sessions[0].Name != "work" {
		t.Errorf("ASSERTION: the session did not come back after the restart: %+v\n%s", d, out)
	}
	if now["old"].RestartNeeded || now["old"].Action != "up to date" {
		t.Errorf("ASSERTION: after the restart, old still plans %q (restart needed %v)\n%s", now["old"].Action, now["old"].RestartNeeded, out)
	}
}
