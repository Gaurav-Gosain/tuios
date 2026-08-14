package main

import (
	"hash/fnv"
	"io"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/apptarget"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vis"
)

// The gate the whole display rests on: attaching one must not change what the
// fuzzer explores. If it does, every recorded demo is a recording of a
// different fuzzer than the one CI runs, and a failure shown on screen is not a
// failure the seed reproduces.
//
// This is the real wiring rather than a stub: the real target, the real
// display, and the decorator that captures the app's frame between actions,
// which is the piece most likely to perturb a run because it is the only one
// that touches the model.
//
// The trace is hashed closest to the target, by a decorator present in both
// arms, so the only difference between them is the display.

type tracer struct {
	fuzz.Target
	h       uint64
	applied int
}

func (t *tracer) Reset() error {
	t.h = 14695981039346656037
	return t.Target.Reset()
}

func (t *tracer) Apply(a fuzz.Action) error {
	f := fnv.New64a()
	_, _ = f.Write([]byte(a.String()))
	t.h = t.h*1099511628211 ^ f.Sum64()
	t.applied++
	return t.Target.Apply(a)
}

// trace is one arm of the comparison. cadence selects the display: "off" runs
// with a nil observer, "batch" and "fps" run with the display drawing to a
// discarded writer.
type trace struct {
	hash     uint64
	applied  int
	executed int
	replays  int
	failed   bool
	rule     string
	elapsed  time.Duration
	captures int64
}

func runArm(t *testing.T, seed uint64, steps int, cadence string) trace {
	t.Helper()
	dir := t.TempDir()

	var last *tracer
	live := &current{}
	newTarget := func() (fuzz.Target, error) {
		inner, err := apptarget.New(dir)
		if err != nil {
			return nil, err
		}
		var target fuzz.Target = inner
		if cadence != "off" {
			w := &watched{Target: inner, live: live}
			live.set(w)
			target = w
		}
		tr := &tracer{Target: target}
		if last == nil {
			// The first replay is the watched one, and its trace is the run.
			last = tr
		}
		return tr, nil
	}

	cfg := fuzz.Config{Seed: seed, Steps: steps, MinWidth: floorW, MinHeight: floorH}
	var d *vis.Display
	if cadence != "off" {
		probe, err := apptarget.New(dir)
		if err != nil {
			t.Fatal(err)
		}
		rules := probe.Rules()
		probe.Close()
		o := vis.Options{
			Rules: rules, Out: io.Discard, Width: 120, Height: 34,
			Screen: live.screen,
		}
		if cadence == "fps" {
			o.FPS = vis.DefaultFPS
		} else {
			o.Batch = vis.DefaultBatch
		}
		d = vis.New(o)
		cfg.Observer = d
		d.Open()
	}

	start := time.Now()
	res, err := fuzz.Run(newTarget, cfg)
	elapsed := time.Since(start)
	if d != nil {
		d.Close()
	}
	if err != nil {
		t.Fatalf("seed %d cadence %s: %v", seed, cadence, err)
	}

	out := trace{
		hash: last.h, applied: last.applied,
		executed: res.Executed, replays: res.Replays,
		failed: res.Failed, elapsed: elapsed,
		captures: live.captures.Load(),
	}
	if res.Failed {
		out.rule = res.Violations[0].Rule
	}
	return out
}

func TestDisplayDoesNotChangeTheRun(t *testing.T) {
	if testing.Short() {
		t.Skip("the fuzzer composes a frame per action")
	}
	const steps = 250
	for _, seed := range []uint64{1, 17} {
		off := runArm(t, seed, steps, "off")
		for _, cadence := range []string{"batch", "fps"} {
			on := runArm(t, seed, steps, cadence)

			if off.hash != on.hash {
				t.Errorf("seed %d: the applied actions hash %x with no display and %x on %s cadence",
					seed, off.hash, on.hash, cadence)
			}
			if off.applied != on.applied {
				t.Errorf("seed %d %s: %d actions applied, want %d", seed, cadence, on.applied, off.applied)
			}
			if off.executed != on.executed || off.replays != on.replays {
				t.Errorf("seed %d %s: the engine did different work: %d executed / %d replays became %d / %d",
					seed, cadence, off.executed, off.replays, on.executed, on.replays)
			}
			if off.failed != on.failed || off.rule != on.rule {
				t.Errorf("seed %d %s: the verdict moved from (%v %q) to (%v %q)",
					seed, cadence, off.failed, off.rule, on.failed, on.rule)
			}
		}
	}
}

// The cost gate. It is stated over the work the display causes rather than over
// wall-clock throughput, because throughput measured twice in sequence on a
// machine running the rest of the suite in parallel swings by more than the
// effect being measured: the same pair reads 6% idle and 34% under load, and a
// gate that reports the machine's mood is not a gate.
//
// The regression actually worth catching is structural and exactly countable:
// the viewport rendering the app once per action instead of once per drawn
// frame, which is the difference between a display costing a few percent and one
// that doubles the run. The wall-clock figure is still measured and logged,
// since it is the number a human wants, but it only fails on a gross change.
func TestDisplayCostsLittleWork(t *testing.T) {
	if testing.Short() {
		t.Skip("the fuzzer composes a frame per action")
	}
	const steps = 400
	off := runArm(t, 3, steps, "off")
	on := runArm(t, 3, steps, "batch")

	if off.captures != 0 {
		t.Errorf("a run with no display rendered the app %d times for the viewport", off.captures)
	}
	// One capture per drawn frame, and a frame is drawn every -batch actions.
	// The slack covers the phase-change frames and the final one.
	ceiling := int64(on.applied/vis.DefaultBatch) + 8
	if on.captures > ceiling {
		t.Errorf("the viewport rendered the app %d times over %d actions drawing every %d; at most %d is per-frame, more is per-action",
			on.captures, on.applied, vis.DefaultBatch, ceiling)
	}
	if on.captures == 0 {
		t.Error("the viewport never rendered the app, so this gate is measuring nothing")
	}

	offRate := float64(off.applied) / off.elapsed.Seconds()
	onRate := float64(on.applied) / on.elapsed.Seconds()
	overhead := (offRate - onRate) / offRate * 100
	t.Logf("%d app renders over %d actions (one per %d); %.0f actions/s undrawn, %.0f drawn (%.1f%% slower, machine-dependent)",
		on.captures, on.applied, vis.DefaultBatch, offRate, onRate, overhead)
	if overhead > 60 {
		t.Errorf("the display cost %.1f%% of throughput, which is past anything the frame budget explains", overhead)
	}
}
