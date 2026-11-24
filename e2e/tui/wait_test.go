package tuie2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
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

// settleSeq numbers the settle probes of the run, so each answer names the
// probe it is for.
var settleSeq atomic.Int64

// settled waits for the client to catch up: it has read every key sent to it
// so far, the frame for them is on the screen, and no animation runs and no
// pane output waits for a frame. It sends a settle probe, an APC the client
// reads in order with the keys before it (internal/app/settle_probe.go), and
// reads the answer from the client's marks file. The answer says how many
// bytes the client had written, and settled waits until the terminal has read
// as many. A busy client is probed again.
//
// A client that does not answer probes gets pause(t, why, d) as before: one
// startIn did not start, one in the test process, one started with
// noSettleProbe, an older tuios, or one whose frames Bubble Tea's renderer
// writes (appearance.cell_frames off), since that renderer writes after the
// answer.
func settled(t *testing.T, term *tuitest.Terminal, why string, d time.Duration) {
	t.Helper()
	marks := settleMarksOf(term)
	if marks == "" {
		pause(t, why, d)
		return
	}
	start, probes := time.Now(), 0
	var a settleAnswer
	waitUntil(t, "the client settles: "+why, uiTimeout, func() (bool, string) {
		probes++
		a = settleProbe(t, term, marks)
		return !a.screen || !a.busy, "an animation runs or pane output waits for its frame"
	})
	if !a.screen {
		pause(t, why, d)
		return
	}
	t.Logf("settled in %s, %d probes, read %d of %d bytes: %s", time.Since(start).Round(time.Millisecond), probes, a.read, a.sent, why)
}

// settleMarksOf is the marks file of a client startIn started, "" for a
// client that does not answer settle probes. A client that answers them
// creates the file when it starts. An older tuios does not, so a test that
// attaches one beside the build under test pauses for it.
func settleMarksOf(term *tuitest.Terminal) string {
	v, ok := clientStarts.Load(term)
	if !ok {
		return ""
	}
	marks := v.(clientStart).marks
	if marks == "" {
		return ""
	}
	if _, err := os.Stat(marks); err != nil {
		return ""
	}
	return marks
}

// settleProbe sends one settle probe and waits for its answer, and then, for
// a client with its own screen, until the terminal has read every byte the
// answer counts.
func settleProbe(t *testing.T, term *tuitest.Terminal, marks string) settleAnswer {
	t.Helper()
	id := strconv.FormatInt(settleSeq.Add(1), 10)
	if err := term.Type("\x1b_tuios-settle;" + id + "\x1b\\"); err != nil {
		t.Fatalf("send settle probe %s: %v", id, err)
	}
	var a settleAnswer
	waitUntil(t, "the client answers settle probe "+id, uiTimeout, func() (bool, string) {
		var ok bool
		a, ok = readSettleAnswer(marks, id)
		return ok, "no answer for probe " + id + " in " + marks
	})
	if !a.screen {
		return a
	}
	waitUntil(t, "the terminal reads the frame before settle probe "+id, uiTimeout, func() (bool, string) {
		a.read, _ = term.Progress()
		return a.read >= a.sent, fmt.Sprintf("read %d of the %d bytes the client wrote", a.read, a.sent)
	})
	return a
}

// settleAnswer is one line of a marks file (internal/app/settle_probe.go),
// and read, the bytes the terminal had read when settleProbe returned.
type settleAnswer struct {
	sent, read   int64
	busy, screen bool
}

// readSettleAnswer finds the answer to probe id in the marks file.
func readSettleAnswer(marks, id string) (settleAnswer, bool) {
	b, err := os.ReadFile(marks)
	if err != nil {
		return settleAnswer{}, false
	}
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) != 4 || f[0] != id {
			continue
		}
		sent, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return settleAnswer{}, false
		}
		return settleAnswer{sent: sent, busy: f[2] == "true", screen: f[3] == "true"}, true
	}
	return settleAnswer{}, false
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
