package fuzz

import "github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"

// Shrinking is what decides whether this fuzzer's output is usable. A raw
// failing run is 2000 actions of noise around the three that matter, and nobody
// reads that. vtgen.Reduce runs the passes to a fixpoint: delete a block,
// delete a single action, then simplify what is left in place, and stop when a
// whole round changes nothing.
//
// still is the oracle: it replays a candidate from a clean target and reports
// whether the same rule still breaks. Every pass is a guess that is kept only
// when still agrees, so the result is guaranteed to reproduce.

// shrink returns the smallest sequence it could reach that still fails.
//
// The simplify pass reduces the actions that survive. A repro reading
// `resize 0 0` is a statement about degenerate sizes; the same repro reading
// `resize 137 29` says nothing, even though both reproduce. Each candidate
// replacement is tried in order from most to least aggressive and the first
// one that still fails wins.
func shrink(as []Action, still func([]Action) bool, obs Observer, minW, minH int) []Action {
	return vtgen.Reduce(as, still, func(a Action) []Action { return simpler(a, minW, minH) }, obs.Shrink)
}

// simpler lists the replacements for one action, most aggressive first. A Tick
// is the floor: it stands for "this step did not need to be anything".
func simpler(a Action, minW, minH int) []Action {
	var out []Action
	if a.Kind != Tick {
		out = append(out, Action{Kind: Tick})
	}
	// Collapse the string to the plainest member of its pool, so a name only
	// stays exotic when the exotic part is load bearing.
	if a.S != "" {
		switch a.Kind {
		case Rename, Text:
			out = append(out, Action{Kind: a.Kind, A: a.A, B: a.B, C: a.C, S: "a"})
		case Guest:
			out = append(out, Action{Kind: a.Kind, A: a.A, B: a.B, C: a.C, S: "x"})
		}
	}
	// Walk each coordinate toward zero. Halving rather than decrementing keeps
	// the pass logarithmic in the coordinate.
	for _, f := range []func(Action) Action{
		func(x Action) Action { x.A, x.B = 0, 0; return x },
		func(x Action) Action { x.A /= 2; return x },
		func(x Action) Action { x.B /= 2; return x },
		func(x Action) Action { x.A, x.B = x.A/2, x.B/2; return x },
	} {
		c := f(a)
		// A resize must stay inside the campaign's declared floor. Without this
		// the shrinker walks a finding out of the region the run was exploring
		// and back into whatever bug class lives below it, and every report
		// reads as that one class no matter what was actually found.
		if c.Kind == Resize {
			c.A, c.B = max(c.A, minW), max(c.B, minH)
		}
		if c != a {
			out = append(out, c)
		}
	}
	return out
}
