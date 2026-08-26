package tfx

// colorshift, ported from ttfx src/effects/colorshift.rs, which ports
// TerminalTextEffects effects/effect_colorshift.py by ChrisBuilds.

func init() {
	Register(Descriptor{
		Name:        "colorshift",
		Description: "Washes a colour gradient across the text as a travelling wave",
		New:         func() Effect { return NewColorShift(DefaultColorShiftConfig()) },
	})
}

// ColorShiftConfig tunes the colorshift effect.
type ColorShiftConfig struct {
	// GradientStops and GradientSteps build the wave's colours.
	GradientStops []Color
	GradientSteps []int
	// GradientFrames is how many frames each colour holds. Raise it to slow
	// the wave down.
	GradientFrames int
	// NoTravel paints every character the same colour at the same time
	// instead of offsetting them into a wave.
	NoTravel bool
	// TravelDirection is the axis the wave moves along.
	TravelDirection GradientDirection
	// ReverseTravelDirection flips the wave.
	ReverseTravelDirection bool
	// NoLoop stops the gradient wrapping back to its first colour, which
	// leaves a visible seam where the wave restarts.
	NoLoop bool
	// Cycles is how many times the wave runs. Zero runs forever, which is
	// what a screen saver wants.
	Cycles int
	// SkipFinalGradient leaves the text in the wave's last colour instead of
	// resolving it.
	SkipFinalGradient bool
	// FinalGradientStops colour the text once the wave stops. They are
	// ignored when the engine is set to resolve to the input's own colours.
	FinalGradientStops     []Color
	FinalGradientSteps     []int
	FinalGradientDirection GradientDirection
}

// DefaultColorShiftConfig is upstream's default colorshift.
func DefaultColorShiftConfig() ColorShiftConfig {
	return ColorShiftConfig{
		GradientStops: []Color{
			MustParseColor("e81416"), MustParseColor("ffa500"), MustParseColor("faeb36"),
			MustParseColor("79c314"), MustParseColor("487de7"), MustParseColor("4b369d"),
			MustParseColor("70369d"),
		},
		GradientSteps:   []int{12},
		GradientFrames:  2,
		TravelDirection: Radial,
		Cycles:          3,
		FinalGradientStops: []Color{
			MustParseColor("e81416"), MustParseColor("ffa500"), MustParseColor("faeb36"),
			MustParseColor("79c314"), MustParseColor("487de7"), MustParseColor("4b369d"),
			MustParseColor("70369d"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: Vertical,
	}
}

// ColorShift keeps every character in place and cycles its colour, offsetting
// each one along an axis so the change reads as a wave crossing the screen.
type ColorShift struct {
	config      ColorShiftConfig
	finalColors map[*Character]ColorPair
	loopCount   map[*Character]int
}

// NewColorShift builds the effect.
func NewColorShift(config ColorShiftConfig) *ColorShift {
	return &ColorShift{
		config:      config,
		finalColors: map[*Character]ColorPair{},
		loopCount:   map[*Character]int{},
	}
}

// Build gives every character a gradient scene, offset by where it sits.
func (c *ColorShift) Build(e *Engine) error {
	finalGradient, err := NewGradient(c.config.FinalGradientStops, c.config.FinalGradientSteps, false)
	if err != nil {
		return err
	}
	canvas := e.Terminal.Canvas
	mapping, err := finalGradient.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight,
		c.config.FinalGradientDirection)
	if err != nil {
		return err
	}
	fallback := finalGradient.Spectrum[0]
	dynamic := e.Terminal.Config.ExistingColorHandling == DynamicExistingColors

	wave, err := NewGradient(c.config.GradientStops, c.config.GradientSteps, !c.config.NoLoop)
	if err != nil {
		return err
	}

	for _, ch := range e.Terminal.GetCharacters(e.Rng, InputOnly(), SortTopToBottomLeftToRight) {
		final := Fg(mapping.At(ch.InputCoord, fallback))
		if dynamic && ch.UsesInputColors {
			final = ch.Animation.InputColors
		}
		c.finalColors[ch] = final

		e.Terminal.SetCharacterVisibility(ch, true)
		colors := c.rotatedSpectrum(e, wave.Spectrum, ch.InputCoord)

		gradientScene := ch.Animation.NewScene("gradient", SceneOptions{UsesInputColors: ch.UsesInputColors})
		for _, color := range colors {
			if err := gradientScene.AddFrame(ch.InputSymbol, c.config.GradientFrames, VisualParams{Colors: Fg(color)}); err != nil {
				return err
			}
		}

		finalScene := ch.Animation.NewScene("final_gradient", SceneOptions{UsesInputColors: ch.UsesInputColors})
		last := colors[len(colors)-1]
		var fgGradient, bgGradient *Gradient
		if final.HasFg {
			if fgGradient, err = NewGradientSteps([]Color{last, final.Fg}, 8, false); err != nil {
				return err
			}
		}
		if final.HasBg {
			if bgGradient, err = NewGradientSteps([]Color{last, final.Bg}, 8, false); err != nil {
				return err
			}
		}
		if fgGradient == nil && bgGradient == nil {
			if err := finalScene.AddFrame(ch.InputSymbol, c.config.GradientFrames, VisualParams{}); err != nil {
				return err
			}
		} else if err := finalScene.ApplyGradientToSymbols([]string{ch.InputSymbol}, c.config.GradientFrames, fgGradient, bgGradient); err != nil {
			return err
		}

		e.ActivateScene(ch, gradientScene.ID)
		e.Activate(ch)
		ch.RegisterEvent(SceneComplete, SceneCaller("gradient"), Callback(c.onCycleComplete))
	}
	return nil
}

// rotatedSpectrum offsets a character's copy of the wave by where it sits, so
// neighbours are a step apart and the colour change travels.
func (c *ColorShift) rotatedSpectrum(e *Engine, spectrum []Color, coord Coord) []Color {
	if c.config.NoTravel {
		return spectrum
	}
	canvas := e.Terminal.Canvas
	var position float64
	switch c.config.TravelDirection {
	case Horizontal:
		position = float64(coord.Column) / float64(canvas.Right)
	case Vertical:
		position = float64(coord.Row) / float64(canvas.Top)
	case Diagonal:
		position = float64(coord.Row+coord.Column) / float64(canvas.Right+canvas.Top)
	case Radial:
		distance, ok := FindNormalizedDistanceFromCenter(
			canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight, coord)
		if ok {
			position = distance
		}
	}
	shift := int(float64(len(spectrum)) * position)
	if c.config.ReverseTravelDirection {
		shift = -shift
	}
	n := len(spectrum)
	if shift < 0 {
		shift = max(n+shift, 0)
	}
	shift = min(shift, n)
	rotated := make([]Color, 0, n)
	rotated = append(rotated, spectrum[shift:]...)
	rotated = append(rotated, spectrum[:shift]...)
	return rotated
}

// onCycleComplete restarts the wave until the cycle budget runs out, then
// resolves the character to its final colour.
func (c *ColorShift) onCycleComplete(e *Engine, ch *Character) {
	c.loopCount[ch]++
	if c.config.Cycles == 0 || c.loopCount[ch] < c.config.Cycles {
		e.ActivateScene(ch, "gradient")
		return
	}
	if !c.config.SkipFinalGradient {
		e.ActivateScene(ch, "final_gradient")
	}
}

// Advance runs one frame and reports whether the effect is still going.
func (c *ColorShift) Advance(e *Engine) bool {
	if e.ActiveCount() == 0 {
		return false
	}
	e.Update()
	return true
}
