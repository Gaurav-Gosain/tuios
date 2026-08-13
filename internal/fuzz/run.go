package fuzz

import (
	"fmt"
	"strings"
)

// Violation is one broken invariant. Rule names the property, and it is what
// the shrinker holds fixed: a sequence that shrinks into a different failure is
// a different finding, and reporting it under the first one's name is how a
// fuzzer sends a maintainer after the wrong bug.
type Violation struct {
	Rule   string
	Detail string
}

func (v Violation) String() string { return v.Rule + ": " + v.Detail }

// Target is the system under test. The driver owns sequencing, seeding, and
// minimisation; a Target owns nothing but "put me back at the start", "do this
// one thing", and "which invariants are broken right now".
//
// Check runs after every action, so it must be cheap enough to run thousands of
// times. Reset must return the target to a state that depends only on the
// actions replayed since, or a shrunk repro will not reproduce.
type Target interface {
	Reset() error
	Apply(Action) error
	Check() []Violation
	Close()
}

// Observer watches a run. It exists so a display can attach without the driver
// knowing anything about presentation; every method may be called from the
// driver's goroutine and must not block for long.
type Observer interface {
	Start(seed uint64, steps int)
	Step(i int, a Action, vs []Violation)
	Shrink(pass string, size int, accepted bool)
	Done(r Result)
}

// NopObserver is the default. Embed it to implement only the methods a display
// cares about.
type NopObserver struct{}

func (NopObserver) Start(uint64, int)             {}
func (NopObserver) Step(int, Action, []Violation) {}
func (NopObserver) Shrink(string, int, bool)      {}
func (NopObserver) Done(Result)                   {}

// Config is one fuzzing run.
type Config struct {
	// Seed identifies the run. It is printed on failure and re-running it
	// reproduces the finding exactly.
	Seed uint64
	// Steps is how many actions to generate when Actions is empty.
	Steps int
	// Actions overrides generation, which is how the coverage-guided entry
	// point and a saved repro both drive the same loop.
	Actions []Action
	// NoShrink reports the raw failing sequence. Only useful when a target's
	// Reset is too slow to replay hundreds of times.
	NoShrink bool
	// ShrinkBudget caps predicate replays. Zero picks a default scaled to the
	// sequence length.
	ShrinkBudget int
	Observer     Observer
}

// Result is what a run found.
type Result struct {
	Seed       uint64
	Failed     bool
	Step       int // the index of the action that broke it, in the minimal sequence
	Violations []Violation
	// Actions is the minimal sequence that still breaks the same rule, or the
	// whole run when it passed.
	Actions  []Action
	Executed int
	Replays  int
}

// Repro is the pasteable reproduction: the seed to re-run, and the minimal
// action script that stands alone even if generation ever changes.
func (r Result) Repro() string {
	if !r.Failed {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "seed %d, %d actions, broke at step %d\n", r.Seed, len(r.Actions), r.Step)
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "  %s\n", v)
	}
	b.WriteString("--- script ---\n")
	b.WriteString(Script(r.Actions))
	b.WriteString("--- end ---\n")
	return b.String()
}

// Run generates a sequence, replays it against a fresh target checking after
// every action, and on failure minimises it against the same rule.
//
// newTarget is called once per replay rather than once per run, because
// shrinking needs to re-run a candidate sequence from a clean start.
func Run(newTarget func() (Target, error), cfg Config) (Result, error) {
	obs := cfg.Observer
	if obs == nil {
		obs = NopObserver{}
	}
	actions := cfg.Actions
	if len(actions) == 0 {
		actions = Generate(cfg.Seed, cfg.Steps)
	}
	res := Result{Seed: cfg.Seed, Actions: actions}
	obs.Start(cfg.Seed, len(actions))

	replays := 0
	replay := func(as []Action, watch bool) (int, []Violation, error) {
		replays++
		t, err := newTarget()
		if err != nil {
			return -1, nil, err
		}
		defer t.Close()
		if err := t.Reset(); err != nil {
			return -1, nil, err
		}
		for i, a := range as {
			if err := t.Apply(a); err != nil {
				return -1, nil, fmt.Errorf("step %d %s: %w", i, a, err)
			}
			vs := t.Check()
			if watch {
				obs.Step(i, a, vs)
			}
			if len(vs) > 0 {
				return i, vs, nil
			}
		}
		return -1, nil, nil
	}

	step, vs, err := replay(actions, true)
	res.Executed = len(actions)
	if err != nil {
		res.Replays = replays
		return res, err
	}
	if step < 0 {
		res.Replays = replays
		obs.Done(res)
		return res, nil
	}

	res.Failed, res.Step, res.Violations = true, step, vs
	res.Actions = actions[:step+1]

	if !cfg.NoShrink {
		budget := cfg.ShrinkBudget
		if budget <= 0 {
			budget = 40 * len(res.Actions)
		}
		rule := vs[0].Rule
		// still reports whether a candidate breaks the same rule. A candidate
		// that breaks a different one is rejected, so the minimal sequence
		// always explains the finding it is attached to.
		still := func(as []Action) bool {
			if budget <= 0 {
				return false
			}
			budget--
			_, cvs, cerr := replay(as, false)
			if cerr != nil {
				return false
			}
			for _, v := range cvs {
				if v.Rule == rule {
					return true
				}
			}
			return false
		}
		res.Actions = shrink(res.Actions, still, obs)
		if s, svs, serr := replay(res.Actions, false); serr == nil && s >= 0 {
			res.Step, res.Violations = s, svs
		}
	}

	res.Replays = replays
	obs.Done(res)
	return res, nil
}
