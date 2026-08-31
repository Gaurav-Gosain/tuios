package session

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withLogFile points the file sink at a fresh file for one test and restores
// whatever was installed before.
func withLogFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.log")
	openDaemonLogFile(path)
	t.Cleanup(closeDaemonLogFile)
	return path
}

// readLog returns the sink file's contents.
func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestStdlibLogReachesRing is item 1's claim: a plain log.Printf, with no call
// site change at all, becomes an entry `tuios logs` can read.
func TestStdlibLogReachesRing(t *testing.T) {
	restoreStdlibLogger(t)
	ClearLogBuffer()

	log.SetFlags(0)
	log.SetOutput(&ringWriter{})
	log.Printf("harness manifest %s: %v", "demo.toml", os.ErrNotExist)

	entries := GetLogEntries(0)
	if len(entries) != 1 {
		t.Fatalf("stdlib log produced %d ring entries, want 1", len(entries))
	}
	if entries[0].Level != "warn" {
		t.Fatalf("stdlib line recorded at %q, want warn", entries[0].Level)
	}
	if got := entries[0].Message; got != "harness manifest demo.toml: file does not exist" {
		t.Fatalf("ring entry is %q", got)
	}
}

// TestStdlibLogKeepsPercentSigns guards the trap in routing an arbitrary writer
// into a logger that formats: a message carrying a per-cent sign must survive
// verbatim, not be read as a verb.
func TestStdlibLogKeepsPercentSigns(t *testing.T) {
	restoreStdlibLogger(t)
	ClearLogBuffer()

	log.SetFlags(0)
	log.SetOutput(&ringWriter{})
	log.Printf("disk is 90%% full")

	entries := GetLogEntries(0)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if got := entries[0].Message; got != "disk is 90% full" {
		t.Fatalf("ring entry is %q, want %q", got, "disk is 90% full")
	}
}

// TestForegroundEchoesToStderr checks the one thing the background daemon must
// not do and the foreground one must: put the line in front of the operator.
func TestForegroundEchoesToStderr(t *testing.T) {
	restoreStdlibLogger(t)
	ClearLogBuffer()

	var buf bytes.Buffer
	log.SetFlags(0)
	log.SetOutput(&ringWriter{echo: &buf})
	log.Print("accept error: too many open files")

	if !strings.Contains(buf.String(), "accept error: too many open files") {
		t.Fatalf("foreground echo is %q", buf.String())
	}

	buf.Reset()
	log.SetOutput(&ringWriter{})
	log.Print("second line")
	if buf.Len() != 0 {
		t.Fatalf("background writer echoed %q", buf.String())
	}
	if n := len(GetLogEntries(0)); n != 2 {
		t.Fatalf("ring holds %d entries, want 2 regardless of echo", n)
	}
}

// TestFileSinkKeepsErrorsAtAnyLevel is item 2's reason for existing: the ring
// dies with the process, so the file has to hold an error even though nobody
// raised the level first.
func TestFileSinkKeepsErrorsAtAnyLevel(t *testing.T) {
	restoreLevel(t, DebugOff)
	path := withLogFile(t)

	LogError("pty spawn failed for %s", "win-1")

	if got := readLog(t, path); !strings.Contains(got, "pty spawn failed for win-1") {
		t.Fatalf("error missing from the log file at level off:\n%s", got)
	}
}

// TestFileSinkHonoursLevelAboveBasic is the other half: detail above the
// always-on tier is written only when the level asks for it, so an idle daemon
// does not fill the disk with protocol traffic.
func TestFileSinkHonoursLevelAboveBasic(t *testing.T) {
	restoreLevel(t, DebugOff)
	path := withLogFile(t)

	ProtocolLog(DebugMessages, "[recv] Input (gob) 12 bytes")
	if got := readLog(t, path); strings.Contains(got, "[recv] Input") {
		t.Fatalf("message-level line reached the file at level off:\n%s", got)
	}

	SetDebugLevel(DebugMessages)
	ProtocolLog(DebugMessages, "[recv] Resize (gob) 8 bytes")
	if got := readLog(t, path); !strings.Contains(got, "[recv] Resize") {
		t.Fatalf("message-level line missing from the file at level messages:\n%s", got)
	}
}

// TestFileSinkHeaderStatesThePrivacyRule checks the header a reader of the file
// lands on. The rule belongs on the artefact, not only in the documentation.
func TestFileSinkHeaderStatesThePrivacyRule(t *testing.T) {
	restoreLevel(t, DebugOff)
	path := withLogFile(t)

	got := readLog(t, path)
	if !strings.Contains(got, "tuios daemon log started") {
		t.Fatalf("no start header:\n%s", got)
	}
	if !strings.Contains(got, "Levels verbose and trace also record pane content, window titles and paths.") {
		t.Fatalf("header does not state the privacy rule:\n%s", got)
	}
}

// TestFileSinkRotatesAtTheCap pins the bound: past the cap the daemon moves the
// file aside and starts a new one, and it keeps exactly one old generation.
func TestFileSinkRotatesAtTheCap(t *testing.T) {
	restoreLevel(t, DebugOff)
	path := withLogFile(t)

	// The cap is pinned here rather than read from the constant, so raising the
	// constant is a failure rather than a longer test.
	const wantCap = 5 << 20
	if daemonLogMaxBytes != wantCap {
		t.Fatalf("the cap is %d bytes, want %d (5 MiB)", daemonLogMaxBytes, wantCap)
	}

	line := strings.Repeat("x", 4096)
	for written := 0; written < wantCap+8192; written += len(line) {
		LogError("%s", line)
	}

	oldStat, err := os.Stat(path + ".old")
	if err != nil {
		t.Fatalf("no rotated file after passing the cap: %v", err)
	}
	if oldStat.Size() < wantCap {
		t.Fatalf("rotated at %d bytes, want at least the %d byte cap", oldStat.Size(), wantCap)
	}
	liveStat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no live file after rotation: %v", err)
	}
	if liveStat.Size() >= wantCap {
		t.Fatalf("live file is %d bytes after rotation, want a fresh one", liveStat.Size())
	}
	if _, err := os.Stat(path + ".old.old"); err == nil {
		t.Fatal("a second generation was kept; the sink holds one")
	}
}

// TestFileSinkSilentWithoutADaemon checks that a client process, which never
// installs the sink, writes no file at all.
func TestFileSinkSilentWithoutADaemon(t *testing.T) {
	restoreLevel(t, DebugOff)
	closeDaemonLogFile()

	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	LogError("this must not open a file")

	if _, err := os.Stat(filepath.Join(dir, "tuios", "daemon.log")); !os.IsNotExist(err) {
		t.Fatalf("a process that never installed the sink wrote a log file (err %v)", err)
	}
}

// TestDefaultDaemonLogPathFollowsTheStateDir keeps the file where every other
// piece of tuios state lives, which is also where the CLI help says to look.
func TestDefaultDaemonLogPathFollowsTheStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	want := filepath.Join(dir, "tuios", "daemon.log")
	if got := DefaultDaemonLogPath(); got != want {
		t.Fatalf("DefaultDaemonLogPath() = %q, want %q", got, want)
	}
}

// restoreStdlibLogger puts the process-wide standard logger back after a test
// has pointed it at a ring writer.
func restoreStdlibLogger(t *testing.T) {
	t.Helper()
	flags, prefix := log.Flags(), log.Prefix()
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	})
}

// restoreLevel sets the debug level for one test and puts the old one back.
func restoreLevel(t *testing.T, level DebugLevel) {
	t.Helper()
	previous := GetDebugLevel()
	SetDebugLevel(level)
	t.Cleanup(func() { SetDebugLevel(previous) })
}
