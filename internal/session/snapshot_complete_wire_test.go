package session

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The state a snapshot has to carry for a pane to come back exactly, beyond
// what the cells, the cursor and the modes say: the tab stops, the titles and
// the title stack, the colours the guest set, the ANSI modes, and the input
// the daemon's parser is part way through when the snapshot is taken. A client
// restored without one of them looks right until the guest sends the sequence
// that reads it.
//
// Ways this can go wrong, each a group of cases below:
//   - the tab stops are dropped, so a tab after the snapshot lands on the
//     default stops; or a cleared table comes back as the default; or the
//     table is restored before the resize that resets it;
//   - the title stack is dropped, so a program that quits after the snapshot
//     pops nothing and the pane keeps the program's title; or the icon name
//     goes, or an entry that saved only one of the two comes back with both;
//   - a colour the guest set with OSC 4 is dropped, so what it prints in that
//     slot after the snapshot is painted in the default colour; or a slot it
//     reset is carried as set; or the OSC 10, 11 and 12 defaults are dropped,
//     so a query after the snapshot is answered differently;
//   - insert mode or newline mode is dropped, so what the guest prints or
//     feeds after the snapshot lands somewhere else;
//   - the snapshot is taken in the middle of an escape sequence or a UTF-8
//     character, and the rest of it arrives on the stream and prints as
//     text; or the replay paints bytes from before the sequence twice, or
//     carries out a control inside it twice;
//   - a client emulator that was itself left in the middle of a sequence by
//     the stream it was on keeps that sequence, and the restore lands in it;
//   - each of the above survives one wire form and not the other.
var snapshotStateCases = []seamCase{
	// Tab stops.
	{"tabs-set", "\x1b[3g\x1b[1;4H\x1bH\x1b[1;11H\x1bH\x1b[1;30H\x1bH\x1b[2;1H", "\tA\tB\tC"},
	{"tabs-cleared", "\x1b[3g\x1b[2;1H", "\tZ"},
	{"tabs-one-cleared", "\x1b[1;9H\x1b[0g\x1b[2;1H", "\tA\tB"},
	{"tabs-cleared-at-cursor", "\x1b[1;17H\x1b[g\x1b[2;1H", "\tA\tB\tC"},
	{"tabs-reset", "\x1b[3g\x1b[?5W\x1b[2;1H", "\tA"},
	{"tabs-backward", "\x1b[3g\x1b[1;6H\x1bH\x1b[1;20H", "\x1b[ZB"},

	// Titles.
	{"title", "\x1b]2;hello\x07", ""},
	{"title-and-icon", "\x1b]0;both\x07\x1b]1;icon\x07", ""},
	{"title-stack-pop", "\x1b]2;shell\x07\x1b[22;0t\x1b]2;vim\x07", "\x1b[23;0t"},
	{"title-stack-two-deep", "\x1b]2;a\x07\x1b[22t\x1b]2;b\x07\x1b[22t\x1b]2;c\x07", "\x1b[23t\x1b[23t"},
	{"title-stack-icon-only", "\x1b]0;both\x07\x1b[22;1t\x1b]0;other\x07", "\x1b[23;1t"},
	{"title-stack-title-only", "\x1b]0;both\x07\x1b[22;2t\x1b]0;other\x07", "\x1b[23;0t"},

	// Colours.
	{"palette-slot", "\x1b]4;1;rgb:ff/00/00\x07", "\x1b[31mred\x1b[m"},
	{"palette-slot-256", "\x1b]4;200;#00ff00\x07", "\x1b[38;5;200mg\x1b[m"},
	{"palette-slot-reset", "\x1b]4;1;#ff0000\x07\x1b]104;1\x07", "\x1b[31mred\x1b[m"},
	{"default-colours", "\x1b]10;#112233\x07\x1b]11;#445566\x07\x1b]12;#778899\x07", ""},
	{"default-colours-reset", "\x1b]10;#112233\x07\x1b]11;#445566\x07\x1b]110\x07", ""},

	// ANSI modes.
	{"insert-mode", "abcdef\x1b[1;1H\x1b[4h", "XY"},
	{"insert-mode-off", "\x1b[4h\x1b[4l\x1b[1;1Habc\x1b[1;1H", "X"},
	{"newline-mode", "\x1b[20h", "a\nb\nc"},
}

func TestWireCarriesTheSnapshotState(t *testing.T) {
	newPure := func() vt.Terminal { return vt.NewEmulator(fidelityCols, fidelityRows) }
	runSnapshotStateCases(t, snapshotStateCases, newPure, newPure)
}

// runSnapshotStateCases is runSeamCases, comparing what the cells cannot show
// as well.
func runSnapshotStateCases(t *testing.T, cases []seamCase, newDaemon, newClient func() vt.Terminal) {
	for _, form := range wireForms {
		for _, c := range cases {
			t.Run(form.name+"/"+c.name, func(t *testing.T) {
				daemon := newDaemon()
				defer closeEmulator(daemon)
				client := newClient()
				defer closeEmulator(client)

				mustFeed(t, daemon, c.before)
				state := TerminalStateOf(daemon, daemon.Width(), daemon.Height(), 100, 0)
				ApplyTerminalState(client, throughWire(t, state, form.packed))
				compareSnapshotState(t, daemon, client, "after the restore")

				mustFeed(t, daemon, c.after)
				mustFeed(t, client, c.after)
				compareEmulators(t, daemon, client)
				compareSnapshotState(t, daemon, client, "after the stream")
			})
		}
	}
}

// cutStreams are output a snapshot can be taken in the middle of. Each is cut
// at every byte.
var cutStreams = []struct {
	name, out string
}{
	{"csi-sgr", "ab\x1b[1;31mred\x1b[0m"},
	{"csi-private", "\x1b[?25l\x1b[?2004hx"},
	// A control inside a CSI is carried out where it is, and the CSI goes on.
	{"csi-with-a-control", "x\x1b[2\n;3Hy"},
	{"osc-title", "\x1b]2;a title\x07after"},
	{"osc-title-st", "\x1b]2;st title\x1b\\after"},
	{"osc8-link", "pre \x1b]8;;https://e.example/\x1b\\link\x1b]8;;\x1b\\ post"},
	{"osc-palette", "\x1b]4;1;#ff0000\x07\x1b[31mR"},
	{"utf8", "a日本b✳c"},
	{"esc-charset", "\x1b(0qqq\x1b(Bx"},
	{"esc-esc", "\x1b\x1b[1mB"},
	{"dcs", "\x1bPzz\x1b\\d"},
	{"apc", "\x1b_not kitty\x1b\\w"},
	{"can", "\x1b[1\x18x"},
}

// TestWireCarriesACutSequence takes the snapshot at every byte of each
// stream, restores it, and lets the rest of the stream arrive on both sides.
// The client then has to match the daemon, which read the stream uncut.
func TestWireCarriesACutSequence(t *testing.T) {
	newPure := func() vt.Terminal { return vt.NewEmulator(fidelityCols, fidelityRows) }
	runCutStreams(t, newPure, newPure)
}

func runCutStreams(t *testing.T, newDaemon, newClient func() vt.Terminal) {
	for _, form := range wireForms {
		for _, s := range cutStreams {
			for cut := 1; cut < len(s.out); cut++ {
				t.Run(fmt.Sprintf("%s/%s/%d", form.name, s.name, cut), func(t *testing.T) {
					daemon := newDaemon()
					defer closeEmulator(daemon)
					client := newClient()
					defer closeEmulator(client)

					mustFeed(t, daemon, s.out[:cut])
					state := TerminalStateOf(daemon, daemon.Width(), daemon.Height(), 100, 0)
					ApplyTerminalState(client, throughWire(t, state, form.packed))
					mustFeed(t, daemon, s.out[cut:])
					mustFeed(t, client, s.out[cut:])
					compareEmulators(t, daemon, client)
					compareSnapshotState(t, daemon, client, "after the rest of the stream")
				})
			}
		}
	}
}

// TestWireDropsTheClientsOwnCut is the pane whose emulator survives a
// workspace switch: its stream stopped part way through a sequence, and the
// snapshot it is brought level with was taken later. The sequence the client
// was left in is not the daemon's any more.
func TestWireDropsTheClientsOwnCut(t *testing.T) {
	newPure := func() vt.Terminal { return vt.NewEmulator(fidelityCols, fidelityRows) }
	runClientsOwnCut(t, newPure, newPure)
}

func runClientsOwnCut(t *testing.T, newDaemon, newClient func() vt.Terminal) {
	for _, form := range wireForms {
		for _, s := range cutStreams {
			for left := 1; left < len(s.out); left++ {
				// The snapshot is taken a few bytes after the client left.
				cut := min(left+3, len(s.out)-1)
				t.Run(fmt.Sprintf("%s/%s/%d-%d", form.name, s.name, left, cut), func(t *testing.T) {
					daemon := newDaemon()
					defer closeEmulator(daemon)
					client := newClient()
					defer closeEmulator(client)

					mustFeed(t, client, s.out[:left])
					mustFeed(t, daemon, s.out[:cut])
					state := TerminalStateOf(daemon, daemon.Width(), daemon.Height(), 100, 0)
					ApplyTerminalState(client, throughWire(t, state, form.packed))
					mustFeed(t, daemon, s.out[cut:]+"END")
					mustFeed(t, client, s.out[cut:]+"END")
					compareEmulators(t, daemon, client)
				})
			}
		}
	}
}

// TestWireCutsAnOversizedSequence is a sequence longer than the emulator
// keeps. The snapshot carries no more of it than the parser would, and the
// client still ends up where the daemon does.
func TestWireCutsAnOversizedSequence(t *testing.T) {
	newPure := func() vt.Terminal { return vt.NewEmulator(fidelityCols, fidelityRows) }
	runOversizedCut(t, newPure, newPure)
}

func runOversizedCut(t *testing.T, newDaemon, newClient func() vt.Terminal) {
	daemon := newDaemon()
	defer closeEmulator(daemon)
	client := newClient()
	defer closeEmulator(client)

	mustFeed(t, daemon, "\x1b]2;"+strings.Repeat("a", 5<<20))
	state := TerminalStateOf(daemon, daemon.Width(), daemon.Height(), 100, 0)
	if n := len(state.PendingInput); n == 0 || n > maxPendingInput {
		t.Fatalf("the snapshot carries %d bytes of the open sequence, want between 1 and %d", n, maxPendingInput)
	}
	ApplyTerminalState(client, throughWire(t, state, true))
	rest := strings.Repeat("b", 1024) + "\x07after"
	mustFeed(t, daemon, rest)
	mustFeed(t, client, rest)
	compareEmulators(t, daemon, client)
	compareSnapshotState(t, daemon, client, "after the end of the sequence")
}

// TestWireKeepsAClientsStateFromAnOlderDaemon is a snapshot from a daemon
// from before these fields. A client that survived a workspace switch keeps
// what it has, rather than reading the missing fields as cleared.
func TestWireKeepsAClientsStateFromAnOlderDaemon(t *testing.T) {
	client := vt.NewEmulator(fidelityCols, fidelityRows)
	defer closeEmulator(client)
	mustFeed(t, client, "\x1b[3g\x1b[1;5H\x1bH\x1b]2;kept\x07\x1b[22t\x1b]4;1;#ff0000\x07\x1b[4h")

	daemon := vt.NewEmulator(fidelityCols, fidelityRows)
	defer closeEmulator(daemon)
	state := TerminalStateOf(daemon, daemon.Width(), daemon.Height(), 100, 0)
	state.TabStops, state.TitlesKnown, state.Title, state.TitleStack = nil, false, "", nil
	state.GuestColors, state.ANSIModes = nil, nil

	before := client.Titles()
	ApplyTerminalState(client, throughWire(t, state, true))
	if got := client.TabStops(); !slices.Equal(got, []int{4}) {
		t.Errorf("tab stops %v, want the client's own [4]", got)
	}
	if got := client.Titles(); got.Title != before.Title || len(got.Stack) != len(before.Stack) {
		t.Errorf("titles %+v, want the client's own %+v", got, before)
	}
	if got := client.GuestColors().Palette[1]; got == nil {
		t.Error("palette slot 1 was cleared by a snapshot that does not carry colours")
	}
	if !client.ANSIModes()[4] {
		t.Error("insert mode was cleared by a snapshot that does not carry ANSI modes")
	}
}

func mustFeed(t *testing.T, term vt.Terminal, s string) {
	t.Helper()
	if _, err := term.Write([]byte(s)); err != nil {
		t.Fatalf("feed: %v", err)
	}
}

// compareSnapshotState compares what compareEmulators cannot read off the
// cells.
func compareSnapshotState(t *testing.T, want, got vt.Terminal, when string) {
	t.Helper()
	if g, w := got.TabStops(), want.TabStops(); !slices.Equal(g, w) {
		t.Errorf("tab stops %s: client %v, daemon %v", when, g, w)
	}
	if g, w := got.Titles(), want.Titles(); g.Title != w.Title || g.Icon != w.Icon || !slices.Equal(g.Stack, w.Stack) {
		t.Errorf("titles %s: client %+v, daemon %+v", when, g, w)
	}
	gc, wc := got.GuestColors(), want.GuestColors()
	for i := range wc.Palette {
		if g, w := colorWireOrNil(gc.Palette[i]), colorWireOrNil(wc.Palette[i]); g != w {
			t.Errorf("palette slot %d %s: client %s, daemon %s", i, when, g, w)
		}
	}
	for _, c := range []struct {
		name string
		g, w any
	}{{"fg", gc.Fg, wc.Fg}, {"bg", gc.Bg, wc.Bg}, {"cursor", gc.Cursor, wc.Cursor}} {
		if g, w := fmt.Sprint(c.g), fmt.Sprint(c.w); g != w {
			t.Errorf("guest %s colour %s: client %s, daemon %s", c.name, when, g, w)
		}
	}
	wm, gm := want.ANSIModes(), got.ANSIModes()
	for _, m := range []int{4, 20} {
		if wm[m] != gm[m] {
			t.Errorf("ANSI mode %d %s: client %v, daemon %v", m, when, gm[m], wm[m])
		}
	}
	// The two backends spell some unfinished input differently: the pure
	// parser has already dispatched an OSC at the ESC that may start its ST,
	// where the libghostty scanner still holds it. Each spelling replays to
	// the same state, which the comparisons above check, so the bytes are
	// compared only between two emulators of one backend.
	if fmt.Sprintf("%T", got) != fmt.Sprintf("%T", want) {
		return
	}
	if g, w := string(got.PendingInput()), string(want.PendingInput()); g != w {
		t.Errorf("pending input %s: client %q, daemon %q", when, g, w)
	}
}

func colorWireOrNil(c any) string {
	if c == nil {
		return "unset"
	}
	return fmt.Sprint(c)
}

// TestSnapshotVTCarriesTheTabsModesAndACut is the stream-pane snapshot, which
// a client that is not tuios paints on an emulator of its own: the tab stops,
// insert and newline mode, and a sequence the snapshot cut, at every byte,
// have to come out the same there as on the daemon once the stream goes on.
func TestSnapshotVTCarriesTheTabsModesAndACut(t *testing.T) {
	check := func(t *testing.T, before, after string) {
		t.Helper()
		daemon := vt.NewEmulator(fidelityCols, fidelityRows)
		defer closeEmulator(daemon)
		client := vt.NewEmulator(fidelityCols, fidelityRows)
		defer closeEmulator(client)

		mustFeed(t, daemon, before)
		st := TerminalStateOf(daemon, daemon.Width(), daemon.Height(), 100, 0)
		mustFeed(t, client, string(snapshotVT(st)))
		mustFeed(t, daemon, after)
		mustFeed(t, client, after)
		for y := range daemon.Height() {
			for x := range daemon.Width() {
				if w, g := cellSig(daemon.CellAt(x, y)), cellSig(client.CellAt(x, y)); w != g {
					t.Errorf("cell (%d,%d)\n  daemon %s\n  client %s", x, y, w, g)
				}
			}
		}
		if g, w := client.TabStops(), daemon.TabStops(); !slices.Equal(g, w) {
			t.Errorf("tab stops: client %v, daemon %v", g, w)
		}
	}
	for _, c := range snapshotStateCases {
		if !strings.HasPrefix(c.name, "tabs-") && !strings.HasSuffix(c.name, "-mode") {
			continue
		}
		t.Run(c.name, func(t *testing.T) { check(t, c.before, c.after) })
	}
	for _, s := range cutStreams {
		if s.name == "osc-palette" {
			// The palette is not in a SNAP frame; see snapshotVT.
			continue
		}
		for cut := 1; cut < len(s.out); cut++ {
			t.Run(fmt.Sprintf("%s/%d", s.name, cut), func(t *testing.T) { check(t, s.out[:cut], s.out[cut:]) })
		}
	}
}
