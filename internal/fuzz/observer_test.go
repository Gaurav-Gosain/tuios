package fuzz

import (
	"hash/fnv"
	"testing"
)

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

func skipUnderRace(t *testing.T) {
	t.Helper()
	if raceEnabled {
		t.Skip("allocation counts are not measurable under the race detector")
	}
}

var allocsActions = Generate(11, 200)

func allocsRun(obs Observer) float64 {
	return testing.AllocsPerRun(3, func() {
		_, _ = Run(func() (Target, error) { return newTraceTarget(0), nil },
			Config{Actions: allocsActions, NoShrink: true, Observer: obs})
	})
}

// The off switch has to be free, not cheap. With no observer attached, Run
// must allocate what the target allocates plus a fixed setup cost, or every
// run in CI pays per action for a display nobody attached.
//
// The floor is the target driven by hand, so the figure is the engine's own
// overhead and nothing else. A nop observer standing in for nil would not
// show here as allocations, since its calls build no values, but it would
// still fetch the rule registry per replay and dispatch once per rule per
// action. Run keeps nil as nil so that neither happens.
func TestNilObserverAllocatesNothing(t *testing.T) {
	skipUnderRace(t)
	bare := testing.AllocsPerRun(3, func() {
		tg := newTraceTarget(0)
		_ = tg.Reset()
		for _, a := range allocsActions {
			_ = tg.Apply(a)
			_ = tg.Check()
		}
	})
	base := allocsRun(nil)
	t.Logf("%.0f allocations driving the target by hand, %.0f through Run, over %d actions",
		bare, base, len(allocsActions))
	if base < bare {
		t.Fatalf("Run cannot allocate less than the target it drives: %.0f then %.0f", bare, base)
	}
	if extra := base - bare; extra > 8 {
		t.Errorf("Run with no observer cost %.0f allocations over the bare target across %d actions, which is per-action work",
			extra, len(allocsActions))
	}
}

// Attaching an observer costs one registry fetch per replay and nothing per
// action. The events are handed over as arguments, never built as values, so
// a display's cost is the display's own and the engine adds none.
func TestAttachedObserverCostsNothingPerAction(t *testing.T) {
	skipUnderRace(t)
	base := allocsRun(nil)
	withObs := allocsRun(newRecorder())
	t.Logf("%.0f allocations without an observer, %.0f with one, over %d actions",
		base, withObs, len(allocsActions))
	if withObs < base {
		t.Fatalf("attaching an observer cannot reduce allocations: %.0f then %.0f", base, withObs)
	}
	if extra := withObs - base; extra > 8 {
		t.Errorf("an attached observer cost %.0f extra allocations over %d actions, which is per-action work",
			extra, len(allocsActions))
	}
}
