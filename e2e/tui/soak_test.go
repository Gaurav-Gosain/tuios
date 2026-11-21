package tuie2e

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return port
}

var goroutineCountRe = regexp.MustCompile(`goroutine profile: total (\d+)`)

func pprofGoroutines(addr string) (int, error) {
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr))
	if err != nil {
		return 0, err
	}
	m := goroutineCountRe.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("no goroutine total in profile")
	}
	return strconv.Atoi(m[1])
}

var heapInUseRe = regexp.MustCompile(`# HeapInuse = (\d+)`)

func pprofHeapInUse(addr string) (uint64, error) {
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/heap?debug=1", addr))
	if err != nil {
		return 0, err
	}
	m := heapInUseRe.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("no HeapInuse in profile")
	}
	return strconv.ParseUint(m[1], 10, 64)
}

func httpGet(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TestSoakMixedActivity runs sustained mixed activity for a bounded period and
// asserts tuios neither crashes, hangs, nor grows without bound.
//
// The three failure modes are checked separately because they look different:
//
//   - A crash shows up as the process exiting, which alive() reports.
//   - A hang shows up as the UI no longer rendering new content. Each cycle
//     ends by demanding a marker the shell computes, so a frozen UI fails the
//     cycle it froze on rather than passing quietly for the full duration. This
//     is the same shape as the deadlock regression test, run under load.
//   - Runaway growth shows up in the goroutine count and in-use heap, read from
//     the process's own pprof endpoint. Goroutines are the sharper signal: a
//     leak per window, per focus change, or per resize accumulates
//     monotonically and is not hidden by GC timing.
//
// The activity deliberately mixes the operations whose interactions caused this
// session's bugs: output bursts, focus changes, tiling, zoom, resize, and
// window create/close.
func TestSoakMixedActivity(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test skipped in -short mode")
	}

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	term, _ := start(t, startOpts{
		args: []string{"--shared-borders", "--pprof", addr},
	})
	waitBoot(t, term)

	for i := 1; i <= 2; i++ {
		newWindow(t, term)
		waitWindowCount(t, term, i, fmt.Sprintf("soak setup window %d", i))
	}
	enableTiling(t, term)

	time.Sleep(3 * time.Second)
	baseGoroutines, err := pprofGoroutines(addr)
	if err != nil {
		t.Fatalf("baseline goroutine count: %v", err)
	}
	baseHeap, err := pprofHeapInUse(addr)
	if err != nil {
		t.Fatalf("baseline heap: %v", err)
	}
	t.Logf("baseline: %d goroutines, %.1f MiB heap in use",
		baseGoroutines, float64(baseHeap)/(1<<20))

	const cycles = 8
	sizes := [][2]int{{120, 40}, {100, 30}, {140, 46}, {90, 28}}

	for cycle := 1; cycle <= cycles; cycle++ {
		enterTerminalMode(t, term)
		marker := fmt.Sprintf("SOAK-%d-DONE", cycle)
		cmd := fmt.Sprintf("for i in $(seq 1 120); do echo soak-%d-$i; done; echo SOAK-$((%d))-DONE",
			cycle, cycle)
		if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
			t.Fatalf("cycle %d: send burst: %v", cycle, err)
		}
		if err := term.WaitForText(marker, soakTimeout); err != nil {
			t.Fatalf("UI stopped rendering during soak cycle %d/%d: %q never appeared: %v\n%s",
				cycle, cycles, marker, err, term.Snapshot())
		}
		leaveTerminalMode(t, term)

		for range 8 {
			if err := term.SendKeys(tuitest.Tab); err != nil {
				t.Fatalf("cycle %d: tab: %v", cycle, err)
			}
		}
		for range 2 {
			if err := term.SendKeys(tuitest.Ctrl('b'), "z"); err != nil {
				t.Fatalf("cycle %d: zoom: %v", cycle, err)
			}
			time.Sleep(120 * time.Millisecond)
		}
		sz := sizes[cycle%len(sizes)]
		if err := term.Resize(sz[0], sz[1]); err != nil {
			t.Fatalf("cycle %d: resize to %dx%d failed (tuios likely died): %v\n%s",
				cycle, sz[0], sz[1], err, term.Snapshot())
		}
		newWindow(t, term)
		waitWindowCount(t, term, 3, fmt.Sprintf("soak cycle %d: transient window", cycle))
		if err := term.SendKeys("x"); err != nil {
			t.Fatalf("cycle %d: close transient window: %v", cycle, err)
		}
		waitWindowCount(t, term, 2, fmt.Sprintf("soak cycle %d: after closing transient window", cycle))

		alive(t, term, fmt.Sprintf("during soak cycle %d", cycle))
	}

	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("final resize: %v", err)
	}
	time.Sleep(3 * time.Second)

	endGoroutines, err := pprofGoroutines(addr)
	if err != nil {
		t.Fatalf("final goroutine count: %v", err)
	}
	endHeap, err := pprofHeapInUse(addr)
	if err != nil {
		t.Fatalf("final heap: %v", err)
	}
	t.Logf("after %d cycles: %d goroutines (from %d), %.1f MiB heap (from %.1f MiB)",
		cycles, endGoroutines, baseGoroutines,
		float64(endHeap)/(1<<20), float64(baseHeap)/(1<<20))

	const goroutineAllowance = 12
	if endGoroutines > baseGoroutines+goroutineAllowance {
		profile, _ := httpGet(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr))
		t.Fatalf("goroutine count grew from %d to %d over %d cycles (allowance %d): "+
			"something is leaking a goroutine per cycle\n%s",
			baseGoroutines, endGoroutines, cycles, goroutineAllowance, topGoroutineStacks(profile))
	}

	if endHeap > baseHeap*4 && endHeap-baseHeap > 64<<20 {
		t.Fatalf("in-use heap grew from %.1f MiB to %.1f MiB over %d cycles, "+
			"more than 4x and more than 64 MiB: this looks unbounded",
			float64(baseHeap)/(1<<20), float64(endHeap)/(1<<20), cycles)
	}

	enterTerminalMode(t, term)
	runInShell(t, term, "echo SOAK-FINAL-$((11*11))", "SOAK-FINAL-121", soakTimeout)
	alive(t, term, "at the end of the soak")
}

func topGoroutineStacks(profile string) string {
	lines := strings.Split(profile, "\n")
	if len(lines) > 60 {
		lines = lines[:60]
	}
	return strings.Join(lines, "\n")
}
