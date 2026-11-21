package app

import (
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/adrg/xdg"
)

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
	flaky := 0
	defer func() { checkFlakyRuns(t, flaky) }()
	for i := range uint64(count) {
		seed := first + i
		res, err := fuzz.Run(func() (fuzz.Target, error) { return newFuzzTarget(dir) },
			fuzz.Config{Seed: seed, Steps: steps, ShrinkBudget: 1500, MinWidth: minW, MinHeight: minH})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if fuzzVerdict(t, fmt.Sprintf("seed %d", seed), res) {
			flaky++
		}
		if time.Now().After(deadline) {
			t.Logf("stopped after %d seeds on the time budget", i+1)
			return
		}
	}
}

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
	for _, s := range [][]byte{
		{},
		{0x00},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		[]byte("tiling shared sidebar zoom workspace"),
		[]byte("press motion resize release detach"),
	} {
		f.Add(s)
	}
	dir := fuzzScratch(f)
	var flaky atomic.Int32
	f.Cleanup(func() { checkFlakyRuns(f, int(flaky.Load())) })
	f.Cleanup(func() { reportFuzzWorkerStderr(f) })
	f.Fuzz(func(t *testing.T, in []byte) {
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
		if fuzzVerdict(t, "the corpus entry", res) {
			flaky.Add(1)
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

func fuzzScratch(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_STATE_HOME", dir)
	xdg.Reload()
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
