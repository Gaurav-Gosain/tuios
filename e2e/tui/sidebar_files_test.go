package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's files section, on the host's own grid.
//
// Everything else that covers the section renders the rail in process and reads
// the strings back. This drives it the way a person does: a real shell reports a
// real directory, a real click puts the section on the rail, and the names are
// read off the terminal the client drew into. Model state and pixels disagreeing
// is the failure this codebase keeps hitting, and a rail that lays out correctly
// in a unit test and lands in the wrong columns on screen would pass every other
// test the section has.

// fileViewFixture builds a directory whose listing has a knowable shape: two
// folders and one file, named so that folders-first and plain alphabetical
// disagree.
func fileViewFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"zulu", "alpha"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRailFilesSectionListsThePanesFolder.
//
// Negative control, confirmed red: skip the files section in the layout draw
// loop of sidebarPanelLinesForTree and the wait for the listing times out.
func TestRailFilesSectionListsThePanesFolder(t *testing.T) {
	dir := fileViewFixture(t)

	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the listing")

	// The pane says where it is, the way a shell with OSC 7 does. The harness
	// shell has no such integration of its own, so the escape is printed
	// outright: it is the same sequence and the same handler either way, and a
	// test that depended on which shell the machine has would be testing that.
	enterTerminalMode(t, term)
	// The marker is a short constant rather than the path. A temp directory is
	// long enough to wrap across two pane lines, and WaitForText looks for its
	// text on one: waiting for the path made the test's own name a thing that
	// could break it.
	runInShell(t, term, "cd "+dir+" && printf 'in-the-dir\\n'", "in-the-dir", uiTimeout)
	runInShell(t, term,
		`printf '\033]7;file://%s\033\\marked\n' "$PWD"`, "marked", uiTimeout)
	leaveTerminalMode(t, term)

	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("the rail never came up: %v\n%s", err, term.Snapshot())
	}

	// No gesture opens it. The section is part of the rail's shipped layout and
	// it follows the focused pane, so the listing arrives on its own once the
	// shell has said where it is. That is the change this branch made, and
	// waiting for it rather than clicking for it is what checks it.

	// The listing sits beside the rail's other sections rather than replacing
	// them, which is the design decision this branch changed: both halves are
	// checked, so a listing that took the rail over fails as loudly as one that
	// never appears.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "alpha/") && strings.Contains(txt, sidebarHeader)
	}, uiTimeout); err != nil {
		t.Fatalf("the files section never listed the pane's folder: %v\n%s", err, term.Snapshot())
	}

	snap := term.Screen()
	txt := snap.Text()
	for _, want := range []string{"alpha/", "zulu/", "brief.txt", filepath.Base(dir)} {
		if !strings.Contains(txt, want) {
			t.Errorf("the listing does not show %q:\n%s", want, term.Snapshot())
		}
	}
	// A folder wears a trailing slash and a file does not, which is the whole
	// of `ls -F`'s distinction and is what the row says with no icon at all.
	if strings.Contains(txt, "brief.txt/") {
		t.Errorf("a file was drawn as a folder:\n%s", term.Snapshot())
	}
	// Folders first, so alpha and zulu are both above brief.txt.
	if _, zRow, ok := findOnGrid(snap, "zulu/"); ok {
		if _, bRow, ok := findOnGrid(snap, "brief.txt"); ok && zRow > bRow {
			t.Errorf("zulu/ is on row %d, below brief.txt on row %d", zRow, bRow)
		}
	}

	// Clicking a folder walks the listing into it, and nothing is typed at the
	// pane: the shell is still where it was.
	aCol, aRow, ok := findOnGrid(snap, "alpha/")
	if !ok {
		t.Fatalf("no row to click:\n%s", term.Snapshot())
	}
	mouseClick(t, term, aCol, aRow, tuitest.MouseLeft, 0)
	// alpha holds nothing, so the listing is the way back out and the word for
	// having nothing in it. Both are the proof that the click moved the view
	// rather than that it did nothing at all.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "empty") && strings.Contains(txt, "/alpha")
	}, uiTimeout); err != nil {
		t.Fatalf("clicking a folder did not walk into it: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "cd "+filepath.Join(dir, "alpha")) {
		t.Errorf("clicking a folder typed a cd at the pane:\n%s", term.Snapshot())
	}

	// The footer control is the switch, and it is the way off as well as on: a
	// section the user cannot take off the rail is one they are stuck with.
	col, row, ok := findFooterFiles(term.Screen())
	if !ok {
		t.Fatalf("the rail drew no files control:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return !strings.Contains(txt, "brief.txt") && strings.Contains(txt, sidebarHeader)
	}, uiTimeout); err != nil {
		t.Fatalf("the footer control did not take the section off the rail: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after browsing the rail's files section")
}

// findFooterFiles locates the footer's "files" control, which is the last row
// of the rail. The word also names the section's own header, so a plain search
// for it finds the header first and clicks a path instead of a control.
func findFooterFiles(s tuitest.Screen) (int, int, bool) {
	lines := strings.Split(s.Text(), "\n")
	for row := len(lines) - 1; row >= 0; row-- {
		if col := strings.Index(lines[row], "files"); col >= 0 {
			return col, row, true
		}
	}
	return 0, 0, false
}

// TestSidebarFileEditor checks the persisted settings field, folder navigation,
// binary refusal and the editor's argv on standalone and daemon panes.
func TestSidebarFileEditor(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			dir := fileViewFixture(t)
			folder := filepath.Join(dir, "alpha")
			name := "note $(touch injected).txt"
			for name, data := range map[string][]byte{
				name: []byte("editable text\n"), "binary.bin": {0, 1, 2},
			} {
				if err := os.WriteFile(filepath.Join(folder, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			editor := filepath.Join(t.TempDir(), "test editor")
			if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'EDITOR_ARG:%s\\n' \"$1\"\nprintf 'EDITOR_FILE:%s\\n' \"$(basename \"$2\")\"\nsleep 60\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			term, base := start(t, startOpts{daemonDefault: daemon, cols: 160, rows: 40})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell")
			openSettings(t, term)
			if err := term.SendKeys("/", "file editor"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("File editor", uiTimeout); err != nil {
				t.Fatalf("editor setting missing: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Tab); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return selectedSettingsRow(s, "File editor") != "" && !strings.Contains(s.Text(), "matches")
			}, uiTimeout); err != nil {
				t.Fatalf("could not reach the Sidebar editor row: %v\n%s", err, term.Snapshot())
			}
			command := "'" + editor + "' --test"
			if err := term.SendKeys(tuitest.Enter, command, tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
			deadline := time.Now().Add(uiTimeout)
			for {
				data, err := os.ReadFile(configPath)
				if err == nil && strings.Contains(string(data), "test editor") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("editor setting not persisted: %v\n%s", err, data)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "File editor")
			}, uiTimeout); err != nil {
				t.Fatalf("settings did not close: %v\n%s", err, term.Snapshot())
			}
			enterTerminalMode(t, term)
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\listed\\n' \"$PWD\"", "listed", uiTimeout)
			leaveTerminalMode(t, term)
			toggleSidebarViaPalette(t, term)
			waitForAll(t, term, uiTimeout, "folder listing", "alpha/", "brief.txt")
			if err := term.SendKeys("s"); err != nil {
				t.Fatal(err)
			}
			col, row, _ := findOnGrid(term.Screen(), "alpha/")
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			waitForAll(t, term, uiTimeout, "folder navigation", "binary.bin", "note $(touch")
			col, row, _ = findOnGrid(term.Screen(), "binary.bin")
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			if err := term.SendKeys(tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("Copied the path.", uiTimeout); err != nil {
				t.Fatalf("Enter changed its navigation action: %v\n%s", err, term.Snapshot())
			}
			// CSI-u carries Shift+Enter distinctly from plain Enter.
			if err := term.SendKeys("\x1b[13;2u"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("It is not a text file.", uiTimeout); err != nil {
				t.Fatalf("binary file was not refused: %v\n%s", err, term.Snapshot())
			}
			waitWindowCount(t, term, 1, "refusing a binary file")
			col, row, _ = findOnGrid(term.Screen(), "note $(touch")
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			if err := term.SendKeys("\x1b[13;2u"); err != nil {
				t.Fatal(err)
			}
			waitForAll(t, term, uiTimeout, "editor invocation", "EDITOR_ARG:--test", "EDITOR_FILE:"+name)
			waitWindowCount(t, term, 2, "opening the editor")
			if _, err := os.Stat(filepath.Join(folder, "injected")); !os.IsNotExist(err) {
				t.Fatalf("file name was executed through a shell: %v", err)
			}
			saveArtifact(t, term, artifactDir(t), "editor-open")
			alive(t, term, "after opening the configured editor")
		})
	}
}

// TestSidebarFileSearch follows a result into its parent folder and leaves the
// result selected for the existing edit action.
func TestSidebarFileSearch(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			dir := fileViewFixture(t)
			if err := os.WriteFile(filepath.Join(dir, "alpha", "needle.txt"), []byte("found\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			editor := filepath.Join(t.TempDir(), "editor")
			if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'FOUND_FILE:%s\\n' \"$(basename \"$1\")\"\nsleep 60\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			term, _ := start(t, startOpts{daemonDefault: daemon, env: []string{"EDITOR=" + editor}})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell")
			enterTerminalMode(t, term)
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\listed\\n' \"$PWD\"", "listed", uiTimeout)
			if err := term.SendKeys(tuitest.Ctrl('b'), "f"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("Search files", uiTimeout); err != nil {
				t.Fatalf("file search did not open: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys("needle.txt"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("alpha/needle.txt", uiTimeout); err != nil {
				t.Fatalf("nested file was not found: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(s.Text(), "files") && strings.Contains(s.Text(), "needle.txt") && strings.Contains(s.Text(), "/alpha") && !strings.Contains(s.Text(), "Search files")
			}, uiTimeout); err != nil {
				t.Fatalf("selection did not navigate the sidebar: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Ctrl('f')); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("Search files", uiTimeout); err != nil {
				t.Fatalf("sidebar shortcut did not open search: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "Search files")
			}, uiTimeout); err != nil {
				t.Fatalf("file search did not close: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys("\x1b[13;2u"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("FOUND_FILE:needle.txt", uiTimeout); err != nil {
				t.Fatalf("selected result was not editable: %v\n%s", err, term.Snapshot())
			}
			saveArtifact(t, term, artifactDir(t), "file-search-result")
		})
	}
}
