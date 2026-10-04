package tuie2e

import (
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
