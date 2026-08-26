package tfx

// decrypt, ported from ttfx src/effects/decrypt.rs, which ports
// TerminalTextEffects effects/effect_decrypt.py by ChrisBuilds.

func init() {
	Register(Descriptor{
		Name:        "decrypt",
		Description: "Types out ciphertext, then decrypts it into the real text",
		New:         func() Effect { return NewDecrypt(DefaultDecryptConfig()) },
	})
}

// DecryptConfig tunes the decrypt effect.
type DecryptConfig struct {
	// TypingSpeed is how many characters appear per typing tick.
	TypingSpeed int
	// CiphertextColors are picked at random per character for the scrambled
	// phase.
	CiphertextColors []Color
	// FinalGradientStops colour the text once it resolves. They are ignored
	// when the engine is set to resolve to the input's own colours.
	FinalGradientStops     []Color
	FinalGradientSteps     []int
	FinalGradientDirection GradientDirection
}

// DefaultDecryptConfig is upstream's default decrypt.
func DefaultDecryptConfig() DecryptConfig {
	return DecryptConfig{
		TypingSpeed:            2,
		CiphertextColors:       []Color{MustParseColor("008000"), MustParseColor("00cb00"), MustParseColor("00ff00")},
		FinalGradientStops:     []Color{MustParseColor("eda000")},
		FinalGradientSteps:     []int{12},
		FinalGradientDirection: Vertical,
	}
}

type decryptPhase int

const (
	decryptTyping decryptPhase = iota
	decryptDecrypting
)

// Decrypt types the text out as ciphertext, then resolves it character by
// character.
type Decrypt struct {
	config DecryptConfig

	typingPending     []*Character
	decryptingPending []*Character
	phase             decryptPhase
	encryptedSymbols  []string
	finalColors       map[*Character]ColorPair
}

// NewDecrypt builds the effect.
func NewDecrypt(config DecryptConfig) *Decrypt {
	d := &Decrypt{config: config, finalColors: map[*Character]ColorPair{}}
	d.makeEncryptedSymbols()
	return d
}

// makeEncryptedSymbols collects the glyph pool the ciphertext draws from:
// printable ASCII, block elements, box drawing, and Latin supplement.
func (d *Decrypt) makeEncryptedSymbols() {
	ranges := [][2]rune{
		{33, 127},
		{9608, 9632},
		{9472, 9599},
		{174, 452},
	}
	for _, r := range ranges {
		for n := r[0]; n < r[1]; n++ {
			d.encryptedSymbols = append(d.encryptedSymbols, string(n))
		}
	}
}

// Build sets up every character's typing and decrypting scenes.
func (d *Decrypt) Build(e *Engine) error {
	gradient, err := NewGradient(d.config.FinalGradientStops, d.config.FinalGradientSteps, false)
	if err != nil {
		return err
	}
	canvas := e.Terminal.Canvas
	mapping, err := gradient.BuildCoordinateColorMapping(
		canvas.TextBottom, canvas.TextTop, canvas.TextLeft, canvas.TextRight,
		d.config.FinalGradientDirection)
	if err != nil {
		return err
	}
	fallback := gradient.Spectrum[0]
	dynamic := e.Terminal.Config.ExistingColorHandling == DynamicExistingColors

	characters := e.Terminal.GetCharacters(e.Rng, InputOnly(), SortTopToBottomLeftToRight)
	for _, ch := range characters {
		if dynamic && ch.UsesInputColors {
			d.finalColors[ch] = ch.Animation.InputColors
		} else {
			d.finalColors[ch] = Fg(mapping.At(ch.InputCoord, fallback))
		}
	}
	if err := d.prepareTyping(e, characters); err != nil {
		return err
	}
	return d.prepareDecrypting(e, characters)
}

func (d *Decrypt) prepareTyping(e *Engine, characters []*Character) error {
	for _, ch := range characters {
		scene := ch.Animation.NewScene("typing", SceneOptions{UsesInputColors: ch.UsesInputColors, Frames: 5})
		for _, block := range []string{"▉", "▓", "▒", "░"} {
			color := Choice(e.Rng, d.config.CiphertextColors)
			if err := scene.AddFrame(block, 2, VisualParams{Colors: Fg(*color)}); err != nil {
				return err
			}
		}
		symbol := Choice(e.Rng, d.encryptedSymbols)
		color := Choice(e.Rng, d.config.CiphertextColors)
		if err := scene.AddFrame(*symbol, 1, VisualParams{Colors: Fg(*color)}); err != nil {
			return err
		}
		d.typingPending = append(d.typingPending, ch)
	}
	return nil
}

func (d *Decrypt) prepareDecrypting(e *Engine, characters []*Character) error {
	for _, ch := range characters {
		if err := d.makeDecryptingScenes(e, ch); err != nil {
			return err
		}
		ch.RegisterEvent(SceneComplete, SceneCaller("fast_decrypt"), ActivateScene("slow_decrypt"))
		ch.RegisterEvent(SceneComplete, SceneCaller("slow_decrypt"), ActivateScene("discovered"))
		e.ActivateScene(ch, "fast_decrypt")
		d.decryptingPending = append(d.decryptingPending, ch)
	}
	return nil
}

func (d *Decrypt) makeDecryptingScenes(e *Engine, ch *Character) error {
	color := *Choice(e.Rng, d.config.CiphertextColors)

	fast := ch.Animation.NewScene("fast_decrypt", SceneOptions{UsesInputColors: ch.UsesInputColors, Frames: 80})
	for i := 0; i < 80; i++ {
		symbol := Choice(e.Rng, d.encryptedSymbols)
		if err := fast.AddFrame(*symbol, 2, VisualParams{Colors: Fg(color)}); err != nil {
			return err
		}
	}

	slowFrames := e.Rng.IntBetween(1, 15)
	slow := ch.Animation.NewScene("slow_decrypt", SceneOptions{UsesInputColors: ch.UsesInputColors, Frames: slowFrames})
	for i := 0; i < slowFrames; i++ {
		symbol := Choice(e.Rng, d.encryptedSymbols)
		// A wide spread of frame durations stops the resolve arriving in
		// visible waves. Three in ten characters hold much longer.
		duration := e.Rng.IntBelow(3, 6)
		if e.Rng.IntBetween(0, 100) <= 30 {
			duration = e.Rng.IntBelow(35, 60)
		}
		if err := slow.AddFrame(*symbol, duration, VisualParams{Colors: Fg(color)}); err != nil {
			return err
		}
	}

	discovered := ch.Animation.NewScene("discovered", SceneOptions{UsesInputColors: ch.UsesInputColors, Frames: 10})
	final := d.finalColors[ch]
	white := MustParseColor("ffffff")
	var fgGradient, bgGradient *Gradient
	var err error
	if final.HasFg {
		if fgGradient, err = NewGradientSteps([]Color{white, final.Fg}, 10, false); err != nil {
			return err
		}
	}
	if final.HasBg {
		if bgGradient, err = NewGradientSteps([]Color{white, final.Bg}, 10, false); err != nil {
			return err
		}
	}
	if fgGradient == nil && bgGradient == nil {
		return discovered.AddFrame(ch.InputSymbol, 5, VisualParams{})
	}
	return discovered.ApplyGradientToSymbols([]string{ch.InputSymbol}, 5, fgGradient, bgGradient)
}

// Advance runs one frame and reports whether the effect is still going.
func (d *Decrypt) Advance(e *Engine) bool {
	if d.phase == decryptTyping {
		if len(d.typingPending) > 0 || e.ActiveCount() > 0 {
			// Skipping a quarter of the ticks gives the typing an uneven,
			// human rhythm rather than a metronome.
			if len(d.typingPending) > 0 && e.Rng.IntBetween(0, 100) <= 75 {
				for i := 0; i < d.config.TypingSpeed && len(d.typingPending) > 0; i++ {
					next := d.typingPending[0]
					d.typingPending = d.typingPending[1:]
					e.Terminal.SetCharacterVisibility(next, true)
					e.ActivateScene(next, "typing")
					e.Activate(next)
				}
			}
			e.Update()
			return true
		}
		e.ClearActive()
		for _, ch := range d.decryptingPending {
			e.Activate(ch)
		}
		for _, ch := range e.ActiveCharacters() {
			e.ActivateScene(ch, "fast_decrypt")
		}
		d.phase = decryptDecrypting
	}

	if e.ActiveCount() > 0 {
		e.Update()
		return true
	}
	return false
}
