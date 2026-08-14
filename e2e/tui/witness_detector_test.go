package tuie2e

import "testing"

// The negative control for the splice detector.
//
// A rule that has never been seen to fail is not known to be a rule. The
// campaign runs green, which is worth exactly nothing on its own: the same green
// is produced by a detector that returns "clean" unconditionally, and this
// suite has already had one assertion that could not have failed.
//
// This proves the detector rather than the plumbing, which is the honest limit
// of what a table test can do. Proving the plumbing needs a binary that really
// splices a stream, driven through TUIOS_E2E_BIN the way NEGATIVE_CONTROLS.md
// describes for the rest of the suite.
//
// The cases that must not fire matter as much as the one that must. The rule
// runs after every action during a fuzz run, in states nobody chose, so a
// version of it that fired on a clipped pane or under an overlay would bury a
// real finding under thousands of false ones.
func TestSpliceDetector(t *testing.T) {
	const tag = "ab12cd"
	line := func(n int) string { return "MK" + tag + "-" + itoa(n) }

	cases := []struct {
		name  string
		rows  []string
		fires bool
	}{{
		name:  "an unbroken run is clean",
		rows:  []string{line(7), line(8), line(9), line(10)},
		fires: false,
	}, {
		name:  "a hole between adjacent rows is a splice",
		rows:  []string{line(7), line(8), line(400), line(401)},
		fires: true,
	}, {
		name:  "one missing line is still a splice",
		rows:  []string{line(7), line(8), line(10)},
		fires: true,
	}, {
		name:  "rows separated by other content are not compared",
		rows:  []string{line(7), line(8), "$ some other command", line(400), line(401)},
		fires: false,
	}, {
		name: "an overlay covering the middle of a pane is not a splice",
		rows: []string{
			line(7), line(8),
			"╭─ help ───────────╮", "│ q quit           │", "╰──────────────────╯",
			line(400), line(401),
		},
		fires: false,
	}, {
		name:  "a pane clipped to its last rows is not a splice",
		rows:  []string{line(400), line(401), line(402)},
		fires: false,
	}, {
		name:  "another pane's lines in between are not compared",
		rows:  []string{line(7), "MK99ff00-3", line(400)},
		fires: false,
	}, {
		name:  "a single line cannot be a splice",
		rows:  []string{line(7)},
		fires: false,
	}, {
		name:  "witness lines going backwards are a splice",
		rows:  []string{line(400), line(7)},
		fires: true,
	}, {
		name:  "a repeated line is a splice",
		rows:  []string{line(7), line(7)},
		fires: true,
	}, {
		name:  "the command that prints them is not itself a witness line",
		rows:  []string{"$ printf 'MK" + tag + "-%d\\n' $(seq 1 200)", line(1), line(2)},
		fires: false,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b, found := spliceIn(c.rows)
			if found != c.fires {
				t.Fatalf("spliceIn(%q) fired=%v, want %v (%d -> %d)",
					c.rows, found, c.fires, a.seq, b.seq)
			}
		})
	}
}

// TestSpliceDetectorSeesTheClientScreen is the same rule fired through the whole
// path it runs on during a campaign: a real pane, a real daemon, a real PTY, and
// the client's own rendered grid read back through tuitest.
//
// The hole is genuine and the pane really printed it. One printf emits 1 2 3 and
// then 200 201 202, so six adjacent rows carry a jump that no clip, overlay or
// covering pane could produce, and the detector is right to fire. That is the
// point: what is under test here is not tuios but whether the rule can see a
// hole at all once it is reading a composited screen through an emulator instead
// of a slice of strings the test built itself.
//
// Without it, the table above proves a function and the campaign proves nothing:
// a rule that silently read the wrong rows, or read them after the pattern had
// been broken by a border, would pass every green run for the same reason it
// would miss every real splice.
func TestSpliceDetectorSeesTheClientScreen(t *testing.T) {
	term, base := livenessSession(t, "splice-plumbing")
	w := firstWindow(t, base, "splice-plumbing")
	tag := w.tag()

	// One command, so the rows are adjacent: a second command would put a prompt
	// and an echoed command line between the two runs, and the rule would
	// correctly decline to compare across them.
	if err := paneSend(base, "splice-plumbing", w.ID,
		"printf 'MK"+tag+"-%d\\n' 1 2 3 200 201 202\n"); err != nil {
		t.Fatalf("seed the pane: %v", err)
	}
	if err := term.WaitForText("MK"+tag+"-202", shellTimeout); err != nil {
		t.Fatalf("the pane never printed its last line: %v\n%s", err, term.Snapshot())
	}

	a, b, found := spliceIn(screenLines(term.Screen()))
	if !found {
		t.Fatalf("the rule read a client screen carrying a %d to %d jump and saw nothing\n%s",
			3, 200, term.Snapshot())
	}
	if a.seq != 3 || b.seq != 200 || a.tag != tag {
		t.Fatalf("the rule fired on %s %d -> %d, want %s 3 -> 200", a.tag, a.seq, b.seq, tag)
	}
}

// TestWitnessTagIsStable pins the tag derivation, because every rule keyed on it
// silently stops comparing anything if it changes shape: a tag the pattern does
// not match makes every pane look like it printed nothing, and a rule that
// compares nothing passes.
func TestWitnessTagIsStable(t *testing.T) {
	w := daemonWindow{ID: "F47AC9E3-42a3-46b7-9729-b5690bbe1f71"}
	if got := w.tag(); got != "f47ac9" {
		t.Fatalf("tag() = %q, want %q", got, "f47ac9")
	}
	if !witnessRe.MatchString("MK" + w.tag() + "-1") {
		t.Fatalf("a line built from tag() does not match the witness pattern")
	}
	short := daemonWindow{ID: "ab"}
	if got := short.tag(); got != "000000" {
		t.Fatalf("a short id gave tag() = %q, want a padded placeholder", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
