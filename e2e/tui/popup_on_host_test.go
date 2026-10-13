package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Popup and scratch entries of [[keybindings.command]], pressed in a session on
// another machine (discussion #643). The popup must open on the machine that
// holds the session, at the size the entry names, and close when its command
// exits.
//
// How these could pass wrongly, written down first:
//   - The command could run on this machine. The far daemon gets a HOME of
//     its own with the script in it, and this machine has no such script, so
//     the marker can only come from the far side, through `~` expanded there.
//   - The popup could open in a session of the same name on this machine.
//     This machine's daemon must hold no session named far.
//   - The popup could open at the default size. Its command reads its own
//     size, which must be well under the 80% by 60% default.
//   - The popup could stay open. It must leave the far daemon's window list.
//   - A refusal could be a generic failure. The toast must name what to add.

// farHomeWith makes a HOME for the far machine, with bin/prj in it.
func farHomeWith(t *testing.T, script string) string {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("mkdir the far bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "prj"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatalf("write the far script: %v", err)
	}
	return home
}

// popupHostClient starts the far daemon with home as its HOME and session far
// in it, and a client here attached to it through host build, with config as
// this machine's key entries.
func popupHostClient(t *testing.T, remote, home, keys string) (string, *tuitest.Terminal) {
	t.Helper()
	return popupHostClientGated(t, remote, home, keys, "")
}

// popupHostClientGated is popupHostClient whose link the file gate cuts,
// with "" for a link that is never cut.
func popupHostClientGated(t *testing.T, remote, home, keys, gate string) (string, *tuitest.Terminal) {
	t.Helper()
	if out, err := tuiosCLIEnv(t, remote, []string{"HOME=" + home}, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	base := t.TempDir()
	ssh := writeFakeSSHTo(t, base, remote)
	if gate != "" {
		ssh = writeGatedFakeSSH(t, base, remote, gate)
	}
	writeOneHostConfig(t, base, tuiosBin)
	path := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
	hosts, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the host config: %v", err)
	}
	if err := os.WriteFile(path, append(hosts, []byte("\n"+keys)...), 0o600); err != nil {
		t.Fatalf("write the key entries: %v", err)
	}
	term := startIn(t, base, startOpts{cols: 160, rows: 40,
		args: []string{"attach", "--host", "build", "far"}, env: []string{"TUIOS_SSH=" + ssh}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
	}, bootTimeout); err != nil {
		t.Fatalf("the client never drew the far session: %v\n%s", err, term.Snapshot())
	}
	return base, term
}

// farRows is the far daemon's list-windows of session far.
func farRows(t *testing.T, remote string) string {
	t.Helper()
	out, _ := tuiosCLI(t, remote, "list-windows", "--json", "-s", "far")
	return out
}

// noLocalFar fails when this machine's daemon holds a session named far.
func noLocalFar(t *testing.T, base string) {
	t.Helper()
	if out, _ := tuiosCLI(t, base, "ls"); strings.Contains(out, "far") {
		t.Fatalf("ASSERTION: this machine's daemon holds a session far, so the entry ran here:\n%s", out)
	}
}

func TestPopupEntryOpensOnTheSessionsMachine(t *testing.T) {
	remote := remoteMachine(t)
	home := farHomeWith(t, "pwd -P > ~/popup-dir; echo FAR-POPUP-$((6*7)) SIZE=$(stty size); sleep 3\n")
	base, term := popupHostClient(t, remote, home, `
[[keybindings.command]]
key = "prefix+alt+o"
type = "popup"
command = "~/bin/prj"
width = "40%"
height = "40%"

[[keybindings.command]]
key = "prefix+alt+y"
type = "scratch"
name = "farnotes"
command = "echo FAR-SCRATCH-$((7*8)); exec sh"
`)
	// The far pane reports a folder on build (OSC 7). The popup starts there.
	farDir := t.TempDir()
	farDir, _ = filepath.EvalSymlinks(farDir)
	typeUntil(t, term, `cd '`+farDir+`' && printf '\033]7;file://build%s\033\\' "$PWD" && echo CD-$((5*5))`, "CD-25")
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(farRows(t, remote), farDir) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(farRows(t, remote), farDir) {
		t.Fatalf("build never took the pane's folder %s:\n%s", farDir, farRows(t, remote))
	}
	time.Sleep(300 * time.Millisecond) // the state push to this client
	before := farRows(t, remote)

	pressCommand(t, term, 'o')
	if err := term.WaitForText("FAR-POPUP-42", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the popup did not run the far machine's ~/bin/prj: %v\n%s", err, term.Snapshot())
	}
	t.Logf("the popup from build:\n%s", term.Snapshot())
	text := term.Screen().Text()

	// At the size the entry names, not the default.
	size := regexp.MustCompile(`SIZE=(\d+) (\d+)`).FindStringSubmatch(text)
	if size == nil {
		t.Fatalf("the popup printed no size:\n%s", text)
	}
	rows, _ := strconv.Atoi(size[1])
	cols, _ := strconv.Atoi(size[2])
	if rows <= 0 || rows > 40*45/100 || cols <= 0 || cols > 160*45/100 {
		t.Fatalf("ASSERTION: the popup is %dx%d, not the 40%% by 40%% the entry names", cols, rows)
	}
	// In the far pane's folder, with the far HOME: the script wrote the file
	// under build's ~.
	if got, err := os.ReadFile(filepath.Join(home, "popup-dir")); err != nil || strings.TrimSpace(string(got)) != farDir {
		t.Errorf("ASSERTION: the popup did not start in the far pane's folder %s: %q, %v", farDir, got, err)
	}
	noLocalFar(t, base)

	// Gone from the far daemon and the screen when the command exits.
	waitGone(t, term, "the popup after its command", "FAR-POPUP-42")
	deadline = time.Now().Add(uiTimeout)
	for farRows(t, remote) != before && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if after := farRows(t, remote); strings.Count(after, "window_id") != strings.Count(before, "window_id") {
		t.Fatalf("ASSERTION: the popup is still on build after its command exited:\nbefore %s\nafter %s", before, after)
	}

	// A scratch entry takes the same way.
	pressCommand(t, term, 'y')
	if err := term.WaitForText("FAR-SCRATCH-56", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the scratch entry did not open on build: %v\n%s", err, term.Snapshot())
	}
	if out := farRows(t, remote); !strings.Contains(out, `"scratch_name": "farnotes"`) && !strings.Contains(out, `"scratch_name":"farnotes"`) {
		t.Fatalf("ASSERTION: build holds no scratch pane farnotes:\n%s", out)
	}
	noLocalFar(t, base)
	alive(t, term, "after a popup and a scratch on build")
}

func TestPopupEntryRefusedByTheHostsAllowList(t *testing.T) {
	remote := remoteMachine(t)
	// Enough to attach and type, not to start a process.
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"write\"]\n")
	home := farHomeWith(t, "echo FAR-POPUP-RAN; sleep 3\n")
	base, term := popupHostClient(t, remote, home, `
[[keybindings.command]]
key = "prefix+alt+o"
type = "popup"
command = "~/bin/prj"
description = "Projects"
`)
	before := farRows(t, remote)

	pressCommand(t, term, 'o')
	if err := term.WaitForText("did not run", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no refusal on the dock: %v\n%s", err, term.Snapshot())
	}
	// The dock cuts a long message. prefix N shows it in full.
	if err := term.SendKeys(tuitest.Ctrl('b'), "N"); err != nil {
		t.Fatalf("send prefix N: %v", err)
	}
	want := `Add "open" to the allow list`
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		flat := strings.Join(strings.Fields(strings.ReplaceAll(s.Text(), "│", " ")), " ")
		// build's allow list is in [hosts."*"], so that is the table to change.
		return strings.Contains(flat, want) && strings.Contains(flat, "Host build does not allow open") &&
			strings.Contains(flat, `[hosts."*"] on build`)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the refusal does not say what to add: %v\n%s", err, term.Snapshot())
	}
	t.Logf("the refusal:\n%s", term.Snapshot())
	if strings.Contains(term.Screen().Text(), "FAR-POPUP-RAN") {
		t.Fatal("ASSERTION: the popup ran on a host that does not allow open")
	}
	if after := farRows(t, remote); strings.Count(after, "window_id") != strings.Count(before, "window_id") {
		t.Fatalf("ASSERTION: build has a new window after a refusal:\nbefore %s\nafter %s", before, after)
	}
	noLocalFar(t, base)
	alive(t, term, "after a refused popup")
}

// A press while the link to build is down says so in plain words, and what to
// do, rather than the error chain of the dial.
func TestPopupEntryWhileTheLinkIsDown(t *testing.T) {
	remote := remoteMachine(t)
	home := farHomeWith(t, "echo FAR-POPUP-RAN; sleep 3\n")
	gate := filepath.Join(t.TempDir(), "gate")
	base, term := popupHostClientGated(t, remote, home, `
[[keybindings.command]]
key = "prefix+alt+o"
type = "popup"
command = "~/bin/prj"
description = "Projects"
`, gate)

	// The positive half: with the link up the popup runs.
	pressCommand(t, term, 'o')
	if err := term.WaitForText("FAR-POPUP-RAN", uiTimeout); err != nil {
		t.Fatalf("the popup did not run with the link up: %v\n%s", err, term.Snapshot())
	}
	waitGone(t, term, "the popup after its command", "FAR-POPUP-RAN")

	cutTheLink(t, gate)
	if err := term.WaitForText("Reconnecting to build", uiTimeout); err != nil {
		t.Fatalf("the client did not see the link drop: %v\n%s", err, term.Snapshot())
	}
	pressCommand(t, term, 'o')
	if err := term.WaitForText("did not run", 45*time.Second); err != nil {
		t.Fatalf("ASSERTION: no message for a press while the link is down: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "N"); err != nil {
		t.Fatalf("send prefix N: %v", err)
	}
	want := "The command Projects did not run. Cannot reach build. The link is reconnecting. Try again in a moment."
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(s.Text(), "│", " ")), " "), want)
	}, uiTimeout); err != nil {
		dumpLinkLogs(t, base, remote)
		t.Fatalf("ASSERTION: the message is not the plain one: %v\n%s", err, term.Snapshot())
	}
	t.Logf("a press while the link is down:\n%s", term.Snapshot())
	if strings.Contains(term.Screen().Text(), "FAR-POPUP-RAN") {
		t.Fatal("ASSERTION: the popup ran while the link was down")
	}
	openTheLink(t, gate)
	alive(t, term, "after a press while the link was down")
}
