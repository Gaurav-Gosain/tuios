package mcp

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestToolAnnotationsFollowTheSpec is a security boundary: a client
// auto-approves a tool that says readOnlyHint, so the hint must be true only
// for a tool that changes nothing, and a tool that types into a pane must say
// it is destructive. The table names every tool, so a new tool fails here until
// someone decides what it is.
func TestToolAnnotationsFollowTheSpec(t *testing.T) {
	type ann struct{ readOnly, destructive, idempotent, openWorld bool }
	readOnly := ann{readOnly: true}
	want := map[string]ann{
		"tuios_list_agents":         readOnly,
		"tuios_list_windows":        readOnly,
		"tuios_get_agent_state":     readOnly,
		"tuios_capture_pane":        readOnly,
		"tuios_peek_prompt":         readOnly,
		"tuios_wait_for":            readOnly,
		"tuios_events":              readOnly,
		"tuios_read_agent_messages": {idempotent: true},
		"tuios_send_agent_message":  {},
		"tuios_set_agent_state":     {idempotent: true},
		"tuios_set_agent_meta":      {idempotent: true},
		"tuios_send_text":           {destructive: true, openWorld: true},
		"tuios_send_keys":           {destructive: true, openWorld: true},
		"tuios_ask_agent":           {destructive: true, openWorld: true},
		"tuios_respond":             {destructive: true, openWorld: true},
		"tuios_fan":                 {destructive: true, openWorld: true},
	}
	var verbs []VerbDoc
	for _, spec := range catalog {
		verbs = append(verbs, VerbDoc{Verb: spec.verb, Description: spec.verb})
	}
	srv := New(Options{Write: true, ScopeAll: true, Verbs: verbs})
	got := srv.toolList()
	if len(got) != len(want) {
		t.Fatalf("%d tools listed, the table names %d: %v", len(got), len(want), srv.ToolNames())
	}
	for _, entry := range got {
		e := entry.(map[string]any)
		name := e["name"].(string)
		w, ok := want[name]
		if !ok {
			t.Errorf("%s is not in the annotation table", name)
			continue
		}
		raw, _ := json.Marshal(e["annotations"])
		var a map[string]bool
		_ = json.Unmarshal(raw, &a)
		if a["readOnlyHint"] != w.readOnly {
			t.Errorf("%s readOnlyHint = %v, want %v", name, a["readOnlyHint"], w.readOnly)
		}
		if a["openWorldHint"] != w.openWorld {
			t.Errorf("%s openWorldHint = %v, want %v", name, a["openWorldHint"], w.openWorld)
		}
		if w.readOnly {
			// The spec gives these two a meaning only when readOnlyHint is false.
			for _, k := range []string{"destructiveHint", "idempotentHint"} {
				if _, present := a[k]; present {
					t.Errorf("%s lists %s although it is read-only", name, k)
				}
			}
			continue
		}
		if a["destructiveHint"] != w.destructive || a["idempotentHint"] != w.idempotent {
			t.Errorf("%s destructive/idempotent = %v/%v, want %v/%v", name, a["destructiveHint"], a["idempotentHint"], w.destructive, w.idempotent)
		}
	}
	// Every tool that types into a pane is destructive, whatever else changes.
	for _, spec := range catalog {
		if spec.write && (spec.hints == nil || !spec.hints.destructive) {
			t.Errorf("%s types into a pane and is not marked destructive", spec.name)
		}
	}
	if !slices.Contains(srv.ToolNames(), "tuios_events") {
		t.Error("tuios_events is not listed")
	}
}
