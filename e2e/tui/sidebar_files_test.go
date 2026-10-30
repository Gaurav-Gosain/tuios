package tuie2e

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
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
	runInShell(t, term, "cd "+dir+" && printf 'in-the-%s\\n' dir", "in-the-dir", uiTimeout)
	runInShell(t, term,
		`printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
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
	for row, line := range slices.Backward(lines) {
		if col := strings.Index(line, "files"); col >= 0 {
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
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
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
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
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

// TestRailFilesSectionFollowsTheDisk is issue #313: a file deleted from the
// pane, or added or deleted by another program, stayed on the rail until the
// listing was asked for again by moving away and back.
//
// Every name the test changes is spelled so that the pane never prints it
// whole. The pane shares the screen with the rail, so a name echoed by the
// shell would satisfy a wait that is meant to read the rail.
//
// Negative control, confirmed red: remove the call to syncFileWatch in
// HandleFileList and both subtests time out waiting for gone.txt to leave the
// rail.
func TestRailFilesSectionFollowsTheDisk(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			dir := fileViewFixture(t)
			for _, name := range []string{"gone.txt", "kept.txt"} {
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			term, _ := start(t, startOpts{daemonDefault: daemon})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell for the listing")
			enterTerminalMode(t, term)
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
			leaveTerminalMode(t, term)
			toggleSidebarViaPalette(t, term)

			// The positive half: the rail lists the folder, so every wait below
			// reads a listing that exists.
			waitForAll(t, term, uiTimeout, "the first listing", "gone.txt", "kept.txt", "brief.txt")
			dirArt := artifactDir(t)
			saveArtifact(t, term, dirArt, "before")

			// Deleted from the pane, the way the issue reports it.
			enterTerminalMode(t, term)
			runInShell(t, term, "rm g\"\"one.txt && printf 'remo%s\\n' ved", "removed", uiTimeout)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				txt := s.Text()
				return !strings.Contains(txt, "gone.txt") && strings.Contains(txt, "kept.txt")
			}, uiTimeout); err != nil {
				saveArtifact(t, term, dirArt, "stale-after-pane-delete")
				t.Fatalf("gone.txt stayed on the rail after the pane deleted it: %v\n%s", err, term.Snapshot())
			}
			saveArtifact(t, term, dirArt, "after-pane-delete")

			// Changed by another program, with nothing typed at the pane.
			if err := os.WriteFile(filepath.Join(dir, "outside.txt"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "brief.txt")); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				txt := s.Text()
				return strings.Contains(txt, "outside.txt") && !strings.Contains(txt, "brief.txt") &&
					strings.Contains(txt, "kept.txt")
			}, uiTimeout); err != nil {
				saveArtifact(t, term, dirArt, "stale-after-outside-change")
				t.Fatalf("the rail did not follow a change made outside tuios: %v\n%s", err, term.Snapshot())
			}
			saveArtifact(t, term, dirArt, "after-outside-change")
			alive(t, term, "after the folder changed under the rail")
		})
	}
}

// longListingFixture is fileViewFixture with forty files after the folders and
// one more after those, so the listing is longer than the files section, and
// a folder tree inside alpha for the tree style: alpha holds inner/ and
// a1.txt, inner holds l2/ and deep.txt, and l2/l3/l4 holds leaf.txt, five
// levels down.
func longListingFixture(t *testing.T) string {
	t.Helper()
	dir := fileViewFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, "alpha", "inner", "l2", "l3", "l4"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{"alpha/a1.txt", "alpha/inner/deep.txt", "alpha/inner/l2/l3/l4/leaf.txt", "zz-last.txt"}
	for i := range 40 {
		files = append(files, fmt.Sprintf("f-%02d.txt", i))
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// cursorToFirstName walks the rail's cursor down until Y copies a path, which
// is the first name of the files listing: alpha. Y does nothing off a name.
func cursorToFirstName(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, dir string) {
	t.Helper()
	from := len(clipboardWrites(out))
	for range 40 {
		sendKeys(t, term, "j", "Y")
		time.Sleep(100 * time.Millisecond)
		if len(clipboardWrites(out)) > from {
			waitClipboardSequence(t, term, out, from, filepath.Join(dir, "alpha"))
			return
		}
	}
	t.Fatalf("Y on the rows of the listing wrote nothing to the clipboard\n%s", term.Snapshot())
}

// copiedRowIs presses Y and checks that the cursor's row is want, by the path
// Y copies, and that the row is drawn, by its name on the screen.
func copiedRowIs(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, want string) {
	t.Helper()
	from := len(clipboardWrites(out))
	sendKeys(t, term, "Y")
	waitClipboardSequence(t, term, out, from, want)
	if err := term.WaitForText(filepath.Base(want), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the cursor is on %s, and the rail does not draw it: %v\n%s",
			filepath.Base(want), err, term.Snapshot())
	}
}

// TestRailFilesSectionScrollsWithTheCursor is issue #1102: j and G moved the
// keyboard cursor past the last name the files section drew, and the section
// did not scroll, so the cursor went off the screen. It runs on both styles,
// and the tree subtest then opens and closes folders the way neo-tree does:
// l opens, h closes or goes up to the folder, a click toggles, and a file made
// inside an open folder shows without a key.
//
// How this could pass wrongly, written down first:
//   - The name could be on screen in the pane, not the rail. No name the test
//     waits for is ever printed in the pane.
//   - The cursor could be on another row while the name happens to be drawn.
//     Y copies the cursor row's path, and the copy must be that name's path.
//   - Fast keys could be dropped while a frame is pending. The walk sends all
//     its keys at once.
//   - A tree row could copy the wrong folder's path. deep.txt's path has two
//     folders in it, and h must land on alpha by its own path.
//
// NEGATIVE CONTROL: with sidebarFilesCursorMove and sidebarFilesCursorJump
// cut to return false, both subtests fail: G leaves the cursor off the
// listing and Y copies no zz-last.txt.
func TestRailFilesSectionScrollsWithTheCursor(t *testing.T) {
	for _, style := range []string{"list", "tree"} {
		t.Run(style, func(t *testing.T) {
			dir := longListingFixture(t)
			out := &lockedBuffer{}
			term, base := start(t, startOpts{daemonDefault: style == "tree", out: out})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell for the listing")
			if style == "tree" {
				for key, val := range map[string]string{"files_style": "tree", "folder_click": "both"} {
					if o, err := tuiosCLI(t, base, "set-config", "appearance.sidebar."+key, val); err != nil {
						t.Fatalf("set %s: %v\n%s", key, err, o)
					}
				}
			}
			enterTerminalMode(t, term)
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
			leaveTerminalMode(t, term)
			toggleSidebarViaPalette(t, term)
			waitForAll(t, term, uiTimeout, "the listing", "alpha/", "zulu/")
			sendKeys(t, term, "s")
			cursorToFirstName(t, term, out, dir)
			copiesFrame(t, term, "rail-files-"+style+"-top")

			// G: the last name of the listing, which the section does not draw
			// until it scrolls.
			sendKeys(t, term, "G")
			copiedRowIs(t, term, out, filepath.Join(dir, "zz-last.txt"))
			copiesFrame(t, term, "rail-files-"+style+"-end")

			// g goes to the first row, "..", and a walk of j sends every key
			// at once, past the fold.
			sendKeys(t, term, "g", "j")
			copiedRowIs(t, term, out, filepath.Join(dir, "alpha"))
			// alpha, zulu, brief.txt, then the forty files.
			for range 42 {
				sendKeys(t, term, "j")
			}
			copiedRowIs(t, term, out, filepath.Join(dir, "f-39.txt"))
			copiesFrame(t, term, "rail-files-"+style+"-walk")
			if style == "list" {
				alive(t, term, "after scrolling the files list")
				return
			}

			// The tree. l on alpha opens it in place, under its row.
			sendKeys(t, term, "g", "j")
			copiedRowIs(t, term, out, filepath.Join(dir, "alpha"))
			sendKeys(t, term, "l")
			waitForAll(t, term, uiTimeout, "alpha open", "inner/", "a1.txt")
			// l on an open folder goes to its first name, and l opens that.
			sendKeys(t, term, "l", "l")
			waitForAll(t, term, uiTimeout, "inner open", "deep.txt")
			snap := term.Screen().Text()
			if !strings.Contains(snap, "│ └") || !strings.Contains(snap, "\uf47c") || !strings.Contains(snap, "\uf460") {
				t.Errorf("ASSERTION: the open folders draw no indent guides or open mark:\n%s", term.Snapshot())
			}
			// Five levels down: l goes into each folder and opens it.
			for _, next := range []string{"l3/", "l4/", "leaf.txt"} {
				sendKeys(t, term, "l", "l")
				waitForAll(t, term, uiTimeout, "the next level", next)
			}
			sendKeys(t, term, "l")
			copiedRowIs(t, term, out, filepath.Join(dir, "alpha", "inner", "l2", "l3", "l4", "leaf.txt"))
			copiesFrame(t, term, "rail-files-tree-deep")
			// Back up and shut them: h on a name goes to its folder, h on an
			// open folder closes it.
			sendKeys(t, term, "h", "h", "h", "h", "h", "h")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "l3/")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: h did not close l2: %v\n%s", err, term.Snapshot())
			}
			copiedRowIs(t, term, out, filepath.Join(dir, "alpha", "inner", "l2"))
			sendKeys(t, term, "j")
			copiedRowIs(t, term, out, filepath.Join(dir, "alpha", "inner", "deep.txt"))
			copiesFrame(t, term, "rail-files-tree-open")

			// A file made in an open folder shows with no key pressed.
			if err := os.WriteFile(filepath.Join(dir, "alpha", "inner", "made-outside.txt"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			waitForAll(t, term, uiTimeout, "the change in an open folder", "made-outside.txt")

			// inner deleted and made again under the same path, faster than the
			// rail reads alpha: it stays open, its old watch is gone with the
			// old folder, and a file made in the new one must still show.
			inner := filepath.Join(dir, "alpha", "inner")
			if err := os.RemoveAll(inner); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(inner, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "deep.txt")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the rail kept the names of the deleted inner: %v\n%s", err, term.Snapshot())
			}
			time.Sleep(1500 * time.Millisecond)
			if err := os.WriteFile(filepath.Join(inner, "after-replace.txt"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("after-replace.txt", uiTimeout); err != nil {
				t.Fatalf("ASSERTION: a file made in the new inner never showed: %v\n%s", err, term.Snapshot())
			}
			// The cursor was on deep.txt, which is gone. It is on the nearest
			// row above it now, inner, and not on the rail's first row.
			copiedRowIs(t, term, out, inner)
			copiesFrame(t, term, "rail-files-tree-replaced")

			// h on an open folder closes it, and h on a closed one goes up to
			// its folder.
			sendKeys(t, term, "h")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "after-replace.txt") && strings.Contains(s.Text(), "inner/")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: h did not close inner: %v\n%s", err, term.Snapshot())
			}
			sendKeys(t, term, "h")
			copiedRowIs(t, term, out, filepath.Join(dir, "alpha"))

			// A click on a folder closes it, and a second one opens it again,
			// with inner still closed. folder_click is "both": a tree folder
			// only opens, and the root stays where it is.
			col, row, ok := findOnGrid(term.Screen(), "alpha/")
			if !ok {
				t.Fatalf("no alpha row:\n%s", term.Snapshot())
			}
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "inner/")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: a click did not close alpha: %v\n%s", err, term.Snapshot())
			}
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			waitForAll(t, term, uiTimeout, "alpha open again", "inner/", "a1.txt")
			if txt := term.Screen().Text(); strings.Contains(txt, "after-replace.txt") || strings.Contains(txt, "/alpha") {
				t.Errorf("ASSERTION: inner opened with alpha, or the click typed a cd:\n%s", term.Snapshot())
			}
			copiesFrame(t, term, "rail-files-tree-click")
			alive(t, term, "after browsing the files tree")
		})
	}
}

// TestPathPickerTypesAPath opens the path picker at a shell prompt, filters
// the pane's folder, walks into a folder with Tab, and types the chosen file's
// path into the shell line with Enter. The shell then runs the line, so the
// path is proved by the shell reading the file, not by the text on screen.
//
// How this could pass wrongly, written down first:
//   - The names could be on screen in the pane, not the picker. The pane
//     never prints a name the test waits for: the cd is echoed by its path,
//     and the file's content is the marker.
//   - Enter could type a path the shell cannot read: a name with a space must
//     arrive quoted. The file's name has a space, and cat must print it.
//   - The filter could do nothing and Tab walk into the first folder anyway.
//     The first folder is alpha, so the filter picks zulu, the second one.
//   - The picker could type into the pane while it is open. The line is
//     checked to hold only "cat " until Enter.
//
// NEGATIVE CONTROL: with PathPickerAccept returning before w.Paste, the wait
// for the typed path times out (run on the standalone subtest).
func TestPathPickerTypesAPath(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			dir := fileViewFixture(t)
			if err := os.WriteFile(filepath.Join(dir, "zulu", "release notes.md"), []byte("PICKED-"+"FILE-OK\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "zulu", "build.log"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			term, _ := start(t, startOpts{daemonDefault: daemon})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell")
			enterTerminalMode(t, term)
			runInShell(t, term, "cd "+dir+" && clear && printf 'in-the-%s\\n' dir", "in-the-dir", uiTimeout)

			sendKeys(t, term, "cat ", tuitest.Alt(";"))
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				txt := s.Text()
				return strings.Contains(txt, "brief.txt") && strings.Contains(txt, "zulu/")
			}, uiTimeout); err != nil {
				t.Fatalf("alt+; did not open the picker on the pane's folder: %v\n%s", err, term.Snapshot())
			}

			sendKeys(t, term, "zu")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				txt := s.Text()
				return !strings.Contains(txt, "brief.txt") && strings.Contains(txt, "zulu/")
			}, uiTimeout); err != nil {
				t.Fatalf("the filter did not narrow the list: %v\n%s", err, term.Snapshot())
			}

			sendKeys(t, term, tuitest.Tab)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				txt := s.Text()
				return strings.Contains(txt, "release notes.md") && strings.Contains(txt, "build.log")
			}, uiTimeout); err != nil {
				t.Fatalf("Tab did not walk into zulu: %v\n%s", err, term.Snapshot())
			}
			if strings.Contains(term.Screen().Text(), "cat z") {
				t.Fatalf("the picker typed into the pane while it was open:\n%s", term.Snapshot())
			}
			sendKeys(t, term, "rel")
			if err := term.WaitForText("zulu/rel", uiTimeout); err != nil {
				t.Fatalf("the filter line does not show the path walked: %v\n%s", err, term.Snapshot())
			}
			savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "path-picker")
			saveArtifact(t, term, artifactDir(t), "path-picker")

			sendKeys(t, term, tuitest.Enter)
			if err := term.WaitForText("cat 'zulu/release notes.md'", uiTimeout); err != nil {
				t.Fatalf("Enter did not type the quoted path at the prompt: %v\n%s", err, term.Snapshot())
			}
			sendKeys(t, term, tuitest.Enter)
			if err := term.WaitForText("PICKED-FILE-OK", uiTimeout); err != nil {
				t.Fatalf("the shell could not read the typed path: %v\n%s", err, term.Snapshot())
			}
			if daemon {
				pathPickerStaysInANarrowPane(t, term, dir)
			}
			alive(t, term, "after typing a path with the picker")
		})
	}
}

// pathPickerStaysInANarrowPane splits the pane, puts the rail beside it, and
// opens the picker with the cursor near the new pane's right edge. The box
// must stay inside the pane: fitted only to the screen, it ran over the next
// pane and the rail.
//
// NEGATIVE CONTROL: with placePathPicker fitting the box to the screen (the
// room from pathPickerRoom ignored), the box's right edge lands on or past
// the pane's right border and the assertion fails.
func pathPickerStaysInANarrowPane(t *testing.T, term *tuitest.Terminal, dir string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "|")
	waitWindowCount(t, term, 2, "splitting the pane")
	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("the rail never came up: %v\n%s", err, term.Snapshot())
	}
	runInShell(t, term, "cd "+dir+" && clear && echo nar\"\"row", "narrow", uiTimeout)

	// The pane's content starts where the shell printed narrow, and its
	// right border is the first border cell after it on that row.
	scr := term.Screen()
	left, row, ok := findOnGrid(scr, "narrow")
	if !ok {
		t.Fatalf("no output line to measure the pane by:\n%s", term.Snapshot())
	}
	right := gridCellAfter(scr, row, left, "│")
	if right < 0 {
		t.Fatalf("the pane has no right border on row %d:\n%s", row, term.Snapshot())
	}
	// The prompt is two cells, so this puts the cursor four cells from the
	// border, where a box anchored at the cursor would run past it.
	sendKeys(t, term, strings.Repeat("x", max(right-left-6, 1)), tuitest.Alt(";"))
	// The box's top border is the only "╭─ " on the screen: a pane's border
	// starts with its buttons, and the rail lists the same names as the box.
	if err := term.WaitForText("╭─ ", uiTimeout); err != nil {
		t.Fatalf("alt+; did not open the picker in the narrow pane: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(200 * time.Millisecond)
	scr = term.Screen()
	boxLeft, boxTop := pathPickerCorner(scr)
	boxRight := gridCellAfter(scr, boxTop, boxLeft, "╮")
	savePNG(t, scr, shot.XTermPalette(), artifactDir(t), "path-picker-narrow-pane")
	saveArtifact(t, term, artifactDir(t), "path-picker-narrow-pane")
	if boxLeft < left || boxRight < 0 || boxRight >= right {
		t.Fatalf("the picker box spans columns %d to %d, outside the pane's content %d to %d:\n%s",
			boxLeft, boxRight, left, right-1, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "╭─ ") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the picker: %v\n%s", err, term.Snapshot())
	}
}

// pathPickerCorner is the cell of the picker box's top-left corner: a "╭"
// followed by "─" and a space, or (-1, -1).
func pathPickerCorner(s tuitest.Screen) (int, int) {
	cols, rows := s.Size()
	for r := range rows {
		for c := range cols - 2 {
			if s.Cell(c, r).Content == "╭" && s.Cell(c+1, r).Content == "─" && s.Cell(c+2, r).Content == " " {
				return c, r
			}
		}
	}
	return -1, -1
}

// gridCellAfter is the first column right of col on row whose cell is want,
// or -1.
func gridCellAfter(s tuitest.Screen, row, col int, want string) int {
	cols, _ := s.Size()
	for c := col + 1; c < cols; c++ {
		if s.Cell(c, row).Content == want {
			return c
		}
	}
	return -1
}

// gridCellBefore is the last column left of col on row whose cell is want,
// or -1.
func gridCellBefore(s tuitest.Screen, row, col int, want string) int {
	for c := col - 1; c >= 0; c-- {
		if s.Cell(c, row).Content == want {
			return c
		}
	}
	return -1
}

// dropShell is a shell the drag test runs in the pane, and the text a drop of
// each fixture name must put on its command line.
type dropShell struct {
	// name is the program, as the label names it and as PATH finds it.
	name string
	// start runs the shell with no user config, and ready is a command whose
	// output says it is reading input. Both are empty for the pane's own sh.
	start, ready, readyMark string
	// printf is the command the drops are appended to. It prints each
	// argument on a line of its own, in brackets.
	printf string
	// quote is the quoted form of a path, as written out by hand for this
	// shell. It is not the code under test.
	quote func(path string) string
}

func posixQuote(p string) string { return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'" }

func dropShells() []dropShell {
	return []dropShell{
		{name: "sh", printf: `printf '[%s]\n' `, quote: posixQuote},
		{name: "bash", start: "PS1='B$ ' bash --norc --noprofile", ready: `echo READY""bash`, readyMark: "READYbash",
			printf: `printf '[%s]\n' `, quote: posixQuote},
		{name: "zsh", start: "PS1='Z$ ' zsh -f", ready: `echo READY""zsh`, readyMark: "READYzsh",
			printf: `printf '[%s]\n' `, quote: posixQuote},
		// fish reads \' and \\ inside single quotes as escapes, so the POSIX
		// form of a name with a backslash would lose it.
		{name: "fish", start: "fish --no-config", ready: `echo READY""fish`, readyMark: "READYfish",
			printf: `printf '[%s]\n' `, quote: func(p string) string {
				return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(p) + "'"
			}},
		// nu's single quotes are raw. A name with a quote goes in double
		// quotes, where \ and " are escaped. ^printf is the external printf.
		{name: "nu", start: "nu --no-config-file", ready: `echo ("READY" + "nu")`, readyMark: "READYnu",
			printf: `^printf '[%s]\n' `, quote: func(p string) string {
				if !strings.Contains(p, "'") {
					return "'" + p + "'"
				}
				return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
			}},
	}
}

// TestRailFileDragIntoPane drags rows of the files section onto a shell pane
// and checks the exact text each drop puts on the command line, in every
// shell this machine has, then runs the line and checks the shell read back
// each name as it is on disk.
//
// How this could pass wrongly, written down first:
//   - The drop could paste the name bare. "$(touch pwned)" would then run, so
//     the test checks no file named pwned exists after each shell.
//   - The quoting could be POSIX in every shell. fish keeps a backslash only
//     when it is doubled, so back\slash.txt reads back wrong there, and nu has
//     no escape for a quote inside single quotes.
//   - The command line could match the echo of something else. The text is
//     the whole line from the printf to the last drop, and the output is the
//     shell's own, in brackets, one name per line.
//   - The label could name the wrong program. Each drag waits for the label
//     to name the shell in the pane before it is released.
//   - A press on a row could stop being a click. The I subtest clicks the row
//     first, which moves the cursor, and the drop path is not used for it.
//   - A refused pane could still get the text. The fake ssh echoes what the
//     terminal sends it, so a paste there would show on screen.
//
// NEGATIVE CONTROL: with quoteDropPath answering pastePathText for every
// dialect, the fish round fails on the first drop, it's a file.txt, whose
// POSIX form is not the fish form. See NEGATIVE_CONTROLS.md.
func TestRailFileDragIntoPane(t *testing.T) {
	// A short folder, so a command line with four paths fits on one row.
	dir, err := os.MkdirTemp("", "dnd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	names := []string{"it's a file.txt", "$(touch pwned)", `back\slash.txt`}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "a b"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The rows dragged, in order, and the path each one pastes. A folder's
	// path ends in a slash.
	rows := append(slices.Clone(names), "a b/")
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = filepath.Join(dir, r)
		if strings.HasSuffix(r, "/") {
			paths[i] += "/"
		}
	}
	// A stand-in for ssh: a program called ssh that holds the terminal.
	fakeBin := t.TempDir()
	catPath, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("no cat to stand in for ssh")
	}
	data, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "ssh"), data, 0o755); err != nil {
		t.Fatal(err)
	}

	const cols, rowsN = 260, 44
	term, _ := start(t, startOpts{daemonDefault: true, cols: cols, rows: rowsN})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the listing")
	enterTerminalMode(t, term)
	// clearPane clears the screen in the shell. Each call waits for a
	// marker of its own. A marker an earlier clear left on screen ends the
	// wait at once, and the keys typed next reach the shell while clear
	// still runs. When sh is dash, as on Debian and Ubuntu, there is no line
	// editor: the terminal echoes those keys, clear erases them from the
	// screen, and the command line on screen never matches.
	clears := 0
	clearPane := func(t *testing.T) {
		t.Helper()
		clears++
		m := fmt.Sprintf("CLEARED%d", clears)
		runInShell(t, term, "clear; echo "+splitMarker(m), m, uiTimeout)
	}
	runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	clearPane(t)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "the listing", rows...)
	art := artifactDir(t)
	frame := func(name string) {
		t.Helper()
		saveArtifact(t, term, art, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), art, name)
	}

	// Where each row is on the rail, read once while the pane shows none of
	// the names, and a cell well inside the pane.
	at := map[string][2]int{}
	for _, r := range rows {
		c, rr, ok := findOnGrid(term.Screen(), r)
		if !ok {
			t.Fatalf("no row for %q:\n%s", r, term.Snapshot())
		}
		at[r] = [2]int{c, rr}
	}
	railCol := at[rows[0]][0]
	paneCol := cols * 3 / 4
	if railCol > cols/2 {
		paneCol = cols / 4
	}
	paneRow := rowsN / 2

	// drag carries one row onto the pane, waits for the label to name the
	// program in it, and releases. shot names a frame taken mid-drag.
	drag := func(row, program, shot string) {
		t.Helper()
		from := at[row]
		mousePress(t, term, from[0], from[1], tuitest.MouseLeft, 0)
		steps := 6
		for i := 1; i <= steps; i++ {
			mouseMotion(t, term, from[0]+(paneCol-from[0])*i/steps, from[1]+(paneRow-from[1])*i/steps, tuitest.MouseLeft, 0)
		}
		want := "(" + program + ")"
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			txt := s.Text()
			return strings.Contains(txt, row) && strings.Contains(txt, want)
		}, uiTimeout); err != nil {
			mouseRelease(t, term, paneCol, paneRow, tuitest.MouseLeft, 0)
			t.Fatalf("the label never named %s for %q: %v\n%s", want, row, err, term.Snapshot())
		}
		if shot != "" {
			frame(shot)
			checkDragLooks(t, term, at, row)
		}
		mouseRelease(t, term, paneCol, paneRow, tuitest.MouseLeft, 0)
		// The row is put down with the drop.
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !dragRingUp(s) }, uiTimeout); err != nil {
			t.Fatalf("the target frame stayed up after the drop:\n%s", term.Snapshot())
		}
	}

	for i, sh := range dropShells() {
		if sh.start != "" {
			if _, err := exec.LookPath(sh.name); err != nil {
				t.Logf("%s is not installed; its round is skipped", sh.name)
				continue
			}
		}
		t.Run(sh.name, func(t *testing.T) {
			enterTerminalMode(t, term)
			if sh.start != "" {
				if err := term.SendKeys(sh.start, tuitest.Enter); err != nil {
					t.Fatal(err)
				}
				runInShell(t, term, sh.ready, sh.readyMark, 3*uiTimeout)
			}
			if sh.start == "" {
				// sh may have no line editor, and then ctrl+l is a character.
				clearPane(t)
			} else {
				if err := term.SendKeys(tuitest.Ctrl('l')); err != nil {
					t.Fatal(err)
				}
				time.Sleep(300 * time.Millisecond)
			}
			if err := term.SendKeys(sh.printf); err != nil {
				t.Fatal(err)
			}
			line := sh.printf
			for j, r := range rows {
				name := ""
				if i == 0 && j == 0 {
					name = "drag-mid"
				}
				drag(r, sh.name, name)
				line += sh.quote(paths[j]) + " "
				if err := term.WaitForText(strings.TrimSpace(line), uiTimeout); err != nil {
					t.Fatalf("after dropping %q the command line is not\n%s\n%v\n%s", r, line, err, term.Snapshot())
				}
			}
			frame("dropped-" + sh.name)
			// The drop kept terminal mode, so Enter goes to the shell.
			if err := term.SendKeys(tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			want := make([]string, len(paths))
			for j, p := range paths {
				want[j] = "[" + p + "]"
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				for _, w := range want {
					if !screenHasLine(s, w) {
						return false
					}
				}
				return true
			}, uiTimeout); err != nil {
				t.Fatalf("%s did not read the names back as they are on disk: %v\n%s", sh.name, err, term.Snapshot())
			}
			frame("ran-" + sh.name)
			if _, err := os.Stat(filepath.Join(dir, "pwned")); !os.IsNotExist(err) {
				t.Fatalf("a dropped name ran as a command in %s: %v", sh.name, err)
			}

			// The path picker types a name by the same quoting rules.
			if sh.start == "" {
				clearPane(t)
			} else {
				if err := term.SendKeys(tuitest.Ctrl('l')); err != nil {
					t.Fatal(err)
				}
				time.Sleep(300 * time.Millisecond)
			}
			sendKeys(t, term, sh.printf, tuitest.Alt(";"))
			if err := term.WaitForText("╭─ ", uiTimeout); err != nil {
				t.Fatalf("alt+; did not open the path picker in %s: %v\n%s", sh.name, err, term.Snapshot())
			}
			sendKeys(t, term, "back")
			time.Sleep(300 * time.Millisecond)
			sendKeys(t, term, tuitest.Enter)
			picked := sh.printf + sh.quote(`back\slash.txt`)
			if err := term.WaitForText(picked, uiTimeout); err != nil {
				t.Fatalf("the path picker did not type %s in %s: %v\n%s", picked, sh.name, err, term.Snapshot())
			}
			sendKeys(t, term, tuitest.Enter)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHasLine(s, `[back\slash.txt]`) }, uiTimeout); err != nil {
				t.Fatalf("%s did not read the picked name back: %v\n%s", sh.name, err, term.Snapshot())
			}
			if sh.start != "" {
				if err := term.SendKeys("exit", tuitest.Enter); err != nil {
					t.Fatal(err)
				}
				runInShell(t, term, "echo BACK\"\"sh", "BACKsh", uiTimeout)
			}
			leaveTerminalMode(t, term)
		})
	}

	// I on a row is the same drop, from the keyboard, on the focused pane.
	t.Run("key", func(t *testing.T) {
		enterTerminalMode(t, term)
		clearPane(t)
		if err := term.SendKeys(`printf '<%s>\n' `); err != nil {
			t.Fatal(err)
		}
		leaveTerminalMode(t, term)
		sendKeys(t, term, "s")
		// A click on the row puts the rail's cursor there, as it did before
		// rows could be dragged.
		from := at["$(touch pwned)"]
		mouseClick(t, term, from[0], from[1], tuitest.MouseLeft, 0)
		sendKeys(t, term, "I")
		want := `printf '<%s>\n' ` + posixQuote(paths[1])
		if err := term.WaitForText(want, uiTimeout); err != nil {
			t.Fatalf("I did not paste the quoted path: %v\n%s", err, term.Snapshot())
		}
		enterTerminalMode(t, term)
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHasLine(s, "<"+paths[1]+">") }, uiTimeout); err != nil {
			t.Fatalf("the shell did not read the name back: %v\n%s", err, term.Snapshot())
		}
		frame("key-insert")
		if _, err := os.Stat(filepath.Join(dir, "pwned")); !os.IsNotExist(err) {
			t.Fatalf("the name ran as a command: %v", err)
		}
		leaveTerminalMode(t, term)
	})

	// A pane running ssh reads its input on another machine. The drop is
	// refused with the reason, and nothing is typed.
	t.Run("ssh refused", func(t *testing.T) {
		enterTerminalMode(t, term)
		runInShell(t, term, "clear; PATH="+fakeBin+":$PATH; echo SSH\"\"ON; ssh", "SSHON", uiTimeout)
		row := "it's a file.txt"
		from := at[row]
		mousePress(t, term, from[0], from[1], tuitest.MouseLeft, 0)
		for i := 1; i <= 6; i++ {
			mouseMotion(t, term, from[0]+(paneCol-from[0])*i/6, from[1]+(paneRow-from[1])*i/6, tuitest.MouseLeft, 0)
		}
		if err := term.WaitForText("This pane runs ssh.", 2*uiTimeout); err != nil {
			mouseRelease(t, term, paneCol, paneRow, tuitest.MouseLeft, 0)
			t.Fatalf("the label did not refuse the ssh pane: %v\n%s", err, term.Snapshot())
		}
		frame("ssh-refused")
		mouseRelease(t, term, paneCol, paneRow, tuitest.MouseLeft, 0)
		time.Sleep(500 * time.Millisecond)
		if strings.Contains(term.Screen().Text(), dir+"/it") {
			t.Fatalf("the refused drop typed the path into the ssh pane:\n%s", term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
			t.Fatal(err)
		}
		leaveTerminalMode(t, term)
	})

	// esc puts the row down: the release over the pane pastes nothing.
	t.Run("esc", func(t *testing.T) {
		enterTerminalMode(t, term)
		runInShell(t, term, "clear; echo ESC\"\"TEST", "ESCTEST", uiTimeout)
		row := "a b/"
		from := at[row]
		mousePress(t, term, from[0], from[1], tuitest.MouseLeft, 0)
		for i := 1; i <= 6; i++ {
			mouseMotion(t, term, from[0]+(paneCol-from[0])*i/6, from[1]+(paneRow-from[1])*i/6, tuitest.MouseLeft, 0)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			txt := s.Text()
			return strings.Contains(txt, "a b/ →") || strings.Contains(txt, "a b/ ->")
		}, uiTimeout); err != nil {
			t.Fatalf("no label: %v\n%s", err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatal(err)
		}
		mouseRelease(t, term, paneCol, paneRow, tuitest.MouseLeft, 0)
		time.Sleep(500 * time.Millisecond)
		if strings.Contains(term.Screen().Text(), dir+"/a b") {
			t.Fatalf("a drag put down with esc still pasted:\n%s", term.Snapshot())
		}
		leaveTerminalMode(t, term)
	})

	alive(t, term, "after dragging rows into the pane")
}

// dragRingUp reports whether a drop target's frame is on screen: the heavy
// corner the frame is drawn with, which no pane border uses.
func dragRingUp(s tuitest.Screen) bool {
	_, _, ok := dragRingCorner(s)
	return ok
}

// dragRingCorner is the cell of the target frame's top-left corner.
func dragRingCorner(s tuitest.Screen) (int, int, bool) {
	cols, rows := s.Size()
	for r := range rows {
		for c := range cols {
			if s.Cell(c, r).Content == "┏" {
				return c, r, true
			}
		}
	}
	return 0, 0, false
}

// checkDragLooks holds the mid-drag frame to what a person must see: a heavy
// frame on the target pane, and the carried row drawn apart from the other
// rows of the listing.
func checkDragLooks(t *testing.T, term *tuitest.Terminal, at map[string][2]int, held string) {
	t.Helper()
	s := term.Screen()
	if !dragRingUp(s) {
		t.Fatalf("no frame on the target pane mid-drag:\n%s", term.Snapshot())
	}
	var other string
	for name := range at {
		if name != held && !strings.HasSuffix(name, "/") && !strings.HasSuffix(held, "/") {
			other = name
			break
		}
	}
	if other == "" {
		return
	}
	h, o := s.Cell(at[held][0], at[held][1]), s.Cell(at[other][0], at[other][1])
	if h.Fg == o.Fg && h.Bg == o.Bg && h.Underline == o.Underline && h.Reverse == o.Reverse {
		t.Fatalf("the carried row %q looks like %q mid-drag:\n%s", held, other, term.Snapshot())
	}
}

// TestRailFileDragAt16Colours drags a row on a 16 colour terminal, where the
// accent is only an ANSI slot. The target frame must be in reverse video and
// the carried row underlined, the palette's cursor attributes at that depth.
func TestRailFileDragAt16Colours(t *testing.T) {
	dir := fileViewFixture(t)
	term, _ := start(t, startOpts{daemonDefault: true, cols: 160, rows: 40, env: []string{"TERM=xterm", "COLORTERM="}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "the listing", "brief.txt", "zulu/")
	col, row, _ := findOnGrid(term.Screen(), "brief.txt")
	paneCol := 120
	if col > 80 {
		paneCol = 40
	}
	mousePress(t, term, col, row, tuitest.MouseLeft, 0)
	for i := 1; i <= 6; i++ {
		mouseMotion(t, term, col+(paneCol-col)*i/6, row+(20-row)*i/6, tuitest.MouseLeft, 0)
	}
	if err := term.WaitFor(dragRingUp, uiTimeout); err != nil {
		mouseRelease(t, term, paneCol, 20, tuitest.MouseLeft, 0)
		t.Fatalf("no target frame at 16 colours: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	art := artifactDir(t)
	saveArtifact(t, term, art, "drag-16")
	savePNG(t, s, shot.XTermPalette(), art, "drag-16")
	rc, rr, _ := dragRingCorner(s)
	if !s.Cell(rc, rr).Reverse {
		t.Errorf("the target frame is not in reverse video at 16 colours:\n%s", term.Snapshot())
	}
	if !s.Cell(col, row).Underline {
		t.Errorf("the carried row is not underlined at 16 colours:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	mouseRelease(t, term, paneCol, 20, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !dragRingUp(s) && !s.Cell(col, row).Underline }, uiTimeout); err != nil {
		t.Fatalf("esc left the frame or the mark up:\n%s", term.Snapshot())
	}
	alive(t, term, "after a drag at 16 colours")
}

// quickLookFixture is a folder with one file of each kind quick look draws:
// code, Markdown, a picture, a file that is not text, and a text longer than
// the preview keeps. The names sort in the order the test walks them.
func quickLookFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var long strings.Builder
	for i := range 6000 {
		fmt.Fprintf(&long, "row %d\n", i+1)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 8), 160, 255})
		}
	}
	var pic bytes.Buffer
	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"big.txt":  long.String(),
		"blob.bin": "\x00\x01\x02\x03binary",
		"main.go":  "package main\n\nimport \"fmt\"\n\n// main says hello.\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
		"notes.md": "# Quick look notes\n\nSome **bold** words.\n\n- first point\n- second point\n",
		"pic.png":  pic.String(),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// quickLookShows waits for every marker in want to be on screen and none in
// not, and saves the frame under name.
func quickLookShows(t *testing.T, term *tuitest.Terminal, name, why string, want, not []string) {
	t.Helper()
	railLists(t, term, why, want, not)
	copiesFrame(t, term, name)
}

// TestRailQuickLook is idea 5 of the files plan: space on a row of the
// rail's files section shows the file, the preview follows j and k, and
// space or esc closes it.
//
// How this could pass wrongly, written down first:
//   - The text could be on screen in the pane. Nothing the test waits for
//     is printed in the pane: the shell only cds.
//   - The code could be drawn plain. The keyword "package" and the name
//     after it must be drawn in two different colours.
//   - The Markdown could be drawn as its source. The heading must show
//     without its "#", and the bold word without its stars.
//   - The picture could be a blank box. The box must hold block glyphs in
//     more than a few colours.
//   - j could reopen the preview rather than move it, or move the rail's
//     cursor and leave the preview behind. Each step names the new file in
//     the panel's title and shows that file's content.
//
// NEGATIVE CONTROL: with quickLookFetch returning nil for a file, the
// preview stays on "Reading" and the wait for main.go's content times out.
func TestRailQuickLook(t *testing.T) {
	// Block glyphs at truecolor: the tier a host without kitty graphics
	// gets, and the one a frame of the grid can show.
	// TestRailQuickLookKittyPicture covers the other tier.
	term, _ := quickLookStart(t, "main.go",
		[]string{"TUIOS_KITTY_GRAPHICS=0", "TERM=xterm-256color", "COLORTERM=truecolor"}, nil)

	// Code: highlighted, with line numbers.
	sendKeys(t, term, " ")
	// space opens the preview and does nothing else: no path is copied.
	quickLookShows(t, term, "quick-look-go", "space did not show main.go",
		[]string{"main.go ", "package main", "fmt.Println", "8 lines"}, []string{"Copied the path."})
	s := term.Screen()
	kc, kr, ok := findOnGrid(s, "package main")
	if !ok {
		t.Fatalf("no code on screen:\n%s", term.Snapshot())
	}
	if kw, name := s.Cell(kc, kr).Fg, s.Cell(kc+len("package "), kr).Fg; kw == name {
		t.Errorf("ASSERTION: the keyword and the name are the same colour %+v: the code is not highlighted", kw)
	}

	// j: the next file, Markdown, laid out.
	sendKeys(t, term, "j")
	quickLookShows(t, term, "quick-look-md", "j did not move the preview to notes.md",
		[]string{"notes.md ", "Quick look notes", "Markdown", "first point"},
		[]string{"# Quick look", "**bold**", "package main"})

	// j: the picture, as block glyphs on this terminal.
	sendKeys(t, term, "j")
	quickLookShows(t, term, "quick-look-png", "j did not move the preview to pic.png",
		[]string{"pic.png ", "Image", "64 x 32"}, []string{"Quick look notes"})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return pictureColours(s) >= 8 }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the picture was not drawn as glyphs in colour (%d colours): %v\n%s", pictureColours(term.Screen()), err, term.Snapshot())
	}
	// Half blocks only: a finer set picks a shape per cell and draws the
	// smooth gradient as a checker.
	if q := quadrantCells(term.Screen()); q > 0 {
		t.Errorf("ASSERTION: the picture has %d quadrant glyphs; the preview draws half blocks", q)
	}
	copiesFrame(t, term, "quick-look-png")

	// k three times: past notes.md and main.go to the file that is not text.
	sendKeys(t, term, "k", "k", "k")
	quickLookShows(t, term, "quick-look-binary", "the preview of a binary file",
		[]string{"blob.bin ", "This file is not text. It has no preview."}, []string{"64 x 32"})
	sendKeys(t, term, "k")
	quickLookShows(t, term, "quick-look-long", "the preview of a long file",
		[]string{"big.txt ", "Showing the first 5000 lines.", "row 1"}, nil)

	// space closes; space opens again; esc closes and keeps the rail.
	sendKeys(t, term, " ")
	railLists(t, term, "space did not close the preview", nil, []string{"Showing the first"})
	sendKeys(t, term, " ")
	railLists(t, term, "space did not open the preview again", []string{"Showing the first 5000 lines."}, nil)
	sendKeys(t, term, tuitest.Esc)
	railLists(t, term, "esc did not close the preview", nil, []string{"Showing the first"})
	sendKeys(t, term, " ")
	railLists(t, term, "after esc the rail lost the keyboard", []string{"Showing the first 5000 lines."}, nil)

	// The files menu's Preview row shows the row the menu was opened on,
	// which is not the cursor row: the cursor stays on big.txt.
	sendKeys(t, term, tuitest.Esc)
	railLists(t, term, "esc did not close the preview", nil, []string{"Showing the first"})
	mc, mr, ok := findOnGrid(term.Screen(), "main.go")
	if !ok {
		t.Fatalf("no main.go row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, mc, mr, tuitest.MouseRight, 0)
	railLists(t, term, "the files menu has no Preview row", []string{"Preview", "Copy path"}, nil)
	copiesFrame(t, term, "quick-look-menu")
	pc, pr, ok := findOnGrid(term.Screen(), "Preview")
	if !ok {
		t.Fatalf("no Preview row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, pc, pr, tuitest.MouseLeft, 0)
	quickLookShows(t, term, "quick-look-menu-preview", "Preview did not show the row the menu was opened on",
		[]string{"main.go ", "package main", "8 lines"}, []string{"Showing the first", "Preview"})
	// Back on the keyboard, the preview follows the cursor again.
	sendKeys(t, term, "j")
	quickLookShows(t, term, "quick-look-menu-follow", "after Preview, j did not move the preview",
		[]string{"blob.bin ", "This file is not text."}, []string{"package main"})

	// The preview takes no key from the pane: once the rail lets go, keys
	// reach the shell, and the preview is gone.
	sendKeys(t, term, tuitest.Esc, tuitest.Esc)
	railLists(t, term, "leaving the rail did not close the preview", nil, []string{"Showing the first"})
	enterTerminalMode(t, term)
	runInShell(t, term, "echo typed-$((40+2))", "typed-42", uiTimeout)
	alive(t, term, "after quick look")
}

// quickLookStart lists the quick look fixture in the rail, gives the rail
// the keyboard and puts its cursor on the row named first.
func quickLookStart(t *testing.T, first string, env []string, out *lockedBuffer) (*tuitest.Terminal, string) {
	t.Helper()
	dir := quickLookFixture(t)
	opts := startOpts{cols: 140, rows: 40, env: env}
	if out != nil {
		opts.out = out
	}
	term, _ := start(t, opts)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the listing")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "the listing", "main.go", "notes.md", "pic.png")
	sendKeys(t, term, "s")
	col, row, ok := findOnGrid(term.Screen(), first)
	if !ok {
		t.Fatalf("no %s row:\n%s", first, term.Snapshot())
	}
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	clickNoteGone(t, term)
	return term, dir
}

// kittyPlaceRe is a kitty placement of a picture the previews uploaded.
var kittyPlaceRe = regexp.MustCompile(`\x1b_Ga=p,i=(\d+),p=\d+,c=(\d+),r=(\d+),`)

// TestRailQuickLookKittyPicture is the picture on a host with kitty graphics,
// which the test terminal says it has: the picture is uploaded and placed in
// the panel, at no more cells than its own pixels fill, and taken down when
// the preview closes.
//
// NEGATIVE CONTROL: with quickLookPlacement answering false, no placement is
// sent and the wait times out.
func TestRailQuickLookKittyPicture(t *testing.T) {
	out := &lockedBuffer{}
	term, _ := quickLookStart(t, "pic.png", nil, out)
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show pic.png", []string{"pic.png ", "64 x 32"}, nil)
	var id string
	deadline := time.Now().Add(uiTimeout)
	for id == "" {
		for _, m := range kittyPlaceRe.FindAllStringSubmatch(out.String(), -1) {
			cols, _ := strconv.Atoi(m[2])
			rows, _ := strconv.Atoi(m[3])
			// 64 x 32 pixels: a few cells at any cell size, never the panel.
			if cols > 0 && cols <= 16 && rows > 0 && rows <= 8 {
				id = m[1]
			}
		}
		if id == "" && time.Now().After(deadline) {
			t.Fatalf("ASSERTION: no kitty placement of the picture at its own size was sent\n%s", term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	sendKeys(t, term, " ")
	unplace := "\x1b_Ga=d,d=i,i=" + id + ","
	if err := term.WaitFor(func(tuitest.Screen) bool { return strings.Contains(out.String(), unplace) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: closing the preview did not take the picture down: %v", err)
	}
	alive(t, term, "after the kitty picture")
}

// clickNoteGone waits out the note a click on a file row leaves: the click
// copies the row's path. The frames start clean, and a note seen after
// that comes from the key under test, not from the click.
func clickNoteGone(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitForText("Copied the path.", uiTimeout); err != nil {
		t.Fatalf("the click on a file row did not copy its path: %v\n%s", err, term.Snapshot())
	}
	railLists(t, term, "the note of the click stayed", nil, []string{"Copied the path."})
}

// quadrantCells counts the quadrant glyphs on screen (U+2596..U+259F).
func quadrantCells(s tuitest.Screen) int {
	n := 0
	cols, rows := s.Size()
	for y := range rows {
		for x := range cols {
			if r := s.Cell(x, y).Rune; r >= 0x2596 && r <= 0x259f {
				n++
			}
		}
	}
	return n
}

// pictureColours counts the colours of the block glyph cells on screen.
func pictureColours(s tuitest.Screen) int {
	seen := map[tuitest.Color]bool{}
	cols, rows := s.Size()
	for y := range rows {
		for x := range cols {
			c := s.Cell(x, y)
			if c.Rune >= 0x2580 && c.Rune <= 0x259f || c.Rune >= 0x1fb00 && c.Rune <= 0x1fbff {
				seen[c.Fg], seen[c.Bg] = true, true
			}
		}
	}
	return len(seen)
}
