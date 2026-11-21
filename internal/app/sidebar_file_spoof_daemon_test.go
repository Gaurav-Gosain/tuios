//go:build unix

package app

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func standInShell(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("sleep", "120")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start the stand-in shell: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

func daemonSpoofPane(t *testing.T, sayDir string, shellPID int) *OS {
	t.Helper()
	requireShellCWD(t)
	m := sidebarTestOS(t, 120, 40, "left")
	m.Settings.SidebarFileActions = true
	m.Settings.SidebarFileDelete = config.SidebarFileDeletePermanent
	t.Cleanup(func() {
		m.Settings.SidebarFileActions = true
		m.Settings.SidebarFileDelete = config.SidebarFileDeleteTrash
	})

	w := &terminal.Window{ID: "daemonpane1", Width: 40, Height: 20, Workspace: 1, PTYID: "pty-1"}
	m.updateWindowFromState(w, &session.WindowState{
		ID: "daemonpane1", PTYID: "pty-1", CustomName: "shell",
		Width: 40, Height: 20, Workspace: 1, ShellPID: shellPID,
	})
	m.Windows = []*terminal.Window{w}
	m.FocusedWindow = 0
	m.filesView.Show = 1

	m.recordWindowCwd("daemonpane1", "file://localhost"+sayDir)
	syncFiles(t, m)
	m.SidebarFocused = true
	railLines(t, m)
	return m
}

// TestADaemonPaneCannotSteerADeleteWithOSC7 is the gap this closes. Every pane
// in the default deployment is this one, and before the daemon sent the pid it
// deleted the file.
func TestADaemonPaneCannotSteerADeleteWithOSC7(t *testing.T) {
	real, victim, bait := spoofDirs(t)
	m := daemonSpoofPane(t, victim, standInShell(t, real))

	if got := m.FileViewDir(); got != victim {
		t.Fatalf("the listing did not follow OSC 7: %q, want %q", got, victim)
	}
	if !m.FileViewSpoofed() {
		t.Fatal("the pane said it was somewhere it is not and the listing believed it")
	}
	if m.FileActionsOn() {
		t.Fatal("the file actions are live on a folder the pane made up")
	}
	if !cursorToFile(m, "keepme.txt") {
		t.Fatalf("no row for keepme.txt; the listing drew %v", entryNames(m))
	}

	m.SidebarFileDelete(true)
	if m.FileConfirmOpen() {
		t.Fatal("the delete raised its confirmation anyway")
	}
	if _, err := os.Lstat(bait); err != nil {
		t.Fatalf("the file went: %v", err)
	}
}

// TestADaemonPaneWithNoPidKeepsItsFileActions holds the rule the corroboration
// is built on: unknown is unknown. A daemon on macOS reads no working directory
// for the pid it sends, a daemon with no live process for a pane sends zero,
// and a daemon older than the field sends nothing at all. All three land here,
// and none of them may take the file actions away.
func TestADaemonPaneWithNoPidKeepsItsFileActions(t *testing.T) {
	_, victim, bait := spoofDirs(t)
	m := daemonSpoofPane(t, victim, 0)

	if m.FileViewSpoofed() {
		t.Fatal("a pane nobody could check was called a liar")
	}
	if !m.FileActionsOn() {
		t.Fatal("a pane nobody could check lost its file actions")
	}
	if !cursorToFile(m, "keepme.txt") {
		t.Fatalf("no row for keepme.txt; the listing drew %v", entryNames(m))
	}
	m.SidebarFileDelete(true)
	if !m.FileConfirmOpen() {
		t.Fatal("the delete raised no confirmation")
	}
	runOp(t, m, m.FileConfirmActivate(fileConfirmRowGo))
	if _, err := os.Lstat(bait); err == nil {
		t.Fatal("the delete did nothing on a pane that is allowed to act")
	}
}

// TestASyncDoesNotClearALocalPanesShellPgid is the regression the adoption
// could have caused. A locally spawned pane records its own shell at spawn
// time; a daemon has no process for one and sends zero. Copying that zero over
// would take the corroboration off the one pane that already had it.
func TestASyncDoesNotClearALocalPanesShellPgid(t *testing.T) {
	requireShellCWD(t)
	real, victim, bait := spoofDirs(t)
	pid := standInShell(t, real)

	m := sidebarTestOS(t, 120, 40, "left")
	m.Settings.SidebarFileActions = true
	m.Settings.SidebarFileDelete = config.SidebarFileDeletePermanent
	t.Cleanup(func() {
		m.Settings.SidebarFileActions = true
		m.Settings.SidebarFileDelete = config.SidebarFileDeleteTrash
	})

	w := &terminal.Window{ID: "localpane1", Width: 40, Height: 20, Workspace: 1, ShellPgid: pid}
	m.updateWindowFromState(w, &session.WindowState{
		ID: "localpane1", CustomName: "shell",
		Width: 40, Height: 20, Workspace: 1, ShellPID: 0,
	})
	if w.ShellPgid != pid {
		t.Fatalf("a sync cleared a local pane's shell pgid: %d, want %d", w.ShellPgid, pid)
	}

	m.Windows = []*terminal.Window{w}
	m.FocusedWindow = 0
	m.filesView.Show = 1
	m.recordWindowCwd("localpane1", "file://localhost"+victim)
	syncFiles(t, m)
	m.SidebarFocused = true
	railLines(t, m)

	if !m.FileViewSpoofed() {
		t.Fatal("the local pane's spoof went unnoticed after a sync")
	}
	if !cursorToFile(m, "keepme.txt") {
		t.Fatalf("no row for keepme.txt; the listing drew %v", entryNames(m))
	}
	m.SidebarFileDelete(true)
	if _, err := os.Lstat(bait); err != nil {
		t.Fatalf("the file went: %v", err)
	}
}

// TestAFreshDaemonPaneAdoptsItsShellPid covers the other half of the client's
// sync. A pane that already exists takes its pid through updateWindowFromState;
// a pane this client is seeing for the first time, and every pane on a restore,
// takes it through adoptWindowState. Both have to, or a pane is uncheckable
// until the next thing about it changes.
func TestAFreshDaemonPaneAdoptsItsShellPid(t *testing.T) {
	requireShellCWD(t)
	real, victim, bait := spoofDirs(t)
	pid := standInShell(t, real)

	m := sidebarTestOS(t, 120, 40, "left")
	m.Settings.SidebarFileActions = true
	m.Settings.SidebarFileDelete = config.SidebarFileDeletePermanent
	t.Cleanup(func() {
		m.Settings.SidebarFileActions = true
		m.Settings.SidebarFileDelete = config.SidebarFileDeleteTrash
	})

	w := &terminal.Window{ID: "freshpane01", Width: 40, Height: 20, Workspace: 1, PTYID: "pty-1"}
	adoptWindowState(w, session.WindowState{
		ID: "freshpane01", PTYID: "pty-1", CustomName: "shell",
		Width: 40, Height: 20, Workspace: 1, ShellPID: pid,
	})
	if w.ShellPgid != pid {
		t.Fatalf("a fresh pane took shell pgid %d, want %d", w.ShellPgid, pid)
	}

	m.Windows = []*terminal.Window{w}
	m.FocusedWindow = 0
	m.filesView.Show = 1
	m.recordWindowCwd("freshpane01", "file://localhost"+victim)
	syncFiles(t, m)
	m.SidebarFocused = true
	railLines(t, m)

	if !m.FileViewSpoofed() {
		t.Fatal("the spoof went unnoticed on a pane adopted from a sync")
	}
	if !cursorToFile(m, "keepme.txt") {
		t.Fatalf("no row for keepme.txt; the listing drew %v", entryNames(m))
	}
	m.SidebarFileDelete(true)
	if _, err := os.Lstat(bait); err != nil {
		t.Fatalf("the file went: %v", err)
	}
}

// TestAnHonestDaemonPaneKeepsItsFileActions is the other half. The pid the
// daemon sends must not cost a truthful pane anything.
func TestAnHonestDaemonPaneKeepsItsFileActions(t *testing.T) {
	_, victim, bait := spoofDirs(t)
	m := daemonSpoofPane(t, victim, standInShell(t, victim))

	if m.FileViewSpoofed() {
		t.Fatal("a pane telling the truth was called a liar")
	}
	if !m.FileActionsOn() {
		t.Fatal("a pane telling the truth lost its file actions")
	}
	if !cursorToFile(m, "keepme.txt") {
		t.Fatalf("no row for keepme.txt; the listing drew %v", entryNames(m))
	}
	m.SidebarFileDelete(true)
	runOp(t, m, m.FileConfirmActivate(fileConfirmRowGo))
	if _, err := os.Lstat(bait); err == nil {
		t.Fatal("an honest daemon pane could not delete a file")
	}
}
