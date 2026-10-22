package tuie2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tmlDaemonPID finds the daemon of the isolation root base.
func tmlDaemonPID(t *testing.T, base string) int {
	t.Helper()
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err != nil || !strings.Contains(string(env), base) {
			continue
		}
		cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if strings.Contains(string(cmd), "daemon") {
			return pid
		}
	}
	t.Fatal("no daemon found for this test")
	return 0
}

// tmlHWM is the most resident memory a process has held, in kB. The peak,
// not the resident set now: the garbage collector gives back what a burst
// held, and the burst is what the test bounds.
func tmlHWM(pid int) int64 {
	st, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	for l := range strings.SplitSeq(string(st), "\n") {
		if strings.HasPrefix(l, "VmHWM:") {
			v, _ := strconv.ParseInt(strings.Fields(l)[1], 10, 64)
			return v
		}
	}
	return 0
}

// TestTMLStalledClientKeepsDaemonMemoryBounded patches a surface with a
// state near 1 MB at the rate limit while the only client is stopped. The
// daemon holds at most one push per client and surface, and a patch travels
// without the state, so its memory stays bounded. When the client runs
// again it catches up to the last value.
//
// Negative control, in NEGATIVE_CONTROLS.md: every push queued on its own
// with the whole state (the daemon grew by about 700 MB).
func TestTMLStalledClientKeepsDaemonMemoryBounded(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("reads memory from /proc")
	}
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlflood"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", sess}})
	waitWindowCount(t, term, 1, "first pane")
	doc := writeTMLDoc(t, base, "f.tml", `<tml version="1" title="Flood"><state>{"n": 0, "big": []}</state><col pad="1"><text>N: {state.n}</text></col></tml>`)
	out, err := tuiosCLI(t, base, "tml", "show", doc, "--pane", "--json", "-s", sess)
	if err != nil {
		t.Fatalf("tml show: %v %s", err, out)
	}
	var o struct {
		Surface string `json:"surface"`
	}
	_ = json.Unmarshal([]byte(out), &o)
	if err := term.WaitForText("N: 0", uiTimeout); err != nil {
		t.Fatalf("no surface: %v\n%s", err, term.Snapshot())
	}
	daemon := tmlDaemonPID(t, base)
	c := dialFileVerbs(t, base)
	chunk := strings.Repeat("x", 60<<10)
	for i := range 15 {
		if _, err := c.call("tml-patch", map[string]any{"surface": o.Surface,
			"ops": []map[string]any{{"op": "add", "path": "/big/-", "value": chunk}}}); err != nil {
			t.Fatalf("grow the state, step %d: %v", i, err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	time.Sleep(time.Second)
	before := tmlHWM(daemon)

	client := term.Pid()
	if err := syscall.Kill(client, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop the client: %v", err)
	}
	stopped := true
	defer func() {
		if stopped {
			_ = syscall.Kill(client, syscall.SIGCONT)
		}
	}()
	last := 0
	start := time.Now()
	for time.Since(start) < 6*time.Second {
		_, err := c.call("tml-patch", map[string]any{"surface": o.Surface,
			"ops": []map[string]any{{"op": "replace", "path": "/n", "value": last + 1}}})
		var fail *verbFailure
		if errors.As(err, &fail) && fail.Code == "rate_limited" {
			time.Sleep(5 * time.Millisecond)
			continue
		} else if err != nil {
			t.Fatalf("patch: %v", err)
		}
		last++
		time.Sleep(9 * time.Millisecond)
	}
	grown := tmlHWM(daemon) - before
	t.Logf("%d patches with the client stopped: the daemon's peak memory grew by %d kB", last, grown)
	if grown > 100<<10 {
		t.Fatalf("the daemon's peak memory grew by %d kB holding pushes for a stopped client", grown)
	}

	_ = syscall.Kill(client, syscall.SIGCONT)
	stopped = false
	if err := term.WaitForText(fmt.Sprintf("N: %d", last), uiTimeout); err != nil {
		t.Fatalf("the client did not catch up to N: %d: %v\n%s", last, err, term.Snapshot())
	}
	alive(t, term, "after the client ran again")
}

// TestTMLSurfaceReadsStayInScope reads a surface of another session by id: a
// pane that holds only read in alpha, and a connection restricted to its own
// session, are refused for a surface in beta. A pane of beta with read
// reads it.
//
// Negative control, in NEGATIVE_CONTROLS.md: tml-get without the reach check
// (the alpha pane read the secret).
func TestTMLSurfaceReadsStayInScope(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, n := range []string{"alpha", "beta"} {
		if out, err := tuiosCLI(t, base, "new", n, "--detach"); err != nil {
			t.Fatalf("new %s: %v %s", n, err, out)
		}
	}
	for _, args := range [][]string{
		{"new-window", "-s", "alpha", "reader", "--grants", "read"},
		{"new-window", "-s", "beta", "local", "--grants", "read"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	doc := writeTMLDoc(t, base, "s.tml", `<tml version="1" title="Secret"><state>{"token":"TOPSECRET-42"}</state><text>{state.token}</text></tml>`)
	out, err := tuiosCLI(t, base, "tml", "show", doc, "--pane", "--json", "-s", "beta")
	if err != nil {
		t.Fatalf("tml show: %v %s", err, out)
	}
	var s struct {
		Surface string `json:"surface"`
	}
	_ = json.Unmarshal([]byte(out), &s)

	read := func(sess, win, name string) string {
		t.Helper()
		outFile, done := filepath.Join(base, name), filepath.Join(base, name+".done")
		line := fmt.Sprintf("%s tml get %s > %s 2>&1; echo done > %s\n", tuiosBin, s.Surface, outFile, done)
		if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", win, line); err != nil {
			t.Fatalf("send-text into %s: %v\n%s", win, err, out)
		}
		deadline := time.Now().Add(shellTimeout)
		for {
			if _, err := os.Stat(done); err == nil {
				data, _ := os.ReadFile(outFile)
				return string(data)
			}
			if time.Now().After(deadline) {
				t.Fatalf("tml get in %s never finished", win)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	if got := read("alpha", "reader", "alpha"); strings.Contains(got, "TOPSECRET-42") || !strings.Contains(got, "is refused for this pane") {
		t.Fatalf("a read pane in alpha was not refused a surface in beta:\n%s", got)
	}
	if got := read("beta", "local", "beta"); !strings.Contains(got, "TOPSECRET-42") {
		t.Fatalf("a read pane in beta could not read its session's surface:\n%s", got)
	}

	c := dialFileVerbs(t, base)
	if _, err := c.call("restrict-connection", map[string]any{"scope": "own"}); err != nil {
		t.Fatalf("restrict-connection: %v", err)
	}
	if _, err := c.call("tml-get", map[string]any{"surface": s.Surface}); err == nil {
		t.Fatal("a connection restricted to its own session read a surface in beta")
	}
}

// TestTMLEmitOverTheLimitIsShown presses a button whose emit value is over
// the 64 KiB limit. The daemon refuses it, the person sees that it did not go
// through, and a small emit after it still reaches the owner.
//
// Negative control, in NEGATIVE_CONTROLS.md: the client only logs a refused
// input (no message on the screen).
func TestTMLEmitOverTheLimitIsShown(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-tmlemit"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new: %v %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", sess}})
	waitWindowCount(t, term, 1, "first pane")
	big := strings.Repeat("z", 70<<10)
	doc := writeTMLDoc(t, base, "e.tml", fmt.Sprintf(`<tml version="1" title="Emit"><state>{"big": %q}</state><col pad="1"><text>EMIT-SURFACE</text>
<button key="e" on-press="emit('big', state.big)">Big</button>
<button key="f" on-press="emit('small', 1)">Small</button></col></tml>`, big))
	stream := startCLIBackground(t, base, "tml", "show", doc, "-s", sess, "--width", "60", "--height", "10", "--events")
	if err := term.WaitForText("EMIT-SURFACE", uiTimeout); err != nil {
		t.Fatalf("no surface: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("e"); err != nil {
		t.Fatalf("send e: %v", err)
	}
	if err := term.WaitForText("could not send what you did", uiTimeout); err != nil {
		t.Fatalf("the refused emit left no message: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "1-emit-refused")
	if err := term.SendKeys("f"); err != nil {
		t.Fatalf("send f: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	select {
	case run := <-stream:
		if !strings.Contains(run.out, `"name":"small"`) || strings.Contains(run.out, `"name":"big"`) {
			t.Fatalf("the owner did not hear exactly the small emit:\n%.400s", run.out)
		}
	case <-time.After(uiTimeout):
		t.Fatal("the owner stream did not end after esc")
	}
}
