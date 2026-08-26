package tfx

import "sort"

// rain, ported from ttfx src/effects/rain.rs, which ports
// TerminalTextEffects effects/effect_rain.py by ChrisBuilds.

func init() {
	Register(Descriptor{
		Name:        "rain",
		Description: "Characters fall from the top of the screen as raindrops and settle into place",
		New:         func() Effect { return NewRain(DefaultRainConfig()) },
	})
}

// RainConfig tunes the rain effect.
type RainConfig struct {
	// RainColors are picked at random per drop.
	RainColors []Color
	// MovementSpeedLow and MovementSpeedHigh bound each drop's fall speed.
	MovementSpeedLow  float64
	MovementSpeedHigh float64
	// RainSymbols are the glyphs a drop can wear on the way down.
	RainSymbols []string
	// FinalGradientStops colour the text once it lands. They are ignored when
	// the engine is set to resolve to the input's own colours.
	FinalGradientStops     []Color
	FinalGradientSteps     []int
	FinalGradientDirection GradientDirection
	// MovementEasing shapes the fall. The default accelerates downward.
	MovementEasing Easing
}

// DefaultRainConfig is upstream's default rain.
func DefaultRainConfig() RainConfig {
	return RainConfig{
		RainColors: []Color{
			MustParseColor("00315C"), MustParseColor("004C8F"), MustParseColor("0075DB"),
			MustParseColor("3F91D9"), MustParseColor("78B9F2"), MustParseColor("9AC8F5"),
			MustParseColor("B8D8F8"), MustParseColor("E3EFFC"),
		},
		MovementSpeedLow:  0.33,
		MovementSpeedHigh: 0.57,
		RainSymbols:       []string{"o", ".", ",", "*", "|"},
		FinalGradientStops: []Color{
			MustParseColor("488bff"), MustParseColor("b2e7de"), MustParseColor("57eaf7"),
		},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: Diagonal,
		MovementEasing:         InQuart,
	}
}

// Rain drops every character in from the top of the canvas, one row at a time
// from the bottom up, and fades it to its final colour when it lands.
type Rain struct {
	config RainConfig

	pending []*Character
	// rowOrder is the row keys still to release, ascending, and rowGroups the
	// characters in each.
	rowOrder  []int
	rowGroups map[int][]*Character
}

// NewRain builds the effect.
func NewRain(config RainConfig) *Rain {
	return &Rain{config: config, rowGroups: map[int][]*Character{}}
}

// Build gives every character a fall path and a landing fade.
func (r *Rain) Build(e *Engine) error {
	gradient, err := NewGradient(r.config.FinalGradientStops, r.config.FinalGradientSteps, false)
	if err != nil {
		return err
	}
	canvas := e.Terminal.Canvas
	mapping, err := gradient.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight,
		r.config.FinalGradientDirection)
	if err != nil {
		return err
	}
	fallback := gradient.Spectrum[0]
	dynamic := e.Terminal.Config.ExistingColorHandling == DynamicExistingColors

	characters := e.Terminal.GetCharacters(e.Rng, InputOnly(), SortTopToBottomLeftToRight)
	for _, ch := range characters {
		final := Fg(mapping.At(ch.InputCoord, fallback))
		if dynamic && ch.UsesInputColors {
			final = ch.Animation.InputColors
		}
		dropColor := *Choice(e.Rng, r.config.RainColors)

		fall := ch.Animation.NewScene("", SceneOptions{UsesInputColors: ch.UsesInputColors})
		symbol := Choice(e.Rng, r.config.RainSymbols)
		if err := fall.AddFrame(*symbol, 1, VisualParams{Colors: Fg(dropColor)}); err != nil {
			return err
		}

		fade := ch.Animation.NewScene("", SceneOptions{UsesInputColors: ch.UsesInputColors})
		var fgGradient, bgGradient *Gradient
		if final.HasFg {
			if fgGradient, err = NewGradientSteps([]Color{dropColor, final.Fg}, 7, false); err != nil {
				return err
			}
		}
		if final.HasBg {
			if bgGradient, err = NewGradientSteps([]Color{dropColor, final.Bg}, 7, false); err != nil {
				return err
			}
		}
		if fgGradient == nil && bgGradient == nil {
			if err := fade.AddFrame(ch.InputSymbol, 3, VisualParams{}); err != nil {
				return err
			}
		} else if err := fade.ApplyGradientToSymbols([]string{ch.InputSymbol}, 3, fgGradient, bgGradient); err != nil {
			return err
		}

		e.ActivateScene(ch, fall.ID)

		// Start the drop off the top of the canvas in its own column and fall
		// to where the character belongs.
		ch.Motion.SetCoordinate(C(ch.InputCoord.Column, canvas.Top))
		speed := e.Rng.Uniform(r.config.MovementSpeedLow, r.config.MovementSpeedHigh)
		path, err := ch.Motion.NewPath("", PathOptions{Speed: speed, Ease: r.config.MovementEasing, HasEase: true})
		if err != nil {
			return err
		}
		if _, err := path.NewWaypoint(ch.InputCoord, nil, ""); err != nil {
			return err
		}
		ch.RegisterEvent(PathComplete, PathCaller(path.ID), ActivateScene(fade.ID))
		e.ActivatePath(ch, path.ID)

		r.rowGroups[ch.InputCoord.Row] = append(r.rowGroups[ch.InputCoord.Row], ch)
	}
	for row := range r.rowGroups {
		r.rowOrder = append(r.rowOrder, row)
	}
	sort.Ints(r.rowOrder)
	return nil
}

// Advance runs one frame and reports whether the effect is still going.
func (r *Rain) Advance(e *Engine) bool {
	if len(r.rowOrder) == 0 && e.ActiveCount() == 0 && len(r.pending) == 0 {
		return false
	}
	if len(r.pending) == 0 && len(r.rowOrder) > 0 {
		row := r.rowOrder[0]
		r.rowOrder = r.rowOrder[1:]
		r.pending = append(r.pending, r.rowGroups[row]...)
		delete(r.rowGroups, row)
	}
	// One or two drops per frame, taken from anywhere in the row, so a row
	// arrives scattered rather than left to right.
	for i, n := 0, e.Rng.IntBetween(1, 2); i < n && len(r.pending) > 0; i++ {
		index := e.Rng.IndexBelow(len(r.pending))
		next := r.pending[index]
		r.pending = append(r.pending[:index], r.pending[index+1:]...)
		e.Terminal.SetCharacterVisibility(next, true)
		e.Activate(next)
	}
	e.Update()
	return true
}
