package vtgen

import (
	"slices"
	"strconv"
	"strings"
)

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
//
// simpler makes the steps that remain plainer without making the script
// shorter. A repeated text run becomes one copy, an oversized payload becomes a
// small one, and a sequence carrying four parameters loses the ones the failure
// does not need. What this buys is a report that says which parameter mattered
// instead of leaving the reader to work it out.
//
// Every simpler form of a step is strictly shorter than the step, so Shrink
// runs Reduce until a whole run changes neither the number of steps nor the
// number of bytes. One run of Reduce accepts one simpler form per step per
// round and stops on a round that removed no step, which left a 4096-byte
// payload at 1024 bytes and "\x1b[1;2;3r" at "\x1b[1r".
func Shrink(s Script, still func(Script) bool) Script {
	for {
		steps, n := len(s), totalBytes(s)
		s = Reduce(s, still, simpler, nil)
		if len(s) == steps && totalBytes(s) == n {
			return s
		}
	}
}

func totalBytes(s Script) int {
	n := 0
	for _, seq := range s {
		n += len(seq.Bytes)
	}
	return n
}

// Reduce is the shrinking driver shared by every fuzzer in the tree. It runs
// dropBlocks, dropSingles and simplify to a fixpoint, then simplifies once
// more. simpler lists the replacements for one element, most aggressive first.
//
// note, when not nil, hears about each candidate: the block pass reports both
// accepted and rejected candidates, the single and simplify passes report
// accepted ones only. pass is "block", "single" or "simplify" and n is the
// candidate's length.
func Reduce[S ~[]E, E any](s S, still func(S) bool, simpler func(E) []E, note func(pass string, n int, ok bool)) S {
	if note == nil {
		note = func(string, int, bool) {}
	}
	best := s
	for {
		start := len(best)
		best = dropBlocks(best, still, note)
		best = dropSingles(best, still, note)
		best = simplify(best, still, simpler, note)
		if len(best) >= start {
			// A round that removed nothing has reached the fixpoint. simplify
			// can change elements without shortening, so the loop ends on
			// length rather than on equality of the slices.
			break
		}
	}
	return simplify(best, still, simpler, note)
}

// dropBlocks is the delta-debugging half: remove contiguous runs, coarse first.
// A failure that needs a setup step and a trigger collapses fast this way,
// where removing one at a time stalls on the setup.
func dropBlocks[S ~[]E, E any](s S, still func(S) bool, note func(string, int, bool)) S {
	for n := len(s) / 2; n >= 1; n /= 2 {
		for i := 0; i+n <= len(s); {
			cand := without(s, i, i+n)
			if len(cand) > 0 && still(cand) {
				note("block", len(cand), true)
				s = cand
				continue
			}
			note("block", len(cand), false)
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
func dropSingles[S ~[]E, E any](s S, still func(S) bool, note func(string, int, bool)) S {
	for i := len(s) - 1; i >= 0; i-- {
		if i >= len(s) {
			continue
		}
		cand := without(s, i, i+1)
		if len(cand) > 0 && still(cand) {
			note("single", len(cand), true)
			s = cand
		}
	}
	return s
}

// simplify replaces each element in place with the first of its simpler
// versions that still fails.
func simplify[S ~[]E, E any](s S, still func(S) bool, simpler func(E) []E, note func(string, int, bool)) S {
	for i := range s {
		for _, cand := range simpler(s[i]) {
			trial := make(S, len(s))
			copy(trial, s)
			trial[i] = cand
			if still(trial) {
				note("simplify", len(trial), true)
				s = trial
				break
			}
		}
	}
	return s
}

// simpler offers plainer versions of one step, most aggressive first, so the
// first one that still fails is also the smallest the step can become in one
// move. Every candidate is strictly shorter than the step, which is what makes
// Shrink terminate.
func simpler(seq Seq) []Seq {
	// The description was written for the bytes the generator drew, and a
	// simplified step no longer carries them: "SU scroll up (params 63;24;3)"
	// beside the bytes of a bare SU contradicts itself. So a simplified step
	// keeps the original description, marked as describing what it was
	// reduced from, once, however many times it is simplified after that.
	desc := seq.Desc
	if !strings.Contains(desc, reducedMarker) {
		from := quote(seq.Bytes)
		if len(from) > 32 {
			from = strconv.Itoa(len(seq.Bytes)) + " bytes"
		}
		desc += reducedMarker + from + ")"
	}

	var out []Seq
	add := func(bytes string) {
		if len(bytes) < len(seq.Bytes) {
			out = append(out, Seq{Kind: seq.Kind, Bytes: bytes, Desc: desc, Cols: seq.Cols, Rows: seq.Rows})
		}
	}

	// A long run of the same payload almost never needs to be long.
	if n := len(seq.Bytes); n > 64 {
		add(seq.Bytes[:16])
		add(seq.Bytes[:n/2])
	}

	// A CSI with parameters usually turns on one of them, or on none.
	if strings.HasPrefix(seq.Bytes, "\x1b[") && len(seq.Bytes) > 3 {
		body := seq.Bytes[2:]
		last := len(body) - 1
		final := body[last:]
		add("\x1b[" + final)
		if params := body[:last]; strings.Contains(params, ";") {
			parts := strings.Split(params, ";")
			for drop := range slices.Backward(parts) {
				kept := append(append([]string{}, parts[:drop]...), parts[drop+1:]...)
				add("\x1b[" + strings.Join(kept, ";") + final)
			}
		}
	}
	return out
}

// reducedMarker introduces the note a simplified step's description carries.
const reducedMarker = " (reduced from "

func without[S ~[]E, E any](s S, i, j int) S {
	out := make(S, 0, len(s)-(j-i))
	out = append(out, s[:i]...)
	out = append(out, s[j:]...)
	return out
}
