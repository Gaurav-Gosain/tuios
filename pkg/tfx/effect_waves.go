package tfx

// waves, ported from ttfx src/effects/waves.rs, which ports
// TerminalTextEffects effects/effect_waves.py by ChrisBuilds.

func init() {
	Register(Descriptor{
		Name:        "waves",
		Description: "Waves travel across the screen and leave the characters behind them",
		New:         func() Effect { return NewWaves(DefaultWavesConfig()) },
	})
}

// WavesConfig tunes the waves effect.
type WavesConfig struct {
	// WaveSymbols are the glyphs a character cycles through as a wave passes
	// over it. The default rises and falls, which is what makes it read as a
	// wave rather than a flicker.
	WaveSymbols []string
	// WaveGradientStops and WaveGradientSteps colour the wave itself.
	WaveGradientStops []Color
	WaveGradientSteps []int
	// WaveCount is how many times the wave runs before the characters settle.
	WaveCount int
	// WaveLength is how many frames each step of the wave holds. Raise it to
	// slow the wave down.
	WaveLength int
	// WaveDirection is the axis the wave travels along.
	WaveDirection CharacterGroup
	// WaveEasing shapes the wave's travel across each character.
	WaveEasing Easing
	// FinalGradientStops colour the text once the waves stop. They are ignored
	// when the engine is set to resolve to the input's own colours.
	FinalGradientStops     []Color
	FinalGradientSteps     []int
	FinalGradientDirection GradientDirection
}

// DefaultWavesConfig is upstream's default waves.
func DefaultWavesConfig() WavesConfig {
	return WavesConfig{
		WaveSymbols: []string{
			"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂", "▁",
		},
		WaveGradientStops: []Color{
			MustParseColor("f0ff65"), MustParseColor("ffb102"), MustParseColor("31a0d4"),
			MustParseColor("ffb102"), MustParseColor("f0ff65"),
		},
		WaveGradientSteps: []int{6},
		WaveCount:         7,
		WaveLength:        2,
		WaveDirection:     GroupColumnLeftToRight,
		WaveEasing:        InOutSine,
		FinalGradientStops: []Color{
			MustParseColor("ffb102"), MustParseColor("31a0d4"), MustParseColor("f0ff65"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: Diagonal,
	}
}

// Waves runs a band of rising and falling blocks across the screen. Nothing
// moves: each character wears the wave for a moment as it passes and is left
// behind it in its own colour.
type Waves struct {
	config         WavesConfig
	pendingColumns [][]*Character
}

// NewWaves builds the effect.
func NewWaves(config WavesConfig) *Waves {
	return &Waves{config: config}
}

// Build gives every character a wave scene and the settling scene that follows
// it, then groups the characters into the bands the wave travels through.
func (w *Waves) Build(e *Engine) error {
	finalGradient, err := NewGradient(w.config.FinalGradientStops, w.config.FinalGradientSteps, false)
	if err != nil {
		return err
	}
	canvas := e.Terminal.Canvas
	mapping, err := finalGradient.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight,
		w.config.FinalGradientDirection)
	if err != nil {
		return err
	}
	fallback := finalGradient.Spectrum[0]
	waveGradient, err := NewGradient(w.config.WaveGradientStops, w.config.WaveGradientSteps, false)
	if err != nil {
		return err
	}
	waveEnd := waveGradient.Spectrum[len(waveGradient.Spectrum)-1]
	dynamic := e.Terminal.Config.ExistingColorHandling == DynamicExistingColors

	for _, ch := range e.Terminal.GetCharacters(e.Rng, InputOnly(), SortTopToBottomLeftToRight) {
		final := Fg(mapping.At(ch.InputCoord, fallback))
		if dynamic && ch.UsesInputColors {
			final = ch.Animation.InputColors
		}

		wave := ch.Animation.NewScene("", SceneOptions{
			Ease:            w.config.WaveEasing,
			HasEase:         true,
			UsesInputColors: ch.UsesInputColors,
			Frames:          w.config.WaveCount * len(w.config.WaveSymbols),
		})
		for i := 0; i < w.config.WaveCount; i++ {
			if err := wave.ApplyGradientToSymbols(
				w.config.WaveSymbols, w.config.WaveLength, waveGradient, nil); err != nil {
				return err
			}
		}

		settle := ch.Animation.NewScene("", SceneOptions{UsesInputColors: ch.UsesInputColors})
		var fgGradient, bgGradient *Gradient
		if final.HasFg {
			if fgGradient, err = NewGradient(
				[]Color{waveEnd, final.Fg}, w.config.FinalGradientSteps, false); err != nil {
				return err
			}
		}
		if final.HasBg {
			if bgGradient, err = NewGradient(
				[]Color{waveEnd, final.Bg}, w.config.FinalGradientSteps, false); err != nil {
				return err
			}
		}
		if fgGradient == nil && bgGradient == nil {
			if err := settle.AddFrame(ch.InputSymbol, 10, VisualParams{}); err != nil {
				return err
			}
		} else if err := settle.ApplyGradientToSymbols(
			[]string{ch.InputSymbol}, 10, fgGradient, bgGradient); err != nil {
			return err
		}

		ch.RegisterEvent(SceneComplete, SceneCaller(wave.ID), ActivateScene(settle.ID))
		e.ActivateScene(ch, wave.ID)
		if dynamic {
			// The picture is already on the screen, so the wave has to pass
			// over it rather than paint it in. Every character is shown from
			// the first frame wearing the colour it will settle back to, and
			// releasing a band only starts its wave.
			//
			// Upstream leaves them hidden because upstream animates text
			// arriving from nothing, which is right for piped text and wrong
			// for a screen that was already there: it made the wave sweep
			// across an empty canvas with the picture trailing behind it.
			ch.Animation.SetAppearance(ch.InputSymbol, final, ch.UsesInputColors)
			e.Terminal.SetCharacterVisibility(ch, true)
		}
	}

	w.pendingColumns = e.Terminal.GetCharactersGrouped(InputOnly(), w.config.WaveDirection)
	return nil
}

// Advance releases one band per frame and reports whether the effect is still
// going.
func (w *Waves) Advance(e *Engine) bool {
	if len(w.pendingColumns) == 0 && e.ActiveCount() == 0 {
		return false
	}
	if len(w.pendingColumns) > 0 {
		next := w.pendingColumns[0]
		w.pendingColumns = w.pendingColumns[1:]
		for _, ch := range next {
			e.Terminal.SetCharacterVisibility(ch, true)
			e.Activate(ch)
		}
	}
	e.Update()
	return true
}
