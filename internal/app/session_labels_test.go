package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestAdoptSessionLabelsCopiesTheMap guards against the model aliasing a state
// push's map, which would let a later daemon mutation change the client's
// labels behind its back.
func TestAdoptSessionLabelsCopiesTheMap(t *testing.T) {
	names := map[int]string{1: "review"}
	m := &OS{Settings: config.Global}
	m.adoptSessionLabels(&session.SessionState{DisplayName: "Payments API", WorkspaceNames: names})

	names[1] = "mutated"
	if got := m.WorkspaceLabel(1); got != "review" {
		t.Errorf("workspace label = %q, want review: the model aliased the pushed map", got)
	}
	if m.SessionDisplayName != "Payments API" {
		t.Errorf("SessionDisplayName = %q, want Payments API", m.SessionDisplayName)
	}
}
