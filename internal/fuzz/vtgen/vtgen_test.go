package vtgen_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"
)

// brokenTerm stands in for the emulator as it was before the scroll region was
// clamped to the screen. It implements exactly the bug and nothing else: it
// takes the bottom row a guest names at face value, and the first scroll inside
// that region indexes past the end of its buffer.
//
// This is here as a calibration weight. A generator that never finds a bug is
// indistinguishable from a generator that never generates anything, and the
// only way to tell them apart is to hand it a bug that is known to exist and
// known to be reachable, and check that it comes back with it. The bug chosen
// is the real one: a scroll region sized for a taller terminal, which is what
// crashed the harness this project tests with, and what crashed this emulator.
type brokenTerm struct {
	rows        []string
	top, bottom int
}

func newBrokenTerm(cols, rows int) *brokenTerm {
	_ = cols
	t := &brokenTerm{rows: make([]string, rows)}
	t.top, t.bottom = 0, rows
	return t
}

func (t *brokenTerm) resize(cols, rows int) {
	_ = cols
	if rows < 0 {
		rows = 0
	}
	if len(t.rows) > rows {
		t.rows = t.rows[:rows]
	} else {
		for len(t.rows) < rows {
			t.rows = append(t.rows, "")
		}
	}
	t.top, t.bottom = 0, rows
}

// write reads only the two sequences the bug needs. Everything else is passed
// over, the way the parser would.
func (t *brokenTerm) write(s string) {
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
			j++
		}
		if j >= len(s) {
			return
		}
		params, final := s[i+2:j], s[j]
		switch final {
		case 'r':
			// The bug: the named bottom is stored without being checked
			// against the screen that exists.
			top, bottom := 1, len(t.rows)
			parts := strings.Split(params, ";")
			if len(parts) > 0 && parts[0] != "" {
				if n, err := strconv.Atoi(parts[0]); err == nil {
					top = n
				}
			}
			if len(parts) > 1 && parts[1] != "" {
				if n, err := strconv.Atoi(parts[1]); err == nil {
					bottom = n
				}
			}
			if top < 1 {
				top = 1
			}
			if top >= bottom {
				break
			}
			t.top, t.bottom = top-1, bottom
		case 'S':
			n := 1
			if params != "" {
				if v, err := strconv.Atoi(params); err == nil && v > 0 {
					n = v
				}
			}
			// The count is capped, which the real emulator also does. The bug
			// being modelled is the index, and it goes out of range on the
			// first iteration; leaving a count of two billion to run would
			// only make the calibration slow.
			n = min(n, len(t.rows)+2)
			// And the consequence: every row of the region is an index.
			for range n {
				for y := t.top; y < t.bottom-1; y++ {
					t.rows[y] = t.rows[y+1]
				}
				if t.bottom-1 >= 0 {
					t.rows[t.bottom-1] = ""
				}
			}
		}
		i = j
	}
}

// replayBroken reports whether a script makes the stand-in panic.
func replayBroken(s vtgen.Script) (failed bool) {
	defer func() {
		if recover() != nil {
			failed = true
		}
	}()
	t := newBrokenTerm(80, 24)
	for _, seq := range s {
		if seq.Kind == "resize" {
			t.resize(seq.Cols, seq.Rows)
			continue
		}
		t.write(seq.Bytes)
	}
	return false
}

// TestGeneratorFindsUnclampedScrollRegion is the calibration run. It asserts
// three things at once: the generator reaches the bug, it reaches it quickly,
// and the shrinker reduces what it finds to something a person can act on.
func TestGeneratorFindsUnclampedScrollRegion(t *testing.T) {
	// Not reduced under -short: the whole run costs a few hundredths of a
	// second, and cutting it below the seed that first reaches the bug would
	// turn the calibration into a test of nothing.
	const seeds, steps = 200, 150

	var (
		found   int
		firstAt = -1
		best    vtgen.Script
	)
	for seed := range uint64(seeds) {
		script := vtgen.New(seed).Script(steps)
		if !replayBroken(script) {
			continue
		}
		found++
		if firstAt < 0 {
			firstAt = int(seed)
			best = vtgen.Shrink(script, replayBroken)
		}
	}

	if found == 0 {
		t.Fatalf("%d seeds of %d steps never produced a scroll region past the end of the "+
			"screen, so the generator is not covering the class that has crashed this "+
			"project twice", seeds, steps)
	}
	t.Logf("%d of %d seeds reached the bug, first at seed %d", found, seeds, firstAt)

	// A report nobody reads is not a report. Delta debugging should be leaving
	// a handful of lines, not a hundred.
	if len(best) > 8 {
		t.Errorf("the shrinker left %d steps, which is too many to read:\n%s", len(best), best)
	}
	if !replayBroken(best) {
		t.Fatalf("the shrunk script no longer reproduces, so shrinking is unsound:\n%s", best)
	}

	// And it has to still contain the thing that matters, rather than having
	// been reduced to something that fails for a different reason.
	text := best.String()
	if !strings.Contains(text, "margins") {
		t.Errorf("the shrunk script does not mention the scroll region:\n%s", text)
	}
	t.Logf("shrunk to %d steps:\n%s", len(best), text)
}

// TestScriptIsReproducible checks the contract the corpus depends on: the same
// seed and the same bytes always decode to the same script. Without it a
// recorded failure is not a failure anyone else can look at.
func TestScriptIsReproducible(t *testing.T) {
	for seed := range uint64(20) {
		a := vtgen.New(seed).Script(50)
		b := vtgen.New(seed).Script(50)
		if a.String() != b.String() {
			t.Fatalf("seed %d produced two different scripts", seed)
		}
	}
	input := []byte("a corpus entry that a mutator produced")
	if vtgen.FromBytes(input).Script(50).String() != vtgen.FromBytes(input).Script(50).String() {
		t.Fatal("the same bytes produced two different scripts")
	}
}

// TestSplitWritesCutsWhereARealReadWould checks the property the helper exists
// for. Reassembling has to be exact, or a replay is testing different input
// from the one the script names, and the cuts have to land mid-character often
// enough to matter, or the helper is only re-testing whole sequences under a
// longer name.
func TestSplitWritesCutsWhereARealReadWould(t *testing.T) {
	cutMidCharacter := false
	for src := range uint64(20) {
		s := vtgen.New(src).Script(80)

		var rebuilt strings.Builder
		for _, piece := range s.SplitWrites(src) {
			rebuilt.WriteString(piece)
			if !utf8.ValidString(piece) {
				cutMidCharacter = true
			}
		}
		if rebuilt.String() != s.Bytes() {
			t.Fatalf("src %d: the pieces do not reassemble into what the script writes", src)
		}
	}
	if !cutMidCharacter {
		t.Error("no boundary landed inside a multi-byte character, which is the case a PTY read hits daily")
	}
}

// TestSplitWritesIsReproducible is the same contract the scripts themselves
// keep: a recorded failure has to replay with the same boundaries, or the
// boundary that caused it is gone.
func TestSplitWritesIsReproducible(t *testing.T) {
	s := vtgen.New(11).Script(60)
	a, b := s.SplitWrites(3), s.SplitWrites(3)
	if len(a) != len(b) {
		t.Fatalf("the same source gave %d pieces and then %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("piece %d differs between two runs of the same source", i)
		}
	}
}

// TestScriptRendersReadably guards the other half of the point of generating by
// grammar: every step says what it is, and the bytes are printed escaped rather
// than raw, so a failure can be pasted into a bug report.
func TestScriptRendersReadably(t *testing.T) {
	s := vtgen.New(7).Script(40)
	text := s.String()
	if strings.ContainsRune(text, 0x1b) {
		t.Error("the rendered script contains a raw escape byte, which will not survive a paste")
	}
	for i, seq := range s {
		if seq.Desc == "" {
			t.Errorf("step %d has no description", i+1)
		}
		if seq.Kind == "" {
			t.Errorf("step %d has no kind", i+1)
		}
	}
	if lines := strings.Count(text, "\n"); lines != len(s) {
		t.Errorf("the rendered script has %d lines for %d steps", lines, len(s))
	}
}
