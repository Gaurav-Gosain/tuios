package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The daemon half of host hot reload.
//
// What is proved here is the wiring: a change to the [hosts] table reaches the
// running daemon's table and its listing verb, with no restart. The link layer
// has its own tests for what happens to the ssh children, and e2e/tui drives
// the whole path with a real config file, a real daemon and a real subprocess.

// hostNamesFromVerb is what list-hosts currently reports.
func hostNamesFromVerb(t *testing.T, d *Daemon) []string {
	t.Helper()
	result, verr := d.verbListHosts(nil, nil)
	if verr != nil {
		t.Fatalf("list-hosts failed: %v", verr)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal the result: %v", err)
	}
	var decoded struct {
		Hosts []federation.HostReport `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode the result: %v", err)
	}
	names := make([]string, 0, len(decoded.Hosts))
	for _, h := range decoded.Hosts {
		names = append(names, h.Host)
	}
	return names
}

func TestApplyHostsChangesWhatTheListingReports(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Hosts: []federation.Host{{Name: "build", Addr: "buildbox"}}})
	if got := strings.Join(hostNamesFromVerb(t, d), ","); got != "build" {
		t.Fatalf("the daemon did not start with the configured host, got %q", got)
	}

	d.ApplyHosts([]federation.Host{
		{Name: "build", Addr: "buildbox"},
		{Name: "lab", Addr: "lab-01"},
	})
	if got := strings.Join(hostNamesFromVerb(t, d), ","); got != "build,lab" {
		t.Errorf("ASSERTION: a host added at run time does not reach the listing, got %q", got)
	}

	d.ApplyHosts([]federation.Host{{Name: "lab", Addr: "lab-01"}})
	if got := strings.Join(hostNamesFromVerb(t, d), ","); got != "lab" {
		t.Errorf("ASSERTION: a host removed at run time still reaches the listing, got %q", got)
	}

	d.ApplyHosts(nil)
	if got := hostNamesFromVerb(t, d); len(got) != 0 {
		t.Errorf("ASSERTION: emptying the table left hosts in the listing, got %v", got)
	}
}

// A daemon that started with no hosts must still take the first one. This is
// the default install, and it used to be the case that could not work: the
// daemon allocated no link manager at all when the table was empty.
func TestADaemonWithNoHostsTakesTheFirstOne(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	if got := hostNamesFromVerb(t, d); len(got) != 0 {
		t.Fatalf("a daemon with no hosts reported some: %v", got)
	}
	d.ApplyHosts([]federation.Host{{Name: "build", Addr: "buildbox"}})
	if got := strings.Join(hostNamesFromVerb(t, d), ","); got != "build" {
		t.Errorf("ASSERTION: the first host added to an empty daemon does not reach the listing, got %q", got)
	}
}

// A host the table refuses is reported rather than dropped in silence, and the
// reason is replaced on each reload rather than piling up.
func TestApplyHostsReportsWhatItDropped(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	d.ApplyHosts([]federation.Host{{Name: "build", Addr: ""}})
	problems := d.configProblems()
	if len(problems) != 1 || !strings.Contains(problems[0], "no addr") {
		t.Errorf("ASSERTION: a host with no address was not reported, got %v", problems)
	}
	d.ApplyHosts([]federation.Host{{Name: "build", Addr: "buildbox"}})
	if got := d.configProblems(); len(got) != 0 {
		t.Errorf("ASSERTION: a fixed config still reports the old problem, got %v", got)
	}
}

// The watcher: a save to the config file reaches the daemon's table.
func TestSavingTheConfigFileChangesTheHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[hosts.build]\naddr = \"buildbox\"\n"), 0o600); err != nil {
		t.Fatalf("write the config: %v", err)
	}

	d := NewDaemon(&DaemonConfig{
		ConfigPath: path,
		Hosts:      []federation.Host{{Name: "build", Addr: "buildbox"}},
	})
	d.startHostsWatch()
	t.Cleanup(d.stopHostsWatch)

	body := "[hosts.build]\naddr = \"buildbox\"\n\n[hosts.lab]\naddr = \"lab-01\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("save the config: %v", err)
	}

	waitForHosts(t, d, "build,lab", "a host added to the config file never reached the daemon")

	if err := os.WriteFile(path, []byte("[hosts.lab]\naddr = \"lab-01\"\n"), 0o600); err != nil {
		t.Fatalf("save the config: %v", err)
	}
	waitForHosts(t, d, "lab", "a host removed from the config file never left the daemon")
}

// A file that does not parse changes nothing. A half-written save caught
// between an editor's two writes must not tear a working link down.
func TestABrokenConfigFileLeavesTheHostsAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[hosts.build]\naddr = \"buildbox\"\n"), 0o600); err != nil {
		t.Fatalf("write the config: %v", err)
	}
	d := NewDaemon(&DaemonConfig{
		ConfigPath: path,
		Hosts:      []federation.Host{{Name: "build", Addr: "buildbox"}},
	})
	d.startHostsWatch()
	t.Cleanup(d.stopHostsWatch)

	if err := os.WriteFile(path, []byte("[hosts.build\naddr = \"unbalanced"), 0o600); err != nil {
		t.Fatalf("save a broken config: %v", err)
	}
	// Long enough for the debounce plus a reload, so this is a wait for the
	// change that must not happen rather than a race against it.
	time.Sleep(600 * time.Millisecond)
	if got := strings.Join(hostNamesFromVerb(t, d), ","); got != "build" {
		t.Errorf("ASSERTION: a config file with an error changed the hosts, got %q", got)
	}
}

// A daemon with no config path follows no file. Every test that builds a
// DaemonConfig by hand is in this case, and none of them should open an inotify
// watch on the developer's own config.
func TestADaemonWithNoConfigPathWatchesNothing(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	d.startHostsWatch()
	d.federationMu.Lock()
	w := d.hostsWatcher
	d.federationMu.Unlock()
	if w != nil {
		t.Error("ASSERTION: a daemon with no config path opened a config watch")
	}
}

// The path is what makes the watch happen, and every real starter goes through
// DaemonConfigFromUser.
func TestDaemonConfigFromUserCarriesTheConfigPath(t *testing.T) {
	cfg := DaemonConfigFromUser(config.DefaultConfig())
	if cfg.ConfigPath == "" {
		t.Error("ASSERTION: DaemonConfigFromUser set no config path, so no daemon would follow the file")
	}
}

func waitForHosts(t *testing.T, d *Daemon, want, why string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		got = strings.Join(hostNamesFromVerb(t, d), ",")
		if got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s. The listing says %q, wanted %q", why, got, want)
}
