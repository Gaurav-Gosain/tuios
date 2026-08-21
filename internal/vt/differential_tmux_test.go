//go:build differential

package vt_test

// A differential test against tmux.
//
// Every other test in this package states what the emulator should do in the
// words of whoever wrote the test, which means a case can be wrong in exactly
// the same way the code is. An independent implementation cannot be. tmux has
// its own VT emulator, written in C, with no shared ancestry with the parser
// here, and it can be driven headlessly: start a pane running `cat` on a file
// of bytes, then ask tmux to print the resulting screen.
//
// It is behind a build tag because it needs the tmux binary and spawns
// processes, which does not belong in the default suite. Run it with:
//
//	go test -tags differential ./internal/vt/ -run TestDifferential -v
//
// A disagreement is a finding, not automatically a bug here: tmux is an
// implementation with its own choices, not a specification. Cases where tmux is
// the one that diverges are listed in tmuxDiffers with the reason, so the test
// stays useful instead of being switched off.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

type diffCase struct {
	name       string
	cols, rows int
	in         string
}

// tmuxDiffers names the cases where tmux, not this emulator, is the odd one
// out, with the reason. Nothing is listed here without a reason that says which
// behaviour is right and why.
var tmuxDiffers = map[string]string{
	"wrap then backspace": "tmux lets the cursor sit one column past the last one and " +
		"decrements from there, so a backspace after a full line leaves it on the last " +
		"column. xterm keeps the cursor on the last column with a pending-wrap flag, and " +
		"its CursorBack clears the flag and then moves back one, landing a column earlier. " +
		"ghostty and every terminal that follows xterm's model agree with this one",

	"restore a pending wrap": "the same root cause as the backspace above. tmux has no " +
		"pending-wrap flag to save, so a DECSC taken at the end of a full line and " +
		"restored has nothing to restore and the next character overwrites the last one " +
		"instead of starting a new line. xterm lists the wrap flag among what DECSC saves",

	"insert splitting a wide character": "tmux declines to insert inside a double-width " +
		"character and shifts from the cell after it instead, which silently moves the " +
		"insertion point the program asked for. This one blanks the character the insert " +
		"splits, which is what ghostty does and what the rest of this emulator already " +
		"does for a wide character cut by a resize or an overwrite",

	"forward tab several stops": "tmux does not implement CHT at all and leaves the cursor " +
		"where it was; xterm has had it since the VT100 and terminfo reaches it through `cht`",

	"tab with a zero parameter": "same as above: tmux ignores CHT, so the zero-means-one " +
		"rule never comes up for it",

	"delete splitting a wide character": "tmux leaves the row untouched rather than delete " +
		"inside a double-width character, so the program's delete silently does nothing. " +
		"Same reasoning as the insert above",
}

func TestDifferential_AgainstTmux(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns tmux processes")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}

	cases := []diffCase{
		// Wrapping, which is where the two most likely disagree.
		{"plain wrap", 10, 4, "abcdefghijklm"},
		{"wrap exactly at the margin", 10, 4, "abcdefghij"},
		{"wrap then carriage return", 10, 4, "abcdefghij\rX"},
		{"wrap then backspace", 10, 4, "abcdefghij\bX"},
		{"no wrap with DECAWM off", 10, 4, "\x1b[?7labcdefghijklm"},
		{"wide characters wrapping", 10, 4, "世世世世世世"},
		{"a wide character with one column left", 10, 4, "abcdefghi世X"},
		{"wide characters on an odd width", 9, 4, "世世世世世"},

		// Scroll regions.
		{"scroll up inside a region", 10, 6, "a\r\nb\r\nc\r\nd\r\ne\x1b[2;4r\x1b[1S"},
		{"scroll down inside a region", 10, 6, "a\r\nb\r\nc\r\nd\r\ne\x1b[2;4r\x1b[1T"},
		{"reverse index at the top of a region", 10, 6, "a\r\nb\r\nc\r\nd\r\ne\x1b[2;4r\x1b[2;1H\x1bM"},
		{"index at the bottom of a region", 10, 6, "a\r\nb\r\nc\r\nd\r\ne\x1b[2;4r\x1b[4;1H\x1bD"},
		{"a region bottom past the screen", 10, 6, "a\r\nb\r\nc\r\nd\r\ne\x1b[1;40r\x1b[2S"},
		{"origin mode addressing", 10, 6, "\x1b[2;4r\x1b[?6h\x1b[1;1HX"},
		{"origin mode clamped below the region", 10, 6, "\x1b[2;4r\x1b[?6h\x1b[9;1HX"},
		{"newline at the bottom of a region", 10, 6, "a\r\nb\r\nc\r\nd\r\ne\x1b[2;4r\x1b[4;1H\n"},

		// Insert and delete.
		{"insert characters", 10, 4, "abcdef\x1b[1;2H\x1b[2@"},
		{"delete characters", 10, 4, "abcdef\x1b[1;2H\x1b[2P"},
		{"insert lines", 10, 4, "a\r\nb\r\nc\x1b[2;1H\x1b[1L"},
		{"delete lines", 10, 4, "a\r\nb\r\nc\x1b[2;1H\x1b[1M"},
		{"erase characters", 10, 4, "abcdef\x1b[1;2H\x1b[3X"},
		{"delete more than the row holds", 10, 4, "abcdef\x1b[1;2H\x1b[99P"},
		{"insert splitting a wide character", 10, 4, "世界ab\x1b[1;2H\x1b[1@"},
		{"delete splitting a wide character", 10, 4, "世界ab\x1b[1;2H\x1b[1P"},
		{"overwriting the lead of a wide character", 10, 4, "世界\x1b[1;1HX"},
		{"overwriting the trail of a wide character", 10, 4, "世界\x1b[1;2HX"},

		// Erasing.
		{"erase to the end of the line", 10, 4, "abcdef\x1b[1;3H\x1b[K"},
		{"erase to the start of the line", 10, 4, "abcdef\x1b[1;3H\x1b[1K"},
		{"erase the whole line", 10, 4, "abcdef\x1b[1;3H\x1b[2K"},
		{"erase to the end of the screen", 10, 4, "a\r\nb\r\nc\x1b[2;1H\x1b[J"},
		{"erase to the start of the screen", 10, 4, "abc\r\ndef\r\nghi\x1b[2;2H\x1b[1J"},
		{"erase the whole screen", 10, 4, "a\r\nb\r\nc\x1b[2J"},

		// Cursor motion with awkward parameters.
		{"cursor forward with a zero parameter", 10, 4, "\x1b[0CX"},
		{"cursor position past the screen", 10, 4, "\x1b[99;99HX"},
		{"cursor position with omitted parameters", 10, 4, "\x1b[;5HX"},
		{"cursor back past the first column", 10, 4, "\x1b[1;3H\x1b[99DX"},
		{"repeat the previous character", 10, 4, "a\x1b[4b"},

		// Tabs. Every one of these fills the screen with the alignment pattern
		// first, so that no cell is left unset. tmux's capture-pane prints a
		// literal tab for any run of unset cells, wherever the cursor actually
		// ended up, so on a blank screen the two sides cannot be compared at
		// all. Over a filled screen the column a tab landed on is visible.
		{"default tab stops", 20, 4, "\x1b#8\tA\tB"},
		{"a tab past the last stop", 20, 4, "\x1b#8\t\t\t\t\tX"},
		{"clearing every tab stop", 20, 4, "\x1b#8\x1b[3g\tX"},
		{"setting a tab stop", 20, 4, "\x1b#8\x1b[1;4H\x1bH\x1b[1;1H\tX"},
		{"backward tab", 20, 4, "\x1b#8\x1b[1;14H\x1b[ZX"},
		{"forward tab several stops", 20, 4, "\x1b#8\x1b[2IX"},
		{"tab with a zero parameter", 20, 4, "\x1b#8\x1b[0IX"},

		// Resets and the alternate screen.
		{"alignment pattern", 8, 3, "\x1b#8"},
		{"soft reset leaves the screen", 10, 4, "abc\x1b[2;3r\x1b[!pX"},
		{"full reset clears the screen", 10, 4, "abc\x1bcX"},
		{"leaving the alternate screen", 10, 4, "main\x1b[?1049hgone\x1b[?1049l"},
		{"save and restore the cursor", 10, 4, "\x1b[2;4H\x1b7\x1b[1;1H\x1b8X"},

		// Text.
		{"a combining mark on an ASCII base", 10, 4, "caféX"},
		{"a keycap sequence", 10, 4, "1️⃣X"},
		{"a joined emoji family", 10, 4, "\U0001f469‍\U0001f469‍\U0001f467X"},
		{"a regional indicator pair", 10, 4, "\U0001f1fa\U0001f1f8X"},
		{"backspace over a wide character", 10, 4, "世\bX"},

		// The sequences this round fixed. Each was wrong here before, so
		// checking them against a second implementation is worth more than
		// checking the ones that already agreed.
		//
		// The character sets are absent on purpose. tmux implements them, but
		// capture-pane prints the byte the guest sent rather than the glyph the
		// designated set maps it to: ESC ( 0 q q q, which every curses program
		// draws a rule with, dumps as qqq under tmux and as a rule here. There
		// is no comparison to be had through this window, so the sets are left
		// to conform_charset_test.go, which checks them against the manual.
		{"shift out with nothing designated", 10, 4, "\x0eqqq"},
		{"insert mode shifts the line right", 10, 4, "abcdef\x1b[1;1H\x1b[4hZ"},
		{"insert mode with a wide character", 10, 4, "abcdef\x1b[1;1H\x1b[4h世"},
		{"insert mode off again", 10, 4, "abcdef\x1b[1;1H\x1b[4h\x1b[4lZ"},
		{"repeat a wide character", 10, 4, "世\x1b[2b"},
		{"repeat a character carrying a mark", 10, 4, "é\x1b[2b"},
		{"the alignment pattern clears the margins", 8, 4, "\x1b[2;3r\x1b#8\x1b[9;1HX"},
		{"restore a pending wrap", 10, 4, "abcdefghij\x1b7\x1b[1;1H\x1b8g"},
		{"restore origin mode with the cursor", 10, 6, "\x1b[2;5r\x1b[?6h\x1b7\x1b[?6l\x1b8\x1b[1;1HX"},
	}

	sock := "tuios-diff-" + t.Name()
	dir := t.TempDir()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })
	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()

	var diffs int
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tmuxScreen(t, tmux, sock, dir, i, tc)
			got := trimTrailingBlankLines(dumpScreen(runCase(t, tc)))
			want = trimTrailingBlankLines(want)

			if got == want {
				if reason, ok := tmuxDiffers[tc.name]; ok {
					t.Errorf("this case is listed as one where tmux differs (%s) but they now agree; "+
						"remove it from tmuxDiffers", reason)
				}
				return
			}
			if reason, ok := tmuxDiffers[tc.name]; ok {
				t.Logf("known difference, tmux is the one diverging: %s", reason)
				return
			}
			diffs++
			t.Errorf("tuios and tmux disagree\ninput %q\n--- tuios ---\n%s\n--- tmux ---\n%s\n--- end ---",
				tc.in, boxed(got), boxed(want))
		})
	}
	if diffs > 0 {
		t.Logf("%d of %d cases disagree", diffs, len(cases))
	}
}

// runCase writes a case into a fresh emulator.
func runCase(t *testing.T, tc diffCase) *vt.Emulator {
	t.Helper()
	emu := vt.NewEmulator(tc.cols, tc.rows)
	if _, err := emu.WriteString(tc.in); err != nil {
		t.Fatalf("write: %v", err)
	}
	return emu
}

// tmuxScreen runs the same bytes through tmux and returns what it drew.
//
// The bytes go into a file and a pane runs `cat` on it, because that puts them
// on the pane's pseudo-terminal exactly as a program would write them, with no
// shell echo or prompt in the way. A sentinel file marks when cat is finished,
// which is more reliable than sleeping.
func tmuxScreen(t *testing.T, tmux, sock, dir string, i int, tc diffCase) string {
	t.Helper()

	bytesPath := filepath.Join(dir, "in")
	donePath := filepath.Join(dir, "done")
	_ = os.Remove(donePath)
	if err := os.WriteFile(bytesPath, []byte(tc.in), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// A session per case, sized for the case. Reusing one costs less but a
	// resize between cases is itself a thing under test elsewhere, and a fresh
	// pane is the only way to be sure nothing carried over.
	name := "c" + itoa(i)
	cmd := exec.Command(tmux, "-L", sock, "-f", "/dev/null",
		"new-session", "-d", "-s", name,
		"-x", itoa(tc.cols), "-y", itoa(tc.rows),
		"cat "+shellQuote(bytesPath)+"; touch "+shellQuote(donePath)+"; sleep 60")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	defer func() { _ = exec.Command(tmux, "-L", sock, "kill-session", "-t", name).Run() }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(donePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tmux never finished writing the input")
		}
		time.Sleep(2 * time.Millisecond)
	}
	// cat having exited means the bytes are on the pty, not that tmux has
	// finished parsing them. One more short settle covers the gap.
	time.Sleep(30 * time.Millisecond)

	out, err := exec.Command(tmux, "-L", sock, "capture-pane", "-p", "-t", name).Output()
	if err != nil {
		t.Fatalf("tmux capture-pane: %v", err)
	}
	return strings.TrimRight(string(out), "\n")
}

func trimTrailingBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
