package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// newDetectOS builds an OS in the given autorun mode with a trust store backed
// by a temp file (so the test never touches the real XDG location) and a single
// focused window whose ID is "focused".
func newDetectOS(t *testing.T, mode string) (*OS, *trust.Store) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Tape.Autorun = mode
	m := NewOS(OSOptions{UserConfig: cfg})

	store, err := trust.LoadFromPath(filepath.Join(t.TempDir(), "tape-trust.toml"))
	if err != nil {
		t.Fatalf("trust store: %v", err)
	}
	m.tapeDetect.store = store
	m.tapeDetect.storeLoaded = true

	w := &terminal.Window{ID: "focused", Workspace: 1}
	m.Windows = append(m.Windows, w)
	m.FocusedWindow = 0
	m.CurrentWorkspace = 1
	return m, store
}

// tapeDir creates a temp directory containing a .tuios.tape and returns the dir.
func tapeDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, trust.TapeFileName), []byte(content), 0o600); err != nil {
		t.Fatalf("writing tape: %v", err)
	}
	return dir
}

// drive simulates the focused window entering dir and the debounce elapsing,
// exercising the real onCwdChange filter and evaluation path.
func drive(t *testing.T, m *OS, windowID, dir string) {
	t.Helper()
	m.onCwdChange(CwdChangedMsg{WindowID: windowID, Cwd: dir})
	m.handleTapeDebounce(m.tapeDetect.gen)
}

// TestDetectionIneligibleTape: a world-writable tape is reported as ignored, not
// offered for trust.
func TestDetectionIneligibleTape(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root defeats the ownership/permission checks")
	}
	m, _ := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")
	if err := os.Chmod(filepath.Join(dir, trust.TapeFileName), 0o666); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	drive(t, m, "focused", dir)

	status, ok := m.tapeIndicatorStatus()
	if !ok || status != trust.StatusIneligible {
		t.Fatalf("indicator = (%v, %v), want (ineligible, true)", status, ok)
	}
	if msg := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(msg, "ignored") {
		t.Fatalf("banner = %q, want it to say the tape was ignored", msg)
	}
}

// TestLocalCwdPathParsing covers the OSC 7 payload parsing, including the remote
// host rejection that keeps tuios from scanning files it cannot read.
func TestLocalCwdPathParsing(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantOK  bool
		comment string
	}{
		{"file://localhost/home/u/p", "/home/u/p", true, "localhost URI"},
		{"file:///home/u/p", "/home/u/p", true, "empty host URI"},
		{"/home/u/p", "/home/u/p", true, "bare absolute path"},
		{"file://remotebox/home/u/p", "", false, "remote host is ignored"},
		{"relative/path", "", false, "relative bare path is rejected"},
		{"", "", false, "empty payload"},
	}
	for _, c := range cases {
		got, ok := localCwdPath(c.raw)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("%s: localCwdPath(%q) = (%q, %v), want (%q, %v)", c.comment, c.raw, got, ok, c.want, c.wantOK)
		}
	}
}
