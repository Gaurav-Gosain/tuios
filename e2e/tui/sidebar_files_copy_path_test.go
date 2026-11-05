package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarFolderCopyPath is issue #414: a folder row of the rail's files
// section had no way to copy its path. Enter and the menu's first row open
// the folder, and only a file row offered "Copy path".
//
// It drives both ways in. The context menu of a folder row has a Copy path
// row, and clicking it writes the folder's absolute path to the clipboard.
// file_copy_path (Y) on a folder row under the keyboard cursor does the same.
// The clipboard is read off the wire, as the OSC 52 write the host terminal
// gets.
//
// How this could pass wrongly, written down first:
//   - The menu row could copy the folder on screen, not the row. The row
//     clicked is zulu, which is not the folder the rail lists.
//   - The menu row could open the folder instead. The listing must still show
//     the parent's rows after the click, and the clipboard must hold the path.
//   - The key could copy nothing on a folder and the test could match a file's
//     path. The first row the cursor reaches is alpha, a folder, and the write
//     must be exactly its path, once.
//   - The folder menu could lose Open folder. It is checked in the same menu.
//
// NEGATIVE CONTROL: each cut fails both subtests. With the Copy path row cut
// from fileRowMenu, the wait for the row times out. With the file_copy_path
// case cut from handleSidebarFileAction, the menu row writes nothing. With
// the default Y binding cut, the key step writes nothing.
func TestSidebarFolderCopyPath(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			dir := fileViewFixture(t)
			out := &lockedBuffer{}
			term, _ := start(t, startOpts{daemonDefault: daemon, out: out})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell for the listing")
			enterTerminalMode(t, term)
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
			leaveTerminalMode(t, term)
			toggleSidebarViaPalette(t, term)
			waitForAll(t, term, uiTimeout, "the listing", "alpha/", "zulu/", "brief.txt")
			art := artifactDir(t)

			// The menu.
			col, row, ok := findOnGrid(term.Screen(), "zulu/")
			if !ok {
				t.Fatalf("no zulu row:\n%s", term.Snapshot())
			}
			mouseClick(t, term, col, row, tuitest.MouseRight, 0)
			waitForAll(t, term, uiTimeout, "the folder menu", "Open folder", "Copy path")
			saveArtifact(t, term, art, "folder-menu")
			from := len(clipboardWrites(out))
			col, row, _ = findOnGrid(term.Screen(), "Copy path")
			mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
			waitClipboardSequence(t, term, out, from, filepath.Join(dir, "zulu"))
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				txt := s.Text()
				return !strings.Contains(txt, "Open folder") && strings.Contains(txt, "brief.txt")
			}, uiTimeout); err != nil {
				t.Fatalf("the menu stayed up, or the click opened the folder: %v\n%s", err, term.Snapshot())
			}

			// The key. Focus the rail and walk the cursor down, pressing Y on
			// each row, until a row copies. Y does nothing off the listing.
			sendKeys(t, term, "s")
			from = len(clipboardWrites(out))
			copied := false
			for range 30 {
				sendKeys(t, term, "j", "Y")
				time.Sleep(100 * time.Millisecond)
				if len(clipboardWrites(out)) > from {
					copied = true
					break
				}
			}
			if !copied {
				t.Fatalf("Y on the rows of the listing wrote nothing to the clipboard\n%s", term.Snapshot())
			}
			waitClipboardSequence(t, term, out, from, filepath.Join(dir, "alpha"))
			saveArtifact(t, term, art, "folder-key-copied")
			alive(t, term, "after copying a folder path")
		})
	}
}

// openStubs writes two programs that record how they were started, one line
// each, to record: xdg-open, which stands in for the system opener, and rule,
// which a [[files.open]] rule names. It returns the folder they are in and the
// path of rule. The test puts the folder first on PATH and sets DISPLAY, so the
// opener tuios picks is the real default path, on the real lookup.
func openStubs(t *testing.T, base, record string) (dir, rule string) {
	t.Helper()
	dir = filepath.Join(base, "stubs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xdg-open", "rule"} {
		body := "#!/bin/sh\nprintf '" + name + " %s\\n' \"$*\" >> '" + record + "'\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(dir, "rule")
}

// openStubEnv is the environment that makes xdg-open the system opener: a
// display, no ssh, no WSL, and the stubs first on PATH.
func openStubEnv(stubs string) []string {
	return []string{
		"PATH=" + stubs + ":" + os.Getenv("PATH"), "DISPLAY=:99", "WAYLAND_DISPLAY=",
		"SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY=", "WSL_DISTRO_NAME=", "BROWSER=",
	}
}

// openRecord reads the stubs' record, one start per line.
func openRecord(record string) []string {
	data, err := os.ReadFile(record)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// waitOpenRecord waits until the record holds want, in order, and nothing else.
func waitOpenRecord(t *testing.T, term *tuitest.Terminal, record string, want []string, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if strings.Join(openRecord(record), "\n") == strings.Join(want, "\n") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: the record holds %q, want %q\n%s", what, openRecord(record), want, term.Snapshot())
}

// TestSidebarFileOpenSystem is file_open_system (o) on the rail's files
// section: the row's file or folder goes to the system opener, or to the
// program of the first [[files.open]] rule it matches.
//
// How this could pass wrongly, written down first:
//   - o could open the folder on screen, not the row. The folder row is alpha,
//     which is not the folder the rail lists.
//   - o on a folder could walk into it, as enter does. The record must name
//     alpha, and the listing must still show brief.txt.
//   - The rule could run for every file, or not at all. brief.txt must go to
//     xdg-open and paper.pdf to the rule, with the rule's own word first.
//   - The opener could get a shell line. The rule's argv is checked word for
//     word, so a joined or quoted line would not match.
//   - The success note could show with no start. The record is the proof, and
//     the note is checked after it.
//
// NEGATIVE CONTROL: see NEGATIVE_CONTROLS.md. With the file_open_system case
// cut from handleSidebarFileAction, o starts nothing and the folder step times
// out. With the rule lookup cut from startFileOpener, paper.pdf goes to
// xdg-open and the rule step fails.
func TestSidebarFileOpenSystem(t *testing.T) {
	dir := fileViewFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "paper.pdf"), []byte("%PDF-1.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	record := filepath.Join(base, "opened.txt")
	stubs, rule := openStubs(t, base, record)
	writeConfig(t, base, fmt.Sprintf("[[files.open]]\nmatch = \"*.PDF\"\ncommand = [%q, \"--page\", \"1\", \"{path}\"]\n", rule))
	term := startIn(t, base, startOpts{daemonDefault: true, env: openStubEnv(stubs)})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the listing")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "the listing", "alpha/", "zulu/", "brief.txt", "paper.pdf")
	art := artifactDir(t)

	// The folder. Walk the cursor down and press o on each row until one
	// starts the opener. o off the listing sorts the agents and starts
	// nothing. The first row of the listing is alpha, a folder.
	sendKeys(t, term, "s")
	opened := false
	for range 30 {
		sendKeys(t, term, "j", "o")
		time.Sleep(100 * time.Millisecond)
		if len(openRecord(record)) > 0 && openRecord(record)[0] != "" {
			opened = true
			break
		}
	}
	if !opened {
		t.Fatalf("o on the rows of the listing started nothing\n%s", term.Snapshot())
	}
	waitOpenRecord(t, term, record, []string{"xdg-open " + filepath.Join(dir, "alpha")}, "o on a folder")
	waitForAll(t, term, uiTimeout, "the success note", "Opened alpha with xdg-open.")
	railLists(t, term, "o on a folder walked into it", []string{"brief.txt", "zulu/"}, nil)
	copiesFrame(t, term, "open-system-folder")
	saveArtifact(t, term, art, "open-system-folder")

	// A file with no rule goes to the system opener.
	col, row, ok := findOnGrid(term.Screen(), "brief.txt")
	if !ok {
		t.Fatalf("no brief.txt row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	clickNoteGone(t, term)
	sendKeys(t, term, "o")
	waitOpenRecord(t, term, record, []string{
		"xdg-open " + filepath.Join(dir, "alpha"),
		"xdg-open " + filepath.Join(dir, "brief.txt"),
	}, "o on a file")
	waitForAll(t, term, uiTimeout, "the success note", "Opened brief.txt with xdg-open.")

	// A file a rule matches goes to the rule's program, in its argv. The rule
	// is written in capitals, so the match also ignores case.
	col, row, ok = findOnGrid(term.Screen(), "paper.pdf")
	if !ok {
		t.Fatalf("no paper.pdf row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	clickNoteGone(t, term)
	sendKeys(t, term, "o")
	waitOpenRecord(t, term, record, []string{
		"xdg-open " + filepath.Join(dir, "alpha"),
		"xdg-open " + filepath.Join(dir, "brief.txt"),
		"rule --page 1 " + filepath.Join(dir, "paper.pdf"),
	}, "o on a file a rule matches")
	waitForAll(t, term, uiTimeout, "the success note", "Opened paper.pdf with rule.")
	saveArtifact(t, term, art, "open-system-rule")

	// The menu row shows the key and does the same.
	col, row, _ = findOnGrid(term.Screen(), "paper.pdf")
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	waitForAll(t, term, uiTimeout, "the file menu", "Open with system")
	copiesFrame(t, term, "open-system-menu")
	col, row, _ = findOnGrid(term.Screen(), "Open with system")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	waitOpenRecord(t, term, record, []string{
		"xdg-open " + filepath.Join(dir, "alpha"),
		"xdg-open " + filepath.Join(dir, "brief.txt"),
		"rule --page 1 " + filepath.Join(dir, "paper.pdf"),
		"rule --page 1 " + filepath.Join(dir, "paper.pdf"),
	}, "the menu row")
	alive(t, term, "after opening files with the system opener")
}
