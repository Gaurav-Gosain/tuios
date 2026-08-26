package tfx

import "sort"

// Effect is one animation. Build runs once and sets up every character's
// scenes and paths; Advance then runs one frame at a time until it reports
// that the effect is over.
//
// Advance does not return the frame. The caller reads it from the engine with
// Frame or FrameRows, so an effect never has to care whether its host wants an
// ANSI string or a cell grid.
type Effect interface {
	Build(e *Engine) error
	Advance(e *Engine) bool
}

// Factory builds a fresh instance of an effect with its default settings.
type Factory func() Effect

// Descriptor is a registered effect: its name, one line about what it does,
// and how to build one.
type Descriptor struct {
	Name        string
	Description string
	New         Factory
}

var registry = map[string]Descriptor{}

// Register adds an effect to the registry. It is meant to be called from an
// init function, and it panics on a duplicate name because that is a
// programming mistake rather than a runtime condition.
func Register(d Descriptor) {
	if _, exists := registry[d.Name]; exists {
		panic("tfx: effect " + d.Name + " is registered twice")
	}
	registry[d.Name] = d
}

// Lookup finds a registered effect by name.
func Lookup(name string) (Descriptor, bool) {
	d, ok := registry[name]
	return d, ok
}

// Names lists every registered effect, sorted.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Descriptors lists every registered effect with its description, sorted by
// name.
func Descriptors() []Descriptor {
	names := Names()
	out := make([]Descriptor, 0, len(names))
	for _, name := range names {
		out = append(out, registry[name])
	}
	return out
}

// Run builds an effect and returns every frame it produces, capped at
// maxFrames. It exists for tests and for a caller that wants the whole
// animation up front rather than one frame per host frame.
func Run(effect Effect, e *Engine, maxFrames int) ([]string, error) {
	if err := effect.Build(e); err != nil {
		return nil, err
	}
	var frames []string
	for len(frames) < maxFrames {
		if !effect.Advance(e) {
			break
		}
		frames = append(frames, e.Frame())
	}
	return frames, nil
}
