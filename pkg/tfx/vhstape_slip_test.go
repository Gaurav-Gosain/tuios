package tfx

import "testing"

// TestVhsTapeAimsAWholeRowAtOneOffset checks the tape tears as a line.
//
// Every character in a row is given the same slip destination: one offset and
// one direction for the row. That shared destination is what separates a tape
// tearing from a scatter of characters wandering off on their own.
//
// It is the destination that is shared and not the position. Each character
// then travels to it at its own speed, so mid-slip the row is ragged, which is
// deliberate and is what makes it look torn rather than slid. An earlier
// version of this test compared positions frame by frame and failed against
// correct code for exactly that reason.
//
// Negative control: moving the offset and direction draws inside the
// per-character loop in buildLine makes the offsets within a row disagree.
func TestVhsTapeAimsAWholeRowAtOneOffset(t *testing.T) {
	term := NewTerminalFromText("aaaaaa\nbbbbbb\ncccccc\ndddddd", TerminalConfig{Width: 60, Height: 8})
	engine := NewEngine(term, NewRng(4))
	effect := NewVhsTape(DefaultVhsTapeConfig())
	if err := effect.Build(engine); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(effect.lines) == 0 {
		t.Fatal("no lines were built")
	}
	for _, line := range effect.lines {
		var want int
		for i, ch := range line.characters {
			path := ch.Motion.Path("glitch")
			if path == nil || len(path.Waypoints) == 0 {
				t.Fatalf("row %d character %q has no glitch waypoint", line.row, ch.InputSymbol)
			}
			got := path.Waypoints[0].Coord.Column - ch.InputCoord.Column
			if i == 0 {
				want = got
				if want == 0 {
					t.Errorf("row %d is aimed at no offset at all", line.row)
				}
				continue
			}
			if got != want {
				t.Errorf("row %d is aimed unevenly: first character offset %d, %q offset %d",
					line.row, want, ch.InputSymbol, got)
			}
		}
	}
}

// TestVhsTapeActuallySlipsARow checks the slip happens, not only that it is set
// up. A build that wires every path correctly and never activates one looks
// identical to a still picture.
//
// Negative control: removing the ActivatePath call from lineGlitch and the
// wave's lineActivatePath leaves every character in its own column throughout.
func TestVhsTapeActuallySlipsARow(t *testing.T) {
	term := NewTerminalFromText("aaaaaa\nbbbbbb\ncccccc\ndddddd", TerminalConfig{Width: 60, Height: 8})
	engine := NewEngine(term, NewRng(4))
	config := DefaultVhsTapeConfig()
	// Make a slip certain rather than waiting on a one in twenty chance.
	config.GlitchLineChance = 1
	config.TotalGlitchTime = 400
	effect := NewVhsTape(config)
	if err := effect.Build(engine); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := 0; i < 4000; i++ {
		if !effect.Advance(engine) {
			break
		}
		for _, ch := range term.InputCharacters {
			if ch.Motion.CurrentCoord.Column != ch.InputCoord.Column {
				return
			}
		}
	}
	t.Error("no character ever slipped sideways")
}
