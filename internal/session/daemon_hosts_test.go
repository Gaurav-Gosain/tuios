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
	time.Sleep(600 * time.Millisecond)
	if got := strings.Join(hostNamesFromVerb(t, d), ","); got != "build" {
		t.Errorf("ASSERTION: a config file with an error changed the hosts, got %q", got)
	}
}
