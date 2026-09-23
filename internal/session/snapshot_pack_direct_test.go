package session

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestDirectPackMatchesPack holds GetTerminalStatePacked, which packs the cells
// as it reads them, to exactly what GetTerminalState followed by Pack gives.
// The two are separate code paths over the same wire form, and the style
// table's order depends on the order the grids are packed in, so a change to
// either path that the other does not follow shows up here as a byte
// difference rather than as wrong colours on a restored pane.
//
// Emulators are driven by random input that covers wide runes, combining
// marks, colours of every kind, links, underline styles, the alternate screen,
// erases, cursor moves and resizes, and each is read at several scrollback
// depths with and without rows the caller already holds.
//
// NEGATIVE CONTROL: measured. Packing the main screen before the history, one
// line out of order, fails on the third emulator with the scrollback, the main
// screen and the style table all differing.
func TestDirectPackMatchesPack(t *testing.T) {
	iterations := 300
	if testing.Short() {
		iterations = 30
	}
	rng := rand.New(rand.NewSource(1))
	pieces := []string{
		"a", "héllo", "世界", "é", " ", "é", "\r\n", "\r\n", "\r\n",
		"\x1b[31m", "\x1b[38;2;1;2;3m", "\x1b[48;5;200m", "\x1b[m", "\x1b[7m", "\x1b[4:3m",
		"\x1b]8;;http://x\x1b\\L\x1b]8;;\x1b\\",
		"\x1b[?1049h", "\x1b[?1049l", "\x1b[2J", "\x1b[K", "\x1b[5;3H",
	}
	for iter := range iterations {
		w, h := 5+rng.Intn(60), 3+rng.Intn(20)
		em := vt.NewEmulator(w, h)
		for k := range 50 + rng.Intn(2000) {
			_, _ = em.Write([]byte(pieces[rng.Intn(len(pieces))]))
			if k%500 == 499 {
				w, h = 5+rng.Intn(60), 3+rng.Intn(20)
				em.Resize(w, h)
			}
		}
		p := &PTY{ID: "direct-pack", terminal: em, width: w, height: h}
		for _, depth := range []int{-1, 0, 7, 1000} {
			for _, have := range []int{0, 3} {
				want := p.GetTerminalState(depth, have)
				want.Pack()
				got := p.GetTerminalStatePacked(depth, have)
				if reflect.DeepEqual(want, got) {
					continue
				}
				for _, f := range []string{"PackedScreen", "PackedScrollback", "PackedMain", "Styles", "Screen", "Scrollback", "MainScreen"} {
					a := reflect.ValueOf(*want).FieldByName(f).Interface()
					b := reflect.ValueOf(*got).FieldByName(f).Interface()
					if !reflect.DeepEqual(a, b) {
						t.Logf("%s differs:\nPack:   %v\ndirect: %v", f, a, b)
					}
				}
				t.Fatalf("iteration %d, depth %d, have %d: the directly packed snapshot differs from GetTerminalState plus Pack", iter, depth, have)
			}
		}
		_ = em.Close()
	}
}
