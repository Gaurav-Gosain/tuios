package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// --start on tuios hosts test and tuios hosts sync, against the fake hosts of
// hosts_sync_test.go. The daemons started here run under each host's own
// root, and remoteMachine stops them when the test ends.

// hostTestJSON is the part of `tuios hosts test --json` the tests read.
type hostTestJSON struct {
	Host         string `json:"host"`
	Status       string `json:"status"`
	Before       string `json:"before"`
	Action       string `json:"action"`
	StartCommand string `json:"start_command"`
	Command      string `json:"command"`
	Error        string `json:"error"`
}

// hostTest runs `tuios hosts test NAME --json` with args and reads the result.
func (f *syncFleet) hostTest(t *testing.T, name string, args ...string) (hostTestJSON, string, error) {
	t.Helper()
	out, err := tuiosCLIEnv(t, f.here, f.env, append([]string{"hosts", "test", name, "--json"}, args...)...)
	var res hostTestJSON
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("hosts test printed no JSON: %v\n%s", err, out)
	}
	if derr := json.NewDecoder(strings.NewReader(out[start:])).Decode(&res); derr != nil {
		t.Fatalf("read the JSON: %v\n%s", derr, out)
	}
	return res, out, err
}

// TestHostsTestStart covers a host that has tuios and no daemon: the message
// names the command that starts it, --dry-run starts nothing, and --start
// starts the daemon so the next test finds it up.
func TestHostsTestStart(t *testing.T) {
	f := newSyncFleet(t, []string{"idle"}, nil)
	idle := f.hosts["idle"]
	idle.install(t, tuiosBin)

	// The message for no_daemon gives the exact command for the host, and
	// the --start that runs it from here.
	out, err := tuiosCLIEnv(t, f.here, f.env, "hosts", "test", "idle")
	saveSyncArtifact(t, "no-daemon.txt", out)
	if err == nil {
		t.Errorf("ASSERTION: hosts test exited 0 for a host with no daemon:\n%s", out)
	}
	if !strings.Contains(out, "no_daemon") {
		t.Fatalf("ASSERTION: the host does not report no_daemon:\n%s", out)
	}
	if !strings.Contains(out, idle.bin+" start-server") {
		t.Errorf("ASSERTION: the message does not name the command to run on the host (%s start-server):\n%s", idle.bin, out)
	}
	if !strings.Contains(out, "tuios hosts test idle --start") {
		t.Errorf("ASSERTION: the message does not suggest --start:\n%s", out)
	}

	// The dry run: the plan, and no daemon.
	plan, out, err := f.hostTest(t, "idle", "--start", "--dry-run")
	saveSyncArtifact(t, "test-start-dry-run.json", out)
	if err != nil {
		t.Errorf("ASSERTION: hosts test --start --dry-run failed: %v\n%s", err, out)
	}
	if plan.Action != "would start daemon" || plan.StartCommand != idle.bin+" start-server" {
		t.Errorf("ASSERTION: the plan is %q with %q, want \"would start daemon\" with %q\n%s", plan.Action, plan.StartCommand, idle.bin+" start-server", out)
	}
	if now, out, _ := f.hostTest(t, "idle"); now.Status != "no_daemon" {
		t.Fatalf("ASSERTION: the dry run changed the host, which now reports %q\n%s", now.Status, out)
	}

	// The start.
	res, out, err := f.hostTest(t, "idle", "--start")
	saveSyncArtifact(t, "test-start.json", out)
	if err != nil {
		t.Errorf("ASSERTION: hosts test --start failed: %v\n%s", err, out)
	}
	if res.Action != "daemon started" || res.Before != "no_daemon" || res.Status != "up" {
		t.Errorf("ASSERTION: --start reported %q (before %q, now %q), want \"daemon started\" from no_daemon to up\n%s", res.Action, res.Before, res.Status, out)
	}
	out, err = tuiosCLIEnv(t, f.here, f.env, "hosts", "test", "idle")
	saveSyncArtifact(t, "after-start.txt", out)
	if err != nil || !strings.Contains(out, "  up") {
		t.Errorf("ASSERTION: after --start the host is not up: %v\n%s", err, out)
	}

	// A second --start finds the daemon up and does nothing.
	out, err = tuiosCLIEnv(t, f.here, f.env, "hosts", "test", "idle", "--start")
	saveSyncArtifact(t, "start-again.txt", out)
	if err != nil || !strings.Contains(out, "--start did nothing") {
		t.Errorf("ASSERTION: --start on a host that is up did not say it did nothing: %v\n%s", err, out)
	}
}

// TestHostsSyncStart covers a host with no tuios: hosts test --start does not
// start anything there, sync --start --dry-run plans an install and a start
// and does neither, and sync --start installs and then starts the daemon.
func TestHostsSyncStart(t *testing.T) {
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"fresh"}, nil)
	fresh := f.hosts["fresh"]

	// No tuios, so no daemon.
	res, out, err := f.hostTest(t, "fresh", "--start")
	saveSyncArtifact(t, "test-start-no-tuios.json", out)
	if err == nil || res.Action == "daemon started" || res.Status == "up" {
		t.Errorf("ASSERTION: hosts test --start reported a start on a host with no tuios: %v\n%s", err, out)
	}

	plan, out, err := f.syncJSON(t, "--binary", next, "--start", "--dry-run")
	saveSyncArtifact(t, "sync-start-dry-run.json", out)
	if err != nil {
		t.Errorf("ASSERTION: sync --start --dry-run failed: %v\n%s", err, out)
	}
	if got := plan["fresh"].Action; got != "would install, would start daemon" {
		t.Errorf("ASSERTION: the plan for fresh is %q, want \"would install, would start daemon\"\n%s", got, out)
	}
	if fresh.sha(t) != "" {
		t.Fatalf("ASSERTION: the dry run installed a binary on fresh")
	}

	rows, out, err := f.syncJSON(t, "--binary", next, "--start")
	saveSyncArtifact(t, "sync-start.json", out)
	if err != nil {
		t.Errorf("ASSERTION: sync --start failed: %v\n%s", err, out)
	}
	r := rows["fresh"]
	if !r.Installed || !r.Daemon.Started || r.Action != "installed, daemon started" {
		t.Errorf("ASSERTION: fresh was not installed and started: %+v\n%s", r, out)
	}

	// The daemon runs the version just installed.
	now, out, _ := f.syncJSON(t, "--binary", next, "--dry-run")
	saveSyncArtifact(t, "sync-after-start.json", out)
	if d := now["fresh"].Daemon; d.State != "running" || d.Version != syncNextVersion {
		t.Errorf("ASSERTION: after sync --start the daemon on fresh is %+v, want running %s\n%s", d, syncNextVersion, out)
	}
}
