package fuzz

import (
	"hash/fnv"
	"testing"
)

// The observer's whole claim is that attaching one does not change what the
// fuzzer explores. Nothing enforces that except these tests: an observer that
// perturbed the run would make every recorded demo a recording of a different
// fuzzer than the one CI runs, and a finding shown on screen would not be a
// finding the seed reproduces.

// traceTarget hashes the actions it is applied, in order, so two runs can be
// compared by one number. It fails at a fixed depth so the shrinker runs too:
// minimisation is the part with the most engine state, and so the part most
// likely to notice an observer.
type traceTarget struct {
	h     uint64
	depth int
	limit int
}

func newTraceTarget(limit int) *traceTarget { return &traceTarget{limit: limit} }

func (t *traceTarget) Reset() error { t.h, t.depth = fnv.New64a().Sum64(), 0; return nil }

func (t *traceTarget) Apply(a Action) error {
	f := fnv.New64a()
	_, _ = f.Write([]byte(a.String()))
	// Order matters, so fold the running value in rather than summing.
	t.h = t.h*1099511628211 ^ f.Sum64()
	t.depth++
	return nil
}

func (t *traceTarget) Check() []Violation {
	if t.limit > 0 && t.depth >= t.limit {
		return []Violation{{Rule: "too-deep", Detail: "ran past the limit"}}
	}
	return nil
}

func (t *traceTarget) Close() {}

func (t *traceTarget) Rules() []RuleInfo {
	return []RuleInfo{
		{Name: "cheap", Family: "a", Doc: "runs first and never breaks"},
		{Name: "too-deep", Family: "a", Doc: "the rigged failure"},
		{Name: "never", Family: "b", Doc: "sits after the failure and so never runs"},
	}
}

// recorder is the shape a display's observer has: fixed state written per
// event, nothing handed back to the engine.
type recorder struct {
	starts, steps, rules, shrinks, dones int
	lastSeed                             uint64
	ruleFails                            map[string]int
	result                               Result
}

func newRecorder() *recorder { return &recorder{ruleFails: map[string]int{}} }

func (r *recorder) Start(seed uint64, _ int) { r.starts++; r.lastSeed = seed }
func (r *recorder) Step(int, Action, []Violation) {
	r.steps++
}

func (r *recorder) Rule(_ int, rule string, ok bool) {
	r.rules++
	if !ok {
		r.ruleFails[rule]++
	}
}
func (r *recorder) Shrink(string, int, bool) { r.shrinks++ }
func (r *recorder) Done(res Result)          { r.dones++; r.result = res }

// runTrace runs one seed and returns the hash of the minimal sequence plus the
// verdict, which together are everything the run decided.
func runTrace(t *testing.T, seed uint64, obs Observer) (uint64, Result) {
	t.Helper()
	res, err := Run(func() (Target, error) { return newTraceTarget(120), nil },
		Config{Seed: seed, Steps: 400, Observer: obs})
	if err != nil {
		t.Fatalf("seed %d: %v", seed, err)
	}
	h := uint64(14695981039346656037)
	for _, a := range res.Actions {
		f := fnv.New64a()
		_, _ = f.Write([]byte(a.String()))
		h = h*1099511628211 ^ f.Sum64()
	}
	return h, res
}

// The gate. Same seed, observer off versus on, identical trace.
func TestObserverDoesNotChangeTheRun(t *testing.T) {
	for _, seed := range []uint64{1, 7, 4242} {
		offHash, off := runTrace(t, seed, nil)
		rec := newRecorder()
		onHash, on := runTrace(t, seed, rec)

		if offHash != onHash {
			t.Errorf("seed %d: the minimal sequence hashes %x with no observer and %x with one",
				seed, offHash, onHash)
		}
		if off.Failed != on.Failed || off.Step != on.Step {
			t.Errorf("seed %d: verdict moved from (failed=%v step=%d) to (failed=%v step=%d)",
				seed, off.Failed, off.Step, on.Failed, on.Step)
		}
		if off.Executed != on.Executed || off.Replays != on.Replays {
			t.Errorf("seed %d: the engine did different work: %d executed / %d replays became %d / %d",
				seed, off.Executed, off.Replays, on.Executed, on.Replays)
		}
		if len(off.Actions) != len(on.Actions) {
			t.Errorf("seed %d: shrank to %d actions without an observer and %d with one",
				seed, len(off.Actions), len(on.Actions))
		}
		if rec.starts != 1 || rec.dones != 1 {
			t.Errorf("seed %d: got %d Start and %d Done, want one of each", seed, rec.starts, rec.dones)
		}
		if on.Failed && rec.shrinks == 0 {
			t.Errorf("seed %d: the run shrank and the observer saw no candidates", seed)
		}
	}
}

// Per-rule reporting has to name the rule a Violation actually carries, and
// stop at it. This is the seam a display's invariant matrix is drawn from: a
// name mismatch shows a broken rule as passing, and reporting past the break
// shows rules as having run when Check had already returned.
func TestRuleReportingStopsAtTheBreak(t *testing.T) {
	rec := newRecorder()
	res, err := Run(func() (Target, error) { return newTraceTarget(20), nil },
		Config{Seed: 3, Steps: 100, Observer: rec})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed {
		t.Fatal("the target was rigged to fail and did not")
	}
	if rec.ruleFails["too-deep"] == 0 {
		t.Error("the rule that broke was never reported as broken")
	}
	if n := rec.ruleFails["cheap"]; n != 0 {
		t.Errorf("the rule before the break was reported broken %d times", n)
	}
	// "never" sits after the break in registry order, so it is reported for
	// every clean step and for none of the failing one. Reporting it on the
	// failing step would claim a rule ran that Check never reached.
	if rec.ruleFails["never"] != 0 {
		t.Error("a rule after the break was reported as broken")
	}
	// Two rules run clean per clean step, then the failing step reports the
	// first two only. The exact figure is what a "checks" counter shows, so it
	// has to be the count of checks that happened.
	steps := rec.steps
	if want := 3*(steps-1) + 2; rec.rules != want {
		t.Errorf("reported %d rule results over %d steps, want %d", rec.rules, steps, want)
	}
}

// A target that cannot name its rules still gets everything else, so a display
// attached to one shows actions and failures and simply has no matrix to draw.
func TestObserverWorksWithoutARuleLister(t *testing.T) {
	rec := newRecorder()
	if _, err := Run(func() (Target, error) { return &needleTarget{}, nil },
		Config{Seed: 5, Steps: 50, Observer: rec}); err != nil {
		t.Fatal(err)
	}
	if rec.steps != 50 {
		t.Errorf("saw %d steps, want 50", rec.steps)
	}
	if rec.rules != 0 {
		t.Errorf("saw %d rule results from a target with no registry", rec.rules)
	}
}

// The off switch has to be free, not cheap. A nil observer must build no event
// values at all, or every run in CI pays for a display nobody attached.
func TestNilObserverAllocatesNothing(t *testing.T) {
	if raceEnabled {
		// The detector's own allocations swamp the difference this measures:
		// the two figures land within a percent of each other and their order
		// flips run to run. Measured 3 of 8 runs failing, on this commit and on
		// one from before the observer existed, so it is the instrument and not
		// the code. The plain build still asserts it on every push.
		t.Skip("allocation counts are not measurable under the race detector")
	}
	actions := Generate(11, 200)
	run := func() {
		_, _ = Run(func() (Target, error) { return newTraceTarget(0), nil },
			Config{Actions: actions, NoShrink: true})
	}
	// Two targets and their slices are the floor; what matters is that the
	// figure does not move when the observer calls are the only difference.
	base := testing.AllocsPerRun(3, run)

	rec := newRecorder()
	withObs := testing.AllocsPerRun(3, func() {
		_, _ = Run(func() (Target, error) { return newTraceTarget(0), nil },
			Config{Actions: actions, NoShrink: true, Observer: rec})
	})
	t.Logf("%.0f allocations without an observer, %.0f with one, over %d actions",
		base, withObs, len(actions))
	if withObs < base {
		t.Fatalf("attaching an observer cannot reduce allocations: %.0f then %.0f", base, withObs)
	}
	// The registry fetch is one slice per replay. Anything per-action would put
	// this in the hundreds.
	if extra := withObs - base; extra > 8 {
		t.Errorf("an attached observer cost %.0f extra allocations over %d actions, which is per-action work",
			extra, len(actions))
	}
}
