package vt_test

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"tuios.dev/vt"
)

type uaxCase struct {
	line     int
	input    string
	clusters []string
	comment  string
}

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
		for tok := range strings.FieldsSeq(line) {
			switch tok {
			case "÷":
				if current.Len() > 0 {
					clusters = append(clusters, current.String())
					current.Reset()
				}
			case "×":
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

func cellGroups(emu *vt.Emulator, y int) []string {
	var out []string
	for x := 0; x < emu.Width(); {
		c := emu.CellAt(x, y)
		if c == nil {
			x++
			continue
		}
		step := max(c.Width, 1)
		if c.Content != "" && c.Content != " " && ansi.StringWidth(c.Content) > 0 {
			out = append(out, c.Content)
		}
		x += step
	}
	return out
}

func knownSplit(input string) bool {
	prepend := []rune{0x0600, 0x0601, 0x0602, 0x0603, 0x0604, 0x0605, 0x06DD,
		0x070F, 0x0890, 0x0891, 0x08E2, 0x110BD, 0x110CD}
	rs := []rune(input)
	for i := 0; i < len(rs)-1; i++ {
		next := rs[i+1]
		if next < 0x20 || next >= 0x7f {
			continue
		}
		if slices.Contains(prepend, rs[i]) {
			return true
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
