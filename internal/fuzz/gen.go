package fuzz

import (
	"math/rand/v2"
)

// source is where a generator's randomness comes from. Two implementations feed
// the same generator code: a seeded PRNG for the local loop, and a byte reader
// for `go test -fuzz`, so a coverage-guided mutation of the corpus bytes is a
// mutation of the action stream and a corpus entry always decodes to the same
// run.
type source interface{ next() uint64 }

type prngSource struct{ r *rand.Rand }

func (p prngSource) next() uint64 { return p.r.Uint64() }

// byteSource reads eight bytes at a time. Corpus entries are short and runs are
// long, so once the input is spent it falls through to a PRNG seeded from the
// input itself: the run stays a deterministic function of the bytes, and the
// mutator still steers the prefix that decides the interesting part.
type byteSource struct {
	b    []byte
	i    int
	tail *rand.Rand
}

func newByteSource(b []byte) *byteSource {
	s := fnv64(b)
	return &byteSource{b: b, tail: rand.New(rand.NewPCG(s, s^0x9e3779b97f4a7c15))}
}

// fnv64 hashes the input to seed the tail PRNG. It is spelled out rather than
// taken from maphash because maphash seeds itself per process, which would make
// one corpus entry decode to a different run in every process that replayed it.
func fnv64(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

func (s *byteSource) next() uint64 {
	if s.i+8 > len(s.b) {
		return s.tail.Uint64()
	}
	var v uint64
	for _, c := range s.b[s.i : s.i+8] {
		v = v<<8 | uint64(c)
	}
	s.i += 8
	return v
}

// Generator turns a source into a stream of actions. It carries the small
// amount of state needed to emit sequences that are awkward on purpose: whether
// a mouse button is currently held, so it can slip a host resize or a detach
// between a press and its release.
type Generator struct {
	src        source
	buf        []Action
	held       int // the button currently down, ButtonNone when none
	holdX      int
	holdY      int
	w, h       int // the host size it last chose, so clicks land somewhere plausible
	weights    []int
	weightsSum int
	// minW and minH floor the host sizes this generator picks. With no floor a
	// run spends nearly its whole budget inside the one bug class that lives
	// below the layout's own minimum pane size and never reaches anything else,
	// so a campaign above the floor is how the rest of the space gets explored.
	minW, minH int
}

// Floor restricts the host sizes the generator produces. Zero means no floor,
// which is the campaign that hunts degenerate viewports.
func (g *Generator) Floor(w, h int) *Generator {
	g.minW, g.minH = w, h
	return g
}

// NewGenerator seeds a generator for the local loop. The same seed always
// yields the same run.
func NewGenerator(seed uint64) *Generator {
	return newGenerator(prngSource{rand.New(rand.NewPCG(seed, seed^0xda3e39cb94b95bdb))})
}

// NewByteGenerator decodes a `go test -fuzz` byte slice into the same alphabet.
func NewByteGenerator(b []byte) *Generator { return newGenerator(newByteSource(b)) }

func newGenerator(src source) *Generator {
	g := &Generator{src: src, w: 120, h: 40, weights: defaultWeights[:]}
	for _, v := range g.weights {
		g.weightsSum += v
	}
	return g
}

// defaultWeights biases the stream toward the states where bugs live. Uniform
// random spends its budget opening and closing overlays; the shapes that have
// actually broken here are layout transitions under a held mouse button, so
// mouse, resize, and the tiling and border toggles carry most of the weight.
// Panes are created far more often than closed so a run does not empty out.
var defaultWeights = [kindCount]int{
	Key: 90, Chord: 110, Text: 12,
	MousePress: 70, MouseMotion: 110, MouseRelease: 70, MouseWheel: 25,
	Resize: 55, NewPane: 45, ClosePane: 14, ZoomPane: 25,
	FocusPane: 30, MovePane: 20, SwitchWorkspace: 45, SwitchSession: 12,
	ToggleTiling: 40, ToggleShared: 40, LayoutMode: 22,
	ToggleSidebar: 26, SidebarCollapse: 26, SidebarPosition: 22,
	OpenOverlay: 30, CloseOverlay: 24, Rename: 20,
	Detach: 6, Attach: 6, Setting: 26, Tick: 60, Guest: 30,
}

func (g *Generator) u(n int) int {
	if n <= 0 {
		return 0
	}
	return int(g.src.next() % uint64(n))
}

func (g *Generator) pick(ss []string) string { return ss[g.u(len(ss))] }

// Next returns the next action. Bursts are emitted one at a time through buf so
// a caller can interleave a check after every single action.
func (g *Generator) Next() Action {
	if len(g.buf) > 0 {
		a := g.buf[0]
		g.buf = g.buf[1:]
		return a
	}
	// One run in sixteen is a canned awkward sequence rather than a lone
	// action. Every pattern below stands for a class that produced a real bug.
	if g.u(16) == 0 {
		g.buf = g.pattern()
		if len(g.buf) > 0 {
			a := g.buf[0]
			g.buf = g.buf[1:]
			return a
		}
	}
	return g.one()
}

// Generate produces a whole run up front, which is what the shrinker replays.
func Generate(seed uint64, n int) []Action { return GenerateFloor(seed, n, 0, 0) }

// GenerateFloor is Generate with a lower bound on the host sizes it picks.
func GenerateFloor(seed uint64, n, minW, minH int) []Action {
	g := NewGenerator(seed).Floor(minW, minH)
	out := make([]Action, 0, n)
	for range n {
		out = append(out, g.Next())
	}
	return out
}

// GenerateBytes is Generate for a coverage-guided input.
func GenerateBytes(b []byte, n int) []Action { return GenerateBytesFloor(b, n, 0, 0) }

// GenerateBytesFloor is GenerateBytes with a host-size floor.
func GenerateBytesFloor(b []byte, n, minW, minH int) []Action {
	g := NewByteGenerator(b).Floor(minW, minH)
	out := make([]Action, 0, n)
	for range n {
		out = append(out, g.Next())
	}
	return out
}

func (g *Generator) one() Action {
	k := g.weighted()
	a := Action{Kind: k}
	switch k {
	case Key:
		a.S = g.pick(plainKeys)
	case Chord:
		a.S = g.pick(chordKeys)
	case Text:
		a.S = g.pick(awkwardNames)
	case MousePress:
		a.A, a.B = g.cell()
		a.C = 1 + g.u(3)
		g.held, g.holdX, g.holdY = a.C, a.A, a.B
	case MouseMotion:
		a.A, a.B = g.cell()
		a.C = g.held
	case MouseRelease:
		a.A, a.B = g.cell()
		a.C = g.held
		if a.C == ButtonNone {
			a.C = ButtonLeft
		}
		g.held = ButtonNone
	case MouseWheel:
		a.A, a.B = g.cell()
		a.C = g.u(2)
	case Resize:
		a.A, a.B = g.size()
		g.w, g.h = a.A, a.B
	case ClosePane, FocusPane, SwitchSession:
		a.A = g.u(8)
	case MovePane:
		a.A = g.u(4)
	case SwitchWorkspace:
		a.A = 1 + g.u(9)
	case LayoutMode:
		a.A = g.u(3)
	case SidebarPosition:
		a.A = g.u(2)
	case OpenOverlay:
		a.A = g.u(overlayCount)
	case Rename:
		a.S = g.pick(awkwardNames)
	case Setting:
		a.A, a.B = g.u(settingCount), g.u(2)
	case Guest:
		a.S = g.pick(guestWrites)
	}
	return a
}

func (g *Generator) weighted() Kind {
	n := g.u(g.weightsSum)
	for k, w := range g.weights {
		if n < w {
			return Kind(k)
		}
		n -= w
	}
	return Tick
}

// cell picks a screen cell. It leans on the edges, because the off-by-one that
// makes a hit rect one column too wide only shows up on the first and last cell
// of a target, and it sometimes picks a cell outside the host entirely, which is
// where a drag that ends off-target goes.
func (g *Generator) cell() (int, int) {
	x, y := g.u(max(g.w, 1)), g.u(max(g.h, 1))
	switch g.u(6) {
	case 0:
		x = 0
	case 1:
		x = max(g.w-1, 0)
	case 2:
		y = 0
	case 3:
		y = max(g.h-1, 0)
	case 4:
		// Off the edge: a release here is the drag that ended outside.
		x, y = g.w+g.u(5), g.h+g.u(5)
	}
	return x, y
}

// size picks a host size. Small and degenerate sizes carry most of the weight:
// zero width, one column, and a viewport too short for the dock are the sizes
// that have divided by zero or produced a negative slice bound.
func (g *Generator) size() (int, int) {
	w, h := 20+g.u(200), 6+g.u(60)
	if g.u(3) == 0 {
		w, h = degenerateW[g.u(len(degenerateW))], degenerateH[g.u(len(degenerateH))]
	}
	return max(w, g.minW), max(h, g.minH)
}

var (
	degenerateW = []int{0, 1, 2, 3, 4, 8, 12, 20, 400}
	degenerateH = []int{0, 1, 2, 3, 4, 5, 6, 10, 200}
)

// pattern returns a canned sequence. Each of these classes has produced a real
// bug in this repo, and none of them is reachable at any useful rate from
// independent draws: they all depend on one action landing inside another's
// window.
func (g *Generator) pattern() []Action {
	x, y := g.cell()
	x2, y2 := g.cell()
	w, h := g.size()
	switch g.u(9) {
	case 0: // A drag that ends outside every target.
		g.held = ButtonNone
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: MouseRelease, A: g.w + 3 + g.u(9), B: g.h + 3 + g.u(9), C: ButtonLeft},
		}
	case 1: // A host resize landing between a press and its release.
		g.held, g.w, g.h = ButtonNone, w, h
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: Resize, A: w, B: h},
			{Kind: MouseRelease, A: x2, B: y2, C: ButtonLeft},
		}
	case 2: // A detach in the middle of a drag.
		g.held = ButtonNone
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: Detach},
			{Kind: Attach},
			{Kind: MouseRelease, A: x2, B: y2, C: ButtonLeft},
		}
	case 3: // A release with nothing held, which must not resurrect a gesture.
		g.held = ButtonNone
		return []Action{
			{Kind: MouseRelease, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonNone},
			{Kind: Tick},
		}
	case 4: // Rapid repeated toggles of the two settings a pane's border follows.
		n := 3 + g.u(6)
		out := make([]Action, 0, 2*n)
		for range n {
			out = append(out, Action{Kind: ToggleShared}, Action{Kind: ToggleTiling})
		}
		return out
	case 5: // A workspace switch while a pane is still animating into place.
		return []Action{
			{Kind: NewPane},
			{Kind: SwitchWorkspace, A: 1 + g.u(9)},
			{Kind: SwitchWorkspace, A: 1 + g.u(9)},
			{Kind: ZoomPane},
			{Kind: SwitchWorkspace, A: 1 + g.u(9)},
		}
	case 6: // A rename carrying text a filesystem or a width table dislikes.
		return []Action{
			{Kind: Rename, S: g.pick(awkwardNames)},
			{Kind: Tick},
			{Kind: Rename, S: g.pick(awkwardNames)},
		}
	case 7: // Shrink to a degenerate viewport and back with panes present.
		return []Action{
			{Kind: NewPane},
			{Kind: Resize, A: max(degenerateW[g.u(len(degenerateW))], g.minW), B: max(degenerateH[g.u(len(degenerateH))], g.minH)},
			{Kind: Tick},
			{Kind: Resize, A: 120, B: 40},
		}
	default: // The sidebar moving under a gesture that started over a pane.
		g.held = ButtonNone
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: SidebarPosition, A: g.u(2)},
			{Kind: SidebarCollapse},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: MouseRelease, A: x2, B: y2, C: ButtonLeft},
		}
	}
}
