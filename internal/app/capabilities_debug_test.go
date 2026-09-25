package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCapabilitiesDebugFileIsPrivate: TUIOS_DEBUG_CAPS wrote
// /tmp/tuios_caps.log, one fixed name every user of the machine races for and
// anyone can read. It belongs under the per-user state directory, named per
// client, and readable by its owner only.
func TestCapabilitiesDebugFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	path := capabilitiesDebugPath()
	if path == "/tmp/tuios_caps.log" {
		t.Fatalf("the capabilities log is still the shared fixed name: %s", path)
	}
	if !strings.HasPrefix(path, filepath.Join(dir, "tuios")) {
		t.Fatalf("the capabilities log is outside the state directory: %s", path)
	}
	// The name carries the pid, so two clients on one machine cannot collide.
	if !strings.Contains(filepath.Base(path), ".") {
		t.Fatalf("the file name does not separate the clients: %s", path)
	}

	writeCapabilitiesDebug(&HostCapabilities{TerminalName: "ghostty", CellWidth: 9, CellHeight: 18})
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no capabilities log written: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("capabilities log mode is %04o, want 0600", perm)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "ghostty") {
		t.Fatalf("the log does not hold what the probe found:\n%s", body)
	}
}
