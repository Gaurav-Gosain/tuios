package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// metaDaemon records set-agent-meta calls and answers resolve-pane.
type metaDaemon struct {
	mu       sync.Mutex
	calls    []fakeCall
	resolved map[string]any
	block    chan struct{}
}

func (m *metaDaemon) Call(verb string, params any) (json.RawMessage, error) {
	if m.block != nil {
		<-m.block
	}
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	m.mu.Lock()
	m.calls = append(m.calls, fakeCall{verb, p})
	m.mu.Unlock()
	switch verb {
	case "set-agent-meta":
		return json.RawMessage(`{"type":"agent_meta_set"}`), nil
	case "resolve-pane":
		if m.resolved == nil {
			return nil, &session.VerbCallError{Code: session.ErrVerbWindowNotFound, Message: "no pane"}
		}
		out, _ := json.Marshal(m.resolved)
		return out, nil
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
}

func (m *metaDaemon) metaCalls() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []map[string]any
	for _, c := range m.calls {
		if c.verb == "set-agent-meta" {
			out = append(out, c.params)
		}
	}
	return out
}

func (m *metaDaemon) count(verb string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c.verb == verb {
			n++
		}
	}
	return n
}

// statusLineRig is one status line run's world: a pane's environment, a fake
// daemon, a stamp directory and a clock.
type statusLineRig struct {
	env     map[string]string
	daemon  *metaDaemon
	dir     string
	now     time.Time
	dials   int
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	dialErr error
}

func newStatusLineRig(t *testing.T) *statusLineRig {
	return &statusLineRig{
		env:    map[string]string{"TUIOS_PANE_ID": "w1", "TUIOS_SESSION": "work"},
		daemon: &metaDaemon{},
		dir:    t.TempDir(),
		now:    time.Unix(1_800_000_000, 0),
	}
}

func (r *statusLineRig) run(o agentStatusLineOptions, harness, payload string) int {
	r.stdout.Reset()
	r.stderr.Reset()
	return runAgentStatusLine(o, harness, agentStatusLineIO{
		stdin:  strings.NewReader(payload),
		stdout: &r.stdout,
		stderr: &r.stderr,
		getenv: func(k string) string { return r.env[k] },
		dial: func() (verbCaller, error) {
			r.dials++
			if r.dialErr != nil {
				return nil, r.dialErr
			}
			return r.daemon, nil
		},
		self:     func() (int, []int) { return 4242, []int{4242, 1} },
		stampDir: func() (string, error) { return r.dir, nil },
		now:      func() time.Time { return r.now },
		runThen:  runStatusLineThen,
	})
}

const claudeStatusPayload = `{"session_id":"s1","model":{"id":"claude-opus-4-7","display_name":"Opus"},"context_window":{"used_percentage":42},"cost":{"total_cost_usd":1.2}}`

// TestAgentStatusLineWritesItsPane sends the model, context and cost to the
// pane named by TUIOS_PANE_ID with set-agent-meta, under the statusline
// source, prints nothing, and sends nothing for the same values again.
func TestAgentStatusLineWritesItsPane(t *testing.T) {
	r := newStatusLineRig(t)
	if code := r.run(agentStatusLineOptions{}, "claude-code", claudeStatusPayload); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if r.stdout.Len() != 0 {
		t.Errorf("printed %q", r.stdout.String())
	}
	calls := r.daemon.metaCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %v", r.daemon.calls)
	}
	c := calls[0]
	tokens, _ := c["tokens"].(map[string]any)
	if c["window"] != "w1" || c["session"] != "work" || c["source"] != "statusline" ||
		tokens["model"] != "Opus" || tokens["context"] != "42%" || tokens["cost"] != "$1.20" || len(tokens) != 3 {
		t.Errorf("call = %v", c)
	}
	if _, ok := c["ttl_ms"]; ok {
		t.Errorf("a ttl was set: %v", c)
	}
	if fi, err := os.Stat(filepath.Join(r.dir, statusLineStampName("work", "w1"))); err != nil {
		t.Errorf("no stamp: %v", err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("stamp mode %v", fi.Mode().Perm())
	}

	// The same values a second later: no call, and no dial either.
	r.now = r.now.Add(time.Second)
	dials := r.dials
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatusPayload)
	if n := len(r.daemon.metaCalls()); n != 1 || r.dials != dials {
		t.Errorf("unchanged values: %d calls, %d dials", n, r.dials-dials)
	}
}

// TestAgentStatusLineThrottle holds a changing cost to one report per 15
// seconds, and sends a model change and a context crossing 80% at once.
func TestAgentStatusLineThrottle(t *testing.T) {
	r := newStatusLineRig(t)
	send := func(model string, ctx, cost float64) {
		p, _ := json.Marshal(map[string]any{
			"session_id":     "s1",
			"model":          map[string]any{"display_name": model},
			"context_window": map[string]any{"used_percentage": ctx},
			"cost":           map[string]any{"total_cost_usd": cost},
		})
		r.run(agentStatusLineOptions{}, "claude-code", string(p))
	}
	send("Opus", 10, 0.10)
	r.now = r.now.Add(5 * time.Second)
	send("Opus", 11, 0.20)
	if n := len(r.daemon.metaCalls()); n != 1 {
		t.Fatalf("a change 5s later was sent: %d calls", n)
	}
	r.now = r.now.Add(11 * time.Second)
	send("Opus", 12, 0.30)
	if n := len(r.daemon.metaCalls()); n != 2 {
		t.Fatalf("a change 16s after the last report was not sent: %d calls", n)
	}
	r.now = r.now.Add(time.Second)
	send("Sonnet", 12, 0.30)
	if n := len(r.daemon.metaCalls()); n != 3 {
		t.Fatalf("a model change was held: %d calls", n)
	}
	r.now = r.now.Add(time.Second)
	send("Sonnet", 81, 0.30)
	if n := len(r.daemon.metaCalls()); n != 4 {
		t.Fatalf("context crossing 80%% was held: %d calls", n)
	}
	// Unchanged for ten minutes: sent once more, for a restarted daemon.
	r.now = r.now.Add(statusLineRefresh)
	send("Sonnet", 81, 0.30)
	if n := len(r.daemon.metaCalls()); n != 5 {
		t.Fatalf("no refresh after %s: %d calls", statusLineRefresh, n)
	}
}

// claudeStatus is a Claude Code status line payload for conversation s1.
func claudeStatus(model string, ctx, cost float64) string {
	p, _ := json.Marshal(map[string]any{
		"session_id":     "s1",
		"model":          map[string]any{"display_name": model},
		"context_window": map[string]any{"used_percentage": ctx},
		"cost":           map[string]any{"total_cost_usd": cost},
	})
	return string(p)
}

// TestAgentStatusLineHeldValuesReachTheTurnEnd: a change the interval held
// back is kept in the stamp, and the turn end sends it. Claude Code runs the
// status line only while the conversation changes, so without this the
// turn's last context and cost stayed unsent until the next turn.
func TestAgentStatusLineHeldValuesReachTheTurnEnd(t *testing.T) {
	r := newStatusLineRig(t)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 10, 0.10))
	r.now = r.now.Add(3 * time.Second)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 20, 0.40))
	r.now = r.now.Add(3 * time.Second)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 30, 0.90))
	if n := len(r.daemon.metaCalls()); n != 1 {
		t.Fatalf("%d calls, want the first only", n)
	}
	stampPath := filepath.Join(r.dir, statusLineStampName("work", "w1"))
	st := readStatusLineStamp(stampPath)
	if st == nil || st.Pending["context"] != "30%" || st.Pending["cost"] != "$0.90" {
		t.Fatalf("stamp %+v, want the held values pending", st)
	}

	// The turn ends: the held values go, once.
	r.now = r.now.Add(time.Second)
	sent, err := flushStatusLine(r.daemon, "work", "w1", r.dir, r.now)
	if err != nil || !sent {
		t.Fatalf("flush sent=%v err=%v", sent, err)
	}
	calls := r.daemon.metaCalls()
	if len(calls) != 2 {
		t.Fatalf("%d calls after the turn end", len(calls))
	}
	tokens, _ := calls[1]["tokens"].(map[string]any)
	if tokens["context"] != "30%" || tokens["cost"] != "$0.90" || calls[1]["source"] != "statusline" || calls[1]["window"] != "w1" {
		t.Errorf("turn end call %v", calls[1])
	}
	if sent, _ := flushStatusLine(r.daemon, "work", "w1", r.dir, r.now); sent {
		t.Error("a second turn end sent the same values again")
	}
	// The status line runs once more after the turn with the same values:
	// nothing to send.
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 30, 0.90))
	if n := len(r.daemon.metaCalls()); n != 2 {
		t.Errorf("%d calls, want no call for values the turn end sent", n)
	}
}

// TestAgentStatusLineChangeAfterTheTurnEndGoesAtOnce: a status line run
// after the turn ended carries the turn's last values, so it is not held,
// and the interval applies again after it.
func TestAgentStatusLineChangeAfterTheTurnEndGoesAtOnce(t *testing.T) {
	r := newStatusLineRig(t)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 10, 0.10))
	r.now = r.now.Add(2 * time.Second)
	if _, err := flushStatusLine(r.daemon, "work", "w1", r.dir, r.now); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(time.Second)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 12, 0.20))
	if n := len(r.daemon.metaCalls()); n != 2 {
		t.Fatalf("%d calls, want the run after the turn end sent", n)
	}
	r.now = r.now.Add(time.Second)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 14, 0.30))
	if n := len(r.daemon.metaCalls()); n != 2 {
		t.Errorf("%d calls, want the interval back once a report went", n)
	}
}

// TestAgentStatusLinePendingRidesTheNextReport: a key only a held run
// named goes with the next report that is due.
func TestAgentStatusLinePendingRidesTheNextReport(t *testing.T) {
	r := newStatusLineRig(t)
	r.run(agentStatusLineOptions{}, "claude-code", `{"session_id":"s1","model":{"display_name":"Opus"}}`)
	r.now = r.now.Add(time.Second)
	r.run(agentStatusLineOptions{}, "claude-code", `{"session_id":"s1","model":{"display_name":"Opus"},"cost":{"total_cost_usd":2}}`)
	r.now = r.now.Add(statusLineInterval)
	r.run(agentStatusLineOptions{}, "claude-code", `{"session_id":"s1","model":{"display_name":"Opus"},"context_window":{"used_percentage":5}}`)
	calls := r.daemon.metaCalls()
	if len(calls) != 2 {
		t.Fatalf("%d calls", len(calls))
	}
	tokens, _ := calls[1]["tokens"].(map[string]any)
	if tokens["cost"] != "$2.00" || tokens["context"] != "5%" {
		t.Errorf("due report %v, want the held cost with it", tokens)
	}
	if st := readStatusLineStamp(filepath.Join(r.dir, statusLineStampName("work", "w1"))); st == nil || len(st.Pending) != 0 {
		t.Errorf("stamp %+v, want nothing pending after a report", st)
	}
}

// TestAgentHookStopFlushesTheStatusLine: Claude Code's Stop hook sends what
// the pane's status line held back.
func TestAgentHookStopFlushesTheStatusLine(t *testing.T) {
	r := newStatusLineRig(t)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 10, 0.10))
	r.now = r.now.Add(time.Second)
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatus("Opus", 60, 3.10))

	h := &hookRun{env: map[string]string{"TUIOS_PANE_ID": "w1", "TUIOS_SESSION": "work"}, stampDir: r.dir}
	h.run(t, agentHookOptions{}, `{"hook_event_name":"Stop","session_id":"s1"}`, "claude-code")
	var meta []map[string]any
	for _, c := range h.daemon.calls {
		if c.verb == "set-agent-meta" {
			meta = append(meta, c.params)
		}
	}
	if len(meta) != 1 {
		t.Fatalf("set-agent-meta calls %v, stderr %s", meta, h.stderr.String())
	}
	tokens, _ := meta[0]["tokens"].(map[string]any)
	if tokens["context"] != "60%" || tokens["cost"] != "$3.10" {
		t.Errorf("tokens %v", tokens)
	}
	if !strings.Contains(h.stderr.String(), `"status_line_flushed":true`) {
		t.Errorf("explain output: %s", h.stderr.String())
	}

	// A prompt is no turn end.
	h2 := &hookRun{env: h.env, stampDir: r.dir}
	h2.run(t, agentHookOptions{}, `{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt":"go"}`, "claude-code")
	for _, c := range h2.daemon.calls {
		if c.verb == "set-agent-meta" {
			t.Errorf("a prompt flushed the status line: %v", c.params)
		}
	}
}

func TestStatusLineDue(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	prev := &statusLineStamp{Session: "s1", Values: map[string]string{"model": "Opus", "context": "42%", "cost": "$1.00"}, At: at.UnixMilli()}
	v := func(model, ctx, cost, sess string) integration.StatusLineValues {
		return integration.StatusLineValues{Model: model, Context: ctx, Cost: cost, Session: sess}
	}
	cases := []struct {
		name  string
		prev  *statusLineStamp
		v     integration.StatusLineValues
		after time.Duration
		want  bool
	}{
		{"first report", nil, v("Opus", "", "", ""), 0, true},
		{"unchanged", prev, v("Opus", "42%", "$1.00", "s1"), time.Minute, false},
		{"a field left out is no change", prev, v("Opus", "", "", "s1"), time.Minute, false},
		{"unchanged, refresh", prev, v("Opus", "42%", "$1.00", "s1"), statusLineRefresh, true},
		{"cost changed early", prev, v("Opus", "42%", "$1.10", "s1"), 14 * time.Second, false},
		{"cost changed late", prev, v("Opus", "42%", "$1.10", "s1"), 15 * time.Second, true},
		{"model changed", prev, v("Sonnet", "42%", "$1.00", "s1"), time.Second, true},
		{"context crosses 80", prev, v("Opus", "80%", "$1.00", "s1"), time.Second, true},
		{"context under 80", prev, v("Opus", "79%", "$1.00", "s1"), time.Second, false},
		{"new conversation", prev, v("Opus", "42%", "$1.00", "s2"), time.Second, true},
		{"clock went back", prev, v("Opus", "43%", "$1.00", "s1"), -time.Minute, true},
	}
	for _, tc := range cases {
		if got, why := statusLineDue(tc.prev, tc.v, at.Add(tc.after), false); got != tc.want {
			t.Errorf("%s: due = %v (%s), want %v", tc.name, got, why, tc.want)
		}
	}
	// At the end of a turn a change goes at once, and no change still sends
	// nothing.
	if due, _ := statusLineDue(prev, v("Opus", "42%", "$1.10", "s1"), at.Add(time.Second), true); !due {
		t.Error("a change at the end of a turn was held")
	}
	if due, _ := statusLineDue(prev, v("Opus", "42%", "$1.00", "s1"), at.Add(time.Second), true); due {
		t.Error("unchanged values at the end of a turn were sent")
	}
	// A turn that ended after the last report sends a change at once.
	rested := *prev
	rested.RestAt = at.Add(time.Second).UnixMilli()
	if due, _ := statusLineDue(&rested, v("Opus", "42%", "$1.10", "s1"), at.Add(2*time.Second), false); !due {
		t.Error("a change after the turn ended was held")
	}
	rested.RestAt = at.Add(-time.Second).UnixMilli()
	if due, _ := statusLineDue(&rested, v("Opus", "42%", "$1.10", "s1"), at.Add(2*time.Second), false); due {
		t.Error("a turn end before the last report let a change through")
	}
}

// TestAgentStatusLineMissingFields writes only the fields the payload has.
func TestAgentStatusLineMissingFields(t *testing.T) {
	r := newStatusLineRig(t)
	r.run(agentStatusLineOptions{}, "claude-code", `{"model":{"id":"claude-opus-4-7"}}`)
	calls := r.daemon.metaCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %v", r.daemon.calls)
	}
	if tokens := calls[0]["tokens"].(map[string]any); len(tokens) != 1 || tokens["model"] != "claude-opus-4-7" {
		t.Errorf("tokens = %v", tokens)
	}

	// Nothing the rail shows: no call at all.
	r2 := newStatusLineRig(t)
	for _, payload := range []string{`{"session_id":"s1"}`, ``, `not json`} {
		r2.run(agentStatusLineOptions{}, "claude-code", payload)
	}
	if r2.dials != 0 {
		t.Errorf("dialled %d times for payloads with nothing to send", r2.dials)
	}
}

// TestAgentStatusLineSkips reports nothing for a status line from an agent
// nested in another harness's pane, and exits 0 when the daemon is gone.
func TestAgentStatusLineSkips(t *testing.T) {
	r := newStatusLineRig(t)
	r.env["TUIOS_AGENT"] = "codex"
	if code := r.run(agentStatusLineOptions{}, "claude-code", claudeStatusPayload); code != 0 || r.dials != 0 {
		t.Errorf("foreign harness: exit %d, %d dials", code, r.dials)
	}

	r = newStatusLineRig(t)
	r.dialErr = errors.New("no daemon")
	if code := r.run(agentStatusLineOptions{explain: true}, "claude-code", claudeStatusPayload); code != 0 {
		t.Errorf("daemon gone: exit %d", code)
	}
	if !strings.Contains(r.stderr.String(), "no daemon") || r.stdout.Len() != 0 {
		t.Errorf("explain = %q, stdout %q", r.stderr.String(), r.stdout.String())
	}
	if _, err := os.Stat(filepath.Join(r.dir, statusLineStampName("work", "w1"))); err == nil {
		t.Error("a report that failed left a stamp, so the values would never be sent")
	}
}

// TestAgentStatusLineGivesUp returns within the deadline when the daemon
// does not answer.
func TestAgentStatusLineGivesUp(t *testing.T) {
	r := newStatusLineRig(t)
	r.daemon.block = make(chan struct{})
	start := time.Now()
	code := r.run(agentStatusLineOptions{timeout: 50 * time.Millisecond}, "claude-code", claudeStatusPayload)
	if code != 0 || time.Since(start) > 2*time.Second {
		t.Errorf("exit %d after %s", code, time.Since(start))
	}
	// Let the abandoned report finish, so it does not write its stamp into
	// the test's directory while the directory is being removed.
	close(r.daemon.block)
	stamp := filepath.Join(r.dir, statusLineStampName("work", "w1"))
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(stamp); err == nil {
			return
		}
	}
	t.Error("the report never finished")
}

// TestAgentStatusLineFindsPaneOnce asks the daemon which pane it runs in when
// no pane is named, and, when there is none, does not ask again for a minute.
func TestAgentStatusLineFindsPaneOnce(t *testing.T) {
	r := newStatusLineRig(t)
	r.env = map[string]string{}
	r.daemon.resolved = map[string]any{"session": "work", "window_id": "w9", "by": "sid"}
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatusPayload)
	calls := r.daemon.metaCalls()
	if len(calls) != 1 || calls[0]["window"] != "w9" {
		t.Fatalf("calls = %v", r.daemon.calls)
	}

	r = newStatusLineRig(t)
	r.env = map[string]string{}
	r.run(agentStatusLineOptions{}, "claude-code", claudeStatusPayload)
	r.now = r.now.Add(10 * time.Second)
	r.run(agentStatusLineOptions{}, "claude-code", `{"model":{"id":"other"}}`)
	if n := r.daemon.count("resolve-pane"); n != 1 || r.dials != 1 {
		t.Errorf("outside any pane: %d resolve-pane calls, %d dials in 10s", n, r.dials)
	}
	r.now = r.now.Add(statusLineNoPaneWait)
	r.run(agentStatusLineOptions{}, "claude-code", `{"model":{"id":"other2"}}`)
	if n := r.daemon.count("resolve-pane"); n != 2 {
		t.Errorf("did not ask again after a minute: %d", n)
	}
}

// TestAgentStatusLineThen runs the person's command with the same stdin,
// prints its output unchanged, and exits with its status, whatever the tuios
// side does.
func TestAgentStatusLineThen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	r := newStatusLineRig(t)
	code := r.run(agentStatusLineOptions{then: `cat; printf '\n%s' "$(echo tail)"`}, "claude-code", claudeStatusPayload)
	if code != 0 {
		t.Errorf("exit %d", code)
	}
	if got := r.stdout.String(); got != claudeStatusPayload+"\ntail" {
		t.Errorf("stdout = %q", got)
	}
	if len(r.daemon.metaCalls()) != 1 {
		t.Errorf("chaining stopped the report: %v", r.daemon.calls)
	}

	r = newStatusLineRig(t)
	r.dialErr = errors.New("no daemon")
	if code := r.run(agentStatusLineOptions{then: "echo out; exit 3"}, "claude-code", claudeStatusPayload); code != 3 {
		t.Errorf("exit %d, want the command's 3", code)
	}
	if r.stdout.String() != "out\n" {
		t.Errorf("stdout = %q", r.stdout.String())
	}
}

func TestRunStatusLineThenMissingShellCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	var out, errb bytes.Buffer
	code := runStatusLineThen(context.Background(), "/no/such/statusline-command", nil, &out, &errb)
	if code == 0 {
		t.Error("a command that cannot run exited 0")
	}
}

func TestStatusLineStampName(t *testing.T) {
	if got := statusLineStampName("../work", "a/b c"); got != "statusline-___work-a_b_c.json" || strings.ContainsAny(got, "/\\") {
		t.Errorf("name = %q", got)
	}
}

// TestInstallStatusLineForPrintsTheThenForm refuses a status line the person
// owns and prints the install command that keeps it.
func TestInstallStatusLineForPrintsTheThenForm(t *testing.T) {
	home := t.TempDir()
	env := integration.Env{Home: home, Getenv: func(string) string { return "" }}
	tg, _ := integration.LookupTarget("claude-code")
	if err := os.MkdirAll(tg.ConfigDir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"statusLine": {"type": "command", "command": "~/bin/my line.sh"}}`
	if err := os.WriteFile(tg.Path(env), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if installStatusLineFor(&out, &errb, tg, env, "tuios", "") {
		t.Fatal("installed over the user's status line")
	}
	want := `tuios integration install claude-code --statusline --then '~/bin/my line.sh'`
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want it to show %q", errb.String(), want)
	}
	if !installStatusLineFor(&out, &errb, tg, env, "tuios", "~/bin/my line.sh") {
		t.Fatalf("chaining failed: %s", errb.String())
	}
	st := tg.Status(env, "tuios")
	if v := statusLineVerdict(st); !strings.Contains(v, "chained to ~/bin/my line.sh") {
		t.Errorf("verdict = %q", v)
	}
	var sbuf bytes.Buffer
	if err := printIntegrationStatus(&sbuf, []integration.Status{st}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sbuf.String(), "statusline: installed") {
		t.Errorf("status = %q", sbuf.String())
	}
}
