package tuie2e

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarFileMenuRail checks both actions with files enabled on only one
// rail, using mouse and Enter. The clipboard and opener record must name
// the row. Using the same fixture on the primary rail is the control.
func TestSidebarFileMenuRail(t *testing.T) {
	for _, rail := range []struct {
		name, right, left string
	}{
		{"secondary", "sessions,terminals,agents", "files,git"},
		{"primary", "files,sessions", "sessions"},
	} {
		for _, action := range []string{"Copy path", "Open with system"} {
			for _, selection := range []string{"mouse", "enter"} {
				t.Run(rail.name+"/"+action+"/"+selection, func(t *testing.T) {
					dir := fileViewFixture(t)
					base := t.TempDir()
					record := filepath.Join(base, "opened.txt")
					stubs, _ := openStubs(t, base, record)
					writeConfig(t, base, fmt.Sprintf(`[appearance.sidebar]
enabled = true
position = "right"
width = 28
sections = %q
[appearance.sidebar.left]
enabled = true
width = 28
sections = %q
`, rail.right, rail.left))
					out := &lockedBuffer{}
					term := startIn(t, base, startOpts{cols: 120, rows: 40, out: out, env: openStubEnv(stubs), daemonDefault: true})
					waitBoot(t, term)
					newWindow(t, term)
					waitWindowCount(t, term, 1, "opening a shell for the listing")
					enterTerminalMode(t, term)
					runInShell(t, term, fmt.Sprintf(`cd %q && printf '\033]7;file://%%s\033\\%%s\n' "$PWD" lis""ted`, dir), "listed", uiTimeout)
					leaveTerminalMode(t, term)
					const name = "brief.txt"
					waitForAll(t, term, uiTimeout, "the listing", name)
					x, y, ok := findOnGrid(term.Screen(), name)
					if !ok || (rail.name == "secondary" && x >= 28) || (rail.name == "primary" && x < 92) {
						t.Fatalf("file row is not on the %s rail:\n%s", rail.name, term.Snapshot())
					}
					mouseClick(t, term, x, y, tuitest.MouseRight, 0)
					waitForAll(t, term, uiTimeout, "the file menu", action)
					art := artifactDir(t)
					saveArtifact(t, term, art, "file-menu")
					from := len(clipboardWrites(out))
					if selection == "mouse" {
						x, y, ok = findOnGrid(term.Screen(), action)
						if !ok {
							t.Fatalf("file menu lost %q:\n%s", action, term.Snapshot())
						}
						mouseClick(t, term, x+1, y, tuitest.MouseLeft, 0)
					} else {
						// Copy path is first; Preview and Edit precede Open with system.
						if action == "Open with system" {
							sendKeys(t, term, "j", "j", "j")
						}
						sendKeys(t, term, tuitest.Enter)
					}
					path := filepath.Join(dir, name)
					if action == "Copy path" {
						waitClipboardSequence(t, term, out, from, path)
					} else {
						waitOpenRecord(t, term, record, []string{systemOpener + " " + path}, action)
					}
					saveArtifact(t, term, art, "action-complete")
				})
			}
		}
	}
}
