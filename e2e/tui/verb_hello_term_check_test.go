package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelloDropsAnUnsafeTerm: the TERM and COLORTERM a hello names go into
// the environment of every pane of a session that connection creates. The
// daemon keeps a value only when it is at most 64 bytes of printable ASCII
// with no spaces, and drops any other to empty, so the pane gets the
// daemon's fallback (xterm-256color, truecolor). PaneTerm checks TERM against
// terminfo on Unix and nothing checks COLORTERM, so without the check a link
// peer could put control characters or a long string in every pane.
//
// Both hellos are covered: the verb hello, sent raw over the socket as any
// peer may send it, followed by new-session on the same connection; and the
// binary protocol's hello, sent by tuios new -d with the value in TERM.
//
// How this could pass wrongly, written down first:
//   - PaneTerm could be what refuses a long TERM. The daemon is given a
//     terminfo folder holding screen-256color's entry under a name of 64
//     bytes and one of 65, so PaneTerm keeps either, and only the bound tells
//     them apart.
//   - The values could be refused rather than dropped, or the whole hello
//     ignored. Each case keeps one good value beside the bad one, and the
//     first case keeps both at the bound: that is the positive half.
//   - The line read could be the shell's echo of the typed command. Only a
//     line without %s in it, ending in the marker, is read.
//   - A control character could hide in the captured line and still compare
//     equal. The wanted lines are the fallback, which no bad value contains.
//
// The panes' text is saved under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "A hello's TERM and
// COLORTERM are checked before a pane gets them").
func TestHelloDropsAnUnsafeTerm(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	terminfo := filepath.Join(base, "terminfo")
	entry := terminfoEntry(t, "screen-256color")
	name64 := "screen-256color-" + strings.Repeat("x", 64-len("screen-256color-"))
	name65 := name64 + "x"
	if err := os.MkdirAll(filepath.Join(terminfo, "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{name64, name65} {
		if err := os.WriteFile(filepath.Join(terminfo, "s", name), entry, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := tuiosCLIEnv(t, base, []string{"TERMINFO=" + terminfo}, "start-server"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}

	const fallback = "COLORTERM=truecolor"
	cases := []struct {
		name, term, colorTerm, want string
	}{
		{"at-the-bound", name64, "24bit", "TERM=" + name64 + " COLORTERM=24bit"},
		{"too-long", name65, strings.Repeat("c", 65), "TERM=xterm-256color " + fallback},
		{"control", "screen-256color", "24bit\x01x", "TERM=screen-256color " + fallback},
		{"space", "screen-256color", "24 bit", "TERM=screen-256color " + fallback},
		{"not-ascii", "screen-256color", "24bït", "TERM=screen-256color " + fallback},
	}
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	var panes strings.Builder
	for _, tc := range cases {
		session := "e2e-hello-" + tc.name
		c := dialVerbs(t, sock)
		if _, code := c.call("hello", map[string]any{"client": "e2e", "term": tc.term, "color_term": tc.colorTerm}); code != "" {
			t.Fatalf("%s: ASSERTION: the hello was refused with %s, want it answered with the value dropped", tc.name, code)
		}
		if _, code := c.call("new-session", map[string]any{"name": session, "width": 160}); code != "" {
			t.Fatalf("%s: new-session: %s", tc.name, code)
		}
		c.close()
		got, pane := paneTerms(t, base, session)
		panes.WriteString("=== verb hello, ")
		panes.WriteString(tc.name)
		panes.WriteString("\n")
		panes.WriteString(pane)
		panes.WriteString("\n")
		if got != tc.want {
			t.Errorf("%s: a session made after the verb hello named term %q and color_term %q gave its pane %q, want %q",
				tc.name, tc.term, tc.colorTerm, got, tc.want)
		}
	}

	binary := []struct {
		name, term, want string
	}{
		{"at-the-bound", name64, "TERM=" + name64 + " " + fallback},
		{"too-long", name65, "TERM=xterm-256color " + fallback},
	}
	for _, tc := range binary {
		session := "e2e-binary-hello-" + tc.name
		if out, err := tuiosCLIEnv(t, base, []string{"TERM=" + tc.term, "COLORTERM=truecolor"}, "new", "-d", session); err != nil {
			t.Fatalf("%s: tuios new -d: %v\n%s", tc.name, err, out)
		}
		got, pane := paneTerms(t, base, session)
		panes.WriteString("=== binary hello, ")
		panes.WriteString(tc.name)
		panes.WriteString("\n")
		panes.WriteString(pane)
		panes.WriteString("\n")
		if got != tc.want {
			t.Errorf("%s: a session made by tuios new -d with TERM %q gave its pane %q, want %q", tc.name, tc.term, got, tc.want)
		}
	}

	if err := os.WriteFile(filepath.Join(artifactDir(t), "panes.txt"), []byte(panes.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// terminfoEntry reads the compiled terminfo entry for name from the system
// folders, in either layout.
func terminfoEntry(t *testing.T, name string) []byte {
	t.Helper()
	for _, dir := range []string{"/usr/share/terminfo", "/lib/terminfo", "/etc/terminfo", "/usr/lib/terminfo"} {
		for _, sub := range []string{name[:1], fmt.Sprintf("%x", name[0])} {
			if data, err := os.ReadFile(filepath.Join(dir, sub, name)); err == nil {
				return data
			}
		}
	}
	t.Fatalf("no compiled terminfo entry for %s on this machine", name)
	return nil
}

// paneTerms asks session's shell for its TERM and COLORTERM and returns them
// as one "TERM=... COLORTERM=..." string, and the pane's text. Each is printed
// on a line of its own, so a 65 byte TERM still fits the 80 column pane tuios
// new -d makes. The verb cases ask for a wider one, for the 65 byte COLORTERM.
func paneTerms(t *testing.T, base, session string) (got, pane string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-l",
		`printf 'TERM=%s END\nCOLORTERM=%s END\n' "$TERM" "$COLORTERM"`+"\r"); err != nil {
		t.Fatalf("ask the shell of %s for its TERM: %v\n%s", session, err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		pane, _ = tuiosCLI(t, base, "capture-pane", "-s", session)
		var term, colorTerm string
		for line := range strings.SplitSeq(pane, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasSuffix(line, " END") || strings.Contains(line, "%s") {
				continue
			}
			line = strings.TrimSuffix(line, " END")
			switch {
			case strings.HasPrefix(line, "TERM="):
				term = line
			case strings.HasPrefix(line, "COLORTERM="):
				colorTerm = line
			}
		}
		if term != "" && colorTerm != "" {
			return term + " " + colorTerm, pane
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell of %s never printed its TERM and COLORTERM:\n%s", session, pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
