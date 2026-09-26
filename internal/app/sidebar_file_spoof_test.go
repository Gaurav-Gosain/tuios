//go:build unix

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// requireShellCWD skips when this platform cannot read another process's
// working directory, which is the whole basis of the guard these tests assert.
//
// cwdIsSpoofed compares the directory a pane claims over OSC 7 against the one
// its process group is really in, and terminal.ShellCWD answers that second
// half through /proc/<pgid>/cwd on Linux and proc_pidinfo on darwin. On any
// other platform ShellCWD reports "nobody looked", cwdIsSpoofed fails open, and
// the six file actions stay live on whatever directory a pane names. Every test
// in this file then passes or fails for a reason that has nothing to do with
// what it claims: the ones asserting a refusal fail, and the ones asserting the
// actions survive pass because the guard never ran.
//
// The gate is the capability rather than the GOOS, so these light up by
// themselves on the day ShellCWD learns to answer somewhere else.
func requireShellCWD(t *testing.T) {
	t.Helper()
	probe := exec.Command("sleep", "120")
	probe.Dir = t.TempDir()
	if err := probe.Start(); err != nil {
		t.Fatalf("could not start the probe process: %v", err)
	}
	defer func() {
		_ = probe.Process.Kill()
		_, _ = probe.Process.Wait()
	}()
	if _, ok := terminal.ShellCWD(probe.Process.Pid); !ok {
		t.Skip("this platform cannot read a process's working directory, so the " +
			"OSC 7 spoof guard never fires: see terminal.ShellCWD")
	}
}

// spoofPane builds a client with one local-PTY-shaped pane whose shell really
// sits in realDir, and makes that pane print an OSC 7 naming sayDir instead.
//
// The shell is a real process, because /proc is the only thing that can
// corroborate one and a fake pgid corroborates nothing. It is started in
// realDir with its own process group, so its pgid is its pid.
func spoofPane(t *testing.T, realDir, sayDir string) *OS {
	t.Helper()
	requireShellCWD(t)
	cmd := exec.Command("sleep", "120")
	cmd.Dir = realDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start the stand-in shell: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	m := sidebarTestOS(t, 120, 40, "left")
	m.Settings.SidebarFileActions = true
	m.Settings.SidebarFileDelete = config.SidebarFileDeletePermanent
	t.Cleanup(func() {
		m.Settings.SidebarFileActions = true
		m.Settings.SidebarFileDelete = config.SidebarFileDeleteTrash
	})
	m.Windows = []*terminal.Window{{
		ID: "spoofpane01", CustomName: "shell", Width: 40, Height: 20,
		Workspace: 1, ShellPgid: cmd.Process.Pid,
	}}
	m.FocusedWindow = 0
	m.filesView.Show = 1

	// The whole attack surface, in one call: OSC 7 is a string the pane printed.
	m.recordWindowCwd("spoofpane01", "file://localhost"+sayDir)
	syncFiles(t, m)
	m.SidebarFocused = true
	railLines(t, m)
	return m
}

// syncFiles runs the sync the update loop runs after every message and applies
// the listing it asks for.
func syncFiles(t *testing.T, m *OS) {
	t.Helper()
	cmd := m.FilesSyncCmd()
	if cmd == nil {
		t.Fatal("the sync asked for no listing")
	}
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
}

// spoofDirs makes a real folder for the shell to sit in and a victim folder
// with one file in it, and returns both plus the file.
func spoofDirs(t *testing.T) (real, victim, bait string) {
	t.Helper()
	root := t.TempDir()
	real = filepath.Join(root, "real")
	victim = filepath.Join(root, "victim")
	for _, d := range []string{real, victim} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bait = filepath.Join(victim, "keepme.txt")
	if err := os.WriteFile(bait, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	return real, victim, bait
}

// TestEveryFileActionRefusesASpoofedFolder walks the six, because one gate
// missed is the whole hole again. Nothing under the victim folder may change.
func TestEveryFileActionRefusesASpoofedFolder(t *testing.T) {
	real, victim, bait := spoofDirs(t)

	for _, tc := range []struct {
		name string
		run  func(m *OS)
	}{
		{"create", func(m *OS) { m.SidebarFileCreate() }},
		{"rename", func(m *OS) { m.SidebarFileRename() }},
		{"delete", func(m *OS) { m.SidebarFileDelete(false) }},
		{"delete for good", func(m *OS) { m.SidebarFileDelete(true) }},
		{"copy", func(m *OS) { m.SidebarFileCopy() }},
		{"cut", func(m *OS) { m.SidebarFileCut() }},
		{"paste", func(m *OS) {
			m.fileClip = fileClipboard{Paths: []string{bait}}
			if cmd := m.SidebarFilePaste(); cmd != nil {
				t.Error("paste answered with a command, so it was going to run")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := spoofPane(t, real, victim)
			if !cursorToFile(m, "keepme.txt") {
				t.Fatalf("no row for keepme.txt; the listing drew %v", entryNames(m))
			}
			before := dirNames(t, victim)
			tc.run(m)
			if m.FilePromptOpen() {
				t.Fatal("a dialog opened over a folder the pane made up")
			}
			if got := dirNames(t, victim); got != before {
				t.Fatalf("the folder changed: %q, was %q", got, before)
			}
			if !m.fileClip.Empty() && tc.name != "paste" {
				t.Fatal("the file clipboard took a path out of a folder the pane made up")
			}
		})
	}
}

// dirNames is one folder's contents as a comparable string.
func dirNames(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// TestAnUncorroboratedPaneKeepsItsFileActions is the case that decides whether
// this change is shippable. A pane whose shell nothing can name cannot be
// corroborated, and unknown is not disagreement.
//
// That is every pane on macOS, where there is no /proc to read, and any pane
// whose shell process is gone. It was also every daemon-backed pane until the
// daemon started sending the pid; see sidebar_file_spoof_daemon_test.go.
func TestAnUncorroboratedPaneKeepsItsFileActions(t *testing.T) {
	real, victim, bait := spoofDirs(t)
	m := spoofPane(t, real, victim)

	// The one thing that separates an uncheckable pane from the local one above:
	// nothing ever told the client which process the shell is.
	m.Windows[0].ShellPgid = 0
	m.filesView.Pinned = false
	m.filesView.Want = ""
	m.recordWindowCwd("spoofpane01", "file://localhost"+victim)
	syncFiles(t, m)
	railLines(t, m)

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

// TestASymlinkedPathIsNotADisagreement is the false positive that would make
// this unusable. /proc hands back the resolved path and a shell prints $PWD, so
// the two spell one folder differently as a matter of course.
func TestASymlinkedPathIsNotADisagreement(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("this filesystem does not do symlinks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "keepme.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The shell sits in the resolved folder, which is what /proc will say, and
	// reports the path it walked in through.
	m := spoofPane(t, realDir, link)
	if m.FileViewSpoofed() {
		t.Fatalf("the same folder spelled two ways read as a disagreement: %q", m.FileViewDir())
	}
	if !m.FileActionsOn() {
		t.Fatal("a symlinked path took the file actions away")
	}
}

// TestWalkingIntoAFolderDoesNotLaunderASpoofedOne: the user navigating inside
// the listing is trusted, but a subfolder of a folder the pane made up is still
// the pane's choice.
func TestWalkingIntoAFolderDoesNotLaunderASpoofedOne(t *testing.T) {
	real, victim, _ := spoofDirs(t)
	sub := filepath.Join(victim, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	m := spoofPane(t, real, victim)
	if !m.FileViewSpoofed() {
		t.Fatal("the spoof was not caught to begin with")
	}
	cmd := m.requestFileList(sub, m.filesView.Origin, true)
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the read answered with %T", msg)
	}
	m.HandleFileList(msg)

	if m.FileViewDir() != sub {
		t.Fatalf("the listing did not walk in: %q", m.FileViewDir())
	}
	if !m.FileViewSpoofed() {
		t.Fatal("walking one folder down cleared the verdict")
	}
	if m.FileActionsOn() {
		t.Fatal("the file actions came back one folder down")
	}
}

// TestAnHonestPaneKeepsItsFileActions is the other half: a local pane whose
// shell really is where it says keeps everything.
func TestAnHonestPaneKeepsItsFileActions(t *testing.T) {
	_, victim, bait := spoofDirs(t)
	m := spoofPane(t, victim, victim)

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
		t.Fatal("an honest pane could not delete a file")
	}
}
