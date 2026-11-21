package app

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper.
//
// It also seeds config.Global with the looks from before v0.8.0 (rail off,
// dock and titles at the bottom, full-screen zoom, single-click typing, thin
// scrollbar). Most fixtures in this package build their OS from the global
// and were measured against that screen: a dock row at the bottom, panes
// that fill the width. The v0.8.0 defaults are tested on their own, from
// config.DefaultSettings, in sidebar_defaults_test.go.
func TestMain(m *testing.M) {
	// After the redirect, so applying the looks reads nothing of the
	// developer's: with no theme set it would follow their Omarchy theme.
	code := testutil.RunIsolated(m, func(string) {
		cfg := config.DefaultConfig()
		config.PinPreV080Appearance(&cfg.Appearance)
		config.ApplyAppearanceConfig(cfg, &config.Global)
	})
	if code == 0 && !leftNoPaneGoroutines() {
		code = 1
	}
	os.Exit(code)
}

// paneGoroutines are the goroutines a test starts when it opens a daemon
// window or plays video through a client model. Each one runs until the
// window is closed or the model is cleaned up.
var paneGoroutines = []string{
	"terminal.(*Window).outputWriter",
	"terminal.(*Window).renderCoalescer",
	"app.(*KittyPassthrough).asyncFrameWriter",
}

// leftNoPaneGoroutines reports whether every pane goroutine has ended once the
// tests are done, and prints the ones still running when some have not.
//
// A leaked one costs nothing on its own, but they add up: a full run once
// left about 2,500, and AllocsPerRun, which counts the whole process, then
// charged their work to the test that happened to be measuring.
func leftNoPaneGoroutines() bool {
	var counts map[string]int
	// A window that a cleanup closed ends its goroutines a moment later.
	deadline := time.Now().Add(5 * time.Second)
	for {
		counts = countGoroutines(paneGoroutines)
		if len(counts) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "FAIL: tests left pane goroutines running after the last test:")
	for _, fn := range paneGoroutines {
		if n := counts[fn]; n > 0 {
			fmt.Fprintf(os.Stderr, "  %d %s\n", n, fn)
		}
	}
	fmt.Fprintln(os.Stderr, "Close each window a test opens: call closeWindowsAtEnd, or the model's Cleanup.")
	return false
}

// countGoroutines counts the live goroutines whose stack names each of fns.
// Functions with no goroutine are left out.
func countGoroutines(fns []string) map[string]int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	counts := map[string]int{}
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		for _, fn := range fns {
			if strings.Contains(g, fn) {
				counts[fn]++
			}
		}
	}
	return counts
}
