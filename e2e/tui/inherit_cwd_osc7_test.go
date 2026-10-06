package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// computerName is the NetBIOS name the test gives the daemon. It is not the
// host name, as on a Windows machine whose name is longer than 15 characters.
const computerName = "TUIOS-E2E-PC"

// TestNewWindowInheritsTheOSC7Folder is issue #491. On native Windows the
// daemon cannot read a shell's folder from its process, so a new window
// started in the daemon's own folder, even with the focused pane's shell
// reporting its folder over OSC 7 at every prompt.
//
// TUIOS_E2E_NO_PROCESS_CWD makes every process read fail, as it does on
// Windows. The pane's shell is /bin/sh, which reports nothing by itself, so
// the only thing that says where it is is the OSC 7 report the test prints.
// Four reports are checked, each from the pane that has the focus:
//
//   - one that names this machine by its host name in capitals. It is the
//     positive half: the fixture and the report work.
//   - one that names this machine by COMPUTERNAME, which PowerShell reports
//     and which can differ from the host name Go reads. It was read as
//     another machine and dropped.
//   - one that names a folder that does not exist. It must not be
//     inherited: the window starts in the session's start folder.
//   - an OSC 9;9 report with a quoted path, which Windows Terminal asks
//     PowerShell to send.
//
// How this could pass wrongly: the new window could start in the right folder
// because the session or the daemon started there. The daemon runs where the
// suite runs, the session starts in base/start, and each pane moves with cd,
// so only the report can name the folder.
func TestNewWindowInheritsTheOSC7Folder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	proj := filepath.Join(base, "proj")
	other := filepath.Join(base, "other")
	start := filepath.Join(base, "start")
	nine := filepath.Join(base, "nine")
	for _, dir := range []string{proj, other, start, nine} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	host = strings.ToUpper(host)

	env := []string{"TUIOS_E2E=1", "TUIOS_E2E_NO_PROCESS_CWD=1", "COMPUTERNAME=" + computerName}
	if out, err := tuiosCLIEnv(t, base, env, "new", "osc", "--detach", "--cwd", start); err != nil {
		t.Fatalf("create osc: %v: %s", err, out)
	}
	first := firstWindow(t, base, "osc").ID

	report := func(window, host, dir string) {
		t.Helper()
		line := `printf '\033]7;file://` + host + `%s\033\\' '` + dir + "'\n"
		if err := paneSend(base, "osc", window, line); err != nil {
			t.Fatalf("send the OSC 7 report: %v", err)
		}
	}
	cd := func(window, dir string) {
		t.Helper()
		if err := paneSend(base, "osc", window, "cd '"+dir+"'\n"); err != nil {
			t.Fatalf("cd into %s: %v", dir, err)
		}
	}

	cd(first, proj)
	report(first, host, proj)
	waitAnnounced(t, base, "osc", first, proj)
	second := newWindowIn(t, base, "osc")
	if got := paneFolder(t, base, "osc", second); got != proj {
		t.Fatalf("ASSERTION: a new window started in %q, want %q, the folder the focused pane reported over OSC 7", got, proj)
	}

	cd(second, other)
	report(second, computerName, other)
	waitAnnounced(t, base, "osc", second, other)
	third := newWindowIn(t, base, "osc")
	if got := paneFolder(t, base, "osc", third); got != other {
		t.Fatalf("ASSERTION: a new window started in %q, want %q, the folder the focused pane reported with host %s", got, other, computerName)
	}

	gone := filepath.Join(base, "gone")
	report(third, host, gone)
	waitAnnounced(t, base, "osc", third, gone)
	fourth := newWindowIn(t, base, "osc")
	if got := paneFolder(t, base, "osc", fourth); got != start {
		t.Fatalf("ASSERTION: a new window started in %q after the focused pane reported a missing folder, want the session's start folder %q", got, start)
	}

	// OSC 9;9, the report Windows Terminal asks PowerShell to send. Its path
	// has no host and is quoted.
	cd(fourth, nine)
	if err := paneSend(base, "osc", fourth, `printf '\033]9;9;"%s"\033\\' '`+nine+"'\n"); err != nil {
		t.Fatalf("send the OSC 9;9 report: %v", err)
	}
	waitAnnounced(t, base, "osc", fourth, nine)
	fifth := newWindowIn(t, base, "osc")
	if got := paneFolder(t, base, "osc", fifth); got != nine {
		t.Fatalf("ASSERTION: a new window started in %q, want %q, the folder the focused pane reported over OSC 9;9", got, nine)
	}
}

// TestNewWindowPrefersTheShellsProcessFolder is the review's regression for
// #491: OSC 7 can be spoofed, so it must not win over the shell's process.
// The shell is /bin/sh, which has no OSC 7 hooks. A program prints a report
// for a folder that exists, and the person then runs cd into another one.
// The shell never corrects the report. A new window must start where the
// shell is, not where the report says.
//
// The positive half is in the same fixture: the daemon must hold the report,
// or the test would not prove that the process wins over it.
func TestNewWindowPrefersTheShellsProcessFolder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	fake := filepath.Join(base, "fake")
	realDir := filepath.Join(base, "real")
	for _, dir := range []string{fake, realDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := tuiosCLI(t, base, "new", "spoof", "--detach"); err != nil {
		t.Fatalf("create spoof: %v: %s", err, out)
	}
	first := firstWindow(t, base, "spoof").ID

	line := `printf '\033]7;file://localhost%s\033\\' '` + fake + "'\n"
	if err := paneSend(base, "spoof", first, line); err != nil {
		t.Fatalf("send the OSC 7 report: %v", err)
	}
	waitAnnounced(t, base, "spoof", first, fake)
	if err := paneSend(base, "spoof", first, "cd '"+realDir+"'\n"); err != nil {
		t.Fatalf("cd into %s: %v", realDir, err)
	}
	if got := paneFolder(t, base, "spoof", first); got != realDir {
		t.Fatalf("the shell is in %q after cd, want %q: the fixture does not hold", got, realDir)
	}

	second := newWindowIn(t, base, "spoof")
	if got := paneFolder(t, base, "spoof", second); got != realDir {
		t.Fatalf("ASSERTION: a new window started in %q, want %q, the folder of the shell's process. The pane's OSC 7 report named %q", got, realDir, fake)
	}
}

// newWindowIn opens a window in session and returns its id.
func newWindowIn(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosOut(base, "new-window", "-s", session, "--print-id")
	if err != nil {
		t.Fatalf("new-window in %s: %v: %s", session, err, out)
	}
	return strings.TrimSpace(out)
}

// paneFolder asks the shell in window where it is and returns its answer. The
// answer goes to a file, since a long path wraps in a narrow pane.
func paneFolder(t *testing.T, base, session, window string) string {
	t.Helper()
	file := filepath.Join(base, "pwd-"+window)
	if err := paneSend(base, session, window, "pwd > '"+file+"'\n"); err != nil {
		t.Fatalf("ask the pane for its folder: %v", err)
	}
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(file); err == nil && strings.HasSuffix(string(data), "\n") {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(100 * time.Millisecond)
	}
	out, _ := tuiosOut(base, "capture-pane", "-s", session, "-w", window)
	t.Fatalf("the pane %s never said where it is:\n%s", window, out)
	return ""
}

// waitAnnounced waits until list-windows reports window in dir. With process
// reads off, only the pane's OSC 7 report can put it there, and list-windows
// shows a reported folder whether or not it exists.
func waitAnnounced(t *testing.T, base, session, window, dir string) {
	t.Helper()
	var got string
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosOut(base, "list-windows", "-s", session, "--json")
		if err == nil {
			var res struct {
				Windows []struct {
					ID  string `json:"window_id"`
					Cwd string `json:"cwd"`
				} `json:"windows"`
			}
			if json.Unmarshal([]byte(out), &res) == nil {
				for _, w := range res.Windows {
					if w.ID == window {
						got = w.Cwd
					}
				}
				if got == dir {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the daemon reports pane %s in %q, want %q from its OSC 7 report", window, got, dir)
}
