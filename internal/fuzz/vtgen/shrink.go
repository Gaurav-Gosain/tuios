package vtgen

import "strings"

// Shrinking is what decides whether this generator is worth having. A failing
// script is a few hundred sequences of noise around the two that matter, and
// nobody reads that. The passes below run to a fixpoint: drop a block, drop a
// single step, then simplify what is left in place, stopping when a whole round
// changes nothing.
//
// still is the oracle. It replays a candidate from a clean emulator and says
// whether the same thing still goes wrong, so every reduction is kept only when
// it reproduces and the result is guaranteed to.

// Shrink returns the smallest script it could reach that still fails.
func Shrink(s Script, still func(Script) bool) Script {
	best := s
	for {
		start := len(best)
		best = dropBlocks(best, still)
		best = dropSingles(best, still)
		best = simplify(best, still)
		if len(best) >= start {
			break
		}
	}
	return simplify(best, still)
}

// dropBlocks is the delta-debugging half: remove contiguous runs, coarse first.
// A failure that needs a setup step and a trigger collapses fast this way,
// where removing one at a time stalls on the setup.
func dropBlocks(s Script, still func(Script) bool) Script {
	for n := len(s) / 2; n >= 1; n /= 2 {
		for i := 0; i+n <= len(s); {
			cand := without(s, i, i+n)
			if len(cand) > 0 && still(cand) {
				s = cand
				continue
			}
			i += n
		}
		if n == 1 {
			break
		}
	}
	return s
}

// dropSingles sweeps back to front so an accepted removal never invalidates an
// index still to be visited.
func dropSingles(s Script, still func(Script) bool) Script {
	for i := len(s) - 1; i >= 0; i-- {
		if i >= len(s) {
			continue
		}
		cand := without(s, i, i+1)
		if len(cand) > 0 && still(cand) {
			s = cand
		}
	}
	return s
}

// simplify makes the steps that remain plainer without making the script
// shorter. A repeated text run becomes one copy, an oversized payload becomes a
// small one, and a sequence carrying four parameters loses the ones the failure
// does not need. What this buys is a report that says which parameter mattered
// instead of leaving the reader to work it out.
func simplify(s Script, still func(Script) bool) Script {
	for i := range s {
		for _, cand := range simpler(s[i]) {
			trial := clone(s)
			trial[i] = cand
			if still(trial) {
				s = trial
				break
			}
		}
	}
	return s
}

// simpler offers plainer versions of one step, most aggressive first.
func simpler(seq Seq) []Seq {
	var out []Seq
	add := func(bytes, desc string) {
		if bytes != seq.Bytes {
			out = append(out, Seq{Kind: seq.Kind, Bytes: bytes, Desc: desc, Cols: seq.Cols, Rows: seq.Rows})
		}
	}

	// A long run of the same payload almost never needs to be long.
	if n := len(seq.Bytes); n > 64 {
		add(seq.Bytes[:n/2], seq.Desc+" (halved)")
		add(seq.Bytes[:16], seq.Desc+" (shortened)")
	}

	// A CSI with several parameters usually turns on one of them.
	if strings.HasPrefix(seq.Bytes, "\x1b[") && strings.Contains(seq.Bytes, ";") {
		body := seq.Bytes[2:]
		if last := len(body) - 1; last > 0 {
			final := body[last:]
			params := body[:last]
			parts := strings.Split(params, ";")
			for drop := len(parts) - 1; drop >= 0; drop-- {
				kept := append(append([]string{}, parts[:drop]...), parts[drop+1:]...)
				add("\x1b["+strings.Join(kept, ";")+final, seq.Desc+" (a parameter dropped)")
			}
			add("\x1b["+final, seq.Desc+" (no parameters)")
		}
	}
	return out
}

func without(s Script, i, j int) Script {
	out := make(Script, 0, len(s)-(j-i))
	out = append(out, s[:i]...)
	out = append(out, s[j:]...)
	return out
}

func clone(s Script) Script {
	out := make(Script, len(s))
	copy(out, s)
	return out
}
