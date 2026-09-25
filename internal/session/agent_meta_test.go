package session

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func strp(s string) *string { return &s }

func metaKeys(tokens []AgentMetaToken) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = t.Key + "=" + t.Value
	}
	return out
}

// TestApplyAgentMetaKeepsFirstArrivalOrder: the rail draws the keys in the
// order the pane holds them, so an update to a key already there must not
// move it, or every statusline tick would reshuffle the row.
func TestApplyAgentMetaKeepsFirstArrivalOrder(t *testing.T) {
	cur, _, err := applyAgentMeta(nil, AgentMetaUpdate{
		Keys: []string{"model", "context"}, Values: []*string{strp("opus"), strp("10%")},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := applyAgentMeta(cur, AgentMetaUpdate{
		Keys: []string{"cost", "model", "context"}, Values: []*string{strp("$1"), strp("sonnet"), strp("42%")},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := metaKeys(next), []string{"model=sonnet", "context=42%", "cost=$1"}; !slices.Equal(got, want) {
		t.Errorf("order after update = %v, want %v", got, want)
	}
	// The input is shared with state snapshots and must not be edited in place.
	if got := metaKeys(cur); !slices.Equal(got, []string{"model=opus", "context=10%"}) {
		t.Errorf("applyAgentMeta wrote into its input: %v", got)
	}
}

// TestApplyAgentMetaRemovesAndClears covers the three ways a key goes: null,
// clear by source, and clear of everything.
func TestApplyAgentMetaRemovesAndClears(t *testing.T) {
	cur, _, _ := applyAgentMeta(nil, AgentMetaUpdate{Keys: []string{"a", "b"}, Values: []*string{strp("1"), strp("2")}, Source: "hook"}, 1)
	cur, _, _ = applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"c"}, Values: []*string{strp("3")}, Source: "statusline"}, 1)

	removed, _, _ := applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"a"}, Values: []*string{nil}}, 2)
	if got := metaKeys(removed); !slices.Equal(got, []string{"b=2", "c=3"}) {
		t.Errorf("after removing a: %v", got)
	}
	bySource, _, _ := applyAgentMeta(cur, AgentMetaUpdate{Source: "hook", Clear: true}, 2)
	if got := metaKeys(bySource); !slices.Equal(got, []string{"c=3"}) {
		t.Errorf("clear by source hook left %v, want only the statusline key", got)
	}
	all, _, _ := applyAgentMeta(cur, AgentMetaUpdate{Clear: true}, 2)
	if all != nil {
		t.Errorf("clear with no source left %v", metaKeys(all))
	}
}

// TestApplyAgentMetaPerPaneLimit: the per-pane cap is what bounds what one
// pane makes every client store.
func TestApplyAgentMetaPerPaneLimit(t *testing.T) {
	var cur []AgentMetaToken
	for i := range AgentMetaMaxPerPane {
		var err error
		cur, _, err = applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"k" + string(rune('a'+i%26)) + string(rune('a'+i/26))}, Values: []*string{strp("v")}}, 1)
		if err != nil {
			t.Fatalf("key %d refused: %v", i, err)
		}
	}
	if _, _, err := applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"one-more"}, Values: []*string{strp("v")}}, 1); err == nil {
		t.Error("a pane took more than the per-pane limit")
	}
}

// TestApplyAgentMetaTTL: an expired key is gone from the next write, and a key
// with no TTL is not.
func TestApplyAgentMetaTTL(t *testing.T) {
	cur, _, _ := applyAgentMeta(nil, AgentMetaUpdate{Keys: []string{"short"}, Values: []*string{strp("x")}, TTL: time.Second}, 0)
	cur, _, _ = applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"long"}, Values: []*string{strp("y")}}, 0)
	if cur[0].Expires != int64(time.Second) || cur[1].Expires != 0 {
		t.Fatalf("expiry stamps = %d, %d", cur[0].Expires, cur[1].Expires)
	}
	later, _, _ := applyAgentMeta(cur, AgentMetaUpdate{Keys: []string{"z"}, Values: []*string{strp("z")}}, int64(2*time.Second))
	if got := metaKeys(later); !slices.Equal(got, []string{"long=y", "z=z"}) {
		t.Errorf("after the TTL: %v", got)
	}
}

// TestCleanAgentMetaValue: a value is drawn on the rail, so an escape in it
// would be a terminal sequence the pane gets to write into chrome.
func TestCleanAgentMetaValue(t *testing.T) {
	got, cut := CleanAgentMetaValue("  fix\x1b[31m the\n\tbug  ")
	if got != "fix [31m the bug" || cut {
		t.Errorf("CleanAgentMetaValue = %q (cut %v)", got, cut)
	}
	long := strings.Repeat("é", AgentMetaMaxValue+5)
	got, cut = CleanAgentMetaValue(long)
	if !cut || len([]rune(got)) != AgentMetaMaxValue {
		t.Errorf("a long value came back %d characters, cut %v", len([]rune(got)), cut)
	}
}

// TestDecodeAgentMetaTokensKeepsOrder: JSON object order is the only order a
// caller can express, and a map would lose it.
func TestDecodeAgentMetaTokensKeepsOrder(t *testing.T) {
	keys, values, err := decodeAgentMetaTokens([]byte(`{"zeta":"1","alpha":null,"mid":"3","zeta":"4"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys, []string{"zeta", "alpha", "mid"}) {
		t.Errorf("keys = %v", keys)
	}
	if *values[0] != "4" || values[1] != nil || *values[2] != "3" {
		t.Errorf("values = %v", values)
	}
	if _, _, err := decodeAgentMetaTokens([]byte(`{"a":1}`)); err == nil {
		t.Error("a number value was accepted")
	}
	if _, _, err := decodeAgentMetaTokens([]byte(`["a"]`)); err == nil {
		t.Error("an array was accepted as tokens")
	}
}

// TestAgentMetaClearsWithTheAgent: metadata describes the agent, so a pane
// cleared to none keeps none, or the next agent in it would wear the last
// one's model.
func TestAgentMetaClearsWithTheAgent(t *testing.T) {
	sess := newTestSessionWithWindow(t)
	id := sess.GetState().Windows[0].ID
	if _, err := sess.SetAgentMeta(id, AgentMetaUpdate{Keys: []string{"model"}, Values: []*string{strp("opus")}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sess.ApplyAgentReport(id, AgentReport{State: AgentStateWorking}); err != nil {
		t.Fatal(err)
	}
	if len(sess.GetState().Windows[0].AgentMeta) != 1 {
		t.Fatal("a state report dropped the metadata")
	}
	if _, _, err := sess.ApplyAgentReport(id, AgentReport{State: AgentStateNone}); err != nil {
		t.Fatal(err)
	}
	if m := sess.GetState().Windows[0].AgentMeta; m != nil {
		t.Errorf("metadata outlived the agent: %v", m)
	}
}

// TestAgentMetaTTLIsPrunedByTheDaemon: clients draw only what the state sync
// hands them, so an expired key has to leave the state on its own, with no
// further write to the pane.
func TestAgentMetaTTLIsPrunedByTheDaemon(t *testing.T) {
	sess := newTestSessionWithWindow(t)
	id := sess.GetState().Windows[0].ID
	if _, err := sess.SetAgentMeta(id, AgentMetaUpdate{
		Keys: []string{"short", "long"}, Values: []*string{strp("x"), nil},
		TTL: 30 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.SetAgentMeta(id, AgentMetaUpdate{Keys: []string{"long"}, Values: []*string{strp("y")}}); err != nil {
		t.Fatal(err)
	}
	version := sess.GetState().Version
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := sess.GetState()
		if got := metaKeys(st.Windows[0].AgentMeta); slices.Equal(got, []string{"long=y"}) {
			if st.Version == version {
				t.Error("the prune changed state without bumping the version, so no client hears of it")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the expired key is still in state: %v", metaKeys(st.Windows[0].AgentMeta))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestWindowSummariesAgreeCoversEveryField: the listing comparison decides
// whether the rail rebuilds for another session, and a field it forgets is a
// field whose change the rail never shows. It is written out by hand because
// the struct holds a slice, so this sets each field in turn and expects a
// difference.
func TestWindowSummariesAgreeCoversEveryField(t *testing.T) {
	base := WindowSummary{}
	rt := reflect.TypeOf(base)
	for i := range rt.NumField() {
		other := base
		f := reflect.ValueOf(&other).Elem().Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Int, reflect.Int64:
			f.SetInt(1)
		case reflect.Uint64:
			f.SetUint(1)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]AgentMetaToken{{Key: "k"}}))
		default:
			t.Fatalf("field %s has a kind this test does not know: %s", rt.Field(i).Name, f.Kind())
		}
		if windowSummariesAgree(base, other) {
			t.Errorf("windowSummariesAgree ignores %s", rt.Field(i).Name)
		}
	}
}
