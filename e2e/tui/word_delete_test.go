package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The text prompts took backspace and ctrl+u and nothing else, so alt+backspace
// and ctrl+w, which every shell and editor reads as "delete the word before the
// cursor", did nothing at all. On macOS with the Option key sent as Alt the
// terminal writes ESC DEL for Option+Backspace, which is tuitest's
// Alt(Backspace), so that is the chord these tests press.
//
// The rename editor and the filtering overlays are two code paths that each had
// their own copy of the edit rule, so each gets its own test: a fix to one would
// not show the other still broken.

// TestOptionBackspaceDeletesAWordInTheRenameEditor types a two-word name, takes
// the last word off with Option+Backspace, types another and commits. The
// committed name is the assertion, not the field: a key the editor ignores
// leaves the field showing what was typed, and only the result tells "ignored"
// from "deleted".
func TestOptionBackspaceDeletesAWordInTheRenameEditor(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open rename editor: %v", err)
	}
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}

	// The editor may open on the window's current name, so start from nothing.
	if err := term.SendKeys(tuitest.Ctrl('u'), "one two"); err != nil {
		t.Fatalf("type the name: %v", err)
	}
	if err := term.WaitForText("one two", uiTimeout); err != nil {
		t.Fatalf("the editor did not take the typed name: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Alt(tuitest.Backspace)); err != nil {
		t.Fatalf("press Option+Backspace: %v", err)
	}
	// The word is gone and the space before it is not: a rule that took the
	// space too would run the next word into the first.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "one two")
	}, uiTimeout); err != nil {
		t.Fatalf("Option+Backspace left the last word in the editor: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys("three", tuitest.Enter); err != nil {
		t.Fatalf("finish the name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "one three") && !renameDialogUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the window was not named %q: %v\n%s", "one three", err, term.Snapshot())
	}
}

// TestControlWDeletesAWordInTheRenameEditor is the same rule on the readline
// spelling of the chord, with trailing spaces: the word before the cursor is
// the one to delete even when blanks stand between it and the cursor.
func TestControlWDeletesAWordInTheRenameEditor(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open rename editor: %v", err)
	}
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('u'), "alpha beta  "); err != nil {
		t.Fatalf("type the name: %v", err)
	}
	if err := term.WaitForText("alpha beta", uiTimeout); err != nil {
		t.Fatalf("the editor did not take the typed name: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('w'), "gamma", tuitest.Enter); err != nil {
		t.Fatalf("delete a word and finish the name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "alpha gamma") && !renameDialogUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the window was not named %q: %v\n%s", "alpha gamma", err, term.Snapshot())
	}
}

// TestOptionBackspaceDeletesAWordInTheCommandPalette covers the filtering
// overlays, which share one edit rule. The palette stands for them. The query
// matches nothing, so no result row can carry the text the assertions read.
func TestOptionBackspaceDeletesAWordInTheCommandPalette(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "on ctrl+p")

	if err := term.SendKeys("zqx1 zqx2 zqx3"); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	if err := term.WaitForText("zqx1 zqx2 zqx3", uiTimeout); err != nil {
		t.Fatalf("the palette did not take the typed query: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Alt(tuitest.Backspace)); err != nil {
		t.Fatalf("press Option+Backspace: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "zqx1 zqx2") && !strings.Contains(text, "zqx3")
	}, uiTimeout); err != nil {
		t.Fatalf("Option+Backspace did not take the last word off the query: %v\n%s", err, term.Snapshot())
	}
	// It is an edit, not a way out: the palette is still the thing on screen.
	if !strings.Contains(term.Screen().Text(), paletteTitle) {
		t.Fatalf("the palette closed on Option+Backspace\n%s", term.Snapshot())
	}
}

// The boxes that kept their own copy of the edit rule now share
// editFilterQuery, so Option+Backspace and Ctrl+W take a word in them as in the
// palette. A word is what the shell's Option+Backspace takes: "src/dir1/dir2"
// becomes "src/dir1/", then "src/".
//
// How this could pass wrongly, written down first:
//   - The typed text is on screen twice in some boxes, as the query and in a
//     "no match" line. The check is that "dir2" is gone from the whole screen.
//   - A word key that emptied the query would also remove "dir2". The check that
//     "src/dir1/" is still on screen tells the two rules apart, and the second
//     press must take "dir1" and keep "src/".
//   - The key could be taken before the box sees it, which only the real client
//     shows.
//   - Copy-mode search switches on the key's code, where Option+Backspace has
//     the code of backspace. A word case put after the backspace case would take
//     one character, and the copy-mode row would show "/src.dir1.dir" where it
//     requires "/src.dir1.". It is the box this ordering can break again.
//
// Copy-mode search draws its prompt, "/" and the query, on the pane's last row.
// The pane is untiled for it: with tiling on, Ctrl+B [ does not enter copy mode
// in this harness.
func TestWordKeysDeleteAWordInTheOtherBoxes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		open         []any
		typed        string
		first, again string // on screen after one word key, and after two
		config       string
	}{
		{"go-to picker", []any{tuitest.Ctrl('b'), "G"}, "src/dir1/dir2", "src/dir1/", "src/", ""},
		// "/" turns the help's search off, so its query has dots in it.
		{"help search", []any{tuitest.Ctrl('b'), "?", "/"}, "src.dir1.dir2", "src.dir1.", "src.", ""},
		// "/" walks the path picker into a folder and never reaches its query, so
		// the query there has dots in it.
		{"path picker", []any{tuitest.Alt(";")}, "src.dir1.dir2", "src.dir1.", "src.", ""},
		{"copy-mode search", []any{tuitest.Ctrl('b'), "[", "/"}, "src.dir1.dir2", "/src.dir1.", "/src.", "[startup]\ntiled = false\n"},
	} {
		for _, key := range []struct {
			name string
			k    tuitest.Key
		}{
			{"option+backspace", tuitest.Alt(tuitest.Backspace)},
			{"ctrl+w", tuitest.Ctrl('w')},
		} {
			t.Run(tc.name+"/"+key.name, func(t *testing.T) {
				base := t.TempDir()
				if tc.config != "" {
					writeConfig(t, base, tc.config)
				}
				term := startIn(t, base, startOpts{})
				waitBoot(t, term)
				newWindow(t, term)
				enterTerminalMode(t, term) // the path picker types into the focused pane
				send := func(keys ...any) {
					t.Helper()
					for _, x := range keys {
						var err error
						switch v := x.(type) {
						case tuitest.Key:
							err = term.SendKeys(v)
						case string:
							err = term.SendKeys(v)
						}
						if err != nil {
							t.Fatalf("send %v: %v", x, err)
						}
					}
					_ = term.WaitStable(uiTimeout)
				}
				has := func(s string) bool { return strings.Contains(term.Screen().Text(), s) }

				send(tc.open...)
				send(tc.typed)
				if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "dir2") }, uiTimeout); err != nil {
					t.Fatalf("the box never showed the typed query: %v\n%s", err, term.Snapshot())
				}
				send(key.k)
				if has("dir2") || !has(tc.first) {
					t.Errorf("after one word key the screen should show %q and no dir2\n%s", tc.first, term.Snapshot())
				}
				send(key.k)
				if has("dir1") || !has(tc.again) {
					t.Errorf("after two word keys the screen should show %q and no dir1\n%s", tc.again, term.Snapshot())
				}
			})
		}
	}
}

// TestWordKeysDeleteAWordInTheFileDialog renames "my notes.txt" from
// the rail's right-click menu. The dialog opens holding the name. Two word
// keys take off "txt", then "." and "notes", leaving "my ". "plan.txt" is
// typed and enter commits.
func TestWordKeysDeleteAWordInTheFileDialog(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tuitest.Key
	}{
		{"option+backspace", tuitest.Alt(tuitest.Backspace)},
		{"ctrl+w", tuitest.Ctrl('w')},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			dir := workDirIn(t, base)
			if err := os.WriteFile(filepath.Join(dir, "my notes.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			term := startIn(t, base, startOpts{})
			waitBoot(t, term)
			newWindow(t, term)
			toggleSidebarViaPalette(t, term)
			waitForAll(t, term, uiTimeout, "the file in the rail's files section", "my notes.txt")

			col, row := findOnScreen(t, term, "my notes.txt")
			mouseClick(t, term, col, row, tuitest.MouseRight, 0)
			if err := term.WaitForText("Rename", uiTimeout); err != nil {
				t.Fatalf("the file's menu offered no Rename: %v\n%s", err, term.Snapshot())
			}
			col, row = findOnScreen(t, term, "Rename")
			mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
			if err := term.WaitForText("Rename my notes.txt", uiTimeout); err != nil {
				t.Fatalf("the menu row did not open the file dialog: %v\n%s", err, term.Snapshot())
			}

			// "my notes.txt" -> "my notes." -> "my ".
			for range 2 {
				if err := term.SendKeys(tc.key); err != nil {
					t.Fatalf("press the word key: %v", err)
				}
			}
			if err := term.SendKeys("plan.txt", tuitest.Enter); err != nil {
				t.Fatalf("finish the name: %v", err)
			}

			want := filepath.Join(dir, "my plan.txt")
			deadline := time.Now().Add(uiTimeout)
			for {
				if _, err := os.Stat(want); err == nil {
					break
				}
				if time.Now().After(deadline) {
					entries, _ := os.ReadDir(dir)
					var names []string
					for _, e := range entries {
						names = append(names, e.Name())
					}
					t.Fatalf("the file was not renamed to %q: the word key left the old name or took the blank too. The folder holds %v\n%s",
						filepath.Base(want), names, term.Snapshot())
				}
				time.Sleep(100 * time.Millisecond)
			}
			if _, err := os.Stat(filepath.Join(dir, "my notes.txt")); err == nil {
				t.Errorf("the old name is still there")
			}
		})
	}
}
