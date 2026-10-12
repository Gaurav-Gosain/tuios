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

// Issue #649: in Claude Code, a prompt holding a URL followed by Chinese text
// ran past the right edge of the pane and pushed the border over.
//
// The URL detector reads up to the next space, so the address held the Chinese
// characters. tuios wrote that address raw into the frame's OSC 8. The UTF-8
// continuation bytes of many runes are 0x80-0x9f, which a parser that reads
// 8-bit controls takes as C1, and 0x9c is ST: "术" is E6 9C AF. The host ended
// the sequence there and printed the rest of the address as text, so the row
// came out wider than the pane and everything after it moved right.
//
// The frame now percent-encodes every byte outside printable ASCII in a link's
// address, as the OSC 8 spec asks. tuitest reads 8-bit controls the way the
// hosts in the report do, so the broken frame shows here as the same overflow.

// unicodeLinkURL is the bare URL the pane prints. "术" carries the 0x9c byte.
const unicodeLinkURL = "https://example.com/技术/方案"

// unicodeMarkedURL is the OSC 8 link's address. It differs from the bare URL,
// so the hover label naming it is not the bare URL's text read off the screen.
const unicodeMarkedURL = "https://example.com/文档/技术"

// percentEncodeHigh percent-encodes every byte of s outside printable ASCII,
// the form the frame must carry.
func percentEncodeHigh(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c > 0x20 && c < 0x7f {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// lastBorderCol returns the column of the last "│" on row, or -1.
func lastBorderCol(s tuitest.Screen, row int) int {
	cols, _ := s.Size()
	for c := cols - 1; c >= 0; c-- {
		if s.Cell(c, row).Content == "│" {
			return c
		}
	}
	return -1
}

// unicodeRowsStayInPane checks the rows the script printed: each starts where
// it should, its last word is where the pane put it, and its right border is
// at the same column as the plain row's. It returns the bare URL's position.
func unicodeRowsStayInPane(t *testing.T, term *tuitest.Terminal, when string) (int, int) {
	t.Helper()
	s := term.Screen()
	_, refRow, ok := findOnGrid(s, "R: a plain row")
	if !ok {
		t.Fatalf("%s: the plain row is not on screen:\n%s", when, term.Snapshot())
	}
	want := lastBorderCol(s, refRow)
	if want < 0 {
		t.Fatalf("%s: the plain row has no right border:\n%s", when, term.Snapshot())
	}
	var urlCol, urlRow int
	for _, r := range []struct {
		prefix, head, last string
		// width is the columns from the start of head to last.
		width int
	}{
		// 技术 and 方案 are two columns each, with "/" and a space between.
		{"U: ", "U: https://example.com/", "TAIL", len("U: https://example.com/") + 4 + 1 + 4 + 1},
		{"G: ", "G: label ", "END", len("G: label ")},
	} {
		col, row, ok := findOnGrid(s, r.prefix)
		if !ok {
			t.Fatalf("%s: the row %q is not on screen:\n%s", when, r.prefix, term.Snapshot())
		}
		if r.prefix == "U: " {
			urlCol, urlRow = col, row
		}
		if !gridMatchesAt(s, col, row, r.head) {
			t.Errorf("%s: the row does not start with %q, so the frame moved its text:\n%s", when, r.head, term.Snapshot())
		}
		if !gridMatchesAt(s, col+r.width, row, r.last) {
			t.Errorf("%s: %q is not at column %d of row %d, so the row is wider than the pane:\n%s",
				when, r.last, col+r.width, row, term.Snapshot())
		}
		if got := lastBorderCol(s, row); got != want {
			t.Errorf("%s: the row %q has its right border at column %d, the plain row at %d:\n%s",
				when, r.prefix, got, want, term.Snapshot())
		}
	}
	return urlCol, urlRow
}

// TestAUnicodeURLStaysInsideItsPane prints a bare URL with Chinese characters
// in it and text after it, and an OSC 8 link whose address holds them. Each row
// keeps the pane's width, focused and unfocused: its text is where the pane put
// it and the pane's right border has not moved. The frame carries the bare
// URL's address percent-encoded, and a ctrl+click still opens the address the
// pane printed. The OSC 8 link's hover label shows its address decoded, and a
// ctrl+click opens it encoded.
//
// Negative control, confirmed red (see NEGATIVE_CONTROLS.md): with the address
// written raw into the OSC 8, the text after the URL and the border move.
func TestAUnicodeURLStaysInsideItsPane(t *testing.T) {
	base := t.TempDir()
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(base, "unicode.sh")
	body := "#!/bin/sh\n" +
		"printf 'R: a plain row\\n'\n" +
		"printf 'U: " + unicodeLinkURL + " TAIL\\n'\n" +
		"printf 'G: \\033]8;;" + unicodeMarkedURL + "\\033\\\\label\\033]8;;\\033\\\\ END\\n'\n" +
		"printf 'UNI''DONE\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n", opener))

	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{out: out, env: []string{"SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the unicode links")
	enterTerminalMode(t, term)
	runInShell(t, term, "sh "+script, "UNIDONE", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}

	col, row := unicodeRowsStayInPane(t, term, "focused")

	// The frame carries the bare URL as a link, with the address percent-encoded.
	encoded := percentEncodeHigh(unicodeLinkURL)
	deadline := time.Now().Add(uiTimeout)
	for !bareLinkFrame(encoded).MatchString(out.String()) {
		if time.Now().After(deadline) {
			t.Fatalf("no frame carries the URL as OSC 8 with the address %q", encoded)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The bare URL still opens the address the pane printed.
	mouseClick(t, term, col+6, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{unicodeLinkURL}, "ctrl+click on a URL with Chinese characters")

	// The OSC 8 link's hover label shows the address as the program wrote it,
	// and a ctrl+click opens it in the encoded form tuios keeps.
	gcol, grow := mustFind(t, term, "G: label")
	mouseHover(t, term, gcol+5, grow)
	if err := term.WaitForText(unicodeMarkedURL, uiTimeout); err != nil {
		t.Errorf("the hover label does not show %q: %v\n%s", unicodeMarkedURL, err, term.Snapshot())
	}
	mouseClick(t, term, gcol+5, grow, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{unicodeLinkURL, percentEncodeHigh(unicodeMarkedURL)},
		"ctrl+click on an OSC 8 link with Chinese characters")

	// An unfocused pane is drawn by another path, the emulator's own renderer.
	newWindow(t, term)
	waitWindowCount(t, term, 2, "opening a second window")
	enableTiling(t, term, "R: a plain row")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled after tiling: %v\n%s", err, term.Snapshot())
	}
	unicodeRowsStayInPane(t, term, "unfocused")
	alive(t, term, "after the unicode links")
}
