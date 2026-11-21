package fuzz

import (
	"math/rand/v2"

	"tuios.dev/vt/vtgen"
)

type source interface{ next() uint64 }

type prngSource struct{ r *rand.Rand }

func (p prngSource) next() uint64 { return p.r.Uint64() }

type byteSource struct {
	b    []byte
	i    int
	tail *rand.Rand
}

func newByteSource(b []byte) *byteSource {
	s := fnv64(b)
	return &byteSource{b: b, tail: rand.New(rand.NewPCG(s, s^0x9e3779b97f4a7c15))}
}

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
	held       int
	holdX      int
	holdY      int
	w, h       int
	weights    []int
	weightsSum int
	vt         *vtgen.Gen
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

var defaultWeights = [kindCount]int{
	Key: 90, Chord: 110, Text: 12,
	MousePress: 70, MouseMotion: 110, MouseRelease: 70, MouseWheel: 25,
	Resize: 55, NewPane: 45, ClosePane: 14, ZoomPane: 25,
	FocusPane: 30, MovePane: 20, SwitchWorkspace: 45, SwitchSession: 12,
	ToggleTiling: 40, ToggleShared: 40, LayoutMode: 22,
	ToggleSidebar: 26, SidebarCollapse: 26, SidebarPosition: 22,
	OpenOverlay: 30, CloseOverlay: 24, Rename: 20,
	Detach: 6, Attach: 6, Setting: 26, Tick: 60, Guest: 30,
	AltScreen: 16, Burst: 14, SecondClient: 0, DaemonRestart: 0,
}

// Bias replaces the action weights, indexed by Kind. A short slice leaves the
// kinds past its end at their default, so a caller names only what it wants to
// move. Nil keeps the defaults, which is what makes it safe to wire straight
// through from a Config field that most callers never set.
func (g *Generator) Bias(w []int) *Generator {
	if len(w) == 0 {
		return g
	}
	merged := defaultWeights
	copy(merged[:], w[:min(len(w), len(merged))])
	g.weights, g.weightsSum = merged[:], 0
	for _, v := range g.weights {
		g.weightsSum += v
	}
	return g
}

// Take draws n actions.
func (g *Generator) Take(n int) []Action {
	out := make([]Action, 0, n)
	for range n {
		out = append(out, g.Next())
	}
	return out
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
	return NewGenerator(seed).Floor(minW, minH).Take(n)
}

// GenerateBytes is Generate for a coverage-guided input.
func GenerateBytes(b []byte, n int) []Action { return GenerateBytesFloor(b, n, 0, 0) }

// GenerateBytesFloor is GenerateBytes with a host-size floor.
func GenerateBytesFloor(b []byte, n, minW, minH int) []Action {
	return NewByteGenerator(b).Floor(minW, minH).Take(n)
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
		a.A, a.B = g.u(settingCount), g.u(settingValues)
	case Guest:
		a.S = g.guest()
	case AltScreen:
		a.A, a.B = g.u(2), g.u(8)
	case Burst:
		a.A, a.B = burstLines[g.u(len(burstLines))], g.u(8)
	}
	return a
}

func (g *Generator) guest() string {
	if g.u(3) == 0 {
		if g.vt == nil {
			g.vt = vtgen.New(g.src.next())
		}
		return g.vt.Next().Bytes
	}
	return g.pick(guestWrites)
}

var burstLines = []int{1, 8, 60, 300, 1200, 5000}

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
		x, y = g.w+g.u(5), g.h+g.u(5)
	}
	return x, y
}

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

func (g *Generator) pattern() []Action {
	x, y := g.cell()
	x2, y2 := g.cell()
	w, h := g.size()
	switch g.u(14) {
	case 0:
		g.held = ButtonNone
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: MouseRelease, A: g.w + 3 + g.u(9), B: g.h + 3 + g.u(9), C: ButtonLeft},
		}
	case 1:
		g.held, g.w, g.h = ButtonNone, w, h
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: Resize, A: w, B: h},
			{Kind: MouseRelease, A: x2, B: y2, C: ButtonLeft},
		}
	case 2:
		g.held = ButtonNone
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: Detach},
			{Kind: Attach},
			{Kind: MouseRelease, A: x2, B: y2, C: ButtonLeft},
		}
	case 3:
		g.held = ButtonNone
		return []Action{
			{Kind: MouseRelease, A: x, B: y, C: ButtonLeft},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonNone},
			{Kind: Tick},
		}
	case 4:
		n := 3 + g.u(6)
		out := make([]Action, 0, 2*n)
		for range n {
			out = append(out, Action{Kind: ToggleShared}, Action{Kind: ToggleTiling})
		}
		return out
	case 5:
		return []Action{
			{Kind: NewPane},
			{Kind: SwitchWorkspace, A: 1 + g.u(9)},
			{Kind: SwitchWorkspace, A: 1 + g.u(9)},
			{Kind: ZoomPane},
			{Kind: SwitchWorkspace, A: 1 + g.u(9)},
		}
	case 6:
		return []Action{
			{Kind: Rename, S: g.pick(awkwardNames)},
			{Kind: Tick},
			{Kind: Rename, S: g.pick(awkwardNames)},
		}
	case 7:
		return []Action{
			{Kind: NewPane},
			{Kind: Resize, A: max(degenerateW[g.u(len(degenerateW))], g.minW), B: max(degenerateH[g.u(len(degenerateH))], g.minH)},
			{Kind: Tick},
			{Kind: Resize, A: 120, B: 40},
		}
	case 8:
		here, away := 1+g.u(9), 1+g.u(9)
		return []Action{
			{Kind: NewPane},
			{Kind: SwitchWorkspace, A: here},
			{Kind: Burst, A: burstLines[g.u(len(burstLines))], B: g.u(8)},
			{Kind: SwitchWorkspace, A: away},
			{Kind: Burst, A: burstLines[g.u(len(burstLines))], B: g.u(8)},
			{Kind: SwitchWorkspace, A: here},
			{Kind: Tick},
		}
	case 9:
		return []Action{
			{Kind: Guest, S: "\xe4\xb8\x96\xe4\xb8\x96\xe4\xb8\x96\xe4\xb8\x96\xe4\xb8\x96\r\n"},
			{Kind: Resize, A: max(1+2*g.u(20), g.minW), B: max(h, g.minH)},
			{Kind: Tick},
			{Kind: Resize, A: max(w, g.minW), B: max(h, g.minH)},
		}
	case 10:
		return []Action{
			{Kind: Burst, A: 60 + g.u(400), B: g.u(8)},
			{Kind: SwitchSession, A: g.u(8)},
			{Kind: Tick},
			{Kind: SwitchSession, A: g.u(8)},
			{Kind: Tick},
		}
	case 11:
		return []Action{
			{Kind: Detach},
			{Kind: Burst, A: burstLines[g.u(len(burstLines))], B: g.u(8)},
			{Kind: Attach},
			{Kind: Tick},
		}
	case 12:
		g.held = ButtonNone
		return []Action{
			{Kind: MousePress, A: x, B: y, C: ButtonLeft},
			{Kind: SecondClient},
			{Kind: MouseMotion, A: x2, B: y2, C: ButtonLeft},
			{Kind: MouseRelease, A: x2, B: y2, C: ButtonLeft},
			{Kind: Tick},
		}
	case 13:
		return []Action{
			{Kind: NewPane},
			{Kind: Burst, A: 60 + g.u(400), B: g.u(8)},
			{Kind: AltScreen, A: 1, B: g.u(8)},
			{Kind: DaemonRestart},
			{Kind: Tick},
		}
	default:
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
