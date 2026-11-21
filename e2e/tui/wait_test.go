package tuie2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type failer interface {
	Helper()
	Fatalf(format string, args ...any)
}

const (
	pollFirst = 20 * time.Millisecond
	pollMax   = 250 * time.Millisecond
)

var (
	timeScaleOnce  sync.Once
	timeScaleValue = 1.0
)

func scaled(d time.Duration) time.Duration {
	timeScaleOnce.Do(func() {
		if v, err := strconv.ParseFloat(os.Getenv("TUIOS_E2E_TIME_SCALE"), 64); err == nil && v > 0 {
			timeScaleValue = v
		}
	})
	return time.Duration(float64(d) * timeScaleValue)
}

func waitUntil(t failer, what string, timeout time.Duration, probe func() (done bool, state string)) {
	t.Helper()
	timeout = scaled(timeout)
	start := time.Now()
	deadline := start.Add(timeout)
	gap := pollFirst
	var state string
	for {
		var done bool
		if done, state = probe(); done {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("ASSERTION: %s\nwaited %s, the last state was:\n%s", what, time.Since(start).Round(time.Millisecond), state)
		}
		time.Sleep(min(gap, time.Until(deadline)))
		gap = min(gap*2, pollMax)
	}
}

func stayTrue(t failer, what string, d time.Duration, probe func() (ok bool, state string)) {
	t.Helper()
	d = scaled(d)
	deadline := time.Now().Add(d)
	for {
		if ok, state := probe(); !ok {
			t.Fatalf("ASSERTION: %s\nit stopped holding after %s, the state was:\n%s", what, d-time.Until(deadline), state)
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(min(pollMax, time.Until(deadline)))
	}
}

func pause(t *testing.T, why string, d time.Duration) {
	t.Helper()
	t.Logf("pause %s: %s", d, why)
	time.Sleep(scaled(d))
}

type fakeT struct{ msg string }

func (*fakeT) Helper() {}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	panic(f)
}

func failureOf(fn func(failer)) (msg string) {
	f := &fakeT{}
	defer func() {
		if r := recover(); r != nil && r != any(f) {
			panic(r)
		}
		msg = f.msg
	}()
	fn(f)
	return ""
}

// TestWaitsFailWithTheirAssertionAndLastState holds the failure text: the
// assertion, how long it waited or held, and the last state the probe saw,
// because a timeout that says only "timed out" cannot be debugged from a CI
// log. It also holds the other side: a wait on a true condition and a held
// condition both pass.
func TestWaitsFailWithTheirAssertionAndLastState(t *testing.T) {
	msg := failureOf(func(f failer) {
		waitUntil(f, "the thing happens", 60*time.Millisecond, func() (bool, string) { return false, "still nothing" })
	})
	for _, want := range []string{"ASSERTION: the thing happens", "waited", "still nothing"} {
		if !strings.Contains(msg, want) {
			t.Errorf("waitUntil failed with %q, which lacks %q", msg, want)
		}
	}
	msg = failureOf(func(f failer) {
		stayTrue(f, "it holds", 200*time.Millisecond, func() (bool, string) { return false, "it broke" })
	})
	for _, want := range []string{"ASSERTION: it holds", "stopped holding", "it broke"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stayTrue failed with %q, which lacks %q", msg, want)
		}
	}
	if msg := failureOf(func(f failer) {
		waitUntil(f, "already true", time.Second, func() (bool, string) { return true, "" })
		stayTrue(f, "still true", 60*time.Millisecond, func() (bool, string) { return true, "" })
	}); msg != "" {
		t.Errorf("a true condition failed: %s", msg)
	}
}

func waitTextOf(t failer, what string, timeout time.Duration, read func() string, ok func(string) bool) string {
	t.Helper()
	var got string
	waitUntil(t, what, timeout, func() (bool, string) {
		got = read()
		return ok(got), got
	})
	return got
}

func waitFileHas(t failer, path, what string, timeout time.Duration, ok func(string) bool) string {
	t.Helper()
	return waitTextOf(t, what+" ("+path+")", timeout, func() string {
		data, _ := os.ReadFile(path) //nolint:gosec // a file of the calling test's own
		return string(data)
	}, ok)
}

func waitFileExists(t failer, path, what string, timeout time.Duration) {
	t.Helper()
	waitUntil(t, what, timeout, func() (bool, string) {
		_, err := os.Stat(path)
		return err == nil, path + " is not there yet"
	})
}
