package fuzz

// Shrinking is what decides whether this fuzzer's output is usable. A raw
// failing run is 2000 actions of noise around the three that matter, and nobody
// reads that. The passes below run to a fixpoint: delete a block, delete a
// single action, then simplify what is left in place, and stop when a whole
// round changes nothing.
//
// still is the oracle: it replays a candidate from a clean target and reports
// whether the same rule still breaks. Every pass is a guess that is kept only
// when still agrees, so the result is guaranteed to reproduce.

// shrink returns the smallest sequence it could reach that still fails.
func shrink(as []Action, still func([]Action) bool, obs Observer) []Action {
	best := as
	for {
		start := len(best)
		best = dropBlocks(best, still, obs)
		best = dropSingles(best, still, obs)
		best = simplify(best, still, obs)
		if len(best) >= start {
			// A round that removed nothing has reached the fixpoint. simplify
			// can change actions without shortening, so the loop ends on
			// length rather than on equality of the slices.
			break
		}
	}
	return simplify(best, still, obs)
}

// dropBlocks is the delta-debugging half: try removing contiguous runs, coarse
// first. Bugs that need a setup prefix plus a trigger shrink fast this way,
// where one-at-a-time removal stalls.
func dropBlocks(as []Action, still func([]Action) bool, obs Observer) []Action {
	for n := len(as) / 2; n >= 1; n /= 2 {
		for i := 0; i+n <= len(as); {
			cand := without(as, i, i+n)
			if len(cand) > 0 && still(cand) {
				obs.Shrink("block", len(cand), true)
				as = cand
				continue
			}
			obs.Shrink("block", len(cand), false)
			i += n
		}
		if n == 1 {
			break
		}
	}
	return as
}

// dropSingles sweeps back to front so an accepted removal never invalidates the
// indexes still to be visited.
func dropSingles(as []Action, still func([]Action) bool, obs Observer) []Action {
	for i := len(as) - 1; i >= 0; i-- {
		if i >= len(as) {
			continue
		}
		cand := without(as, i, i+1)
		if len(cand) > 0 && still(cand) {
			obs.Shrink("single", len(cand), true)
			as = cand
		}
	}
	return as
}

// simplify reduces the actions that survive. A repro reading `resize 0 0` is a
// statement about degenerate sizes; the same repro reading `resize 137 29` says
// nothing, even though both reproduce. Each candidate replacement is tried in
// order from most to least aggressive and the first one that still fails wins.
func simplify(as []Action, still func([]Action) bool, obs Observer) []Action {
	for i := range as {
		for _, cand := range simpler(as[i]) {
			next := make([]Action, len(as))
			copy(next, as)
			next[i] = cand
			if still(next) {
				obs.Shrink("simplify", len(next), true)
				as = next
				break
			}
		}
	}
	return as
}

// simpler lists the replacements for one action, most aggressive first. A Tick
// is the floor: it stands for "this step did not need to be anything".
func simpler(a Action) []Action {
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
		if c := f(a); c != a {
			out = append(out, c)
		}
	}
	return out
}

func without(as []Action, lo, hi int) []Action {
	out := make([]Action, 0, len(as)-(hi-lo))
	out = append(out, as[:lo]...)
	out = append(out, as[hi:]...)
	return out
}
