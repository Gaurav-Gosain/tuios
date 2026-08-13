package app

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
)

// The entry points. Three of them, because the same engine has to run in three
// very different budgets:
//
//	CI, bounded and fast:
//	  go test ./internal/app/ -run TestFuzzModel
//
//	locally, unbounded, coverage guided:
//	  go test ./internal/app/ -run '^$' -fuzz FuzzModel -fuzztime 30m
//
//	locally, a wider seeded sweep without the fuzzing engine:
//	  TUIOS_FUZZ_SEEDS=20000 TUIOS_FUZZ_STEPS=600 go test ./internal/app/ \
//	    -run TestFuzzModelSweep -timeout 3h
//
// A failure from any of them prints a seed and a minimal script. Re-running the
// seed reproduces it, and TUIOS_FUZZ_SCRIPT replays the script on its own.

// fuzzSteps is the run length for the bounded entry points. It is short on
// purpose: a bug that needs 2000 actions to appear almost always also appears
// in 300, and every action costs a full frame composition in the oracle.
const fuzzSteps = 300

// TestFuzzModel is the CI gate. It is bounded by a seed count rather than by
// time so a slow runner produces the same verdict as a fast one, which a
// wall-clock budget does not.
func TestFuzzModel(t *testing.T) {
	if testing.Short() {
		t.Skip("the fuzzer composes a frame per action")
	}
	seeds := envInt(t, "TUIOS_FUZZ_SEEDS", 12)
	steps := envInt(t, "TUIOS_FUZZ_STEPS", fuzzSteps)
	runFuzzSeeds(t, 0, seeds, steps)
}

// TestFuzzModelSweep is the local soak. It is the same loop with a bigger
// budget and a settable starting seed, so a run can pick up where the last one
// stopped instead of re-walking ground already covered.
func TestFuzzModelSweep(t *testing.T) {
	if os.Getenv("TUIOS_FUZZ_SEEDS") == "" {
		t.Skip("set TUIOS_FUZZ_SEEDS to run the sweep")
	}
	first := uint64(envInt(t, "TUIOS_FUZZ_FIRST", 0))
	runFuzzSeeds(t, first, envInt(t, "TUIOS_FUZZ_SEEDS", 1000), envInt(t, "TUIOS_FUZZ_STEPS", fuzzSteps))
}

// fuzzFloorW and fuzzFloorH are the host sizes the default campaigns stay above.
// Below the layout's own minimum pane size the panes clamp, stack, and take
// negative origins, and every finding there belongs to one class; with no floor
// a run reports that class within two actions and never reaches anything else.
// TUIOS_FUZZ_FLOOR_W=0 TUIOS_FUZZ_FLOOR_H=0 runs the campaign that hunts it.
const (
	fuzzFloorW = 60
	fuzzFloorH = 20
)

func runFuzzSeeds(t *testing.T, first uint64, count, steps int) {
	t.Helper()
	dir := fuzzScratch(t)
	minW := envInt(t, "TUIOS_FUZZ_FLOOR_W", fuzzFloorW)
	minH := envInt(t, "TUIOS_FUZZ_FLOOR_H", fuzzFloorH)
	deadline := time.Now().Add(fuzzBudget(t))
	for i := range uint64(count) {
		seed := first + i
		res, err := fuzz.Run(func() (fuzz.Target, error) { return newFuzzTarget(dir) },
			fuzz.Config{Seed: seed, Steps: steps, ShrinkBudget: 1500, MinWidth: minW, MinHeight: minH})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if res.Failed {
			t.Errorf("seed %d broke %s\n%s", seed, res.Violations[0].Rule, res.Repro())
		}
		if time.Now().After(deadline) {
			t.Logf("stopped after %d seeds on the time budget", i+1)
			return
		}
	}
}

// fuzzBudget leaves room inside the test binary's own timeout to report a
// finding. A fuzzer killed by the harness mid-shrink prints nothing usable.
func fuzzBudget(t *testing.T) time.Duration {
	if d, ok := t.Deadline(); ok {
		if left := time.Until(d) - 90*time.Second; left > 0 {
			return left
		}
	}
	return 8 * time.Minute
}

// FuzzModel is the coverage-guided entry point. Go's engine mutates the byte
// slice, the alphabet decodes it, and the corpus and minimisation of the bytes
// come for free; the action-level shrinking that produces a readable repro is
// still ours, because a minimal byte slice is not a minimal action sequence.
func FuzzModel(f *testing.F) {
	// Seeds that reach the arrangements the matrix tests care about, so the
	// engine starts from inside the interesting region rather than walking to
	// it. The bytes are opaque to a reader; what matters is that they are
	// stable and that each decodes to a run reaching a different corner.
	for _, s := range [][]byte{
		{},
		{0x00},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		[]byte("tiling shared sidebar zoom workspace"),
		[]byte("press motion resize release detach"),
	} {
		f.Add(s)
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, in []byte) {
		// The corpus entry decides the run; the seed is carried only so a
		// finding can be re-run through the seeded loop as well.
		// The floor goes to the shrinker as well as to the generator. With it set
		// on one side only, the shrinker halves a host size step by step until the
		// run leaves the region it was exploring and lands in the class below the
		// floor, and every report reads as that class whatever was found.
		res, err := fuzz.Run(func() (fuzz.Target, error) { return newFuzzTarget(dir) },
			fuzz.Config{
				Actions:      fuzz.GenerateBytesFloor(in, fuzzSteps, fuzzFloorW, fuzzFloorH),
				ShrinkBudget: 400,
				MinWidth:     fuzzFloorW,
				MinHeight:    fuzzFloorH,
			})
		if err != nil {
			t.Fatal(err)
		}
		if res.Failed {
			t.Fatalf("broke %s\n%s", res.Violations[0].Rule, res.Repro())
		}
	})
}

// TestFuzzScript replays a saved repro. This is how a maintainer confirms a
// finding and, later, that a fix closed it:
//
//	TUIOS_FUZZ_SCRIPT=/tmp/repro.txt go test ./internal/app/ -run TestFuzzScript -v
func TestFuzzScript(t *testing.T) {
	path := os.Getenv("TUIOS_FUZZ_SCRIPT")
	if path == "" {
		t.Skip("set TUIOS_FUZZ_SCRIPT to a saved repro to replay it")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := fuzz.ParseScript(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	res, err := fuzz.Run(func() (fuzz.Target, error) { return newFuzzTarget(fuzzScratch(t)) },
		fuzz.Config{Actions: actions, NoShrink: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed {
		t.Fatalf("the script still breaks %s\n%s", res.Violations[0].Rule, res.Repro())
	}
	t.Logf("%d actions replayed clean", len(actions))
}

// fuzzScratch is the sidebar state directory, and it also moves the crash-log
// directory off the developer's real state home. A recovered panic writes a
// report, and a long sweep that finds one repeatedly would otherwise leave
// thousands of files in ~/.local/state.
func fuzzScratch(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, v, err)
	}
	return n
}
