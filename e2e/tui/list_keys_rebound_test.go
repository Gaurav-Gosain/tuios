package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestReboundListKeysMoveEveryList sets [keybindings.lists] to move down on
// ctrl+j and up on ctrl+k, and drives three different lists with them: the
// command palette (a picker with a filter), the review panel and the
// scrollback browser. The palette's and the browser's footers must name
// ctrl+k/ctrl+j, not the arrows or j/k: a hint that names an unbound key is a
// lie. Each was a list that read the up and down keys in its
// own way before R7; each must move on the keys the config names.
//
// How this could pass wrongly, written down first:
//   - The cursor could be read off the wrong line. The palette's cursor row
//     is the row marked › under the search line's rule, the review's is the
//     diff row marked › after the file list's column, and the browser's is
//     the command marked > in its list.
//   - A key could seem to move a list that was going to move anyway. Each
//     list is read once it is still, the key is pressed once, and the
//     cursor must land on a different row.
//   - The browser could start on a row from which down does nothing. g, the
//     default first-row key the config leaves alone, puts it on the first
//     row, the newest command, and ctrl+j must then show the one before it.
//   - The config could be ignored while the defaults still move the lists.
//     ctrl+j and ctrl+k are not default list keys, so a list that moves on
//     them read the config.
//
// Negative control (NEGATIVE_CONTROLS.md): the R7 5a tip, where the review
// panel and the scrollback browser read their own keys, fails on the review
// and the browser steps.
func TestReboundListKeysMoveEveryList(t *testing.T) {
	base, repo := fanFixture(t)
	writeConfig(t, base, "[keybindings.lists]\nlist_down = [\"ctrl+j\"]\nlist_up = [\"ctrl+k\"]\n")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte(reviewDepthBase), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "main.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "main")
	session, path := reviewFanAt(t, base, repo, "keys")
	if err := os.WriteFile(filepath.Join(path, "main.go"), []byte(reviewDepthNew), 0o644); err != nil {
		t.Fatal(err)
	}
	term := attachIn(t, base, session, startOpts{cols: 160, rows: 40})
	dir := artifactDir(t)

	// moved presses key once and waits for cursor() to differ from what it
	// read before the key.
	moved := func(what string, key any, cursor func(tuitest.Screen) string) {
		t.Helper()
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("%s: the screen never settled: %v", what, err)
		}
		before := cursor(term.Screen())
		if before == "" {
			t.Fatalf("%s: no cursor row on screen\n%s", what, term.Snapshot())
		}
		sendKeys(t, term, key)
		// A list that did not move is reported and the test goes on, so one
		// run names every list that ignores the config.
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			now := cursor(s)
			return now != "" && now != before
		}, uiTimeout); err != nil {
			t.Errorf("ASSERTION: %s did not move the cursor off %q\n%s", what, before, term.Snapshot())
		}
		saveArtifact(t, term, dir, strings.ReplaceAll(what, " ", "-"))
	}

	// The palette: a picker with a filter, so only keys that are not
	// letters move it, which ctrl+j and ctrl+k are not.
	sendKeys(t, term, tuitest.Ctrl('p'))
	waitPaletteOpen(t, term, "for the rebound list keys")
	paletteCursor := func(s tuitest.Screen) string {
		lines := strings.Split(s.Text(), "\n")
		rule := -1
		for i, line := range lines {
			if strings.Contains(line, paletteTitle) {
				for j := i + 1; j < len(lines); j++ {
					if strings.Contains(lines[j], "──────────") {
						rule = j
						break
					}
				}
				break
			}
		}
		if rule < 0 {
			return ""
		}
		for _, line := range lines[rule+1:] {
			if _, after, ok := strings.Cut(line, "› "); ok {
				return strings.TrimSpace(strings.ReplaceAll(after, "│", " "))
			}
		}
		return ""
	}
	// Each list's footer names the keys the config holds, not the arrows
	// or j and k it would name by default.
	footerNames := func(what string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "ctrl+k/ctrl+j")
		}, uiTimeout); err != nil {
			t.Errorf("ASSERTION: the %s footer does not name ctrl+k/ctrl+j\n%s", what, term.Snapshot())
		}
	}
	footerNames("palette")
	moved("palette ctrl+j", tuitest.Ctrl('j'), paletteCursor)
	moved("palette ctrl+k", tuitest.Ctrl('k'), paletteCursor)
	closePalette(t, term, "after the rebound list keys")

	// The review panel: its diff rows.
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "M main.go")
	// The diff's cursor row is the one marked › after the file list's
	// column rule.
	reviewCursor := func(s tuitest.Screen) string {
		for line := range strings.SplitSeq(s.Text(), "\n") {
			if _, after, ok := strings.Cut(line, "│ › "); ok {
				return strings.TrimSpace(strings.ReplaceAll(after, "│", " "))
			}
		}
		return ""
	}
	moved("review ctrl+j", tuitest.Ctrl('j'), reviewCursor)
	moved("review ctrl+k", tuitest.Ctrl('k'), reviewCursor)
	sendKeys(t, term, tuitest.Esc)
	waitGone(t, term, "the review", "M main.go")

	// The scrollback browser: three commands in a shell, each with its own
	// output, marked the way a shell with OSC 133 marks them.
	newWindow(t, term)
	enterTerminalMode(t, term)
	block := func(name string) string {
		return `printf '\033]133;A\007$ \033]133;B\007` + name + `\n\033]133;C\007'; echo out; printf '\033]133;D;0\007'; `
	}
	runInShell(t, term, block("cmd-one")+block("cmd-two")+block("cmd-three")+`printf 'DONE-%s\n' MARK`, "DONE-MARK", shellTimeout)
	sendKeys(t, term, tuitest.Ctrl('b'), "s")
	if err := term.WaitForText(browserTitle, uiTimeout); err != nil {
		t.Fatalf("the scrollback browser did not open: %v\n%s", err, term.Snapshot())
	}
	// The browser's cursor row is the command marked > in its list.
	browserCursor := func(s tuitest.Screen) string {
		for line := range strings.SplitSeq(s.Text(), "\n") {
			if _, after, ok := strings.Cut(line, "> cmd-"); ok {
				if name, _, ok := strings.Cut(after, " "); ok {
					return "cmd-" + name
				}
			}
		}
		return ""
	}
	// The browser lists the newest command first, so its first row is
	// cmd-three and the row under it cmd-two.
	footerNames("scrollback browser")
	if strings.Contains(term.Screen().Text(), "j/k") {
		t.Errorf("ASSERTION: the scrollback browser footer still names j/k\n%s", term.Snapshot())
	}
	sendKeys(t, term, "g")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return browserCursor(s) == "cmd-three" }, uiTimeout); err != nil {
		t.Fatalf("g did not put the browser on the first row, the newest command\n%s", term.Snapshot())
	}
	moved("browser ctrl+j", tuitest.Ctrl('j'), browserCursor)
	if got := browserCursor(term.Screen()); got != "cmd-two" {
		t.Errorf("ASSERTION: ctrl+j from the first row is on %q, want cmd-two\n%s", got, term.Snapshot())
	}
	moved("browser ctrl+k", tuitest.Ctrl('k'), browserCursor)
	alive(t, term, "after the rebound list keys moved three lists")
}
