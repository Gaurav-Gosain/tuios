package tfx

import (
	"fmt"
	"strconv"
	"strings"
)

// ExistingColorHandling says what an effect does with colours the input
// already carried.
type ExistingColorHandling int

// The three input-colour policies.
const (
	// IgnoreExistingColors throws the input colours away and uses the
	// effect's own gradient. This is upstream's default.
	IgnoreExistingColors ExistingColorHandling = iota
	// DynamicExistingColors resolves every character back to the colour it
	// arrived with. A screen saver over a captured screen wants this: the
	// screen reassembles in its own colours.
	DynamicExistingColors
	// AlwaysExistingColors pins every frame to the input colour, so the
	// effect animates shape but never colour.
	AlwaysExistingColors
)

// SyncMetric ties an animation to the progress of the character's motion.
type SyncMetric int

// The two sync metrics, plus the absence of one.
const (
	SyncNone SyncMetric = iota
	SyncDistance
	SyncStep
)

// CharacterVisual is one character's appearance for one frame, with its SGR
// string precomputed because the renderer emits it per visible cell per frame.
type CharacterVisual struct {
	Symbol    string
	Bold      bool
	Italic    bool
	Underline bool
	Colors    ColorPair

	formatted string
}

// VisualParams are the settable fields of a CharacterVisual.
type VisualParams struct {
	Bold      bool
	Italic    bool
	Underline bool
	Colors    ColorPair
}

// NewCharacterVisual builds a visual and formats its SGR string.
func NewCharacterVisual(symbol string, p VisualParams) *CharacterVisual {
	v := &CharacterVisual{
		Symbol:    symbol,
		Bold:      p.Bold,
		Italic:    p.Italic,
		Underline: p.Underline,
		Colors:    p.Colors,
	}
	v.formatted = v.format()
	return v
}

// PlainVisual is the character with no styling at all.
func PlainVisual(symbol string) *CharacterVisual {
	return NewCharacterVisual(symbol, VisualParams{})
}

// Formatted returns the symbol wrapped in its SGR sequences.
func (v *CharacterVisual) Formatted() string { return v.formatted }

func (v *CharacterVisual) format() string {
	var b strings.Builder
	if v.Bold {
		b.WriteString("\x1b[1m")
	}
	if v.Italic {
		b.WriteString("\x1b[3m")
	}
	if v.Underline {
		b.WriteString("\x1b[4m")
	}
	if v.Colors.HasFg {
		writeSGRColor(&b, "38", v.Colors.Fg)
	}
	if v.Colors.HasBg {
		writeSGRColor(&b, "48", v.Colors.Bg)
	}
	if b.Len() == 0 {
		return v.Symbol
	}
	b.WriteString(v.Symbol)
	b.WriteString("\x1b[0m")
	return b.String()
}

func writeSGRColor(b *strings.Builder, prefix string, c Color) {
	b.WriteString("\x1b[")
	b.WriteString(prefix)
	b.WriteString(";2;")
	b.WriteString(strconv.Itoa(int(c.R)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.G)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.B)))
	b.WriteByte('m')
}

// visualKey identifies a formatted appearance. Every field is comparable, so
// it works as a map key directly.
type visualKey struct {
	symbol string
	params VisualParams
}

// visualCache shares CharacterVisuals across every character in one run.
//
// This is not a micro-optimisation. A screen saver animating a full 200 by 50
// screen has thousands of characters, and decrypt gives each of them about a
// hundred frames; without sharing that is a million formatted strings for a
// pool of only a couple of thousand distinct appearances. The cache lives on
// the Terminal, so it is freed when the run is.
type visualCache struct {
	m map[visualKey]*CharacterVisual
}

func newVisualCache() *visualCache {
	return &visualCache{m: make(map[visualKey]*CharacterVisual)}
}

func (c *visualCache) get(symbol string, p VisualParams) *CharacterVisual {
	if c == nil {
		return NewCharacterVisual(symbol, p)
	}
	key := visualKey{symbol: symbol, params: p}
	if v, ok := c.m[key]; ok {
		return v
	}
	v := NewCharacterVisual(symbol, p)
	c.m[key] = v
	return v
}

// Frame is one visual held for a number of ticks.
type Frame struct {
	Visual       *CharacterVisual
	Duration     int
	ticksElapsed int
}

// Scene is a named sequence of frames on one character.
type Scene struct {
	ID        string
	IsLooping bool
	Sync      SyncMetric
	Ease      Easing
	HasEase   bool

	// allFrames is stable storage. frames and playedFrames hold indices into
	// it, which preserves the object identity that upstream's frame index map
	// depends on.
	allFrames    []Frame
	frames       []int
	playedFrames []int
	// frameIndexMap maps an easing tick to a frame index. It is only built
	// for a scene that eases, because it holds one entry per tick and an
	// eighty-frame scene over thousands of characters is a lot of entries to
	// keep for a lookup nothing will make.
	frameIndexMap    []int
	easingTotalSteps int
	easingStep       int

	visuals *visualCache

	// preexistingColors overrides every frame's colours when the animation is
	// pinned to the input colours.
	preexistingColors ColorPair
	hasPreexisting    bool
	preexistingBold   bool
}

// AddFrame appends a frame held for duration ticks.
func (s *Scene) AddFrame(symbol string, duration int, p VisualParams) error {
	if duration < 1 {
		return fmt.Errorf("a frame must last at least one tick, got %d", duration)
	}
	if s.hasPreexisting {
		p.Colors = s.preexistingColors
	}
	if s.preexistingBold {
		p.Bold = true
	}
	index := len(s.allFrames)
	s.allFrames = append(s.allFrames, Frame{Visual: s.visuals.get(symbol, p), Duration: duration})
	s.frames = append(s.frames, index)
	s.easingTotalSteps += duration
	if s.HasEase {
		for i := 0; i < duration; i++ {
			s.frameIndexMap = append(s.frameIndexMap, index)
		}
	}
	return nil
}

// ApplyGradientToSymbols spreads a colour spectrum across a list of symbols,
// pairing whichever list is longer against the shorter one.
func (s *Scene) ApplyGradientToSymbols(symbols []string, duration int, fg, bg *Gradient) error {
	fgHas := fg != nil && len(fg.Spectrum) > 0
	bgHas := bg != nil && len(bg.Spectrum) > 0
	if !fgHas && !bgHas {
		return fmt.Errorf("at least one gradient must have at least one colour")
	}
	if len(symbols) == 0 {
		return fmt.Errorf("at least one symbol is needed")
	}

	var pairs []ColorPair
	switch {
	case fgHas && bgHas:
		if len(fg.Spectrum) >= len(bg.Spectrum) {
			for _, p := range cyclicDistribution(fg.Spectrum, bg.Spectrum) {
				pairs = append(pairs, FgBg(p.large, p.small))
			}
		} else {
			for _, p := range cyclicDistribution(bg.Spectrum, fg.Spectrum) {
				pairs = append(pairs, FgBg(p.small, p.large))
			}
		}
	case fgHas:
		for _, c := range fg.Spectrum {
			pairs = append(pairs, Fg(c))
		}
	default:
		for _, c := range bg.Spectrum {
			pairs = append(pairs, Bg(c))
		}
	}

	if len(symbols) >= len(pairs) {
		for _, p := range cyclicDistribution(symbols, pairs) {
			if err := s.AddFrame(p.large, duration, VisualParams{Colors: p.small}); err != nil {
				return err
			}
		}
		return nil
	}
	for _, p := range cyclicDistribution(pairs, symbols) {
		if err := s.AddFrame(p.small, duration, VisualParams{Colors: p.large}); err != nil {
			return err
		}
	}
	return nil
}

type distributed[L any, S any] struct {
	large L
	small S
}

// cyclicDistribution pairs every element of the larger list with an element of
// the smaller one, spreading the remainder across the run rather than dumping
// it at the end. Ported from Scene.apply_gradient_to_symbols upstream.
func cyclicDistribution[L any, S any](larger []L, smaller []S) []distributed[L, S] {
	if len(smaller) == 0 {
		return nil
	}
	repeatFactor := len(larger) / len(smaller)
	overflowCount := len(larger) % len(smaller)
	overflowUsed := false
	smallerIndex := 0
	currentRepeat := 0
	out := make([]distributed[L, S], 0, len(larger))
	for _, element := range larger {
		if currentRepeat >= repeatFactor {
			switch {
			case overflowCount > 0 && !overflowUsed:
				overflowUsed = true
				overflowCount--
			case overflowCount > 0:
				smallerIndex++
				currentRepeat = 0
				overflowUsed = false
			default:
				smallerIndex++
				currentRepeat = 0
			}
		}
		currentRepeat++
		if smallerIndex >= len(smaller) {
			smallerIndex = len(smaller) - 1
		}
		out = append(out, distributed[L, S]{large: element, small: smaller[smallerIndex]})
	}
	return out
}

// activate returns the visual the scene starts on.
func (s *Scene) activate() (*CharacterVisual, error) {
	if len(s.frames) == 0 {
		return nil, fmt.Errorf("scene %q has no frames", s.ID)
	}
	return s.allFrames[s.frames[0]].Visual, nil
}

// nextVisual ticks the head frame, retiring it when its duration runs out and
// refilling the queue when the scene loops.
func (s *Scene) nextVisual() *CharacterVisual {
	head := s.frames[0]
	visual := s.allFrames[head].Visual
	s.allFrames[head].ticksElapsed++
	if s.allFrames[head].ticksElapsed == s.allFrames[head].Duration {
		s.allFrames[head].ticksElapsed = 0
		s.playedFrames = append(s.playedFrames, head)
		s.frames = s.frames[1:]
		if s.IsLooping && len(s.frames) == 0 {
			s.frames = s.playedFrames
			s.playedFrames = nil
		}
	}
	return visual
}

// reset puts every frame back in the queue in its original order.
func (s *Scene) reset() {
	remaining := s.frames
	for _, index := range remaining {
		s.allFrames[index].ticksElapsed = 0
		s.playedFrames = append(s.playedFrames, index)
	}
	s.frames = s.playedFrames
	s.playedFrames = nil
	s.easingStep = 0
}

// Animation is one character's animation state: its scenes, which one is
// active, and the visual it currently shows.
type Animation struct {
	scenes      orderedMap[Scene]
	activeScene string
	hasActive   bool

	ExistingColorHandling ExistingColorHandling
	InputColors           ColorPair
	InputBold             bool

	currentVisual *CharacterVisual
	visuals       *visualCache
}

func newAnimation(inputSymbol string, visuals *visualCache) Animation {
	return Animation{
		scenes:        newOrderedMap[Scene](),
		currentVisual: visuals.get(inputSymbol, VisualParams{}),
		visuals:       visuals,
	}
}

// CurrentVisual is what the renderer draws for this character right now.
func (a *Animation) CurrentVisual() *CharacterVisual { return a.currentVisual }

// Scene looks a scene up by id, returning nil when it is absent.
func (a *Animation) Scene(id string) *Scene { return a.scenes.Get(id) }

// NewScene registers a scene. An empty id gets an auto-allocated one, which is
// returned. A duplicate id replaces the old scene, as it does upstream.
func (a *Animation) NewScene(id string, opts SceneOptions) *Scene {
	if id == "" {
		id = a.scenes.nextAutoID()
	}
	scene := &Scene{
		ID:        id,
		IsLooping: opts.Looping,
		Sync:      opts.Sync,
		Ease:      opts.Ease,
		HasEase:   opts.HasEase,
		visuals:   a.visuals,
	}
	if opts.Frames > 0 {
		scene.allFrames = make([]Frame, 0, opts.Frames)
		scene.frames = make([]int, 0, opts.Frames)
	}
	if a.ExistingColorHandling == AlwaysExistingColors && opts.UsesInputColors {
		scene.preexistingColors = a.InputColors
		scene.hasPreexisting = true
		scene.preexistingBold = a.InputBold
	}
	a.scenes.Set(id, scene)
	return scene
}

// SceneOptions are the knobs NewScene accepts.
type SceneOptions struct {
	Looping         bool
	Sync            SyncMetric
	Ease            Easing
	HasEase         bool
	UsesInputColors bool
	// Frames is how many frames the caller is about to add, if it knows.
	// Nothing depends on it being right, but a scene with eighty frames
	// regrows its slices seven times without it, and over a full screen that
	// regrowth is most of what the build allocates.
	Frames int
}

// ActiveSceneIsComplete reports whether the active scene has run out of
// frames. A looping scene always reads as complete, which is upstream's
// behaviour and the reason a loop-only character counts as inactive.
func (a *Animation) ActiveSceneIsComplete() bool {
	if !a.hasActive {
		return true
	}
	scene := a.scenes.Get(a.activeScene)
	if scene == nil {
		return true
	}
	return len(scene.frames) == 0 || scene.IsLooping
}

// SetAppearance overrides the current visual outside of any scene.
func (a *Animation) SetAppearance(symbol string, colors ColorPair, usesInputColors bool) {
	bold := false
	if a.ExistingColorHandling == AlwaysExistingColors && usesInputColors {
		colors = a.InputColors
		bold = a.InputBold
	}
	a.currentVisual = a.visuals.get(symbol, VisualParams{Bold: bold, Colors: colors})
}
