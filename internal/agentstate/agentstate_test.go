package agentstate_test

import (
	"os"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/agentstate"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"tuios.dev/tml"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// TestTMLStatusTakesEveryAgentState holds the tml status component's state
// enum to the agent states. tml is a library that imports nothing of tuios, so
// it keeps its own list; a state added here and not there would not render in
// a status component until tml changes, and a tml change needs a pin move.
// The enum is every state but none: a status with no agent state draws no
// status, so none is left out on purpose.
func TestTMLStatusTakesEveryAgentState(t *testing.T) {
	status, ok := tml.Lookup("status")
	if !ok {
		t.Fatal("tml has no status component")
	}
	var enum []string
	for _, a := range status.Attrs {
		if a.Name == "state" {
			enum = a.Enum
		}
	}
	if len(enum) == 0 {
		t.Fatal("tml's status component has no state enum")
	}
	want := slices.DeleteFunc(agentstate.Names(), func(n string) bool { return n == agentstate.None.Name() })
	if !slices.Equal(enum, want) {
		t.Errorf("tml's status takes states %v, the agent states other than none are %v", enum, want)
	}
}
