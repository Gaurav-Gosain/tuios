package session

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// State a snapshot has to carry that no cell shows. Each piece decides what
// output that has not arrived yet does, so a client restored without it looks
// right until the guest sends the sequence that reads it, and then diverges.
//
// Ways this can go wrong, each a group of cases below:
//   - an open OSC 8 link on the pen is dropped, so the text after the
//     snapshot is not a link on the client;
//   - DECSCA protection is dropped, from the pen or from the cells, so a
//     selective erase (DECSED, DECSEL) after the snapshot erases cells the
//     guest protected, on either screen;
//   - the character REP repeats is dropped, so CSI b after the snapshot
//     prints nothing, or it is carried after the character set mapped it, so
//     the repeat is mapped twice;
//   - the cursor DECSC saved is dropped, in any of its parts (position, pen,
//     character sets, origin mode, pending wrap, protection), so DECRC after
//     the snapshot lands somewhere else; or the main screen's is dropped while
//     the alternate screen is up, so leaving it with 1049 puts the shell's
//     cursor somewhere else;
//   - each of the above survives one wire form and not the other.
var cursorStateCases = []seamCase{
	// Gap 1: the pen's hyperlink.
	{"open-link", "\x1b]8;;https://a.example/\x1b\\link ", "more\x1b]8;;\x1b\\ plain"},
	{"open-link-left-open", "plain \x1b]8;;https://b.example/\x1b\\in", "side"},
	{"open-link-under-pen", "\x1b[1;32m\x1b]8;;https://c.example/\x1b\\x", "y\x1b[m"},

	// Gap 2: DECSCA.
	{"protected-cells-decsed", "\x1b[1\"qPROT\x1b[0\"q free", "\x1b[H\x1b[?2J"},
	{"protected-cells-decsed-below", "a\r\n\x1b[1\"qKEEP\x1b[0\"q gone\r\nlast", "\x1b[1;1H\x1b[?0J"},
	{"protected-cells-decsel", "\x1b[2;1H\x1b[1\"qXY\x1b[0\"qzz", "\x1b[2;1H\x1b[?2K"},
	{"protected-cells-ed-erases", "\x1b[1\"qPROT\x1b[0\"q", "\x1b[H\x1b[2J"},
	{"protected-pen", "\x1b[1\"qAB", "CD\x1b[0\"qEF\x1b[H\x1b[?2K"},
	{"protected-wide", "\x1b[1\"q日本\x1b[0\"q", "\x1b[H\x1b[?2J"},
	{"protected-under-alt", "main\x1b[1\"qP\x1b[0\"q\x1b[?1049halt", "\x1b[?1049l\x1b[H\x1b[?2J"},
	{"protected-on-alt", "\x1b[?1049h\x1b[1\"qALT\x1b[0\"q", "\x1b[H\x1b[?2J"},

	// Gap 3: the character REP repeats.
	{"rep-ascii", "abcX", "\x1b[3b"},
	// The character is not the last one on the screen, so a restore that
	// paints the screen row by row does not set it by accident.
	{"rep-not-last-on-screen", "\x1b[5;1Hzz\x1b[1;1HX", "\x1b[2b"},
	{"rep-under-pending-wrap", strings.Repeat("p", fidelityCols-1) + "\x1b[5;1Hk\x1b[1;40Hq", "\x1b[1b"},
	{"rep-after-move", "Q\r\n", "\x1b[2b"},
	{"rep-line-drawing-under-pending-wrap", "\x1b[5;1Hk\x1b[1;1H\x1b(0" + strings.Repeat("x", fidelityCols), "\x1b[1b\x1b(B"},
	{"rep-wide", "日", "\x1b[2b"},
	{"rep-line-drawing", "\x1b(0q\x1b(B", "\x1b(0\x1b[3b\x1b(B"},
	{"rep-after-reset", "abc\x1bc", "\x1b[3b"},

	// Gap 4: the cursor DECSC saved.
	{"saved-position", "\x1b[3;5H\x1b7\x1b[1;1Hhome", "\x1b8X"},
	{"saved-pen", "\x1b[31;4m\x1b7\x1b[0m", "\x1b8red"},
	{"saved-charsets", "\x1b(0\x1b7\x1b(B", "\x1b8qqq"},
	{"saved-origin", "\x1b[3;6r\x1b[?6h\x1b[2;3H\x1b7\x1b[?6l\x1b[1;1H", "\x1b8\x1b[1;1HO"},
	{"saved-pending-wrap", "\x1b[1;1H" + strings.Repeat("w", fidelityCols) + "\x1b7\x1b[5;1H", "\x1b8W"},
	{"saved-protected", "\x1b[1\"q\x1b7\x1b[0\"q", "\x1b8P\x1b[0\"q\x1b[H\x1b[?2J"},
	{"saved-under-alt", "\x1b[4;7Hshell\x1b[?1049h\x1b[2;2Hvim", "\x1b[?1049lX"},
	{"saved-in-alt", "\x1b[?1049h\x1b[3;3H\x1b7\x1b[1;1H", "\x1b8A"},
	{"saved-by-scosc", "\x1b[6;2H\x1b[s\x1b[1;1H", "\x1b[uS"},
}

func TestWireCarriesTheCursorState(t *testing.T) {
	newPure := func() vt.Terminal { return vt.NewEmulator(fidelityCols, fidelityRows) }
	runSeamCases(t, cursorStateCases, newPure, newPure)
}

// TestCursorStateFieldsAreOptionalOnTheWire: a peer from before these fields
// decodes a snapshot that has them, and a snapshot from such a peer decodes
// here with none of them. Applied to an emulator that survived, it leaves the
// saved cursor and REP's character as the emulator had them, and no cell
// protected, which is all an older daemon could have meant.
func TestCursorStateFieldsAreOptionalOnTheWire(t *testing.T) {
	type oldState struct {
		Width, Height    int
		CursorX, CursorY int
	}
	type oldPayload struct {
		PTYID string
		State *oldState
	}
	src := vt.NewEmulator(fidelityCols, fidelityRows)
	_, _ = src.Write([]byte("\x1b[1\"qP\x1b[3;4H\x1b7\x1b]8;;https://a.example/\x1b\\x\x1b[?1049hy"))
	state := TerminalStateOf(src, fidelityCols, fidelityRows, 100, 0)
	if !state.PenProtected || state.MainProtected == nil || state.SavedCursor == nil ||
		state.MainSavedCursor == nil || !state.LastPrintedKnown || state.LastPrinted != "y" {
		t.Fatalf("the snapshot does not carry every field, so this tests nothing: %+v", state)
	}
	data, err := encodePayload(&TerminalStatePayload{PTYID: "x", State: state})
	if err != nil {
		t.Fatal(err)
	}
	var old oldPayload
	if err := decodePayload(data, &old); err != nil {
		t.Fatalf("an old peer cannot decode the snapshot: %v", err)
	}
	if old.State == nil || old.State.Width != fidelityCols || old.State.CursorX != state.CursorX {
		t.Fatalf("an old peer lost the fields it knows: %+v", old.State)
	}

	data, err = encodePayload(&oldPayload{PTYID: "x", State: &oldState{Width: fidelityCols, Height: fidelityRows}})
	if err != nil {
		t.Fatal(err)
	}
	var cur TerminalStatePayload
	if err := decodePayload(data, &cur); err != nil {
		t.Fatalf("a snapshot from an old peer does not decode: %v", err)
	}
	st := cur.State
	if st == nil || st.PenProtected || st.Protected != nil || st.MainProtected != nil ||
		st.LastPrinted != "" || st.LastPrintedKnown || st.SavedCursor != nil || st.MainSavedCursor != nil {
		t.Fatalf("a snapshot from an old peer decoded with fields it never sent: %+v", st)
	}

	client := vt.NewEmulator(fidelityCols, fidelityRows)
	_, _ = client.Write([]byte("\x1b[5;6H\x1b7\x1b[1\"qZ\x1b[0\"qq"))
	ApplyTerminalState(client, st)
	if got := client.LastPrinted(); got != "q" {
		t.Errorf("REP's character after an old snapshot: %q, want the client's own %q", got, "q")
	}
	if got := client.SavedCursor(false); got.X != 5 || got.Y != 4 {
		t.Errorf("saved cursor after an old snapshot: (%d,%d), want the client's own (5,4)", got.X, got.Y)
	}
	if runs := client.ProtectedCells(false); len(runs) != 0 {
		t.Errorf("protected cells after an old snapshot: %v, want none", runs)
	}
	// The positive half: a current snapshot does replace them.
	ApplyTerminalState(client, throughWire(t, TerminalStateOf(src, fidelityCols, fidelityRows, 100, 0), true))
	if got := client.LastPrinted(); got != "y" {
		t.Errorf("REP's character after a current snapshot: %q, want %q", got, "y")
	}
	// Entering the alternate screen with 1049 saved the cursor after the x.
	if got := client.SavedCursor(true); got.X != 4 || got.Y != 2 {
		t.Errorf("main saved cursor after a current snapshot: (%d,%d), want (4,2)", got.X, got.Y)
	}
}
