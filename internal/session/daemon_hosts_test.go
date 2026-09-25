package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
