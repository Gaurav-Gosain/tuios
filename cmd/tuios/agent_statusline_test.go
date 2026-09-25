package main

import (
	"bytes"
	"encoding/json"
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
}

func (m *metaDaemon) Call(verb string, params any) (json.RawMessage, error) {
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

func TestStatusLineStampName(t *testing.T) {
	if got := statusLineStampName("../work", "a/b c"); got != "statusline-___work-a_b_c.json" || strings.ContainsAny(got, "/\\") {
		t.Errorf("name = %q", got)
	}
}
