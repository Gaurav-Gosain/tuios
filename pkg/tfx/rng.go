package tfx

import "math/rand/v2"

// Rng is the engine's source of randomness.
//
// ttfx clones Python's Mersenne Twister so that it can reproduce upstream
// frame for frame. This port makes no parity claim, so it uses Go's own
// generator. Seeding it makes a run reproducible, which is what the tests
// need; nothing here will match either of the other two implementations.
type Rng struct {
	r *rand.Rand
}

// NewRng builds a generator from a seed. The same seed gives the same run.
func NewRng(seed uint64) *Rng {
	return &Rng{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

// IntBetween returns an integer in [low, high]. Both ends are included, which
// is what upstream's randint does. A reversed range returns low.
func (g *Rng) IntBetween(low, high int) int {
	if high <= low {
		return low
	}
	return low + g.r.IntN(high-low+1)
}

// IntBelow returns an integer in [low, high). Upstream calls this randrange.
func (g *Rng) IntBelow(low, high int) int {
	if high <= low {
		return low
	}
	return low + g.r.IntN(high-low)
}

// Float returns a float in [0, 1).
func (g *Rng) Float() float64 { return g.r.Float64() }

// Uniform returns a float in [low, high].
func (g *Rng) Uniform(low, high float64) float64 {
	if high <= low {
		return low
	}
	return low + g.r.Float64()*(high-low)
}

// IndexBelow returns an index in [0, n). It exists so the generic helpers
// below can stay free functions: Go does not allow a generic method.
func (g *Rng) IndexBelow(n int) int {
	if n <= 0 {
		return 0
	}
	return g.r.IntN(n)
}

// Choice picks one element. It returns nil for an empty slice, so callers that
// can be handed an empty option list must check.
func Choice[T any](g *Rng, items []T) *T {
	if len(items) == 0 {
		return nil
	}
	return &items[g.IndexBelow(len(items))]
}

// Shuffle reorders a slice in place.
func Shuffle[T any](g *Rng, items []T) {
	g.r.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
}
