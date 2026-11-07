package session

import (
	"bytes"
	"encoding/gob"
	"testing"
)

// The agent link rides the state sync and the session listing, both gob. A
// client from before the link, and a daemon from before it, must read the
// other side's frames as they always did: gob drops a field the reader does
// not have and leaves a field the writer did not send at its zero value.
// These pin both directions, and that a new peer gets the whole link.

// preLinkWindow is a window as a build before the agent link decodes it.
type preLinkWindow struct {
	ID        string
	StartedBy *PaneRef
}

type preLinkState struct {
	Name    string
	Windows []preLinkWindow
}

type preLinkSummary struct {
	ID    string
	Title string
}

func gobRoundTrip(t *testing.T, in, out any) {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := gob.NewDecoder(&buf).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestAgentLinkWireCompat(t *testing.T) {
	parent := PaneRef{Session: "work", Window: "parent-window"}
	link := &AgentLink{Harness: "claude-code", AgentSessionID: "conv-1", Root: PaneRef{Session: "work", Window: "root-window"}, Depth: 2}
	state := SessionState{Name: "work", Windows: []WindowState{{ID: "child", StartedBy: &parent, AgentLink: link}}}

	t.Run("new to new carries the link", func(t *testing.T) {
		var got SessionState
		gobRoundTrip(t, state, &got)
		w := got.Windows[0]
		if w.StartedBy == nil || *w.StartedBy != parent || w.AgentLink == nil || *w.AgentLink != *link {
			t.Fatalf("got started_by %+v and link %+v, want %+v and %+v", w.StartedBy, w.AgentLink, parent, *link)
		}
	})
	t.Run("new to old reads as before", func(t *testing.T) {
		var got preLinkState
		gobRoundTrip(t, state, &got)
		if len(got.Windows) != 1 || got.Windows[0].ID != "child" || got.Windows[0].StartedBy == nil {
			t.Fatalf("an old reader got %+v", got)
		}
	})
	t.Run("old to new has no link", func(t *testing.T) {
		var got SessionState
		gobRoundTrip(t, preLinkState{Name: "work", Windows: []preLinkWindow{{ID: "child", StartedBy: &parent}}}, &got)
		if w := got.Windows[0]; w.AgentLink != nil || w.StartedBy == nil {
			t.Fatalf("a frame without the link decoded to link %+v, started_by %+v", w.AgentLink, w.StartedBy)
		}
	})
	t.Run("the listing both ways", func(t *testing.T) {
		sum := WindowSummary{ID: "child", Title: "helper", AgentParent: &parent}
		var old preLinkSummary
		gobRoundTrip(t, sum, &old)
		if old.ID != "child" || old.Title != "helper" {
			t.Fatalf("an old reader got %+v", old)
		}
		var got WindowSummary
		gobRoundTrip(t, sum, &got)
		if got.AgentParent == nil || *got.AgentParent != parent {
			t.Fatalf("the listing lost the parent: %+v", got.AgentParent)
		}
		var none WindowSummary
		gobRoundTrip(t, preLinkSummary{ID: "child", Title: "helper"}, &none)
		if none.AgentParent != nil {
			t.Fatalf("a listing without the parent decoded to %+v", none.AgentParent)
		}
	})
}
