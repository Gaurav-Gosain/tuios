// Command tuios-fuzz runs the property fuzzer against tuios, optionally drawing
// the run while it happens.
//
// It is a separate binary rather than a `tuios fuzz` subcommand for one reason:
// the fuzzer is test-only, and nothing in the binary users install may import
// it. A subcommand would link the engine, the observer and the display into
// tuios. Same repo, same module, same version, built from the same source, and
// deliberately not linked into the shipped program. The assertion that keeps it
// that way is in cmd/tuios/imports_test.go.
//
//	tuios-fuzz                        one seed, drawn, per-batch
//	tuios-fuzz -seeds 50 -display=off a campaign, no display
//	tuios-fuzz -seed 42 -cadence fps  one seed at 30fps
//	tuios-fuzz -replay repro.txt      replay a saved script
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/apptarget"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vis"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tuios-fuzz:", err)
		os.Exit(1)
	}
}

// options are the knobs, each with a default that makes the bare command do the
// most useful thing: one seed, drawn, at the cadence that is legible.
type options struct {
	seed    uint64
	seeds   int
	steps   int
	display string
	cadence string
	fps     int
	batch   int
	ascii   bool
	mono    bool
	width   int
	height  int
	replay  string
	hold    bool
}

func parse() (options, error) {
	var o options
	var seed string
	flag.StringVar(&seed, "seed", "", "the seed to run, in hex or decimal (default: from the clock)")
	flag.IntVar(&o.seeds, "seeds", 1, "how many consecutive seeds to run")
	flag.IntVar(&o.steps, "steps", 2000, "actions to generate per seed")
	flag.StringVar(&o.display, "display", "on", "on or off; off is the shipping-CI path and costs nothing")
	flag.StringVar(&o.cadence, "cadence", "batch", "batch draws every -batch actions, fps draws -fps times a second")
	flag.IntVar(&o.fps, "fps", vis.DefaultFPS, "frames per second in fps cadence")
	flag.IntVar(&o.batch, "batch", vis.DefaultBatch, "actions per frame in batch cadence")
	flag.BoolVar(&o.ascii, "ascii", false, "draw with ASCII only")
	flag.BoolVar(&o.mono, "mono", false, "draw without colour, keeping attributes")
	flag.IntVar(&o.width, "width", 0, "frame width (default: the terminal's)")
	flag.IntVar(&o.height, "height", 0, "frame height (default: the terminal's)")
	flag.StringVar(&o.replay, "replay", "", "replay a saved repro script instead of generating")
	flag.BoolVar(&o.hold, "hold", true, "hold the end card until a key, so a recording ends on it")
	flag.Parse()

	if seed != "" {
		v, err := strconv.ParseUint(seed, 0, 64)
		if err != nil {
			// The end card prints the seed as bare hex, so that is what a reader
			// copies back in.
			v, err = strconv.ParseUint(seed, 16, 64)
			if err != nil {
				return o, fmt.Errorf("seed %q is not a number", seed)
			}
		}
		o.seed = v
	} else {
		o.seed = uint64(time.Now().UnixNano())
	}
	if o.display != "on" && o.display != "off" {
		return o, fmt.Errorf("-display is on or off, not %q", o.display)
	}
	if o.cadence != "batch" && o.cadence != "fps" {
		return o, fmt.Errorf("-cadence is batch or fps, not %q", o.cadence)
	}
	return o, nil
}

func run() error {
	o, err := parse()
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "tuios-fuzz")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	actions, err := loadReplay(o)
	if err != nil {
		return err
	}

	for i := range o.seeds {
		seed := o.seed + uint64(i)
		res, err := campaign(o, dir, seed, actions)
		if err != nil {
			return fmt.Errorf("seed %d: %w", seed, err)
		}
		if res.Failed {
			// The exit code is what a script reads, and a finding is a finding
			// whether or not anybody was watching it happen.
			fmt.Fprint(os.Stderr, res.Repro())
			os.Exit(2)
		}
		if o.display == "off" {
			fmt.Printf("seed %016x held: %d actions, %d replays\n", seed, res.Executed, res.Replays)
		}
	}
	return nil
}

func loadReplay(o options) ([]fuzz.Action, error) {
	if o.replay == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(o.replay)
	if err != nil {
		return nil, err
	}
	return fuzz.ParseScript(string(raw))
}

// campaign runs one seed. The display, when it is on, is attached as the
// engine's observer and as a decorator around the target that captures the
// app's frame; neither can change what the engine explores.
func campaign(o options, dir string, seed uint64, actions []fuzz.Action) (fuzz.Result, error) {
	live := &current{}
	newTarget := func() (fuzz.Target, error) {
		t, err := apptarget.New(dir)
		if err != nil {
			return nil, err
		}
		if o.display == "off" {
			return t, nil
		}
		w := &watched{Target: t, live: live}
		live.set(w)
		return w, nil
	}

	cfg := fuzz.Config{
		Seed: seed, Steps: o.steps, Actions: actions,
		MinWidth: floorW, MinHeight: floorH,
	}

	if o.display == "off" {
		return fuzz.Run(newTarget, cfg)
	}

	probe, err := apptarget.New(dir)
	if err != nil {
		return fuzz.Result{}, err
	}
	rules := probe.Rules()
	probe.Close()

	w, h := frameSize(o)
	if !vis.Fits(w, h) {
		fmt.Fprintf(os.Stderr,
			"terminal is %dx%d, the instruments need %dx%d; running without a display\n",
			w, h, vis.MinWidth, vis.MinHeight)
		return fuzz.Run(newTarget, cfg)
	}

	d := vis.New(vis.Options{
		Rules:   rules,
		Out:     os.Stdout,
		Width:   w,
		Height:  h,
		FPS:     cadenceFPS(o),
		Batch:   cadenceBatch(o),
		ASCII:   o.ascii,
		Mono:    o.mono,
		Screen:  live.screen,
		Command: fmt.Sprintf("tuios-fuzz -seed %016x", seed),
	})
	cfg.Observer = d

	restore := rawMode()
	defer restore()
	d.Open()
	res, runErr := fuzz.Run(newTarget, cfg)
	d.Close()
	restore()

	if o.hold {
		waitForKey()
	}
	return res, runErr
}

func cadenceFPS(o options) int {
	if o.cadence == "fps" {
		return o.fps
	}
	return 0
}

func cadenceBatch(o options) int {
	if o.cadence == "batch" {
		return o.batch
	}
	return 0
}

// floorW and floorH keep the generator above the host size where every pane
// clamps and stacks. Every finding below that floor belongs to one class, and a
// run with no floor reports that class within two actions and never reaches
// anything else. They match the in-package campaign's floor.
const (
	floorW = 60
	floorH = 20
)

func frameSize(o options) (int, int) {
	w, h := o.width, o.height
	if w > 0 && h > 0 {
		return w, h
	}
	tw, th, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || tw <= 0 {
		tw, th = 120, 34
	}
	if w <= 0 {
		w = tw
	}
	if h <= 0 {
		h = th
	}
	return w, h
}

func rawMode() func() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return func() {}
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return func() {}
	}
	// A run killed mid-frame must not leave the terminal in raw mode with the
	// cursor hidden, so the restore is wired to the signals that kill it too.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigs:
			_ = term.Restore(fd, old)
			fmt.Print("\x1b[?25h\x1b[?1049l")
			os.Exit(130)
		case <-done:
		}
	}()
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		close(done)
		signal.Stop(sigs)
		_ = term.Restore(fd, old)
	}
}

// waitForKey holds the end card up. The card is the artifact a recording ends
// on, so it stays until somebody dismisses it rather than for a guessed number
// of seconds.
func waitForKey() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	defer func() { _ = term.Restore(fd, old) }()
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
}
