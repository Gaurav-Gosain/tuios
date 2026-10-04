package session

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// herdrFixture is a daemon with one session and two panes, and a caller the
// test places in either pane, the way the process table places a Crush.
type herdrFixture struct {
	d      *Daemon
	sess   *Session
	a, b   string
	caller string
	pid    int
}

func newHerdrFixture(t *testing.T) *herdrFixture {
	t.Helper()
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	if _, err := sess.AddDaemonWindow("Second", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	st := sess.GetState()
	// A pid above any pid_max: no process has it, so the claim has no
	// anchor and follows the shell alone unless a test sets a real one.
	f := &herdrFixture{d: d, sess: sess, a: st.Windows[0].ID, b: st.Windows[1].ID, pid: 1 << 30}
	d.setApprovalPeer(func(*connState) (bool, string) { return f.caller != "", f.caller })
	return f
}

// call sends one request from the pane f.caller names.
func (f *herdrFixture) call(t *testing.T, method string, params map[string]any) (any, string) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	out, code, msg := f.d.herdrCall(&connState{peerPID: f.pid}, herdrRequest{ID: "t", Method: method, Params: raw})
	if code != "" {
		return nil, code + ": " + msg
	}
	return out, ""
}

// report is Crush's pane.report_agent, from pane and with seq.
func (f *herdrFixture) report(t *testing.T, pane, state, message, sid string, seq uint64) string {
	t.Helper()
	f.caller = pane
	_, errText := f.call(t, "pane.report_agent", map[string]any{
		"pane_id": pane, "source": "crush", "agent": "crush", "state": state,
		"message": message, "seq": seq, "agent_session_id": sid,
	})
	return errText
}

func (f *herdrFixture) window(t *testing.T, id string) WindowState {
	t.Helper()
	for _, w := range f.sess.GetState().Windows {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("no window %s", id)
	return WindowState{}
}

func (f *herdrFixture) wantState(t *testing.T, id string, want AgentState, step string) WindowState {
	t.Helper()
	w := f.window(t, id)
	if w.AgentState != want {
		t.Fatalf("%s: pane %s is %s, want %s (message %q)", step, id, w.AgentState.Name(), want.Name(), w.AgentMessage)
	}
	return w
}

// TestHerdrCrushTurnInParallelPanes plays Crush's own sequence of reports in
// two panes at once, as #3541 sends them: the first idle, working, a
// permission block with its message, a question block, back to working, and
// idle after the turn, which is a finished turn. Each pane moves only on its
// own reports.
func TestHerdrCrushTurnInParallelPanes(t *testing.T) {
	f := newHerdrFixture(t)
	seqA, seqB := uint64(1_000), uint64(5_000)
	next := func(s *uint64) uint64 { *s++; return *s }

	for _, p := range []struct {
		pane string
		seq  *uint64
	}{{f.a, &seqA}, {f.b, &seqB}} {
		if e := f.report(t, p.pane, "idle", "", "", next(p.seq)); e != "" {
			t.Fatalf("first report: %s", e)
		}
		f.wantState(t, p.pane, AgentStateIdle, "first report")
	}

	if e := f.report(t, f.a, "working", "", "s-a", next(&seqA)); e != "" {
		t.Fatal(e)
	}
	f.wantState(t, f.a, AgentStateWorking, "a working")
	f.wantState(t, f.b, AgentStateIdle, "b untouched by a")

	f.report(t, f.a, "blocked", "Permission: bash - go test ./...", "s-a", next(&seqA))
	w := f.wantState(t, f.a, AgentStateNeedsInput, "permission")
	if w.AgentKind != harnessKindApproval || w.AgentMessage != "Permission: bash - go test ./..." {
		t.Fatalf("permission block: kind %q message %q", w.AgentKind, w.AgentMessage)
	}
	if w.AgentHarness != "crush" {
		t.Fatalf("harness %q, want crush", w.AgentHarness)
	}

	f.report(t, f.b, "working", "", "s-b", next(&seqB))
	f.report(t, f.b, "blocked", "Which database should the tests use?", "s-b", next(&seqB))
	w = f.wantState(t, f.b, AgentStateNeedsInput, "question")
	if w.AgentKind != harnessKindQuestion {
		t.Fatalf("question block: kind %q, want question", w.AgentKind)
	}
	f.wantState(t, f.a, AgentStateNeedsInput, "a still blocked")

	f.report(t, f.a, "working", "", "s-a", next(&seqA))
	f.wantState(t, f.a, AgentStateWorking, "a answered")
	f.report(t, f.a, "idle", "", "s-a", next(&seqA))
	f.wantState(t, f.a, AgentStateDone, "a turn ended")
	f.wantState(t, f.b, AgentStateNeedsInput, "b still blocked")

	// An old Crush sends blocked with no message: a permission request.
	f.report(t, f.a, "blocked", "", "s-a", next(&seqA))
	w = f.wantState(t, f.a, AgentStateNeedsInput, "blocked with no message")
	if w.AgentKind != harnessKindApproval {
		t.Fatalf("blocked with no message: kind %q, want approval", w.AgentKind)
	}
}

// TestHerdrSeqDropsStaleReportsEvenAfterRelease: a report whose seq is not
// above the last is dropped without an error, and so is one that arrives
// after the release, which Crush sends on its own connection while a report
// it queued may still be on its way.
func TestHerdrSeqDropsStaleReportsEvenAfterRelease(t *testing.T) {
	f := newHerdrFixture(t)
	f.report(t, f.a, "working", "", "", 10)
	if e := f.report(t, f.a, "idle", "", "", 10); e != "" {
		t.Fatalf("a stale report is answered with an error: %s", e)
	}
	f.wantState(t, f.a, AgentStateWorking, "equal seq dropped")
	f.report(t, f.a, "idle", "", "", 9)
	f.wantState(t, f.a, AgentStateWorking, "lower seq dropped")

	f.caller = f.a
	if _, e := f.call(t, "pane.release_agent", map[string]any{"pane_id": f.a, "source": "crush", "agent": "crush", "seq": 20}); e != "" {
		t.Fatal(e)
	}
	f.wantState(t, f.a, AgentStateNone, "released")
	f.report(t, f.a, "working", "", "", 15)
	f.wantState(t, f.a, AgentStateNone, "a queued report after the release")

	// A restarted Crush seeds its seq from the clock, far above.
	f.report(t, f.a, "idle", "", "", uint64(time.Now().UnixNano()))
	f.wantState(t, f.a, AgentStateIdle, "a new Crush in the same pane")
}

// TestHerdrPaneReportsOnlyForItself: the v0.8.1 rule. A report for another
// pane, and any request from outside every pane, is refused; the caller's own
// reports go through.
func TestHerdrPaneReportsOnlyForItself(t *testing.T) {
	f := newHerdrFixture(t)
	f.caller = f.a
	if _, e := f.call(t, "pane.report_agent", map[string]any{"pane_id": f.b, "source": "crush", "agent": "crush", "state": "working", "seq": 1}); e == "" {
		t.Fatal("a report for another pane went through")
	}
	f.wantState(t, f.b, AgentStateNone, "the other pane")
	f.caller = ""
	for _, m := range []string{"pane.report_agent", "pane.report_metadata", "notification.show"} {
		if _, e := f.call(t, m, map[string]any{"pane_id": f.a, "source": "crush", "agent": "crush", "state": "working", "title": "x"}); e == "" {
			t.Fatalf("%s from outside every pane went through", m)
		}
	}
	f.caller = f.a
	if _, e := f.call(t, "layout.apply", map[string]any{"pane_id": f.a}); !strings.HasPrefix(e, "unsupported") {
		t.Fatalf("layout.apply answered %q, want unsupported", e)
	}
}

// TestHerdrSessionSwitchWhileWorking: Crush moves to another conversation
// mid-run (#3541 resets its run then). The same process reporting a new
// session id is the same harness, so the report is taken and the pane does
// not stay working. Another process with another session id is a nested run
// and is refused, as for any reporter.
func TestHerdrSessionSwitchWhileWorking(t *testing.T) {
	f := newHerdrFixture(t)
	f.report(t, f.a, "working", "", "s-1", 1)
	f.report(t, f.a, "idle", "", "s-2", 2)
	w := f.wantState(t, f.a, AgentStateDone, "switched session")
	if w.AgentSessionID != "s-2" {
		t.Fatalf("session %q, want s-2", w.AgentSessionID)
	}
	f.report(t, f.a, "working", "", "s-2", 3)
	f.pid = 1<<30 + 1
	f.report(t, f.a, "idle", "", "s-3", 4)
	f.wantState(t, f.a, AgentStateWorking, "a nested run")
}

// TestHerdrMetadataAndNotification: #3541's pane.report_metadata becomes the
// pane's agent metadata (title and tokens, a null clearing one, a name tuios
// cannot hold skipped), with a high-water mark of its own; notification.show
// is answered for the caller's pane.
func TestHerdrMetadataAndNotification(t *testing.T) {
	f := newHerdrFixture(t)
	f.report(t, f.a, "idle", "", "", 100)
	f.caller = f.a
	meta := func(seq uint64, title any, tokens map[string]any) string {
		p := map[string]any{"pane_id": f.a, "source": "crush", "tokens": tokens, "seq": seq}
		if title != nil {
			p["title"] = title
		}
		_, e := f.call(t, "pane.report_metadata", p)
		return e
	}
	// Crush numbers state and metadata from one counter, so 101 comes after
	// a state report at 100 and is still fresh.
	if e := meta(101, "Fix the flaky test", map[string]any{"session": "s-1", "model": "claude-opus", "Bad Key!": "x"}); e != "" {
		t.Fatal(e)
	}
	got := agentMetaMap(f.window(t, f.a).AgentMeta, time.Now().UnixNano())
	if got["title"] != "Fix the flaky test" || got["model"] != "claude-opus" || got["session"] != "s-1" || len(got) != 3 {
		t.Fatalf("metadata %v", got)
	}
	meta(102, nil, map[string]any{"session": nil})
	meta(90, "stale", map[string]any{"model": "stale"})
	got = agentMetaMap(f.window(t, f.a).AgentMeta, time.Now().UnixNano())
	if _, ok := got["session"]; ok || got["title"] != "Fix the flaky test" || got["model"] != "claude-opus" {
		t.Fatalf("after a clear and a stale report: %v", got)
	}
	// A release clears the pane and its metadata.
	f.call(t, "pane.release_agent", map[string]any{"pane_id": f.a, "source": "crush", "agent": "crush", "seq": 103})
	if n := len(f.window(t, f.a).AgentMeta); n != 0 {
		t.Fatalf("%d metadata tokens after the release", n)
	}

	if _, e := f.call(t, "notification.show", map[string]any{"title": "Crush finished", "body": "All tests pass"}); e != "" {
		t.Fatal(e)
	}
	if _, e := f.call(t, "notification.show", map[string]any{"body": "no title"}); e == "" {
		t.Fatal("a notification with no title was answered")
	}
}

// TestHerdrUnknownAgentKeepsItsName: an agent tuios has no harness for
// (Prime Agent, Command Code) shows under the name it reports.
func TestHerdrUnknownAgentKeepsItsName(t *testing.T) {
	f := newHerdrFixture(t)
	f.caller = f.a
	if _, e := f.call(t, "pane.report_agent", map[string]any{"pane_id": f.a, "source": "prime", "agent": "Prime Agent", "state": "working", "seq": 1}); e != "" {
		t.Fatal(e)
	}
	if w := f.wantState(t, f.a, AgentStateWorking, "prime"); w.AgentHarness != "prime-agent" {
		t.Fatalf("harness %q, want prime-agent", w.AgentHarness)
	}
}

// TestHerdrCrashClearsAtTheShell: a Crush killed mid-turn sends no release.
// Once the pane is back at its shell, and the last report is older than the
// grace, the pane clears instead of staying working forever. A pane whose
// foreground is not a shell keeps the report.
func TestHerdrCrashClearsAtTheShell(t *testing.T) {
	f := newHerdrFixture(t)
	f.report(t, f.a, "working", "", "", 1)
	w := f.window(t, f.a)
	shell := agentBaseName(f.sess.getShell())
	at := func(argv0 string) func(string) (foregroundInfo, bool) {
		return func(string) (foregroundInfo, bool) {
			return foregroundInfo{pid: 7, shellPID: 7, argv: []string{argv0}, comm: argv0}, true
		}
	}
	none := func(foregroundInfo) (detection, bool) { return detection{}, false }

	// Within the grace the report stands: the reading may predate Crush.
	f.sess.scanAgentDetection(at(shell), none, nil)
	f.wantState(t, f.a, AgentStateWorking, "within the grace")

	f.sess.stateMu.Lock()
	c := f.sess.agentClaims[w.ID]
	if c.herdrAt == 0 {
		f.sess.stateMu.Unlock()
		t.Fatal("the herdr report did not mark its claim")
	}
	c.herdrAt -= int64(herdrShellGrace) + int64(time.Second)
	f.sess.agentClaims[w.ID] = c
	f.sess.stateMu.Unlock()

	// A pane started on the harness itself, which is its own "shell".
	f.sess.scanAgentDetection(at("some-agent"), none, nil)
	f.wantState(t, f.a, AgentStateWorking, "a pane running a program")

	f.sess.scanAgentDetection(at(shell), none, nil)
	f.wantState(t, f.a, AgentStateNone, "back at the shell after a crash")
}

// TestHerdrEnvTellsEveryPaneByDefault: like herdr, every pane is told, so a
// Crush started from a shell prompt reports; "agents" narrows it to a pane
// that starts such a harness, and "off" tells none. HERDR_BIN_PATH names
// this tuios, for a reporter that runs herdr's CLI.
func TestHerdrEnvTellsEveryPaneByDefault(t *testing.T) {
	m := &Manager{}
	m.SetHerdrSocket("/run/tuios.sock.herdr")
	m.SetHerdrBin("/usr/bin/tuios")
	sid, wid := "5f0c8a1e-7b2d-4c3e-9f10-aabbccddeeff", "0d1e2f3a-4b5c-6d7e-8f90-112233445566"
	want := []string{"HERDR_ENV=1", "HERDR_SOCKET_PATH=/run/tuios.sock.herdr", "HERDR_PANE_ID=w5f0c8a1e7b2d:p0d1e2f3a4b5c",
		"HERDR_WORKSPACE_ID=w5f0c8a1e7b2d", "HERDR_BIN_PATH=/usr/bin/tuios"}
	if got := m.HerdrEnv(sid, wid, 0, nil); !slices.Equal(got, want) {
		t.Fatalf("a shell pane by default: %q", got)
	}
	m.SetHerdrProtocol("agents")
	if got := m.HerdrEnv(sid, wid, 0, nil); got != nil {
		t.Fatalf("a shell pane with agents: %q", got)
	}
	if got := m.HerdrEnv(sid, wid, 0, []string{"/opt/bin/crush"}); len(got) != 5 {
		t.Fatalf("a Crush pane with agents: %q", got)
	}
	m.SetHerdrProtocol("off")
	if got := m.HerdrEnv(sid, wid, 0, []string{"crush"}); got != nil {
		t.Fatalf("off: %q", got)
	}
}

// ageHerdrClaim moves the pane's herdr claim past the grace.
func (f *herdrFixture) ageHerdrClaim(t *testing.T, id string) {
	t.Helper()
	f.sess.stateMu.Lock()
	defer f.sess.stateMu.Unlock()
	c := f.sess.agentClaims[id]
	if c.herdrAt == 0 {
		t.Fatal("the herdr report did not mark its claim")
	}
	c.herdrAt -= int64(herdrShellGrace) + int64(time.Second)
	f.sess.agentClaims[id] = c
}

// TestHerdrClaimFollowsTheReporterUnderAWrapper: a Crush started under a
// wrapper (sh -c 'crush; exec fish') runs in the wrapper's process group, so
// the pane reads as at its shell while Crush works. The claim follows the
// reporting process instead, and clears only once that process is gone.
func TestHerdrClaimFollowsTheReporterUnderAWrapper(t *testing.T) {
	f := newHerdrFixture(t)
	crush := exec.Command("sleep", "60")
	if err := crush.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crush.Process.Kill(); _ = crush.Wait() })
	f.pid = crush.Process.Pid
	f.report(t, f.a, "working", "", "", 1)
	f.ageHerdrClaim(t, f.a)

	shell := agentBaseName(f.sess.getShell())
	atShell := func(string) (foregroundInfo, bool) {
		return foregroundInfo{pid: 7, shellPID: 7, argv: []string{shell}, comm: shell}, true
	}
	none := func(foregroundInfo) (detection, bool) { return detection{}, false }
	f.sess.scanAgentDetection(atShell, none, nil)
	f.wantState(t, f.a, AgentStateWorking, "a live Crush under a wrapper")

	_ = crush.Process.Kill()
	_ = crush.Wait()
	f.sess.scanAgentDetection(atShell, none, nil)
	f.wantState(t, f.a, AgentStateNone, "the Crush gone")
}

// TestHerdrMetadataAfterReleaseStaysCleared: Crush numbers state and
// metadata from one counter. Metadata it queued before its release reaches
// the socket after it, and must not bring the pane's metadata back.
func TestHerdrMetadataAfterReleaseStaysCleared(t *testing.T) {
	f := newHerdrFixture(t)
	f.report(t, f.a, "idle", "", "", 100)
	f.call(t, "pane.release_agent", map[string]any{"pane_id": f.a, "source": "crush", "agent": "crush", "seq": 105})
	if _, e := f.call(t, "pane.report_metadata", map[string]any{"pane_id": f.a, "source": "crush", "title": "late", "seq": 103}); e != "" {
		t.Fatal(e)
	}
	if n := len(f.window(t, f.a).AgentMeta); n != 0 {
		t.Fatalf("%d metadata tokens after a queued report behind the release", n)
	}
	f.call(t, "pane.report_metadata", map[string]any{"pane_id": f.a, "source": "crush", "title": "new", "seq": 106})
	if got := agentMetaMap(f.window(t, f.a).AgentMeta, time.Now().UnixNano()); got["title"] != "new" {
		t.Fatalf("metadata after the release: %v", got)
	}
}

// TestHerdrHarnessNameIsPlain: a label tuios does not know becomes a name
// of letters, digits, '-' and '_', never a path.
func TestHerdrHarnessNameIsPlain(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc": "etc", "Prime Agent": "prime-agent", "a/b\\c.d": "abcd", "crush": "crush", "claude": "claude-code",
	} {
		if got := herdrHarness(in); got != want {
			t.Errorf("herdrHarness(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHerdrEventsAreRateLimited: one pane cannot flood notifications and
// metadata. A burst goes through, then the pane is refused until tokens
// come back, and another pane is not affected.
func TestHerdrEventsAreRateLimited(t *testing.T) {
	f := newHerdrFixture(t)
	f.caller = f.a
	refused := 0
	for i := range 40 {
		_, e := f.call(t, "notification.show", map[string]any{"title": "n" + itoa(i)})
		if e != "" {
			refused++
		}
	}
	if refused == 0 || refused > 40-int(herdrEventBurst) {
		t.Fatalf("%d of 40 notifications refused, want some and at most %d", refused, 40-int(herdrEventBurst))
	}
	if _, e := f.call(t, "pane.report_metadata", map[string]any{"pane_id": f.a, "source": "crush", "title": "x", "seq": 1}); e == "" {
		t.Fatal("metadata went through an empty bucket")
	}
	f.caller = f.b
	if _, e := f.call(t, "notification.show", map[string]any{"title": "other pane"}); e != "" {
		t.Fatalf("the other pane was refused: %s", e)
	}
}

// TestHerdrScriptYieldsToTuiosHook: a person with herdr's pi plugin and
// tuios's pi integration installed has two reporters in one pane. tuios's
// own report holds the pane; a report from herdr's script (source herdr:pi)
// for the same harness is dropped, and so is its release. Crush reports to
// herdr by itself and never yields.
func TestHerdrScriptYieldsToTuiosHook(t *testing.T) {
	f := newHerdrFixture(t)
	raw, _ := json.Marshal(map[string]any{"session": "work", "window": f.a, "state": "working", "harness": "pi"})
	if _, verr := f.d.verbSetAgentState(nil, raw); verr != nil {
		t.Fatal(verr.Message)
	}
	f.caller = f.a
	f.call(t, "pane.report_agent", map[string]any{"pane_id": f.a, "source": "herdr:pi", "agent": "pi", "state": "idle", "seq": 1})
	f.wantState(t, f.a, AgentStateWorking, "herdr's script behind tuios's hook")
	f.call(t, "pane.release_agent", map[string]any{"pane_id": f.a, "source": "herdr:pi", "agent": "pi", "seq": 2})
	f.wantState(t, f.a, AgentStateWorking, "herdr's script release behind tuios's hook")

	// With no tuios report in the pane, herdr's script is the reporter.
	f.caller = f.b
	f.call(t, "pane.report_agent", map[string]any{"pane_id": f.b, "source": "herdr:pi", "agent": "pi", "state": "working", "seq": 2})
	f.wantState(t, f.b, AgentStateWorking, "herdr's script alone")
}
