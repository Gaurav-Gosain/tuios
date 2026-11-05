package tuie2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The paths that install a tuios and move the daemon onto it, upgrading it
// live where it can and restarting it where it cannot: `tuios hosts sync
// --restart` on a host and on this machine, and scripts/install.sh. Each runs
// `tuios daemon upgrade --or-restart`, or without the restart, against real
// daemons under isolation roots. See docs/design/live-handoff.md, section 1.2.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// serverHello is the named server's pid, image and version.
func serverHello(t *testing.T, base, server string) daemonImage {
	t.Helper()
	out, err := serverCLI(t, base, server, "call", "hello", "{}")
	if err != nil {
		t.Fatalf("hello on server %s: %v\n%s", server, err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var img daemonImage
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &img); err != nil || img.PID == 0 {
		t.Fatalf("hello on server %s: %v\n%s", server, err, out)
	}
	return img
}

// serverPanePIDs is each window's process on the named server, by window id.
func serverPanePIDs(t *testing.T, base, server, session string) map[string]int {
	t.Helper()
	out, err := serverCLI(t, base, server, "list-windows", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-windows on server %s: %v\n%s", server, err, out)
	}
	var list struct {
		Windows []struct {
			ID  string `json:"window_id"`
			PID int    `json:"pid"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &list); err != nil {
		t.Fatalf("list-windows on server %s: decode: %v\n%s", server, err, out)
	}
	pids := map[string]int{}
	for _, w := range list.Windows {
		pids[w.ID] = w.PID
	}
	return pids
}

// runBinaryIn runs bin with base's isolation and env, the way a person on
// that machine runs the tuios on their PATH.
func runBinaryIn(t *testing.T, base, bin string, env []string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(bin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// installCopy copies src to dst, executable.
func installCopy(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil { //nolint:gosec // a binary the test runs
		t.Fatal(err)
	}
}

// changedPIDs lists the windows of before whose pid changed or went.
func changedPIDs(before, after map[string]int) []string {
	var changed []string
	for id, pid := range before {
		if after[id] != pid {
			changed = append(changed, id)
		}
	}
	slices.Sort(changed)
	return changed
}

// TestHostsSyncLocalUpgradesTheNamedServerLive runs `tuios --server NAME
// hosts sync --local --restart --yes`, the shape of `just deploy`, on a
// machine with two daemons of the old version: the default one and a named
// one, each with a program in a pane. The named daemon must be upgraded live,
// keeping its pid and its pane's process, and run the new version. The
// default daemon must be untouched: same image, same version, same pane.
//
// The tuios this machine's PATH finds is a copy under the isolation root, and
// PATH holds nothing else that has one, so the local probe can only find it.
func TestHostsSyncLocalUpgradesTheNamedServerLive(t *testing.T) {
	next := syncNextBinary(t)
	f := newSyncFleet(t, nil, nil)
	const server = "e2e-lhsync"
	binDir := filepath.Join(f.here, "localbin")
	local := filepath.Join(binDir, "tuios")
	installCopy(t, tuiosBin, local)
	env := append(slices.Clone(f.env), "PATH="+binDir+":/usr/bin:/bin", "TUIOS_PANE_ID=")
	stopServerAtEnd(t, f.here, server)

	for _, args := range [][]string{
		{"new", "work", "--detach"},
		{"new-window", "sleeper", "-s", "work", "--", "sleep", "600"},
		{"--server", server, "new", "work", "--detach"},
		{"--server", server, "new-window", "sleeper", "-s", "work", "--", "sleep", "600"},
	} {
		if out, err := runBinaryIn(t, f.here, local, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	defBefore, defPanes := helloImage(t, f.here), panePIDs(t, f.here, "work")
	namedBefore, namedPanes := serverHello(t, f.here, server), serverPanePIDs(t, f.here, server, "work")

	out, err := tuiosCLIEnv(t, f.here, env, "--server", server, "hosts", "sync", "--local", "--binary", next, "--restart", "--yes", "--json")
	saveSyncArtifact(t, "local-named.json", out)
	if err != nil {
		t.Fatalf("ASSERTION: sync --local --restart on server %s failed: %v\n%s", server, err, out)
	}
	var rep struct {
		Hosts []syncResultRow `json:"hosts"`
	}
	if err := json.NewDecoder(strings.NewReader(out[strings.Index(out, "{"):])).Decode(&rep); err != nil || len(rep.Hosts) != 1 {
		t.Fatalf("read the JSON: %v\n%s", err, out)
	}
	row := rep.Hosts[0]

	defAfter, defPanesAfter := helloImage(t, f.here), panePIDs(t, f.here, "work")
	namedAfter, namedPanesAfter := serverHello(t, f.here, server), serverPanePIDs(t, f.here, server, "work")
	writeHandoffArtifact(t, map[string]any{
		"server": server, "row": row,
		"named_before": namedBefore, "named_after": namedAfter, "named_panes_before": namedPanes, "named_panes_after": namedPanesAfter,
		"default_before": defBefore, "default_after": defAfter, "default_panes_before": defPanes, "default_panes_after": defPanesAfter,
	})

	if !sameFile(row.InstallPath, local) {
		t.Fatalf("ASSERTION: sync installed at %q, want the copy on the test's PATH %s", row.InstallPath, local)
	}
	if !row.Daemon.Upgraded || row.Daemon.Restarted || !strings.Contains(row.Action, "daemon upgraded live") {
		t.Errorf("ASSERTION: the row does not say the daemon was upgraded live: %+v", row)
	}
	if namedAfter.PID != namedBefore.PID || namedAfter.Instance == namedBefore.Instance || namedAfter.Version != syncNextVersion {
		t.Errorf("ASSERTION: server %s is %+v after the sync, from %+v; want the same pid, a new image and %s", server, namedAfter, namedBefore, syncNextVersion)
	}
	if len(namedPanes) != 2 || len(changedPIDs(namedPanes, namedPanesAfter)) != 0 {
		t.Errorf("ASSERTION: the panes of server %s were %v and are %v", server, namedPanes, namedPanesAfter)
	}
	if defAfter != defBefore {
		t.Errorf("ASSERTION: the default daemon changed when server %s was synced: %+v then %+v", server, defBefore, defAfter)
	}
	if len(defPanes) != 2 || len(changedPIDs(defPanes, defPanesAfter)) != 0 {
		t.Errorf("ASSERTION: the default daemon's panes were %v and are %v", defPanes, defPanesAfter)
	}
}

// TestHostsSyncRestartsADaemonThatPredatesLiveUpgrades syncs a host whose
// daemon is from a build without live upgrades, TUIOS_E2E_BASELINE_BIN, with
// --restart --yes. The live upgrade is refused by that daemon, so sync must
// restart it with the new binary, and the row must say so and why: the daemon
// predates live upgrades. The session comes back on a new daemon of the new
// version.
func TestHostsSyncRestartsADaemonThatPredatesLiveUpgrades(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from upstream main, which has no live upgrades, and name it there")
	}
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"old"}, nil)
	old := f.hosts["old"]
	old.install(t, baseline)
	if out, err := old.run(t, "new", "work", "--detach"); err != nil {
		t.Fatalf("start the daemon on old: %v\n%s", err, out)
	}
	before, out, err := f.syncJSON(t, "--binary", next, "--dry-run")
	if err != nil || before["old"].Daemon.PID == 0 {
		t.Fatalf("read old: %v\n%s", err, out)
	}

	rows, out, err := f.syncJSON(t, "--binary", next, "--restart", "--yes")
	saveSyncArtifact(t, "restart-old-daemon.json", out)
	if err != nil {
		t.Fatalf("ASSERTION: sync --restart --yes failed: %v\n%s", err, out)
	}
	r := rows["old"]
	if !r.Installed || !r.Daemon.Restarted || r.Daemon.Upgraded || r.RestartNeeded || !strings.Contains(r.Action, "daemon restarted") {
		t.Errorf("ASSERTION: old was not installed and restarted: %+v\n%s", r, out)
	}
	if !slices.ContainsFunc(r.Notes, func(n string) bool {
		return strings.Contains(n, "could not be upgraded live") && strings.Contains(n, "predates live upgrades")
	}) {
		t.Errorf("ASSERTION: the row does not say the daemon was restarted because it predates live upgrades: %v", r.Notes)
	}

	var now map[string]syncResultRow
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		now, out, _ = f.syncJSON(t, "--binary", next, "--dry-run")
		if d := now["old"].Daemon; d.Version == syncNextVersion && d.SessionCount == 1 {
			break
		}
	}
	saveSyncArtifact(t, "after-restart-old-daemon.json", out)
	d := now["old"].Daemon
	if d.Version != syncNextVersion || d.PID == before["old"].Daemon.PID {
		t.Errorf("ASSERTION: the daemon on old was not restarted on the new version: %+v\n%s", d, out)
	}
	if d.SessionCount != 1 || len(d.Sessions) != 1 || d.Sessions[0].Name != "work" {
		t.Errorf("ASSERTION: the session did not come back after the restart: %+v\n%s", d, out)
	}
}

// TestInstallScriptUpgradesTheDaemonLive runs scripts/install.sh
// --upgrade-server into a prefix under the isolation root, with a daemon of
// the old version running one program in a pane. The script must build the
// checkout, install it, and upgrade the daemon live to the installed binary:
// the same pid, a new image of the installed version, the same pane process.
//
// install.sh finds the daemon from XDG_RUNTIME_DIR, and without it from the
// person's own /tmp/tuios-<uid>, so the test refuses to run unless the
// variable it passes is the isolation root's.
func TestInstallScriptUpgradesTheDaemonLive(t *testing.T) {
	base := remoteMachine(t)
	runtimeDir := xdgDir(base, "XDG_RUNTIME_DIR")
	if !strings.HasPrefix(runtimeDir, base+string(filepath.Separator)) && !strings.HasPrefix(runtimeDir, shortRuntimeRoot+string(filepath.Separator)) {
		t.Fatalf("the runtime folder %q is not the test's own", runtimeDir)
	}
	prefix := filepath.Join(base, "prefix")
	installCopy(t, tuiosBin, filepath.Join(prefix, "tuios"))
	old := filepath.Join(prefix, "tuios")
	if out, err := runBinaryIn(t, base, old, nil, "new", "work", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	if out, err := runBinaryIn(t, base, old, nil, "new-window", "sleeper", "-s", "work", "--", "sleep", "600"); err != nil {
		t.Fatalf("start a program: %v\n%s", err, out)
	}
	img, panes := helloImage(t, base), panePIDs(t, base, "work")

	// The build cache and module cache are the real ones: HOME is the
	// isolation root's, and a cold cache would rebuild every dependency.
	var goEnv []string
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		v, err := exec.Command("go", "env", key).Output()
		if err != nil {
			t.Fatalf("go env %s: %v", key, err)
		}
		goEnv = append(goEnv, key+"="+strings.TrimSpace(string(v)))
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts", "install.sh"), "--prefix", prefix, "--upgrade-server")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TUIOS_PREFIX=", "TUIOS_SERVER=")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, goEnv...)
	cmd.Env = append(cmd.Env, "PATH="+prefix+":"+os.Getenv("PATH"))
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	saveSyncArtifact(t, "install-upgrade.txt", out)
	if err != nil {
		t.Fatalf("ASSERTION: install.sh --upgrade-server failed: %v\n%s", err, out)
	}
	installed, err := exec.Command(old, "--version").Output()
	if err != nil {
		t.Fatalf("the installed tuios does not run: %v", err)
	}
	wantVersion := strings.Fields(strings.TrimPrefix(firstOutputLine(string(installed)), "tuios version "))[0]

	after, afterPanes := helloImage(t, base), panePIDs(t, base, "work")
	writeHandoffArtifact(t, map[string]any{"before": img, "after": after, "installed_version": wantVersion, "panes_before": panes, "panes_after": afterPanes, "output": out})
	if !strings.Contains(out, "==> upgrading the daemon live") || !strings.Contains(out, "Upgraded in") {
		t.Errorf("ASSERTION: install.sh did not report a live upgrade:\n%s", out)
	}
	if after.PID != img.PID || after.Instance == img.Instance || after.Version != wantVersion {
		t.Errorf("ASSERTION: the daemon is %+v after the install, from %+v; want the same pid, a new image and %s", after, img, wantVersion)
	}
	if len(panes) != 2 || len(changedPIDs(panes, afterPanes)) != 0 {
		t.Errorf("ASSERTION: the panes were %v and are %v after the install", panes, afterPanes)
	}
}

func firstOutputLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// TestDaemonUpgradeOrRestartStartsTheNamedServer runs `tuios --server NAME
// daemon upgrade --or-restart` with no daemon running anywhere. There is
// nothing to upgrade, so the command must start the named server's daemon
// with the binary, say so, and leave the default server alone: no daemon may
// be running there afterwards.
func TestDaemonUpgradeOrRestartStartsTheNamedServer(t *testing.T) {
	base := remoteMachine(t)
	const server = "e2e-lhstart"
	stopServerAtEnd(t, base, server)
	second := copyTuios(t)

	out, err := serverCLI(t, base, server, "daemon", "upgrade", "--binary", second, "--or-restart", "--json")
	var res map[string]any
	lines := strings.Split(strings.TrimSpace(out), "\n")
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &res)
	if err != nil || res["action"] != "started" || res["reason"] != "no daemon is running" {
		writeHandoffArtifact(t, map[string]any{"server": server, "output": out, "error": errString(err), "result": res})
		t.Fatalf("ASSERTION: daemon upgrade --or-restart with no daemon did not start one: %v\n%s", err, out)
	}
	img := serverHello(t, base, server)
	exe := executableOf(t, img.PID)
	stopped, _ := tuiosCLI(t, base, "kill-server")
	writeHandoffArtifact(t, map[string]any{"server": server, "output": out, "error": errString(err), "result": res, "hello": img, "executable": exe, "default_kill_server": stopped})

	if int(res["pid"].(float64)) != img.PID || !sameFile(exe, second) {
		t.Errorf("ASSERTION: server %s runs %s as pid %d, want %s as the reported pid %v", server, exe, img.PID, second, res["pid"])
	}
	if !strings.Contains(stopped, "not running") {
		t.Errorf("ASSERTION: a daemon runs on the default server after the named server was started:\n%s", stopped)
	}
}

// TestHostsSyncInAPaneLeavesADaemonItCannotUpgrade runs `tuios hosts sync
// --local --restart --yes` as if from a pane of this machine's daemon, which
// is from a build without live upgrades, TUIOS_E2E_BASELINE_BIN. A restart
// would end the pane the command runs in, so the daemon may only be upgraded
// live, and that daemon refuses. sync must install the binary, leave the
// daemon running with its pane, and say why.
func TestHostsSyncInAPaneLeavesADaemonItCannotUpgrade(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from upstream main, which has no live upgrades, and name it there")
	}
	next := syncNextBinary(t)
	f := newSyncFleet(t, nil, nil)
	binDir := filepath.Join(f.here, "localbin")
	local := filepath.Join(binDir, "tuios")
	installCopy(t, baseline, local)
	env := append(slices.Clone(f.env), "PATH="+binDir+":/usr/bin:/bin")
	for _, args := range [][]string{{"new", "work", "--detach"}, {"new-window", "sleeper", "-s", "work", "--", "sleep", "600"}} {
		if out, err := runBinaryIn(t, f.here, local, append(slices.Clone(env), "TUIOS_PANE_ID="), args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	img, panes := helloImage(t, f.here), panePIDs(t, f.here, "work")

	out, err := tuiosCLIEnv(t, f.here, append(env, "TUIOS_PANE_ID=e2e-not-a-pane"), "hosts", "sync", "--local", "--binary", next, "--restart", "--yes", "--json")
	saveSyncArtifact(t, "in-pane.json", out)
	var rep struct {
		Hosts []syncResultRow `json:"hosts"`
	}
	if jerr := json.NewDecoder(strings.NewReader(out[strings.Index(out, "{"):])).Decode(&rep); jerr != nil || len(rep.Hosts) != 1 {
		t.Fatalf("read the JSON: %v (sync: %v)\n%s", jerr, err, out)
	}
	row := rep.Hosts[0]
	after, afterPanes := helloImage(t, f.here), panePIDs(t, f.here, "work")
	writeHandoffArtifact(t, map[string]any{"row": row, "before": img, "after": after, "panes_before": panes, "panes_after": afterPanes, "sync_error": errString(err)})

	if err != nil || !sameFile(row.InstallPath, local) || !row.Installed {
		t.Fatalf("ASSERTION: sync did not install at %s: %v\n%s", local, err, out)
	}
	if row.Daemon.Upgraded || row.Daemon.Restarted || !row.RestartNeeded {
		t.Errorf("ASSERTION: the row says the daemon moved, or that it needs nothing: %+v", row)
	}
	if !slices.ContainsFunc(row.Notes, func(n string) bool {
		return strings.Contains(n, "could not be upgraded live") && strings.Contains(n, "predates live upgrades") && strings.Contains(n, "not restarted")
	}) {
		t.Errorf("ASSERTION: the row does not say why the daemon was left: %v", row.Notes)
	}
	if after != img || len(panes) != 2 || len(changedPIDs(panes, afterPanes)) != 0 {
		t.Errorf("ASSERTION: the daemon changed: %+v then %+v, panes %v then %v", img, after, panes, afterPanes)
	}
}

// TestHostsSyncReportsAFallbackAndDoesNotRestart syncs a host whose daemon
// takes the live upgrade and then cannot adopt its panes in the new image
// (TUIOS_TEST_HANDOFF_FAIL=error), so it goes back to the binary it ran, with
// every program. --restart asked for the daemon to move, but a restart now
// would end programs that are still running for a failure that needs reading:
// sync must not restart it, and must fail the host saying the daemon went back.
func TestHostsSyncReportsAFallbackAndDoesNotRestart(t *testing.T) {
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"old"}, nil)
	old := f.hosts["old"]
	old.install(t, tuiosBin)
	failing := []string{"TUIOS_TEST_HANDOFF=1", "TUIOS_TEST_HANDOFF_FAIL=error", "TUIOS_PANE_ID="}
	for _, args := range [][]string{{"new", "work", "--detach"}, {"new-window", "sleeper", "-s", "work", "--", "sleep", "600"}} {
		if out, err := runBinaryIn(t, old.base, old.bin, failing, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	img, panes := helloImage(t, old.base), panePIDs(t, old.base, "work")

	rows, out, err := f.syncJSON(t, "--binary", next, "--restart", "--yes")
	saveSyncArtifact(t, "fallback.json", out)
	r := rows["old"]
	after, afterPanes := helloImage(t, old.base), panePIDs(t, old.base, "work")
	writeHandoffArtifact(t, map[string]any{"row": r, "sync_error": errString(err), "before": img, "after": after, "panes_before": panes, "panes_after": afterPanes})

	if err == nil || !strings.Contains(r.Error, "could not take the panes over") || r.Daemon.Restarted || r.Daemon.Upgraded {
		t.Errorf("ASSERTION: sync did not fail the host with the fallback, or moved the daemon: %v %+v\n%s", err, r, out)
	}
	if !r.Installed {
		t.Errorf("ASSERTION: the binary was not installed: %+v", r)
	}
	if after.PID != img.PID || after.Version == syncNextVersion {
		t.Errorf("ASSERTION: the daemon is %+v after the fallback, from %+v; want the same pid on the old version", after, img)
	}
	if len(panes) != 2 || len(changedPIDs(panes, afterPanes)) != 0 {
		t.Errorf("ASSERTION: the panes were %v and are %v after the fallback", panes, afterPanes)
	}
}

// TestOrRestartNeverRunsABinaryOthersCanWrite: the owner and mode check that
// refuses an upgrade to a binary someone else could have put there also stops
// --or-restart, which would otherwise run that binary for a restart, or for a
// start where no daemon runs. A script anyone can write is given to `daemon
// upgrade --or-restart` once with a daemon running and once with none: each
// must be refused with the check's message and action "refused", the script
// must never run (its marker must not exist), the running daemon must keep
// its pid and instance, and where none ran none may be started.
func TestOrRestartNeverRunsABinaryOthersCanWrite(t *testing.T) {
	const want = "can be written by its group or by others (mode 0777), so this daemon will not run it"
	bin := filepath.Join(t.TempDir(), "tuios")
	marks := t.TempDir()
	try := func(base, marker string) (map[string]any, string, bool, error) {
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho ran >>'"+marker+"'\necho 'tuios version v99.0.0'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(bin, 0o777); err != nil {
			t.Fatal(err)
		}
		out, err := handoffCLI(t, base, 60*time.Second, "daemon", "upgrade", "--or-restart", "--json", "--binary", bin)
		var got map[string]any
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "{") {
				_ = json.Unmarshal([]byte(line), &got)
			}
		}
		_, statErr := os.Stat(marker)
		return got, out, statErr == nil, err
	}

	running := t.TempDir()
	startHandoffDaemon(t, running)
	before := helloImage(t, running)
	gotA, outA, ranA, errA := try(running, filepath.Join(marks, "running"))
	after := helloImage(t, running)

	idle := t.TempDir()
	killDaemon(t, idle)
	gotB, outB, ranB, errB := try(idle, filepath.Join(marks, "idle"))
	_, helloErr := callVerb(t, idle, "hello", map[string]any{})

	writeHandoffArtifact(t, map[string]any{
		"binary":  bin,
		"running": map[string]any{"json": gotA, "text": outA, "error": errString(errA), "marker_exists": ranA, "before": before, "after": after},
		"idle":    map[string]any{"json": gotB, "text": outB, "error": errString(errB), "marker_exists": ranB, "daemon_started": helloErr == nil},
	})

	for _, c := range []struct {
		name string
		got  map[string]any
		out  string
		ran  bool
		err  error
	}{{"with a daemon running", gotA, outA, ranA, errA}, {"with no daemon running", gotB, outB, ranB, errB}} {
		if c.err == nil || !strings.Contains(c.out, want) {
			t.Errorf("ASSERTION: %s, --or-restart to a script anyone can write was not refused with %q (error %v):\n%s", c.name, want, c.err, c.out)
		}
		if c.got["action"] != "refused" {
			t.Errorf("ASSERTION: %s, the action was %v, not \"refused\":\n%s", c.name, c.got["action"], c.out)
		}
		if c.ran {
			t.Errorf("ASSERTION: %s, --or-restart ran the script anyone can write", c.name)
		}
	}
	if after != before {
		t.Errorf("ASSERTION: the running daemon changed from %+v to %+v", before, after)
	}
	if helloErr == nil {
		t.Errorf("ASSERTION: a daemon was started where none ran")
	}
}

// writeTuiosScript writes an executable sh script named tuios, owned by this
// user and writable by no one else, so the owner and mode check passes it.
func writeTuiosScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tuios")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// orRestartJSON runs `tuios daemon upgrade --or-restart --json --binary bin`
// against base and returns its JSON outcome and everything it printed.
func orRestartJSON(t *testing.T, base, bin string) (map[string]any, string, error) {
	t.Helper()
	out, err := handoffCLI(t, base, 150*time.Second, "daemon", "upgrade", "--or-restart", "--json", "--binary", bin)
	var got map[string]any
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "{") {
			_ = json.Unmarshal([]byte(line), &got)
		}
	}
	return got, out, err
}

// TestOrRestartSaysRestartedOnlyWhenANewDaemonAnswers: a restart is reported
// only once a daemon other than the old one answers. Two binaries fail
// handoff-accepts, so --or-restart restarts with them. One stops the daemon
// with a real kill-server and starts none, though its start-server exits 0.
// The other stops nothing and starts nothing, so the old daemon still
// answers. Each run must fail, and say so: never action "restarted".
func TestOrRestartSaysRestartedOnlyWhenANewDaemonAnswers(t *testing.T) {
	fake := func(kill string) string {
		return writeTuiosScript(t, `case "$1" in
--version) echo "tuios version v99.0.0" ;;
kill-server) `+kill+` ;;
start-server) exit 0 ;;
*) echo "unknown command" >&2; exit 1 ;;
esac
`)
	}
	cases := []struct {
		name, bin, want string
		stopped         bool
	}{
		{"no daemon after the restart", fake(`exec '` + tuiosBin + `' "$@"`), "start-server returned, and no daemon answers.", true},
		{"the old daemon still answers", fake(`exit 0`), "The daemon that answers after the restart is the old one", false},
	}
	art := map[string]any{}
	for _, c := range cases {
		base := t.TempDir()
		startHandoffDaemon(t, base)
		before := helloImage(t, base)
		got, out, err := orRestartJSON(t, base, c.bin)
		after, helloErr := callVerb(t, base, "hello", map[string]any{})
		art[c.name] = map[string]any{"json": got, "text": out, "error": errString(err), "before": before, "hello_after": after, "hello_error": errString(helloErr)}

		if err == nil || got["action"] == "restarted" || !strings.Contains(out, c.want) {
			t.Errorf("ASSERTION: %s, --or-restart did not fail with %q, or said restarted (action %v, error %v):\n%s", c.name, c.want, got["action"], err, out)
		}
		if c.stopped != (helloErr != nil) {
			t.Errorf("ASSERTION: %s, the daemon is not in the state the fake left it (stopped %v, hello error %v)", c.name, c.stopped, helloErr)
		}
	}
	writeHandoffArtifact(t, art)
}

// TestOrRestartNeverRunsAFileSwappedInBetweenItsRuns: the restart runs the
// binary by path three times, so it pins the file and checks it before each
// run. This binary fails handoff-accepts, so --or-restart restarts with it,
// and its --version renames another script over its own path. That script
// writes a marker when it runs. The restart must refuse before kill-server:
// the marker must not exist, and the daemon must keep its pid and instance.
func TestOrRestartNeverRunsAFileSwappedInBetweenItsRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "swapped-ran")
	swap := writeTuiosScript(t, `echo ran >>'`+marker+`'
exit 0
`)
	bin := writeTuiosScript(t, `case "$1" in
--version) echo "tuios version v99.0.0"; mv -f '`+swap+`' "$0" ;;
*) echo "unknown command" >&2; exit 1 ;;
esac
`)
	base := t.TempDir()
	startHandoffDaemon(t, base)
	before := helloImage(t, base)
	got, out, err := orRestartJSON(t, base, bin)
	after := helloImage(t, base)
	_, statErr := os.Stat(marker)
	writeHandoffArtifact(t, map[string]any{"json": got, "text": out, "error": errString(err), "before": before, "after": after, "swapped_ran": statErr == nil})

	if err == nil || !strings.Contains(out, "was replaced or changed after it was checked") || got["action"] == "restarted" {
		t.Errorf("ASSERTION: --or-restart did not refuse the file swapped in after its check (action %v, error %v):\n%s", got["action"], err, out)
	}
	if statErr == nil {
		t.Errorf("ASSERTION: --or-restart ran the file swapped in after its check")
	}
	if after != before {
		t.Errorf("ASSERTION: the daemon changed from %+v to %+v", before, after)
	}
}
