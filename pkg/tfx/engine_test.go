package tfx

import (
	"math"
	"regexp"
	"strings"
	"testing"
)

// Expected values in this file come from the published formulas and from hand
// arithmetic, never from the function under test. Each test names the change
// that must break it.

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips SGR sequences so a frame can be compared as text.
func plain(frame string) string { return sgr.ReplaceAllString(frame, "") }

// trimRight drops trailing blanks from every row, which is how the canvas pads
// short lines.
func trimRight(frame string) string {
	lines := strings.Split(frame, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// nonBlank returns the frame's rows with the blank ones dropped.
func nonBlank(frame string) []string {
	var out []string
	for _, line := range strings.Split(trimRight(plain(frame)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestRoundHalfEvenBreaksTiesToEven pins the rounding coordinates land on.
//
// Negative control: swapping math.RoundToEven for math.Round in roundHalfEven
// turns 0.5 into 1, 2.5 into 3 and -0.5 into -1, so three of these fail.
func TestRoundHalfEvenBreaksTiesToEven(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{0.5, 0}, {1.5, 2}, {2.5, 2}, {3.5, 4},
		{-0.5, 0}, {-1.5, -2}, {-2.5, -2},
		{0.4, 0}, {0.6, 1}, {-0.6, -1},
	}
	for _, c := range cases {
		if got := roundHalfEven(c.in); got != c.want {
			t.Errorf("roundHalfEven(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFloorDivRoundsTowardsNegativeInfinity pins Python's // semantics.
//
// Negative control: using Go's plain / operator makes -7/2 come out as -3
// instead of -4, so the negative cases fail.
func TestFloorDivRoundsTowardsNegativeInfinity(t *testing.T) {
	cases := []struct {
		a, b, want int
	}{
		{7, 2, 3}, {-7, 2, -4}, {7, -2, -4}, {-7, -2, 3}, {6, 2, 3}, {-6, 2, -3},
	}
	for _, c := range cases {
		if got := floorDiv(c.a, c.b); got != c.want {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestEasingCurvesMatchPublishedFormulas checks a sample of curves against
// values computed here from the formulas on easings.net, independently of the
// switch statement in easing.go.
//
// Negative control: giving OutQuad the Linear body makes it report 0.5 at
// p=0.5 instead of 0.75.
func TestEasingCurvesMatchPublishedFormulas(t *testing.T) {
	cases := []struct {
		name string
		e    Easing
		p    float64
		want float64
	}{
		{"linear", Linear, 0.3, 0.3},
		{"out_quad at half", OutQuad, 0.5, 0.75},
		{"in_quad at half", InQuad, 0.5, 0.25},
		{"in_cubic at half", InCubic, 0.5, 0.125},
		{"out_cubic at half", OutCubic, 0.5, 0.875},
		{"in_quart at half", InQuart, 0.5, 0.0625},
		{"in_sine at half", InSine, 0.5, 1 - math.Cos(math.Pi/4)},
		{"out_sine at half", OutSine, 0.5, math.Sin(math.Pi / 4)},
		{"in_expo at zero", InExpo, 0, 0},
		{"out_expo at one", OutExpo, 1, 1},
		{"in_out_quad below half", InOutQuad, 0.25, 0.125},
		{"in_circ at one", InCirc, 1, 1},
		{"out_bounce at one", OutBounce, 1, 1},
	}
	for _, c := range cases {
		if got := c.e.Ease(c.p); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: Ease(%v) = %v, want %v", c.name, c.p, got, c.want)
		}
	}
}

// TestEasingCurvesSpanZeroToOne checks every curve starts at 0 and ends at 1,
// which is the contract the path stepper relies on to land on the waypoint.
//
// Negative control: dropping the p==1 special case from OutExpo makes it
// return 1-2^-10, which is not 1.
func TestEasingCurvesSpanZeroToOne(t *testing.T) {
	for e := Linear; e <= InOutBounce; e++ {
		if got := e.Ease(0); math.Abs(got) > 1e-9 {
			t.Errorf("easing %d at p=0 = %v, want 0", int(e), got)
		}
		if got := e.Ease(1); math.Abs(got-1) > 1e-9 {
			t.Errorf("easing %d at p=1 = %v, want 1", int(e), got)
		}
	}
}

// TestGradientUsesIntegerFloorSteps pins the deliberately non-linear ramp.
//
// Black to white in four steps: the per-channel delta is floorDiv(255, 4) = 63,
// so the spectrum is 0, 63, 126, 189 and then the exact end stop 255. A float
// lerp would give 0, 85, 170, 255.
//
// Negative control: replacing floorDiv with a float lerp changes the middle
// two entries.
func TestGradientUsesIntegerFloorSteps(t *testing.T) {
	g, err := NewGradientSteps([]Color{RGB(0, 0, 0), RGB(255, 255, 255)}, 4, false)
	if err != nil {
		t.Fatalf("NewGradientSteps: %v", err)
	}
	want := []uint8{0, 63, 126, 189, 255}
	if len(g.Spectrum) != len(want) {
		t.Fatalf("spectrum has %d colours, want %d: %v", len(g.Spectrum), len(want), g.Spectrum)
	}
	for i, w := range want {
		if g.Spectrum[i].R != w {
			t.Errorf("spectrum[%d].R = %d, want %d", i, g.Spectrum[i].R, w)
		}
	}
}

// TestGradientSingleStopRepeats checks the one-stop shortcut.
//
// Negative control: falling through to the pair loop with one stop panics on
// the stops[pairIndex+1] index.
func TestGradientSingleStopRepeats(t *testing.T) {
	g, err := NewGradientSteps([]Color{RGB(10, 20, 30)}, 3, false)
	if err != nil {
		t.Fatalf("NewGradientSteps: %v", err)
	}
	if len(g.Spectrum) != 3 {
		t.Fatalf("spectrum has %d colours, want 3", len(g.Spectrum))
	}
	for i, c := range g.Spectrum {
		if c != RGB(10, 20, 30) {
			t.Errorf("spectrum[%d] = %v, want the only stop", i, c)
		}
	}
}

// TestGradientLoopReturnsToFirstStop checks a looped gradient returns to its
// first stop, which is what lets a cycling effect run with no visible seam.
//
// Negative control: ignoring doLoop leaves the last colour as the last stop
// rather than the first.
func TestGradientLoopReturnsToFirstStop(t *testing.T) {
	first := RGB(255, 0, 0)
	g, err := NewGradientSteps([]Color{first, RGB(0, 255, 0)}, 3, true)
	if err != nil {
		t.Fatalf("NewGradientSteps: %v", err)
	}
	if got := g.Spectrum[len(g.Spectrum)-1]; got != first {
		t.Errorf("looped gradient ends on %v, want the first stop %v", got, first)
	}
}

// TestBezierLengthStopsShortOfTheCurve pins the arc-length quirk.
//
// A straight line dressed as a curve has a true doubled-row length of 20 from
// (1,1) to (21,1). The estimate walks t=0.1 through t=0.9 only, so it covers
// nine tenths of that: 18. Keeping this wrong is deliberate, because path speed
// divides by it.
//
// Negative control: extending the loop to t=1.0 makes the estimate 20.
func TestBezierLengthStopsShortOfTheCurve(t *testing.T) {
	start, end := C(1, 1), C(21, 1)
	control := []Coord{C(11, 1)}
	trueLength := FindLengthOfLine(start, end, true)
	estimate := FindLengthOfBezierCurve(start, control, end)
	if math.Abs(trueLength-20) > 1e-9 {
		t.Fatalf("the straight-line length is %v, want 20", trueLength)
	}
	if math.Abs(estimate-18) > 1e-9 {
		t.Errorf("bezier length estimate = %v, want 18 (nine tenths of %v)", estimate, trueLength)
	}
}

// TestLineLengthDoublesRowDelta checks the terminal cell aspect correction.
//
// Three rows apart with no column change is 3 raw and 6 doubled.
//
// Negative control: dropping the factor of two makes the doubled case 3.
func TestLineLengthDoublesRowDelta(t *testing.T) {
	a, b := C(1, 1), C(1, 4)
	if got := FindLengthOfLine(a, b, false); math.Abs(got-3) > 1e-9 {
		t.Errorf("raw length = %v, want 3", got)
	}
	if got := FindLengthOfLine(a, b, true); math.Abs(got-6) > 1e-9 {
		t.Errorf("doubled length = %v, want 6", got)
	}
}

// TestCyclicDistributionSpreadsTheRemainder checks that four items over three
// buckets do not dump the extra one at the end.
//
// Negative control: a plain index/repeatFactor mapping pairs the fourth item
// with the third bucket, giving 0,1,2,2 rather than 0,0,1,2.
func TestCyclicDistributionSpreadsTheRemainder(t *testing.T) {
	pairs := cyclicDistribution([]int{10, 11, 12, 13}, []string{"a", "b", "c"})
	got := make([]string, 0, len(pairs))
	for _, p := range pairs {
		got = append(got, p.small)
	}
	want := []string{"a", "a", "b", "c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("distribution = %v, want %v", got, want)
	}
}

// TestTerminalPlacesTextBottomUp checks the coordinate flip: the last input
// line is row 1 and the first is the top row.
//
// Negative control: indexing rows top-down puts "ab" on row 1, so the frame
// comes out upside down.
func TestTerminalPlacesTextBottomUp(t *testing.T) {
	term := NewTerminalFromText("ab\ncd", TerminalConfig{Width: 4, Height: 2})
	top := term.CharacterAtInputCoord(C(1, 2))
	bottom := term.CharacterAtInputCoord(C(1, 1))
	if top == nil || top.InputSymbol != "a" {
		t.Errorf("row 2 column 1 = %v, want the character a", top)
	}
	if bottom == nil || bottom.InputSymbol != "c" {
		t.Errorf("row 1 column 1 = %v, want the character c", bottom)
	}
}

// TestTerminalSkipsSpacesAsInputCharacters checks that a gap in the text is not
// an input character, so effects animate the text and not its whitespace.
//
// Negative control: allocating a character for every rune makes this count 3.
func TestTerminalSkipsSpacesAsInputCharacters(t *testing.T) {
	term := NewTerminalFromText("a b", TerminalConfig{Width: 3, Height: 1})
	if got := len(term.InputCharacters); got != 2 {
		t.Errorf("input characters = %d, want 2 (the space is not one)", got)
	}
}

// TestFrameRendersTopRowFirst checks the painter's row order against the input.
//
// Negative control: rendering rows bottom-up prints "cd" before "ab".
func TestFrameRendersTopRowFirst(t *testing.T) {
	term := NewTerminalFromText("ab\ncd", TerminalConfig{Width: 2, Height: 2})
	for _, ch := range term.InputCharacters {
		term.SetCharacterVisibility(ch, true)
	}
	if got := plain(term.Frame()); got != "ab\ncd" {
		t.Errorf("frame = %q, want %q", got, "ab\ncd")
	}
}

// TestHigherLayerWinsTheCell checks the painter order where two characters
// share a cell.
//
// Negative control: dropping the layer comparison lets the lower-layer
// character win whenever it was allocated later.
func TestHigherLayerWinsTheCell(t *testing.T) {
	term := NewTerminalFromText("a", TerminalConfig{Width: 1, Height: 1})
	low := term.InputCharacters[0]
	high := term.AddCharacter("Z", C(1, 1))
	high.Layer = 1
	term.SetCharacterVisibility(low, true)
	term.SetCharacterVisibility(high, true)
	if got := plain(term.Frame()); got != "Z" {
		t.Errorf("frame = %q, want the higher layer character Z", got)
	}
	high.Layer = -1
	if got := plain(term.Frame()); got != "a" {
		t.Errorf("frame = %q, want the input character a once Z drops below it", got)
	}
}

// TestSceneHoldsAFrameForItsDuration checks the tick accounting.
//
// Negative control: retiring a frame after one tick regardless of duration
// makes the second and third reads return the later frame.
func TestSceneHoldsAFrameForItsDuration(t *testing.T) {
	term := NewTerminalFromText("x", TerminalConfig{Width: 1, Height: 1})
	engine := NewEngine(term, NewRng(1))
	ch := term.InputCharacters[0]
	scene := ch.Animation.NewScene("s", SceneOptions{})
	if err := scene.AddFrame("1", 3, VisualParams{}); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	if err := scene.AddFrame("2", 1, VisualParams{}); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	engine.ActivateScene(ch, "s")
	engine.Activate(ch)

	want := []string{"1", "1", "1", "2"}
	for i, w := range want {
		engine.Update()
		if got := ch.Animation.CurrentVisual().Symbol; got != w {
			t.Errorf("tick %d showed %q, want %q", i+1, got, w)
		}
	}
}

// TestSceneCompleteChainsToTheNextScene checks the event plumbing decrypt
// depends on.
//
// Negative control: not firing SceneComplete leaves the character on the first
// scene's last symbol.
func TestSceneCompleteChainsToTheNextScene(t *testing.T) {
	term := NewTerminalFromText("x", TerminalConfig{Width: 1, Height: 1})
	engine := NewEngine(term, NewRng(1))
	ch := term.InputCharacters[0]

	first := ch.Animation.NewScene("first", SceneOptions{})
	if err := first.AddFrame("A", 1, VisualParams{}); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	second := ch.Animation.NewScene("second", SceneOptions{})
	if err := second.AddFrame("B", 1, VisualParams{}); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	ch.RegisterEvent(SceneComplete, SceneCaller("first"), ActivateScene("second"))
	engine.ActivateScene(ch, "first")
	engine.Activate(ch)

	engine.Update()
	if got := ch.Animation.CurrentVisual().Symbol; got != "B" {
		t.Errorf("after the first scene finished the character shows %q, want B", got)
	}
}

// TestPathLandsExactlyOnItsWaypoint checks the motion stepper terminates on the
// target rather than near it.
//
// Negative control: clamping t before the last step, or dropping the
// currentStep == maxSteps branch, leaves the character short of the waypoint.
func TestPathLandsExactlyOnItsWaypoint(t *testing.T) {
	term := NewTerminalFromText("x", TerminalConfig{Width: 20, Height: 5})
	engine := NewEngine(term, NewRng(1))
	ch := term.InputCharacters[0]
	ch.Motion.SetCoordinate(C(1, 1))
	path, err := ch.Motion.NewPath("p", PathOptions{Speed: 1})
	if err != nil {
		t.Fatalf("NewPath: %v", err)
	}
	target := C(15, 3)
	if _, err := path.NewWaypoint(target, nil, ""); err != nil {
		t.Fatalf("NewWaypoint: %v", err)
	}
	engine.ActivatePath(ch, "p")
	engine.Activate(ch)

	for i := 0; i < 200 && !ch.Motion.MovementIsComplete(); i++ {
		engine.Update()
	}
	if !ch.Motion.MovementIsComplete() {
		t.Fatalf("the path never finished")
	}
	if ch.Motion.CurrentCoord != target {
		t.Errorf("the character stopped at %v, want %v", ch.Motion.CurrentCoord, target)
	}
}

// TestPathCompleteFiresOnce checks the event that hands a landed raindrop to
// its fade scene.
//
// Negative control: firing PathComplete on every tick after arrival makes the
// count climb past 1.
func TestPathCompleteFiresOnce(t *testing.T) {
	term := NewTerminalFromText("x", TerminalConfig{Width: 10, Height: 5})
	engine := NewEngine(term, NewRng(1))
	ch := term.InputCharacters[0]
	ch.Motion.SetCoordinate(C(1, 1))
	path, err := ch.Motion.NewPath("p", PathOptions{Speed: 2})
	if err != nil {
		t.Fatalf("NewPath: %v", err)
	}
	if _, err := path.NewWaypoint(C(8, 1), nil, ""); err != nil {
		t.Fatalf("NewWaypoint: %v", err)
	}
	fired := 0
	ch.RegisterEvent(PathComplete, PathCaller("p"), Callback(func(*Engine, *Character) { fired++ }))
	engine.ActivatePath(ch, "p")
	engine.Activate(ch)

	for i := 0; i < 100; i++ {
		engine.Update()
	}
	if fired != 1 {
		t.Errorf("PathComplete fired %d times, want 1", fired)
	}
}

// TestDecryptResolvesToTheInputText runs the whole effect and checks the last
// frame is the text it started from.
//
// Negative control: pointing the discovered scene at a fixed symbol instead of
// ch.InputSymbol makes the final frame a wall of that symbol.
func TestDecryptResolvesToTheInputText(t *testing.T) {
	const input = "hello world"
	term := NewTerminalFromText(input, TerminalConfig{Width: 20, Height: 3})
	engine := NewEngine(term, NewRng(7))
	effect := NewDecrypt(DefaultDecryptConfig())

	frames, err := Run(effect, engine, 20000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("the effect produced no frames")
	}
	if len(frames) >= 20000 {
		t.Fatal("the effect never finished within the frame cap")
	}
	rows := nonBlank(frames[len(frames)-1])
	if len(rows) != 1 || rows[0] != input {
		t.Errorf("the final frame reads %q, want %q", rows, input)
	}
}

// TestDecryptScramblesBeforeItResolves checks the effect actually animates
// rather than printing the answer immediately.
//
// Negative control: activating the discovered scene first makes an early frame
// already read as the input.
func TestDecryptScramblesBeforeItResolves(t *testing.T) {
	const input = "hello world"
	term := NewTerminalFromText(input, TerminalConfig{Width: 20, Height: 3})
	engine := NewEngine(term, NewRng(7))
	effect := NewDecrypt(DefaultDecryptConfig())

	frames, err := Run(effect, engine, 20000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) < 50 {
		t.Fatalf("the effect ran for %d frames, want a real animation", len(frames))
	}
	middle := strings.Join(nonBlank(frames[len(frames)/2]), "")
	if strings.Contains(middle, input) {
		t.Errorf("the middle frame already reads as the input: %q", middle)
	}
}

// TestRainSettlesIntoTheInputText runs rain to completion.
//
// Negative control: giving the fall path a waypoint other than the character's
// input coordinate leaves the text scrambled or off-column.
func TestRainSettlesIntoTheInputText(t *testing.T) {
	const input = "rain check"
	term := NewTerminalFromText(input, TerminalConfig{Width: 20, Height: 6})
	engine := NewEngine(term, NewRng(3))
	effect := NewRain(DefaultRainConfig())

	frames, err := Run(effect, engine, 20000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("the effect produced no frames")
	}
	if len(frames) >= 20000 {
		t.Fatal("the effect never finished within the frame cap")
	}
	rows := nonBlank(frames[len(frames)-1])
	if len(rows) != 1 || rows[0] != input {
		t.Errorf("the final frame reads %q, want %q", rows, input)
	}
}

// TestRainStartsAtTheTopOfTheCanvas checks the drops fall rather than appear.
//
// Negative control: leaving the character at its input coordinate makes the
// first visible row the text's own row.
func TestRainStartsAtTheTopOfTheCanvas(t *testing.T) {
	term := NewTerminalFromText("rain", TerminalConfig{Width: 10, Height: 8})
	engine := NewEngine(term, NewRng(3))
	effect := NewRain(DefaultRainConfig())
	if err := effect.Build(engine); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, ch := range term.InputCharacters {
		if ch.Motion.CurrentCoord.Row != term.Canvas.Top {
			t.Errorf("character %q starts at row %d, want the canvas top row %d",
				ch.InputSymbol, ch.Motion.CurrentCoord.Row, term.Canvas.Top)
		}
		if ch.Motion.CurrentCoord.Column != ch.InputCoord.Column {
			t.Errorf("character %q starts in column %d, want its own column %d",
				ch.InputSymbol, ch.Motion.CurrentCoord.Column, ch.InputCoord.Column)
		}
	}
}

// TestInputColorsResolveBackToThemselves checks the mode a screen saver runs
// in: the captured screen must reassemble in its own colours, not the effect's.
//
// Negative control: ignoring DynamicExistingColors makes the final frame carry
// the effect's gradient colour instead of the input's red.
func TestInputColorsResolveBackToThemselves(t *testing.T) {
	red := RGB(255, 0, 0)
	grid := [][]InputCell{{
		{Symbol: "a", Fg: red, HasFg: true},
		{Symbol: "b", Fg: red, HasFg: true},
	}}
	term := NewTerminalFromCells(grid, TerminalConfig{
		Width: 2, Height: 1, ExistingColorHandling: DynamicExistingColors,
	})
	engine := NewEngine(term, NewRng(11))
	effect := NewDecrypt(DefaultDecryptConfig())

	frames, err := Run(effect, engine, 20000)
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
}

// TestCellGridRowZeroIsTheTop checks the flip between capture order and canvas
// order.
//
// Negative control: reading the grid bottom-up puts the second row's text on
// the top line of the frame.
func TestCellGridRowZeroIsTheTop(t *testing.T) {
	grid := [][]InputCell{
		{{Symbol: "t"}},
		{{Symbol: "b"}},
	}
	term := NewTerminalFromCells(grid, TerminalConfig{Width: 1, Height: 2})
	for _, ch := range term.InputCharacters {
		term.SetCharacterVisibility(ch, true)
	}
	if got := plain(term.Frame()); got != "t\nb" {
		t.Errorf("frame = %q, want %q", got, "t\nb")
	}
}

// TestFrameRowsMatchTheFrameString checks the two output paths agree.
//
// Negative control: reversing one of the two row orders makes them disagree.
func TestFrameRowsMatchTheFrameString(t *testing.T) {
	term := NewTerminalFromText("ab\ncd", TerminalConfig{Width: 3, Height: 2})
	for _, ch := range term.InputCharacters {
		term.SetCharacterVisibility(ch, true)
	}
	var rebuilt strings.Builder
	for i, row := range term.FrameRows() {
		if i > 0 {
			rebuilt.WriteByte('\n')
		}
		for _, visual := range row {
			if visual == nil {
				rebuilt.WriteByte(' ')
			} else {
				rebuilt.WriteString(visual.Formatted())
			}
		}
	}
	if rebuilt.String() != term.Frame() {
		t.Errorf("FrameRows rebuilt %q, want %q", rebuilt.String(), term.Frame())
	}
}

// TestRegistryHoldsThePortedEffects guards the registry wiring, since a
// missing init leaves an effect unreachable by name.
//
// Negative control: deleting any effect file's init makes its name vanish.
func TestRegistryHoldsThePortedEffects(t *testing.T) {
	want := []string{"decrypt", "rain", "vhstape", "waves"}
	got := Names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("registered effects = %v, want %v", got, want)
	}
	for _, name := range want {
		d, ok := Lookup(name)
		if !ok {
			t.Fatalf("effect %q is not in the registry", name)
		}
		if d.New == nil || d.New() == nil {
			t.Errorf("effect %q has no working factory", name)
		}
		if d.Description == "" {
			t.Errorf("effect %q has no description", name)
		}
	}
}

// TestSeededRunsAreReproducible checks the generator is the only source of
// variation, so a test failure can be reproduced.
//
// Negative control: seeding from the clock makes the two runs differ.
func TestSeededRunsAreReproducible(t *testing.T) {
	run := func() []string {
		term := NewTerminalFromText("repeat", TerminalConfig{Width: 12, Height: 3})
		engine := NewEngine(term, NewRng(42))
		frames, err := Run(NewDecrypt(DefaultDecryptConfig()), engine, 400)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return frames
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("the two runs produced %d and %d frames", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("frame %d differs between two runs with the same seed", i)
		}
	}
}
