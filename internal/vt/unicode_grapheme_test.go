package vt_test

// Grapheme clustering, driven by the official UAX #29 break test file.
//
// The file is used as a corpus of adversarial input, not as an oracle. The
// emulator does not implement clustering itself, it delegates to the segmenter
// in x/ansi, which tracks its own Unicode version; holding the emulator to
// Unicode 17 would mostly measure that library's release schedule. What these
// tests assert is the part the emulator owns: a cluster its segmenter
// recognises has to end up in one cell group, has to survive a write boundary
// falling anywhere inside it, and has to survive a resize.
//
// See testdata/unicode/README.md for provenance and licence.

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// uaxCase is one line of GraphemeBreakTest.txt: the input string and the
// clusters the file says it breaks into.
type uaxCase struct {
	line     int
	input    string
	clusters []string
	comment  string
}

// loadGraphemeBreakTest parses GraphemeBreakTest.txt.
//
// Each data line is a run of code points separated by ÷ (a break here) or ×
// (no break here), starting and ending with ÷, followed by a # comment:
//
//	÷ 0061 × 0301 ÷ 0062 ÷	#  ÷ [0.2] LATIN SMALL LETTER A ...
func loadGraphemeBreakTest(t *testing.T) []uaxCase {
	t.Helper()
	path := filepath.Join("testdata", "unicode", "GraphemeBreakTest.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close() //nolint:errcheck

	var cases []uaxCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := sc.Text()
		comment := ""
		if i := strings.IndexByte(line, '#'); i >= 0 {
			comment = strings.TrimSpace(line[i+1:])
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var (
			input    strings.Builder
			clusters []string
			current  strings.Builder
		)
		for _, tok := range strings.Fields(line) {
			switch tok {
			case "÷":
				if current.Len() > 0 {
					clusters = append(clusters, current.String())
					current.Reset()
				}
			case "×":
				// No break: the next code point joins the current cluster.
			default:
				cp, err := strconv.ParseUint(tok, 16, 32)
				if err != nil {
					t.Fatalf("%s:%d: bad code point %q: %v", path, lineNo, tok, err)
				}
				input.WriteRune(rune(cp))
				current.WriteRune(rune(cp))
			}
		}
		if current.Len() > 0 {
			clusters = append(clusters, current.String())
		}
		if input.Len() == 0 {
			continue
		}
		cases = append(cases, uaxCase{line: lineNo, input: input.String(), clusters: clusters, comment: comment})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(cases) < 500 {
		t.Fatalf("%s parsed to only %d cases, which means the parser is wrong", path, len(cases))
	}
	return cases
}

// segment splits s the way the emulator's own segmenter does, which is the
// clustering the emulator is held to.
func segment(s string) []string {
	var out []string
	for len(s) > 0 {
		cluster, _ := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		out = append(out, cluster)
		s = s[len(cluster):]
	}
	return out
}

// visible drops the clusters that leave no mark on the grid, so that a screen
// can be compared against them.
//
// Two kinds go. A cluster of zero width is a combining mark with no base to
// attach to, which every terminal discards rather than giving a cell of its
// own. A cluster that is a single space is indistinguishable from the blank
// cell underneath it once it is on the grid, so it is dropped from both sides
// rather than guessed at.
func visible(clusters []string) []string {
	out := clusters[:0:0]
	for _, c := range clusters {
		if c == " " || ansi.StringWidth(c) == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// cellGroups reads a row back as the clusters it holds: each occupied cell and
// its continuation cells collapse to one entry. A cluster that got split across
// cells shows up here as two entries where there should be one.
func cellGroups(emu *vt.Emulator, y int) []string {
	var out []string
	for x := 0; x < emu.Width(); {
		c := emu.CellAt(x, y)
		if c == nil {
			x++
			continue
		}
		step := c.Width
		if step < 1 {
			step = 1
		}
		// Filtered the same way visible filters the expectation, so the two
		// sides are comparable. A cluster that got split still shows up, as a
		// head whose content is short of what it should be.
		if c.Content != "" && c.Content != " " && ansi.StringWidth(c.Content) > 0 {
			out = append(out, c.Content)
		}
		x += step
	}
	return out
}

// knownSplit reports whether the emulator is known to split this input, so the
// sweep can pass while the divergence stays on the record.
//
// One pattern is known: a Prepend character followed by a printable ASCII base.
// Prepend binds forwards, so `U+0600 b` is one cluster, but the printable-ASCII
// path draws its character as soon as it sees it and cannot be talked out of it
// by a character that arrived earlier. The two end up in separate cells.
//
// It is left unfixed on purpose. The fix is to route ASCII through the buffered
// path whenever anything is buffered, and that turns every character after the
// first non-ASCII one on a line into a buffered write, which is a performance
// cliff on the hottest path in the emulator for the sake of six Arabic and two
// Kaithi code points. TestConform_PrependBeforeASCII records the divergence and
// will announce itself if it is ever fixed.
func knownSplit(input string) bool {
	prepend := []rune{0x0600, 0x0601, 0x0602, 0x0603, 0x0604, 0x0605, 0x06DD,
		0x070F, 0x0890, 0x0891, 0x08E2, 0x110BD, 0x110CD}
	rs := []rune(input)
	for i := 0; i < len(rs)-1; i++ {
		next := rs[i+1]
		if next < 0x20 || next >= 0x7f {
			continue
		}
		for _, p := range prepend {
			if rs[i] == p {
				return true
			}
		}
	}
	return false
}

// TestUnicode_ClusterOccupiesOneCell drives every sequence in the UAX #29 file
// through the emulator and checks that each cluster the segmenter recognises
// landed in exactly one cell group, in order.
//
// Control characters are skipped: the file includes CR, LF and other controls
// as cluster material, and a terminal is required to act on those rather than
// print them.
func TestUnicode_ClusterOccupiesOneCell(t *testing.T) {
	cases := loadGraphemeBreakTest(t)
	checked := 0
	for _, tc := range cases {
		if hasControl(tc.input) || knownSplit(tc.input) {
			continue
		}
		want := visible(segment(tc.input))
		if len(want) == 0 {
			continue
		}

		emu := vt.NewEmulator(80, 2)
		if _, err := emu.WriteString(tc.input); err != nil {
			t.Fatalf("line %d: write: %v", tc.line, err)
		}
		got := cellGroups(emu, 0)
		if !equalStrings(got, want) {
			t.Errorf("line %d: %s\n  input    %s\n  clusters %s\n  on screen %s",
				tc.line, tc.comment, escapeRunes(tc.input), escapeSlice(want), escapeSlice(got))
		}
		checked++
	}
	if checked < 400 {
		t.Fatalf("only %d cases ran, which means the filter is wrong", checked)
	}
	t.Logf("checked %d of %d UAX #29 sequences", checked, len(cases))
}

// TestUnicode_ClusterSurvivesAWriteBoundary splits every sequence at every byte
// boundary and requires the screen to come out the same as the unsplit write.
//
// This is not a hypothetical. A PTY read returns whatever bytes have arrived,
// so a cluster genuinely does arrive in pieces, and the emulator carries state
// across writes to reassemble it. The exhaustive sweep is behind -short because
// it runs a few hundred thousand writes.
func TestUnicode_ClusterSurvivesAWriteBoundary(t *testing.T) {
	cases := loadGraphemeBreakTest(t)
	for _, tc := range cases {
		if hasControl(tc.input) {
			continue
		}

		whole := vt.NewEmulator(80, 2)
		if _, err := whole.WriteString(tc.input); err != nil {
			t.Fatalf("line %d: write: %v", tc.line, err)
		}
		want := cellGroups(whole, 0)

		splits := boundaries(tc.input, testing.Short())
		for _, at := range splits {
			part := vt.NewEmulator(80, 2)
			if _, err := part.WriteString(tc.input[:at]); err != nil {
				t.Fatalf("line %d: write: %v", tc.line, err)
			}
			if _, err := part.WriteString(tc.input[at:]); err != nil {
				t.Fatalf("line %d: write: %v", tc.line, err)
			}
			if got := cellGroups(part, 0); !equalStrings(got, want) {
				t.Errorf("line %d: %s\n  splitting %s after %d bytes changed the screen\n  whole %s\n  split %s",
					tc.line, tc.comment, escapeRunes(tc.input), at, escapeSlice(want), escapeSlice(got))
				break
			}
		}
	}
}

// TestUnicode_SegmenterAgreesWithUAX29 reports how far the segmenter the
// emulator uses has drifted from the pinned Unicode version. It does not fail
// on drift, because that is the library's release schedule rather than a bug
// here, but it names every disagreement so an upgrade on either side is a
// visible change rather than a silent one.
func TestUnicode_SegmenterAgreesWithUAX29(t *testing.T) {
	cases := loadGraphemeBreakTest(t)
	var differ int
	for _, tc := range cases {
		if !equalStrings(visible(segment(tc.input)), visible(tc.clusters)) {
			differ++
			if differ <= 20 {
				t.Logf("line %d differs from Unicode 17.0: %s\n  UAX  %s\n  ansi %s\n  %s",
					tc.line, escapeRunes(tc.input), escapeSlice(visible(tc.clusters)),
					escapeSlice(visible(segment(tc.input))), tc.comment)
			}
		}
	}
	t.Logf("segmenter disagrees with UAX #29 on %d of %d sequences", differ, len(cases))
}

// boundaries returns the byte offsets to split an input at. In short mode it
// samples rather than trying every one.
func boundaries(s string, short bool) []int {
	var out []int
	for i := 1; i < len(s); i++ {
		out = append(out, i)
	}
	if short && len(out) > 4 {
		out = out[:4]
	}
	return out
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func escapeRunes(s string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, r := range []rune(s) {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatInt(int64(r), 16))
	}
	b.WriteByte(']')
	return b.String()
}

func escapeSlice(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = escapeRunes(s)
	}
	return "{" + strings.Join(parts, " ") + "}"
}
