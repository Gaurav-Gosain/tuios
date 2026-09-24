package agentproto

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"
)

// feedReporter is a fakeReporter that also takes metadata and activity.
type feedReporter struct {
	*fakeReporter
	metas      chan map[string]string
	activities chan Activity
}

func (r *feedReporter) SetMeta(_ context.Context, tokens map[string]string) error {
	r.metas <- maps.Clone(tokens)
	return nil
}

func (r *feedReporter) ReportActivity(_ context.Context, a Activity) error {
	r.activities <- a
	return nil
}

func (r *feedReporter) nextMeta(t *testing.T) map[string]string {
	t.Helper()
	select {
	case m := <-r.metas:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no metadata")
		return nil
	}
}

func (r *feedReporter) nextActivity(t *testing.T) Activity {
	t.Helper()
	select {
	case a := <-r.activities:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("no activity")
		return Activity{}
	}
}

func (r *feedReporter) noMeta(t *testing.T) {
	t.Helper()
	select {
	case m := <-r.metas:
		t.Errorf("unexpected metadata %v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// modelAgent is a fakeAgent that names its model when it starts.
type modelAgent struct {
	*fakeAgent
	model string
}

func (a *modelAgent) Start(context.Context, string) (Info, error) {
	return Info{Agent: "fake", Model: a.model}, nil
}

// feedSession is a running session whose reporter takes metadata and
// activity.
type feedSession struct {
	s     *Session
	agent *fakeAgent
	rep   *feedReporter
	keys  *io.PipeWriter
}

// startFeedSession starts a feed session and waits for its idle report.
func startFeedSession(t *testing.T, model string) feedSession {
	t.Helper()
	agent := newFakeAgent()
	rep := &feedReporter{fakeReporter: newFakeReporter(), metas: make(chan map[string]string, 64), activities: make(chan Activity, 64)}
	in, keys := io.Pipe()
	s := &Session{
		Agent:    &modelAgent{fakeAgent: agent, model: model},
		Events:   NewEvents(),
		In:       in,
		Out:      &screen{},
		Reporter: rep,
		Header:   "codex: fake",
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = keys.Close()
	})
	go s.Run(ctx)
	if r := rep.next(t); r.state != "idle" {
		t.Fatalf("first report %+v, want idle", r)
	}
	return feedSession{s: s, agent: agent, rep: rep, keys: keys}
}

// TestSessionMetaFeed sends the model the agent started with, then context,
// cost and plan progress as the agent states them, each key only when it
// changes.
func TestSessionMetaFeed(t *testing.T) {
	f := startFeedSession(t, "o5")
	s, rep := f.s, f.rep
	if m := rep.nextMeta(t); !reflect.DeepEqual(m, map[string]string{"model": "o5"}) {
		t.Errorf("first metadata %v, want the model", m)
	}
	s.Emit(Usage{ContextUsed: 84_000, ContextSize: 200_000, Cost: 1.2, HasCost: true})
	if m := rep.nextMeta(t); !reflect.DeepEqual(m, map[string]string{"context": "42%", "cost": "$1.20"}) {
		t.Errorf("usage metadata %v", m)
	}
	// The same values again send nothing.
	s.Emit(Usage{ContextUsed: 84_100, ContextSize: 200_000, Cost: 1.201, HasCost: true})
	rep.noMeta(t)
	s.Emit(Plan{Entries: []PlanEntry{{"a", "completed"}, {"b", "in_progress"}, {"c", "pending"}}})
	if m := rep.nextMeta(t); !reflect.DeepEqual(m, map[string]string{"plan": "1/3"}) {
		t.Errorf("plan metadata %v", m)
	}
	// A usage that states only the model leaves the rest alone.
	s.Emit(Usage{Model: "o5-mini"})
	if m := rep.nextMeta(t); !reflect.DeepEqual(m, map[string]string{"model": "o5-mini"}) {
		t.Errorf("model change %v", m)
	}
	// An empty plan states nothing.
	s.Emit(Plan{})
	rep.noMeta(t)
}

// TestSessionActivity reports the prompt, each tool call once as it starts
// and once as it ends, and the end of the turn.
func TestSessionActivity(t *testing.T) {
	f := startFeedSession(t, "")
	s, agent, rep := f.s, f.agent, f.rep
	rep.noMeta(t)
	if _, err := io.WriteString(f.keys, pasteStart+"run the tests\nplease"+pasteEnd+"\r"); err != nil {
		t.Fatal(err)
	}
	<-agent.prompts
	if r := rep.next(t); r.state != "working" {
		t.Fatalf("report %+v", r)
	}
	if a := rep.nextActivity(t); a.Event != ActivityPrompt || a.Text != "run the tests" {
		t.Errorf("prompt activity %+v", a)
	}
	cmd := Tool{ID: "c1", Kind: "execute", Title: "go test ./...", Status: ToolPending, Input: map[string]string{"command": "go test ./..."}}
	s.Emit(cmd)
	cmd.Status = ToolRunning
	s.Emit(cmd)
	cmd.Status = ToolFailed
	s.Emit(cmd)
	s.Emit(cmd)
	edit := Tool{ID: "f1", Kind: "edit", Title: "edit a.go", Status: ToolDone, Diffs: []Diff{{Path: "a.go"}, {Path: "b.go"}}}
	s.Emit(edit)

	want := []Activity{
		{Event: ActivityTool, Tool: "Bash", Target: "go test ./..."},
		{Event: ActivityToolFailed, Tool: "Bash", Target: "go test ./..."},
		{Event: ActivityToolDone, Tool: "Edit", Target: "a.go, b.go"},
	}
	for i, w := range want {
		got := rep.nextActivity(t)
		ok := got.OK
		got.OK = nil
		if got != w {
			t.Errorf("activity %d = %+v, want %+v", i, got, w)
		}
		if i > 0 && (ok == nil || *ok != (w.Event == ActivityToolDone)) {
			t.Errorf("activity %d ok = %v", i, ok)
		}
	}
	s.Emit(Text{Text: "All green.\nmore"})
	agent.ends <- TurnResult{Stop: StopFinished}
	if r := rep.next(t); r.state != "done" {
		t.Fatalf("report %+v", r)
	}
	if a := rep.nextActivity(t); a.Event != ActivityTurnEnd || a.Text != "All green." {
		t.Errorf("turn end %+v", a)
	}
	select {
	case a := <-rep.activities:
		t.Errorf("extra activity %+v", a)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestActivityTool(t *testing.T) {
	cases := []struct {
		tool         Tool
		name, target string
	}{
		{Tool{Kind: "execute", Title: "run tests", Input: map[string]string{"command": "go test"}}, "Bash", "go test"},
		{Tool{Kind: "execute", Title: "make"}, "Bash", "make"},
		{Tool{Kind: "read", Title: "a.go"}, "Read", "a.go"},
		{Tool{Kind: "other", Title: "github.search"}, "Tool", "github.search"},
		{Tool{Kind: "fetch", Title: "https://x\x1b[31m"}, "Fetch", "https://x[31m"},
		{Tool{Kind: "execute", Input: map[string]string{"command": strings.Repeat("x", 500)}}, "Bash", strings.Repeat("x", 197) + "..."},
	}
	for _, tc := range cases {
		name, target := activityTool(tc.tool)
		if name != tc.name || target != tc.target {
			t.Errorf("activityTool(%+v) = %q %q, want %q %q", tc.tool, name, target, tc.name, tc.target)
		}
	}
}

func TestMetaFromUsage(t *testing.T) {
	cases := []struct {
		u    Usage
		want map[string]string
	}{
		{Usage{}, map[string]string{}},
		{Usage{ContextUsed: 1, ContextSize: 0}, map[string]string{}},
		{Usage{ContextUsed: 300, ContextSize: 200}, map[string]string{"context": "100%"}},
		{Usage{Cost: 0.004, HasCost: true, Currency: "usd"}, map[string]string{"cost": "$0.00"}},
		{Usage{Cost: 2, HasCost: true, Currency: "EUR"}, map[string]string{"cost": "2.00 EUR"}},
		{Usage{Cost: -1, HasCost: true}, map[string]string{}},
		{Usage{Model: " o5\n"}, map[string]string{"model": "o5"}},
	}
	for _, tc := range cases {
		if got := metaFromUsage(tc.u); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("metaFromUsage(%+v) = %v, want %v", tc.u, got, tc.want)
		}
	}
}

// TestCodexUsageAndPlan: token usage becomes the context use of the model's
// window, a plan update a plan, and a reroute the model.
func TestCodexUsageAndPlan(t *testing.T) {
	_, p, ev := startCodex(t)
	notify(p, "thread/tokenUsage/updated", map[string]any{"threadId": "th-1", "turnId": "tu-1", "tokenUsage": map[string]any{
		"total":              map[string]any{"totalTokens": 900_000},
		"last":               map[string]any{"totalTokens": 51_000, "inputTokens": 50_000},
		"modelContextWindow": 272_000,
	}})
	// No window: nothing to state.
	notify(p, "thread/tokenUsage/updated", map[string]any{"tokenUsage": map[string]any{"last": map[string]any{"totalTokens": 1}, "modelContextWindow": nil}})
	notify(p, "turn/plan/updated", map[string]any{"threadId": "th-1", "turnId": "tu-1", "explanation": nil, "plan": []any{
		map[string]any{"step": "read", "status": "completed"},
		map[string]any{"step": "fix", "status": "inProgress"},
		map[string]any{"step": "test", "status": "pending"},
	}})
	notify(p, "model/rerouted", map[string]any{"threadId": "th-1", "turnId": "tu-1", "fromModel": "o5", "toModel": "o5-mini", "reason": "highRiskCyberActivity"})

	if u := ev.next(t).(Usage); u.ContextUsed != 51_000 || u.ContextSize != 272_000 || u.HasCost || u.Model != "" {
		t.Errorf("usage = %+v", u)
	}
	plan := ev.next(t).(Plan)
	if planProgress(plan) != "1/3" || plan.Entries[1].Status != "in_progress" || plan.Entries[1].Content != "fix" {
		t.Errorf("plan = %+v", plan)
	}
	if u := ev.next(t).(Usage); u.Model != "o5-mini" {
		t.Errorf("reroute = %+v", u)
	}
}

// TestACPUsageUpdate reads usage_update as ACP schema 0.11 has it, which is
// what opencode's ACP agent sends: used and size in tokens, and an optional
// cost of {amount, currency}. Each field is read on its own.
func TestACPUsageUpdate(t *testing.T) {
	_, p, ev := startACP(t)
	update := func(u map[string]any) {
		u["sessionUpdate"] = "usage_update"
		notify(p, "session/update", map[string]any{"sessionId": "s-1", "update": u})
	}
	update(map[string]any{"used": 53_000, "size": 200_000, "cost": map[string]any{"amount": 0.42, "currency": "USD"}})
	update(map[string]any{"used": 60_000, "size": 200_000})
	update(map[string]any{"used": "many", "size": 200_000, "cost": map[string]any{"amount": 1.5, "currency": "EUR"}})
	update(map[string]any{"used": 1, "size": 0, "cost": nil})
	update(map[string]any{"used": 70_000, "size": 200_000, "cost": 3})
	// A plan still arrives in order behind them.
	notify(p, "session/update", map[string]any{"sessionId": "s-1", "update": map[string]any{"sessionUpdate": "plan", "entries": []any{}}})

	want := []Usage{
		{ContextUsed: 53_000, ContextSize: 200_000, Cost: 0.42, HasCost: true, Currency: "USD"},
		{ContextUsed: 60_000, ContextSize: 200_000},
		{Cost: 1.5, HasCost: true, Currency: "EUR"},
		{ContextUsed: 70_000, ContextSize: 200_000},
	}
	for i, w := range want {
		if got := ev.next(t).(Usage); got != w {
			t.Errorf("usage %d = %+v, want %+v", i, got, w)
		}
	}
	if _, ok := ev.next(t).(Plan); !ok {
		t.Error("an update with nothing stated was emitted")
	}
}

// TestACPModelFromSession reads the model from session/new's models, by its
// name when the agent lists one.
func TestACPModelFromSession(t *testing.T) {
	for _, tc := range []struct {
		models any
		want   string
	}{
		{map[string]any{"currentModelId": "anthropic/claude-sonnet-4-6", "availableModels": []any{
			map[string]any{"modelId": "openai/o5", "name": "o5"},
			map[string]any{"modelId": "anthropic/claude-sonnet-4-6", "name": "Claude Sonnet 4.6"},
		}}, "Claude Sonnet 4.6"},
		{map[string]any{"currentModelId": "m1"}, "m1"},
		{nil, ""},
		{"not an object", ""},
	} {
		r, w, p := newPeer(t)
		a := NewACP(r, w, "1", newEvents().emit)
		done := make(chan Info, 1)
		go func() {
			info, _ := a.Start(context.Background(), "/src")
			done <- info
		}()
		p.respond(p.expect("initialize"), map[string]any{"protocolVersion": 1})
		sess := p.expect("session/new")
		res := map[string]any{"sessionId": "s-1"}
		if tc.models != nil {
			res["models"] = tc.models
		}
		raw, _ := json.Marshal(res)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		p.respond(sess, m)
		if info := <-done; info.Model != tc.want {
			t.Errorf("models %v: model %q, want %q", tc.models, info.Model, tc.want)
		}
	}
}
