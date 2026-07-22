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

// TestDetectionShowsPassiveIndicatorOnceAndRunsNothing is the central stage-1
// assertion: entering a directory with a tape surfaces exactly one passive
// banner and a dock badge, and creates no window, session, or layout.
func TestDetectionShowsPassiveIndicatorOnceAndRunsNothing(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	notifBefore := len(m.Notifications)
	windowsBefore := len(m.Windows)
	workspacesBefore := len(m.WorkspaceLayouts)

	drive(t, m, "focused", dir)

	// Exactly one passive banner.
	if got := len(m.Notifications) - notifBefore; got != 1 {
		t.Fatalf("notifications added = %d, want 1", got)
	}
	if msg := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(msg, "untrusted") {
		t.Fatalf("banner = %q, want it to mention untrusted", msg)
	}

	// Passive indicator active and untrusted; dock badge present.
	status, ok := m.tapeIndicatorStatus()
	if !ok || status != trust.StatusUntrusted {
		t.Fatalf("indicator = (%v, %v), want (untrusted, true)", status, ok)
	}
	if badge := m.tapeDockBadge(); !strings.Contains(badge, "tape") {
		t.Fatalf("dock badge = %q, want a tape badge", badge)
	}

	// Nothing executed: no new window, no session, no layout built.
	if len(m.Windows) != windowsBefore {
		t.Fatalf("windows = %d, want unchanged %d (detection must not create windows)", len(m.Windows), windowsBefore)
	}
	if len(m.WorkspaceLayouts) != workspacesBefore {
		t.Fatalf("workspace layouts changed; detection must not build a layout")
	}

	// Re-entering the same directory this run repeats no banner (dedupe).
	drive(t, m, "focused", dir)
	if got := len(m.Notifications) - notifBefore; got != 1 {
		t.Fatalf("notifications after re-entry = %d, want still 1 (deduped)", got)
	}
}

// TestDetectionOffModeDoesNothing: with autorun off there is no evaluation, no
// banner, and no indicator, even when a tape is present.
func TestDetectionOffModeDoesNothing(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunOff)
	// Guard against an env override flipping the mode during the test run.
	t.Setenv("TUIOS_TAPE_AUTORUN", config.TapeAutorunOff)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	if m.tapeAutorunEnabled() {
		t.Fatal("autorun should be disabled in off mode")
	}

	notifBefore := len(m.Notifications)
	if cmd := m.onCwdChange(CwdChangedMsg{WindowID: "focused", Cwd: dir}); cmd != nil {
		t.Fatal("onCwdChange should return no command in off mode")
	}
	drive(t, m, "focused", dir)

	if len(m.Notifications) != notifBefore {
		t.Fatal("off mode produced a notification")
	}
	if _, ok := m.tapeIndicatorStatus(); ok {
		t.Fatal("off mode produced an indicator")
	}
}

// TestDetectionIgnoresBackgroundWindow: only the focused window triggers
// detection; a background window changing directory is ignored.
func TestDetectionIgnoresBackgroundWindow(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	notifBefore := len(m.Notifications)
	if cmd := m.onCwdChange(CwdChangedMsg{WindowID: "not-focused", Cwd: dir}); cmd != nil {
		t.Fatal("a background window must not schedule detection")
	}
	if len(m.Notifications) != notifBefore {
		t.Fatal("a background window produced a notification")
	}
	if _, ok := m.tapeIndicatorStatus(); ok {
		t.Fatal("a background window produced an indicator")
	}
}

// TestDetectionTrustedTape: a trusted, unedited tape reports trusted in both the
// banner and the indicator, and still runs nothing in stage 1.
func TestDetectionTrustedTape(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Session \"proj\"\nType \"nvim .\" Enter\n")

	// Trust it up front, as a prior run would have.
	res, err := store.Check(filepath.Join(dir, trust.TapeFileName))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := store.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	windowsBefore := len(m.Windows)
	drive(t, m, "focused", dir)

	status, ok := m.tapeIndicatorStatus()
	if !ok || status != trust.StatusTrusted {
		t.Fatalf("indicator = (%v, %v), want (trusted, true)", status, ok)
	}
	if msg := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(msg, "trusted") {
		t.Fatalf("banner = %q, want it to mention trusted", msg)
	}
	if len(m.Windows) != windowsBefore {
		t.Fatal("a trusted tape must still not run anything in stage 1")
	}
}

// TestDetectionDeniedTapeIsSilent: a denied path produces no banner and no
// indicator.
func TestDetectionDeniedTapeIsSilent(t *testing.T) {
	m, store := newDetectOS(t, config.TapeAutorunAsk)
	dir := tapeDir(t, "Type \"echo hi\" Enter\n")

	res, err := store.Check(filepath.Join(dir, trust.TapeFileName))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := store.Deny(res.Path); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	notifBefore := len(m.Notifications)
	drive(t, m, "focused", dir)

	if len(m.Notifications) != notifBefore {
		t.Fatal("a denied tape produced a notification")
	}
	if _, ok := m.tapeIndicatorStatus(); ok {
		t.Fatal("a denied tape produced an indicator")
	}
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

// TestDetectionClearsIndicatorLeavingProject: cd'ing from a tape directory to a
// plain one clears the passive indicator.
func TestDetectionClearsIndicatorLeavingProject(t *testing.T) {
	m, _ := newDetectOS(t, config.TapeAutorunAsk)
	withTape := tapeDir(t, "Type \"echo hi\" Enter\n")
	plain := t.TempDir()

	drive(t, m, "focused", withTape)
	if _, ok := m.tapeIndicatorStatus(); !ok {
		t.Fatal("expected an indicator inside the project")
	}

	drive(t, m, "focused", plain)
	if _, ok := m.tapeIndicatorStatus(); ok {
		t.Fatal("indicator should clear on leaving the project directory")
	}
}

// TestCwdCallbackDeliversToChannel proves the wiring from a window's OSC 7
// callback (set by setupCwdWatch) to the Update loop's channel.
func TestCwdCallbackDeliversToChannel(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	w := &terminal.Window{ID: "w1", Workspace: 1}
	m.setupCwdWatch(w)
	if w.CwdFunc == nil {
		t.Fatal("setupCwdWatch did not install a CwdFunc")
	}

	w.CwdFunc("file://localhost/home/user/project")

	select {
	case msg := <-m.PendingCwdChange:
		if msg.WindowID != "w1" || msg.Cwd != "file://localhost/home/user/project" {
			t.Fatalf("channel got %+v, want the window id and cwd", msg)
		}
	default:
		t.Fatal("cwd change was not delivered to the channel")
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
