package fuzz

import (
	"strings"
	"testing"
)

// The engine's own guarantees, tested against a fake target. A shrinker that
// drops the action that actually caused the failure reports a repro that does
// not reproduce, which is worse than reporting nothing, so the minimisation is
// checked for both directions: it reaches the minimum, and everything it
// reports still fails.

// needleTarget fails once it has seen a given action, optionally only after a
// prefix action has been seen first. That is the shape of a real finding: some
// setup, then a trigger.
type needleTarget struct {
	setup, trigger Action
	sawSetup       bool
	failed         bool
}

func (n *needleTarget) Reset() error { n.sawSetup, n.failed = false, false; return nil }

func (n *needleTarget) Apply(a Action) error {
	if a == n.setup {
		n.sawSetup = true
	}
	if a == n.trigger && (n.setup == Action{} || n.sawSetup) {
		n.failed = true
	}
	return nil
}

func (n *needleTarget) Check() []Violation {
	if n.failed {
		return []Violation{{Rule: "needle", Detail: "saw the trigger"}}
	}
	return nil
}

func (n *needleTarget) Close() {}

func TestShrinkReachesTheMinimalSequence(t *testing.T) {
	setup := Action{Kind: ToggleShared}
	trigger := Action{Kind: Resize, A: 0, B: 0}

	noise := make([]Action, 0, 200)
	for i := range 200 {
		noise = append(noise, Action{Kind: Key, S: "k", A: i})
	}
	actions := append(append(append([]Action{}, noise[:100]...), setup), noise[100:]...)
	actions = append(actions, trigger)

	res, err := Run(func() (Target, error) { return &needleTarget{setup: setup, trigger: trigger}, nil },
		Config{Seed: 1, Actions: actions})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Failed {
		t.Fatal("the target was rigged to fail and did not")
	}
	if len(res.Actions) != 2 {
		t.Fatalf("shrank %d actions to %d, want the 2 that matter: %v", len(actions), len(res.Actions), res.Actions)
	}
	if res.Actions[0] != setup || res.Actions[1] != trigger {
		t.Fatalf("minimal sequence is %v, want [%v %v]", res.Actions, setup, trigger)
	}
}

// A reported repro must reproduce. This is the property that makes the output
// worth pasting into an issue.
func TestReportedReproStillFails(t *testing.T) {
	trigger := Action{Kind: SwitchWorkspace, A: 7}
	actions := Generate(99, 300)
	actions = append(actions, trigger)

	res, err := Run(func() (Target, error) { return &needleTarget{trigger: trigger}, nil },
		Config{Seed: 99, Actions: actions})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	replay := &needleTarget{trigger: trigger}
	_ = replay.Reset()
	for _, a := range res.Actions {
		_ = replay.Apply(a)
	}
	if len(replay.Check()) == 0 {
		t.Fatalf("the reported repro does not reproduce: %v", res.Actions)
	}
	if !strings.Contains(res.Repro(), "needle") {
		t.Errorf("the repro text names no rule:\n%s", res.Repro())
	}
}

// A run that shrinks into a different bug must not be reported under the first
// one's name, or the maintainer reads a repro for one rule and debugs another.
func TestShrinkHoldsTheRuleFixed(t *testing.T) {
	first := Action{Kind: Resize, A: 3, B: 3}
	other := Action{Kind: ZoomPane}
	tgt := func() (Target, error) { return &twoRuleTarget{a: first, b: other}, nil }

	res, err := Run(tgt, Config{Seed: 2, Actions: []Action{other, {Kind: Tick}, first}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The zoom breaks rule-b first, so that is the finding, and the resize that
	// breaks rule-a must not be what the minimal sequence ends on.
	if res.Violations[0].Rule != "rule-b" {
		t.Fatalf("reported %q, want the first rule broken", res.Violations[0].Rule)
	}
	if got := res.Actions[len(res.Actions)-1]; got != other {
		t.Fatalf("minimal sequence ends on %v, want %v", got, other)
	}
}

type twoRuleTarget struct {
	a, b   Action
	hitA   bool
	hitB   bool
	closed bool
}

func (m *twoRuleTarget) Reset() error { m.hitA, m.hitB = false, false; return nil }

func (m *twoRuleTarget) Apply(x Action) error {
	switch x {
	case m.a:
		m.hitA = true
	case m.b:
		m.hitB = true
	}
	return nil
}

func (m *twoRuleTarget) Check() []Violation {
	var vs []Violation
	if m.hitB {
		vs = append(vs, Violation{Rule: "rule-b", Detail: "b"})
	}
	if m.hitA {
		vs = append(vs, Violation{Rule: "rule-a", Detail: "a"})
	}
	return vs
}

func (m *twoRuleTarget) Close() { m.closed = true }

// A seed is the whole reproduction, so the same seed must produce the same run
// in a later process. Anything drawn from a per-process source breaks this.
func TestSeedIsTheWholeReproduction(t *testing.T) {
	for _, seed := range []uint64{0, 1, 7, 1 << 40} {
		a, b := Generate(seed, 400), Generate(seed, 400)
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("seed %d diverged at step %d: %v vs %v", seed, i, a[i], b[i])
			}
		}
	}
	if x, y := Generate(1, 200), Generate(2, 200); equalRun(x, y) {
		t.Fatal("two different seeds produced the same run")
	}
}

// The byte decoder is the coverage-guided entry point, so a corpus entry has to
// mean the same run every time it is replayed.
func TestByteInputDecodesDeterministically(t *testing.T) {
	for _, in := range [][]byte{nil, {}, {0}, []byte("a longer corpus entry with some bytes in it")} {
		a, b := GenerateBytes(in, 300), GenerateBytes(in, 300)
		if !equalRun(a, b) {
			t.Fatalf("input %q decoded to two different runs", in)
		}
	}
}

func equalRun(a, b []Action) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Every action a run can produce has to survive the round trip through the
// repro format, including the names holding a newline, a NUL, or a quote.
func TestScriptRoundTripsEveryGeneratedAction(t *testing.T) {
	var all []Action
	for seed := range uint64(40) {
		all = append(all, Generate(seed, 200)...)
	}
	back, err := ParseScript(Script(all))
	if err != nil {
		t.Fatalf("ParseScript: %v", err)
	}
	if len(back) != len(all) {
		t.Fatalf("round trip returned %d actions, sent %d", len(back), len(all))
	}
	for i := range all {
		if back[i] != all[i] {
			t.Fatalf("step %d round tripped %v as %v", i, all[i], back[i])
		}
	}
}

// The generator has to actually reach every kind it declares a weight for, or
// part of the alphabet is dead and nobody notices.
func TestGeneratorReachesEveryWeightedKind(t *testing.T) {
	seen := map[Kind]int{}
	for seed := range uint64(20) {
		for _, a := range Generate(seed, 2000) {
			seen[a.Kind]++
		}
	}
	for k := range kindCount {
		if defaultWeights[k] > 0 && seen[k] == 0 {
			t.Errorf("%s carries weight %d but was never generated", k, defaultWeights[k])
		}
	}
}

// The awkward classes are the point of the generator, so their presence is
// pinned rather than left to chance.
func TestGeneratorProducesTheAwkwardShapes(t *testing.T) {
	var wideName, pathName, zeroSize, releaseOutside, resizeMidGesture, detachMidDrag bool
	for seed := range uint64(30) {
		run := Generate(seed, 3000)
		held := false
		for i, a := range run {
			switch a.Kind {
			case Rename, Text:
				if strings.Contains(a.S, "/") || strings.Contains(a.S, "\\") {
					pathName = true
				}
				if strings.ContainsRune(a.S, '世') {
					wideName = true
				}
			case Resize:
				if a.A == 0 || a.B == 0 {
					zeroSize = true
				}
				if held {
					resizeMidGesture = true
				}
			case MousePress:
				held = true
			case MouseRelease:
				held = false
			case Detach:
				if held {
					detachMidDrag = true
				}
			}
			// A release landing well outside anything the host can draw.
			if a.Kind == MouseRelease && i > 0 && a.A > 200 {
				releaseOutside = true
			}
		}
	}
	for name, got := range map[string]bool{
		"a wide-rune name": wideName, "a path separator in a name": pathName,
		"a zero dimension": zeroSize, "a release outside every target": releaseOutside,
		"a resize mid-gesture": resizeMidGesture, "a detach mid-drag": detachMidDrag,
	} {
		if !got {
			t.Errorf("the generator never produced %s", name)
		}
	}
}
