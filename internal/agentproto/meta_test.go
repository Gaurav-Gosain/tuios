package agentproto

import (
	"context"
	"encoding/json"
	"testing"
)

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
