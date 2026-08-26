package tfx

// vhstape, ported from ttfx src/effects/vhstape.rs, which ports
// TerminalTextEffects effects/effect_vhstape.py by ChrisBuilds.

func init() {
	Register(Descriptor{
		Name:        "vhstape",
		Description: "Rows slip sideways and lose detail like a worn video tape, then the picture is redrawn",
		New:         func() Effect { return NewVhsTape(DefaultVhsTapeConfig()) },
	})
}

// VhsTapeConfig tunes the vhstape effect.
type VhsTapeConfig struct {
	// GlitchLineColors are cycled through by a row that slips, which is what
	// gives it the colour fringing of a misaligned tape head.
	GlitchLineColors []Color
	// NoiseColors are the greys the snow is drawn in.
	NoiseColors []Color
	// GlitchLineChance is the chance per frame that another row slips.
	GlitchLineChance float64
	// NoiseChance is the chance per frame that the whole picture takes snow.
	NoiseChance float64
	// TotalGlitchTime is how many frames the glitching lasts before the tape
	// gives up and the picture is redrawn.
	TotalGlitchTime int
	// FinalGradientStops colour the redrawn picture. They are ignored when the
	// engine is set to resolve to the input's own colours.
	FinalGradientStops     []Color
	FinalGradientSteps     []int
	FinalGradientDirection GradientDirection
}

// DefaultVhsTapeConfig is upstream's default vhstape.
func DefaultVhsTapeConfig() VhsTapeConfig {
	return VhsTapeConfig{
		GlitchLineColors: []Color{
			MustParseColor("ffffff"), MustParseColor("ff0000"), MustParseColor("00ff00"),
			MustParseColor("0000ff"), MustParseColor("ffffff"),
		},
		NoiseColors: []Color{
			MustParseColor("1e1e1f"), MustParseColor("3c3b3d"), MustParseColor("6d6c70"),
			MustParseColor("a2a1a6"), MustParseColor("cbc9cf"), MustParseColor("ffffff"),
		},
		GlitchLineChance: 0.05,
		NoiseChance:      0.004,
		TotalGlitchTime:  600,
		FinalGradientStops: []Color{
			MustParseColor("ab48ff"), MustParseColor("e7b2b2"), MustParseColor("fffebd"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: Vertical,
	}
}

// vhsLine is one row of the picture.
//
// Upstream indexes its lines by position in the list and works out which line a
// canvas row belongs to by arithmetic. That holds only while every row has
// something on it. A captured screen has blank rows, those rows produce no
// group, and the arithmetic then aims the wave at the wrong row or at none.
// Carrying the row on the line and looking it up is what makes the wave land
// where it is pointed over arbitrary content.
type vhsLine struct {
	row        int
	characters []*Character
}

type vhsPhase int

const (
	vhsGlitching vhsPhase = iota
	vhsNoise
	vhsRedraw
	vhsComplete
)

// VhsTape plays the screen back off a worn tape: rows slip sideways with colour
// fringing, a band of tracking noise walks up the picture, the whole thing
// dissolves into snow, and then it is redrawn row by row.
type VhsTape struct {
	config VhsTapeConfig

	lines     []vhsLine
	lineByRow map[int]int

	// waveTop is the row the three-row tracking band currently sits under.
	waveTop    int
	hasWaveTop bool
	// waveLines and glitchLines hold indices into lines.
	waveLines   []int
	glitchLines []int

	stableColors map[*Character]ColorPair
	finalColors  map[*Character]ColorPair

	glitchingStepsElapsed int
	phase                 vhsPhase
	toRedraw              []int
	redrawing             bool
}

// NewVhsTape builds the effect.
func NewVhsTape(config VhsTapeConfig) *VhsTape {
	return &VhsTape{
		config:       config,
		lineByRow:    map[int]int{},
		stableColors: map[*Character]ColorPair{},
		finalColors:  map[*Character]ColorPair{},
	}
}

// dynamicNeutralGrey is what a character with no foreground of its own wears
// while the tape is playing, so it is visible against the snow.
var dynamicNeutralGrey = MustParseColor("808080")

// Build gives every row its slip paths and every character its colour-fringing,
// snow and redraw scenes.
func (v *VhsTape) Build(e *Engine) error {
	gradient, err := NewGradient(v.config.FinalGradientStops, v.config.FinalGradientSteps, false)
	if err != nil {
		return err
	}
	canvas := e.Terminal.Canvas
	mapping, err := gradient.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight,
		v.config.FinalGradientDirection)
	if err != nil {
		return err
	}
	fallback := gradient.Spectrum[0]
	dynamic := e.Terminal.Config.ExistingColorHandling == DynamicExistingColors

	for _, ch := range e.Terminal.GetCharacters(e.Rng, InputOnly(), SortTopToBottomLeftToRight) {
		if dynamic && ch.UsesInputColors {
			stable := ch.Animation.InputColors
			if !stable.HasFg {
				stable.Fg, stable.HasFg = dynamicNeutralGrey, true
			}
			v.stableColors[ch] = stable
			v.finalColors[ch] = ch.Animation.InputColors
			continue
		}
		colors := Fg(mapping.At(ch.InputCoord, fallback))
		v.stableColors[ch] = colors
		v.finalColors[ch] = colors
	}

	for _, characters := range e.Terminal.GetCharactersGrouped(InputOnly(), GroupRowBottomToTop) {
		if len(characters) == 0 {
			continue
		}
		if err := v.buildLine(e, characters); err != nil {
			return err
		}
		v.lineByRow[characters[0].InputCoord.Row] = len(v.lines)
		v.lines = append(v.lines, vhsLine{row: characters[0].InputCoord.Row, characters: characters})
	}

	for _, ch := range e.Terminal.GetCharacters(e.Rng, InputOnly(), SortTopToBottomLeftToRight) {
		e.Terminal.SetCharacterVisibility(ch, true)
		e.ActivateScene(ch, "base")
	}
	v.toRedraw = make([]int, 0, len(v.lines))
	for i := range v.lines {
		v.toRedraw = append(v.toRedraw, i)
	}
	return nil
}

// buildLine gives one row its paths and scenes. The whole row shares an offset
// and a direction, which is what makes it slip as a line rather than as a
// scatter of characters.
func (v *VhsTape) buildLine(e *Engine, characters []*Character) error {
	snowChars := []string{"#", "*", ".", ":"}
	offset := e.Rng.IntBetween(4, 25)
	direction := *Choice(e.Rng, []int{-1, 1})
	holdTime := e.Rng.IntBetween(1, 50)

	for _, ch := range characters {
		stable := v.stableColors[ch]
		final := v.finalColors[ch]

		glitch, err := ch.Motion.NewPath("glitch", PathOptions{Speed: 2, HoldTime: holdTime})
		if err != nil {
			return err
		}
		if _, err := glitch.NewWaypoint(
			C(ch.InputCoord.Column+offset*direction, ch.InputCoord.Row), nil, "glitch"); err != nil {
			return err
		}
		restore, err := ch.Motion.NewPath("restore", PathOptions{Speed: 2})
		if err != nil {
			return err
		}
		if _, err := restore.NewWaypoint(ch.InputCoord, nil, "restore"); err != nil {
			return err
		}
		waveMid, err := ch.Motion.NewPath("glitch_wave_mid", PathOptions{Speed: 2})
		if err != nil {
			return err
		}
		if _, err := waveMid.NewWaypoint(
			C(ch.InputCoord.Column+8, ch.InputCoord.Row), nil, "glitch_wave_mid"); err != nil {
			return err
		}
		waveEnd, err := ch.Motion.NewPath("glitch_wave_end", PathOptions{Speed: 2})
		if err != nil {
			return err
		}
		if _, err := waveEnd.NewWaypoint(
			C(ch.InputCoord.Column+14, ch.InputCoord.Row), nil, "glitch_wave_end"); err != nil {
			return err
		}

		base := ch.Animation.NewScene("base", SceneOptions{UsesInputColors: ch.UsesInputColors, Frames: 1})
		if err := base.AddFrame(ch.InputSymbol, 1, VisualParams{Colors: stable}); err != nil {
			return err
		}

		// The fringing scenes are tied to how far the row has slipped rather
		// than to elapsed frames, so the colour separation tracks the movement.
		//
		// They carry the character's own background through. Upstream sets a
		// foreground and nothing else, which is right for piped text and wrong
		// over a captured screen: a selection bar or a filled panel lost its
		// fill for as long as its row was torn and got it back afterwards. A
		// tape drags the whole picture sideways, backgrounds included.
		fringe := func(color Color) ColorPair {
			pair := Fg(color)
			if stable.HasBg {
				pair.Bg, pair.HasBg = stable.Bg, true
			}
			return pair
		}
		forward := ch.Animation.NewScene("rgb_glitch_fwd", SceneOptions{
			Sync: SyncStep, UsesInputColors: ch.UsesInputColors, Frames: len(v.config.GlitchLineColors),
		})
		for _, color := range v.config.GlitchLineColors {
			if err := forward.AddFrame(ch.InputSymbol, 1, VisualParams{Colors: fringe(color)}); err != nil {
				return err
			}
		}
		backward := ch.Animation.NewScene("rgb_glitch_bwd", SceneOptions{
			Sync: SyncStep, UsesInputColors: ch.UsesInputColors, Frames: len(v.config.GlitchLineColors),
		})
		for i := len(v.config.GlitchLineColors) - 1; i >= 0; i-- {
			if err := backward.AddFrame(
				ch.InputSymbol, 1, VisualParams{Colors: fringe(v.config.GlitchLineColors[i])}); err != nil {
				return err
			}
		}

		snow := ch.Animation.NewScene("snow", SceneOptions{UsesInputColors: ch.UsesInputColors, Frames: 26})
		for i := 0; i < 25; i++ {
			if err := snow.AddFrame(*Choice(e.Rng, snowChars), 2,
				VisualParams{Colors: Fg(*Choice(e.Rng, v.config.NoiseColors))}); err != nil {
				return err
			}
		}
		if err := snow.AddFrame(ch.InputSymbol, 1, VisualParams{Colors: stable}); err != nil {
			return err
		}

		finalSnow := ch.Animation.NewScene("final_snow", SceneOptions{
			UsesInputColors: ch.UsesInputColors, Frames: 30,
		})
		for i := 0; i < 30; i++ {
			if err := finalSnow.AddFrame(*Choice(e.Rng, snowChars), 2,
				VisualParams{Colors: Fg(*Choice(e.Rng, v.config.NoiseColors))}); err != nil {
				return err
			}
		}

		redraw := ch.Animation.NewScene("final_redraw", SceneOptions{
			UsesInputColors: ch.UsesInputColors, Frames: 2,
		})
		if err := redraw.AddFrame("█", 6, VisualParams{Colors: Fg(MustParseColor("ffffff"))}); err != nil {
			return err
		}
		if err := redraw.AddFrame(ch.InputSymbol, 1, VisualParams{Colors: final}); err != nil {
			return err
		}

		ch.RegisterEvent(PathComplete, PathCaller("glitch"), ActivatePath("restore"))
		ch.RegisterEvent(PathActivated, PathCaller("glitch"), ActivateScene("rgb_glitch_fwd"))
		ch.RegisterEvent(PathActivated, PathCaller("restore"), ActivateScene("rgb_glitch_bwd"))
		ch.RegisterEvent(PathActivated, PathCaller("glitch_wave_mid"), ActivateScene("rgb_glitch_fwd"))
		ch.RegisterEvent(PathActivated, PathCaller("glitch_wave_end"), ActivateScene("rgb_glitch_fwd"))
		ch.RegisterEvent(SceneComplete, SceneCaller("rgb_glitch_bwd"), ActivateScene("base"))
	}
	return nil
}

func (v *VhsTape) lineSnow(e *Engine, index int) {
	for _, ch := range v.lines[index].characters {
		e.ActivateScene(ch, "snow")
	}
}

func (v *VhsTape) lineSetHoldTime(index, holdTime int) {
	for _, ch := range v.lines[index].characters {
		if path := ch.Motion.Path("glitch"); path != nil {
			path.HoldTime = holdTime
		}
	}
}

// lineGlitch sends a row sideways. Each character gets its own speed, so the
// row tears rather than sliding as one rigid block.
func (v *VhsTape) lineGlitch(e *Engine, index int, final bool) {
	for _, ch := range v.lines[index].characters {
		glitch, restore := ch.Motion.Path("glitch"), ch.Motion.Path("restore")
		if glitch == nil || restore == nil {
			continue
		}
		if final {
			glitch.HoldTime = 0
			restore.HoldTime = 0
		}
		glitch.Speed = 40 / float64(e.Rng.IntBetween(20, 40))
		restore.Speed = 40 / float64(e.Rng.IntBetween(20, 40))
		e.ActivatePath(ch, "glitch")
	}
}

func (v *VhsTape) lineRestore(e *Engine, index int) {
	for _, ch := range v.lines[index].characters {
		if restore := ch.Motion.Path("restore"); restore != nil {
			restore.Speed = 40 / float64(e.Rng.IntBetween(20, 40))
		}
		e.ActivatePath(ch, "restore")
	}
}

func (v *VhsTape) lineActivatePath(e *Engine, index int, pathID string) {
	for _, ch := range v.lines[index].characters {
		e.ActivatePath(ch, pathID)
	}
}

func (v *VhsTape) lineMovementComplete(index int) bool {
	for _, ch := range v.lines[index].characters {
		if !ch.Motion.MovementIsComplete() {
			return false
		}
	}
	return true
}

func (v *VhsTape) activateLine(e *Engine, index int) {
	for _, ch := range v.lines[index].characters {
		e.Activate(ch)
	}
}

func containsInt(list []int, want int) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// glitchWave walks a three-row band of tracking noise up the picture. The band
// mostly holds still and occasionally steps, which is what a tape does.
func (v *VhsTape) glitchWave(e *Engine) {
	canvas := e.Terminal.Canvas
	if !v.hasWaveTop {
		if canvas.TextHeight < 3 {
			return
		}
		lower := max(3, roundHalfEven(float64(canvas.TextHeight)*0.5))
		v.waveTop = canvas.TextBottom + e.Rng.IntBetween(lower, canvas.TextHeight)
		v.hasWaveTop = true
	}

	for _, index := range v.waveLines {
		if !v.lineMovementComplete(index) {
			return
		}
	}

	if len(v.waveLines) > 0 {
		delta := 0
		if e.Rng.Float() < 0.3 {
			if e.Rng.Float() < 0.3 {
				delta = 1
			} else {
				delta = -1
			}
		}
		v.waveTop = max(2, min(v.waveTop+delta, canvas.TextTop))
	}

	var newWaveLines []int
	for row := v.waveTop - 2; row <= v.waveTop; row++ {
		if index, ok := v.lineByRow[row]; ok {
			newWaveLines = append(newWaveLines, index)
		}
	}

	for _, index := range v.waveLines {
		if !containsInt(newWaveLines, index) {
			v.lineRestore(e, index)
			v.activateLine(e, index)
		}
	}
	v.waveLines = newWaveLines

	if v.waveTop < canvas.TextBottom+2 {
		for _, index := range v.waveLines {
			v.lineRestore(e, index)
			v.activateLine(e, index)
		}
		v.waveLines = nil
		v.hasWaveTop = false
		return
	}
	// The middle row of the band travels furthest, so the band bows.
	pathIDs := []string{"glitch_wave_mid", "glitch_wave_end", "glitch_wave_mid"}
	for i, index := range v.waveLines {
		if i >= len(pathIDs) {
			break
		}
		v.lineActivatePath(e, index, pathIDs[i])
		v.activateLine(e, index)
	}
}

// Advance runs one frame and reports whether the effect is still going.
func (v *VhsTape) Advance(e *Engine) bool {
	if v.phase == vhsComplete && e.ActiveCount() == 0 {
		return false
	}
	switch v.phase {
	case vhsGlitching:
		v.advanceGlitching(e)
	case vhsNoise:
		if e.ActiveCount() == 0 {
			for _, ch := range e.Terminal.GetCharacters(e.Rng, InputOnly(), SortTopToBottomLeftToRight) {
				e.ActivateScene(ch, "final_snow")
				e.Activate(ch)
			}
			v.phase = vhsRedraw
		}
	case vhsRedraw:
		if v.redrawing || e.ActiveCount() == 0 {
			v.redrawing = true
			if len(v.toRedraw) == 0 {
				v.phase = vhsComplete
				break
			}
			next := v.toRedraw[len(v.toRedraw)-1]
			v.toRedraw = v.toRedraw[:len(v.toRedraw)-1]
			for _, ch := range v.lines[next].characters {
				e.ActivateScene(ch, "final_redraw")
				e.Activate(ch)
			}
		}
	case vhsComplete:
	}
	e.Update()
	return true
}

func (v *VhsTape) advanceGlitching(e *Engine) {
	waveSettled := len(v.waveLines) == 0
	if !waveSettled {
		waveSettled = true
		for _, index := range v.waveLines {
			if !v.lineMovementComplete(index) {
				waveSettled = false
				break
			}
		}
	}
	if waveSettled {
		v.glitchWave(e)
	}

	still := v.glitchLines[:0]
	for _, index := range v.glitchLines {
		if !v.lineMovementComplete(index) {
			still = append(still, index)
		}
	}
	v.glitchLines = still

	if e.Rng.Float() < v.config.GlitchLineChance && len(v.glitchLines) < 3 && len(v.lines) > 0 {
		candidate := e.Rng.IndexBelow(len(v.lines))
		if !containsInt(v.waveLines, candidate) && !containsInt(v.glitchLines, candidate) {
			v.lineSetHoldTime(candidate, e.Rng.IntBetween(20, 75))
			v.glitchLines = append(v.glitchLines, candidate)
			v.lineGlitch(e, candidate, false)
			v.activateLine(e, candidate)
		}
	}

	if e.Rng.Float() < v.config.NoiseChance {
		for index := range v.lines {
			v.lineSnow(e, index)
			if !containsInt(v.waveLines, index) && !containsInt(v.glitchLines, index) {
				v.activateLine(e, index)
			}
		}
	}

	v.glitchingStepsElapsed++
	if v.glitchingStepsElapsed >= v.config.TotalGlitchTime {
		for _, index := range v.waveLines {
			v.lineRestore(e, index)
		}
		for _, index := range v.glitchLines {
			v.lineRestore(e, index)
		}
		v.phase = vhsNoise
	}
}
