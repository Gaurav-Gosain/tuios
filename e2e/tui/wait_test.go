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

// The waits every test uses. A test waits on the thing it needs, never on a
// guess of how long that takes:
//
//   - waitUntil polls a probe until it is done, and says what it saw last when
//     it gives up;
//   - stayTrue proves that something holds for a while, which is the only
//     honest form of "nothing happens";
//   - pause is the one bare pause, for a delay with nothing to observe, and it
//     names its reason. TestNoNewBareSleeps lets the number of time.Sleep calls
//     in a file only fall.
//
// TUIOS_E2E_TIME_SCALE multiplies every timeout and every pause, so a slow
// machine scales the whole suite at once.

// failer is what the waits need of a test: *testing.T, or a fake that records
// the failure.
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

// scaled is d times TUIOS_E2E_TIME_SCALE.
func scaled(d time.Duration) time.Duration {
	timeScaleOnce.Do(func() {
		if v, err := strconv.ParseFloat(os.Getenv("TUIOS_E2E_TIME_SCALE"), 64); err == nil && v > 0 {
			timeScaleValue = v
		}
	})
	return time.Duration(float64(d) * timeScaleValue)
}

// waitUntil polls probe until it reports done, within timeout. The probe
// returns what it saw, so the failure says what the state was when the wait
// ran out. The first look is immediate, and the gap between looks grows from
// pollFirst to pollMax: a condition that already holds costs nothing, and one
// that takes seconds is not hammered.
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

// stayTrue checks probe at every poll for d, and fails at the first look that
// is not ok. It proves the condition held for that long, where a bare pause
// proves only that time passed.
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

// pause waits for d, for a delay with nothing to observe: a product timer
// that has no visible end, or the pace of a gesture. why says which, and shows
// with -v.
func pause(t *testing.T, why string, d time.Duration) {
	t.Helper()
	t.Logf("pause %s: %s", d, why)
	time.Sleep(scaled(d))
}

// afterModeSwitch waits out the window in which tuios ignores unmodified
// single-character keys after the terminal mode starts (insertGuard). The end
// of the window is not on screen, so it is a pause by design, kept here and
// not in two hundred tests.
func afterModeSwitch(t *testing.T) {
	t.Helper()
	time.Sleep(scaled(insertGuard))
}

// fakeT records the first failure and stops the wait, as t.Fatalf does.
type fakeT struct{ msg string }

func (*fakeT) Helper() {}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	panic(f)
}

// failureOf runs fn against a fake test and returns the message it failed with,
// or "" when it did not fail.
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

// waitTextOf polls read until ok accepts what it returns, and returns it. The
// failure prints what the source held last, so a test that waits on a file, a
// listing or a capture says what it saw. It is the one shape behind the
// wait helpers that poll text.
func waitTextOf(t failer, what string, timeout time.Duration, read func() string, ok func(string) bool) string {
	t.Helper()
	var got string
	waitUntil(t, what, timeout, func() (bool, string) {
		got = read()
		return ok(got), got
	})
	return got
}

// waitFileHas waits until the file at path exists and ok accepts its text, and
// returns the text. A file that is not there yet reads as empty.
func waitFileHas(t failer, path, what string, timeout time.Duration, ok func(string) bool) string {
	t.Helper()
	return waitTextOf(t, what+" ("+path+")", timeout, func() string {
		data, _ := os.ReadFile(path) //nolint:gosec // a file of the calling test's own
		return string(data)
	}, ok)
}

// waitFileExists waits until path exists.
func waitFileExists(t failer, path, what string, timeout time.Duration) {
	t.Helper()
	waitUntil(t, what, timeout, func() (bool, string) {
		_, err := os.Stat(path)
		return err == nil, path + " is not there yet"
	})
}
