package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// heapAllocRe pulls the live heap bytes out of the debug=1 heap profile
// trailer, and goroutineTotalRe the total off the goroutine profile's first
// line.
var (
	heapAllocRe      = regexp.MustCompile(`# HeapAlloc = (\d+)`)
	goroutineTotalRe = regexp.MustCompile(`^goroutine profile: total (\d+)`)
)

// TestKilledSessionsReleaseTheirTranscriptJoins joins a window of each of 30
// sessions to a transcript with the real SessionStart hook, then kills the
// 30. A join registers its window in the daemon's transcript watcher. A
// session killed while joined must leave the watcher, and its directory
// watch is given back: the watcher's callback is what kept the session in
// memory. One more session, never killed, is joined in a directory one of
// the 30 shares, and it must still hear its transcript grow after the kills.
// A killed session's panes wind down after kill-session returns, so the
// watches are read once the daemon's goroutines are back to the baseline.
// The heap is not asserted: it is read every 500 ms for 10 s after that and
// written to the artifact, because pane timers of about a second still hold
// a just-killed session for that long.
//
// The artifact is transcript-release.txt under artifactDir: the watch counts,
// heap readings and goroutine totals at each step, the survivor's cost, the
// heap readings after the kills with their time offsets and the growth per
// killed session, and every request, reply and event.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md, "Killed sessions release
// their transcript joins"): with the call that ends the joins cut from
// Session.Stop, the test fails at "the killed sessions kept their watches".
// The watch count is the only thing that sees the leak, and it is read on
// Linux only (the daemon's /proc fdinfo), so off Linux the test does not see
// the leak.
func TestKilledSessionsReleaseTheirTranscriptJoins(t *testing.T) {
	const k = 30
	base := t.TempDir()
	killDaemon(t, base)
	art := &atArtifact{t: t, name: "transcript-release.txt"}
	t.Cleanup(art.save)

	// The daemon, started as startDaemonWithPprof starts it, keeping the
	// process for its pid.
	env := os.Environ()
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	env = append(env, "SHELL=/bin/sh", "ENV=", "PS1="+perfPromptMark+"$ ")
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	daemon := exec.Command(tuiosBin, "daemon", "--pprof", addr)
	daemon.Env = env
	daemon.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := daemon.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		_, _ = daemon.Process.Wait()
	})
	profile := fmt.Sprintf("http://%s/debug/pprof/heap?gc=1&debug=1", addr)
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := httpGet(profile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never served /debug/pprof on %s", addr)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// heap is the post-collection HeapAlloc: the profile asked for twice in
	// a row with the second kept, and the least of three such readings.
	heap := func() int64 {
		t.Helper()
		least := int64(-1)
		for range 3 {
			var body string
			for range 2 {
				b, err := httpGet(profile)
				if err != nil {
					t.Fatalf("read the heap profile: %v", err)
				}
				body = b
			}
			m := heapAllocRe.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("no HeapAlloc in the heap profile")
			}
			v, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if least < 0 || v < least {
				least = v
			}
		}
		return least
	}
	goroutines := func() int {
		t.Helper()
		body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr))
		if err != nil {
			t.Fatalf("read the goroutine profile: %v", err)
		}
		m := goroutineTotalRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no total in the goroutine profile")
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// watches is the number of inotify watches the daemon holds, summed over
	// every inotify descriptor it has open. It reports false off Linux.
	watches := func() (int, bool) {
		t.Helper()
		if runtime.GOOS != "linux" {
			return 0, false
		}
		proc := fmt.Sprintf("/proc/%d", daemon.Process.Pid)
		fds, err := os.ReadDir(filepath.Join(proc, "fd"))
		if err != nil {
			t.Fatalf("list the daemon's descriptors: %v", err)
		}
		n := 0
		for _, fd := range fds {
			if link, err := os.Readlink(filepath.Join(proc, "fd", fd.Name())); err != nil || link != "anon_inode:inotify" {
				continue
			}
			info, err := os.ReadFile(filepath.Join(proc, "fdinfo", fd.Name()))
			if err != nil {
				t.Fatalf("read the daemon's inotify descriptor: %v", err)
			}
			for line := range strings.SplitSeq(string(info), "\n") {
				if strings.HasPrefix(line, "inotify wd:") {
					n++
				}
			}
		}
		return n, true
	}
	record := func(step string) (int, bool, int64) {
		t.Helper()
		w, ok := watches()
		h := heap()
		count := "not counted off Linux"
		if ok {
			count = strconv.Itoa(w)
		}
		art.add(step, fmt.Sprintf("watches %s\nheap_alloc %d", count, h))
		t.Logf("%s: watches %s, HeapAlloc %d", step, count, h)
		return w, ok, h
	}

	// session makes a session of one window and returns that window.
	session := func(name string) string {
		t.Helper()
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
		ids := windowIDs(t, base, name)
		if len(ids) != 1 {
			t.Fatalf("session %s: want one window, got %v", name, ids)
		}
		return ids[0]
	}
	// transcript writes a transcript of one record at dir/id.jsonl.
	transcript := func(dir, id string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(path, []byte(atRecord(id+"-1", "FIRST-"+id)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// join runs the real SessionStart hook from outside every pane.
	join := func(name, win, path, id string) {
		t.Helper()
		hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", name, "--window", win)
		hook.Dir = workDirIn(t, base)
		hook.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
		}
		hook.Stdin = strings.NewReader(string(hookPayload(path, id)))
		if out, err := hook.CombinedOutput(); err != nil {
			t.Fatalf("agent-hook SessionStart for %s: %v\n%s", name, err, out)
		}
	}
	kill := func(name string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "kill-session", name); err != nil {
			t.Fatalf("kill-session %s: %v\n%s", name, err, out)
		}
	}
	projects := atProjects(base)

	// Warm-up: the first join's one-time costs land before the baseline.
	warmWin := session("warm")
	join("warm", warmWin, transcript(filepath.Join(projects, "warm"), "warm"), "warm")
	kill("warm")

	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	local := dialVerbs(t, sock)
	pres, lerr := local.call("attach-presence", nil)
	if lerr != "" {
		t.Fatalf("a local attach-presence: %s", lerr)
	}
	nonce := pres["human_nonce"]
	read := func(name, win string) map[string]any {
		t.Helper()
		p := map[string]any{"session": name, "window": win, "human_nonce": nonce}
		res, lerr := local.call("agent-transcript", p)
		var verr *spVerbErr
		if lerr != "" {
			verr = &spVerbErr{Code: lerr}
		}
		art.call("agent-transcript", p, res, verr)
		if lerr != "" {
			t.Fatalf("agent-transcript for %s: %s", name, lerr)
		}
		return res
	}

	// The survivor, joined in D before the baseline, with its event stream.
	_, _, hBefore := record("before the survivor")
	shared := filepath.Join(projects, "shared")
	survivorPath := transcript(shared, "survivor")
	survivorWin := session("survivor")
	join("survivor", survivorWin, survivorPath, "survivor")
	events := dialVerbs(t, sock)
	if ack, lerr := events.call("subscribe", map[string]any{"session": "survivor", "types": []string{"transcript"}}); lerr != "" || ack["type"] != "subscribed" {
		t.Fatalf("subscribe: %v %s", ack, lerr)
	}

	// Baseline.
	w0, linux, h0 := record("baseline (W0, H0)")
	g0 := goroutines()
	art.add("baseline goroutines", strconv.Itoa(g0))
	art.add("survivor's cost", fmt.Sprintf("heap_alloc %+d", h0-hBefore))

	names := make([]string, k)
	wins := make([]string, k)
	for i := range k {
		names[i] = fmt.Sprintf("gone-%02d", i)
		dir := filepath.Join(projects, names[i])
		if i == 0 {
			dir = shared
		}
		wins[i] = session(names[i])
		join(names[i], wins[i], transcript(dir, names[i]), names[i])
	}

	// R1: every join is watched, and reads its first record.
	wPeak, _, hPeak := record("joined, before the kills")
	if linux && wPeak != w0+k-1 {
		t.Fatalf("the joins are not watched: want %d watches, got %d", w0+k-1, wPeak)
	}
	if !linux {
		t.Logf("watch counts are read on Linux only")
	}
	for i := range k {
		if got := atEntries(t, read(names[i], wins[i])); len(got) != 1 || got[0].Text != "FIRST-"+names[i] {
			t.Fatalf("session %s's join did not read its first record: %v", names[i], got)
		}
	}

	for _, name := range names {
		kill(name)
	}

	// Settle: the killed sessions' panes wind down after kill-session
	// returns.
	settled := time.Now().Add(30 * time.Second)
	g := goroutines()
	for g > g0+2 {
		if time.Now().After(settled) {
			t.Fatalf("the daemon did not settle after the kills: %d goroutines, baseline %d", g, g0)
		}
		time.Sleep(250 * time.Millisecond)
		g = goroutines()
	}
	art.add("settled goroutines", strconv.Itoa(g))

	wEnd, _ := watches()
	count := "not counted off Linux"
	if linux {
		count = strconv.Itoa(wEnd)
	}
	art.add("after the kills, settled", "watches "+count)
	t.Logf("after the kills, settled: watches %s", count)

	// The heap after the kills, for the artifact only: a reading every
	// 500 ms for 10 s, each with its offset. The least is what stays.
	var readings strings.Builder
	start := time.Now()
	least := int64(-1)
	for i := range 21 {
		time.Sleep(time.Until(start.Add(time.Duration(i) * 500 * time.Millisecond)))
		h := heap()
		fmt.Fprintf(&readings, "+%dms heap_alloc %d, %+d over H0, %+d per killed session\n",
			time.Since(start).Milliseconds(), h, h-h0, (h-h0)/k)
		if least < 0 || h < least {
			least = h
		}
	}
	art.add("heap after the kills, settled", strings.TrimSpace(readings.String()))
	art.add("growth", fmt.Sprintf("peak heap_alloc %+d over H0\nleast after the kills %+d over H0, %+d per killed session",
		hPeak-h0, least-h0, (least-h0)/k))
	t.Logf("heap after the kills: least HeapAlloc %d, %+d over H0, %+d per killed session", least, least-h0, (least-h0)/k)

	// R2: the watches are given back.
	if linux && wEnd != w0 {
		t.Fatalf("the killed sessions kept their watches: want %d (W0), got %d (%d before the kills)", w0, wEnd, wPeak)
	}

	// R4: the survivor's watch is live.
	cursor, _ := read("survivor", survivorWin)["cursor"].(string)
	appendLines(t, survivorPath, atRecord("survivor-2", "SECOND-survivor"))
	ev := func() string {
		t.Helper()
		_ = events.conn.SetReadDeadline(time.Now().Add(uiTimeout))
		for {
			line, err := events.br.ReadString('\n')
			if err != nil {
				t.Fatalf("no transcript event for the survivor after the kills: %v", err)
			}
			art.add("event", strings.TrimSpace(line))
			var e map[string]any
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("decode event %q: %v", line, err)
			}
			if c, _ := e["cursor"].(string); e["type"] == "transcript" && e["window"] == survivorWin && c != "" && c != cursor {
				return c
			}
		}
	}()
	art.add("result", fmt.Sprintf("W0 %d, peak %d, after the kills %d; H0 %d, peak %d, least after the kills %d; survivor's event %s", w0, wPeak, wEnd, h0, hPeak, least, ev))
}
