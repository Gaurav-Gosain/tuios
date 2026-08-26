package tfx

import (
	"strings"
	"testing"
)

// TestWavesSettlesIntoTheInputText runs waves to completion.
//
// Negative control: pointing the settling scene at a fixed symbol instead of
// ch.InputSymbol leaves the final frame reading as that symbol.
func TestWavesSettlesIntoTheInputText(t *testing.T) {
	const input = "wave goodbye"
	term := NewTerminalFromText(input, TerminalConfig{Width: 20, Height: 4})
	engine := NewEngine(term, NewRng(9))

	frames, err := Run(NewWaves(DefaultWavesConfig()), engine, 40000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("the effect produced no frames")
	}
	if len(frames) >= 40000 {
		t.Fatal("the effect never finished within the frame cap")
	}
	rows := nonBlank(frames[len(frames)-1])
	if len(rows) != 1 || rows[0] != input {
		t.Errorf("the final frame reads %q, want %q", rows, input)
	}
}

// TestWavesNeverMovesACharacter checks the effect is a sweep and not a
// rearrangement: a wave passes over the text and leaves it where it was.
//
// Negative control: giving the characters a path makes some frame's text sit
// in a different column from the input.
func TestWavesNeverMovesACharacter(t *testing.T) {
	term := NewTerminalFromText("abc", TerminalConfig{Width: 6, Height: 2})
	engine := NewEngine(term, NewRng(9))
	effect := NewWaves(DefaultWavesConfig())
	if err := effect.Build(engine); err != nil {
		t.Fatalf("Build: %v", err)
	}
	home := map[*Character]Coord{}
	for _, ch := range term.InputCharacters {
		home[ch] = ch.InputCoord
	}
	for i := 0; i < 40000; i++ {
		if !effect.Advance(engine) {
			break
		}
		for ch, want := range home {
			if ch.Motion.CurrentCoord != want {
				t.Fatalf("frame %d moved %q from %v to %v", i, ch.InputSymbol, want, ch.Motion.CurrentCoord)
			}
		}
	}
}

// TestWavesActuallyShowsAWave checks the wave symbols reach the screen, since
// an effect whose scenes never activate still passes the resolve test.
//
// Negative control: not activating the wave scene makes every frame read as
// the plain input from the first frame onward.
func TestWavesActuallyShowsAWave(t *testing.T) {
	term := NewTerminalFromText("abcdef", TerminalConfig{Width: 10, Height: 3})
	engine := NewEngine(term, NewRng(9))
	frames, err := Run(NewWaves(DefaultWavesConfig()), engine, 40000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) < 20 {
		t.Fatalf("the effect ran for %d frames, want a real animation", len(frames))
	}
	joined := strings.Join(frames[:len(frames)/2], "")
	found := false
	for _, symbol := range DefaultWavesConfig().WaveSymbols {
		if strings.Contains(joined, symbol) {
			found = true
			break
		}
	}
	if !found {
		t.Error("no wave symbol appeared in the first half of the run")
	}
}

// TestVhsTapeRedrawsTheInputText runs vhstape to completion.
//
// Negative control: pointing the redraw scene at a fixed symbol leaves the
// final frame reading as that symbol.
func TestVhsTapeRedrawsTheInputText(t *testing.T) {
	const input = "tracking"
	term := NewTerminalFromText(input, TerminalConfig{Width: 20, Height: 6})
	engine := NewEngine(term, NewRng(4))
	config := DefaultVhsTapeConfig()
	config.TotalGlitchTime = 60

	frames, err := Run(NewVhsTape(config), engine, 40000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("the effect produced no frames")
	}
	if len(frames) >= 40000 {
		t.Fatal("the effect never finished within the frame cap")
	}
	rows := nonBlank(frames[len(frames)-1])
	if len(rows) != 1 || rows[0] != input {
		t.Errorf("the final frame reads %q, want %q", rows, input)
	}
}

// TestVhsTapeKeepsRowsOnTheirOwnRow checks a slip is horizontal only. A tape
// tears sideways; a character that changed row would be a bug in the paths.
//
// Negative control: giving a waypoint a different row makes this fail.
func TestVhsTapeKeepsRowsOnTheirOwnRow(t *testing.T) {
	term := NewTerminalFromText("aaaa\nbbbb\ncccc", TerminalConfig{Width: 30, Height: 6})
	engine := NewEngine(term, NewRng(4))
	config := DefaultVhsTapeConfig()
	config.GlitchLineChance = 1
	config.TotalGlitchTime = 120
	effect := NewVhsTape(config)
	if err := effect.Build(engine); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := 0; i < 3000; i++ {
		if !effect.Advance(engine) {
			break
		}
		for _, ch := range term.InputCharacters {
			if ch.Motion.CurrentCoord.Row != ch.InputCoord.Row {
				t.Fatalf("frame %d moved %q off row %d to row %d",
					i, ch.InputSymbol, ch.InputCoord.Row, ch.Motion.CurrentCoord.Row)
			}
		}
	}
}

// TestVhsTapeAimsTheWaveByRowNotByPosition is the guard on the one deliberate
// change from upstream.
//
// Upstream indexes lines by position in its list and works out which line a
// canvas row belongs to by arithmetic. That holds only while every row has
// something on it. A captured screen has blank rows, those produce no group,
// and the arithmetic then aims the tracking band at the wrong row.
//
// Negative control: keying lineByRow on the loop index rather than on the row
// makes the lookup disagree with the row a line actually occupies.
func TestVhsTapeAimsTheWaveByRowNotByPosition(t *testing.T) {
	// Row 3 of this input is blank, so it produces no group at all.
	term := NewTerminalFromText("top\n\n\nbottom", TerminalConfig{Width: 10, Height: 4})
	engine := NewEngine(term, NewRng(4))
	effect := NewVhsTape(DefaultVhsTapeConfig())
	if err := effect.Build(engine); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(effect.lines) == 0 {
		t.Fatal("no lines were built")
	}
	for index, line := range effect.lines {
		got, ok := effect.lineByRow[line.row]
		if !ok {
			t.Errorf("line on row %d is not in the row index", line.row)
			continue
		}
		if got != index {
			t.Errorf("row %d maps to line %d, want %d", line.row, got, index)
		}
		for _, ch := range line.characters {
			if ch.InputCoord.Row != line.row {
				t.Errorf("line claims row %d but holds a character on row %d", line.row, ch.InputCoord.Row)
			}
		}
	}
	// And the blank row must not resolve to anything.
	blankRows := 0
	for row := term.Canvas.Bottom; row <= term.Canvas.Top; row++ {
		if _, ok := effect.lineByRow[row]; !ok {
			blankRows++
		}
	}
	if blankRows == 0 {
		t.Error("the blank rows resolved to lines, so the index is not row based")
	}
}

// TestNewEffectsResolveTheInputColours checks both new effects under the colour
// policy a screen saver runs in, which is the whole reason they read as the
// screen coming back rather than as a demo.
//
// Negative control: ignoring DynamicExistingColors makes the final frame carry
// the effect's own gradient instead of the input's red.
func TestNewEffectsResolveTheInputColours(t *testing.T) {
	red := RGB(255, 0, 0)
	for _, name := range []string{"waves", "vhstape"} {
		t.Run(name, func(t *testing.T) {
			grid := [][]InputCell{{
				{Symbol: "a", Fg: red, HasFg: true},
				{Symbol: "b", Fg: red, HasFg: true},
			}}
			term := NewTerminalFromCells(grid, TerminalConfig{
				Width: 2, Height: 1, ExistingColorHandling: DynamicExistingColors,
			})
			engine := NewEngine(term, NewRng(12))
			d, ok := Lookup(name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			frames, err := Run(d.New(), engine, 40000)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(frames) == 0 {
				t.Fatal("the effect produced no frames")
			}
			last := frames[len(frames)-1]
			if got := plain(last); got != "ab" {
				t.Fatalf("the final frame reads %q, want %q", got, "ab")
			}
			if !strings.Contains(last, "\x1b[38;2;255;0;0m") {
				t.Errorf("the final frame does not carry the input's red: %q", last)
			}
		})
	}
}
