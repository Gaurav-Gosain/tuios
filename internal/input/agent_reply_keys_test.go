package input

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAgentRowXFallsThroughWithNothingQueued: x on an agent row drops a queued
// message only when one waits; on a row with none it opens the rail's own
// menu, as it did before the agent rows had keys.
func TestAgentRowXFallsThroughWithNothingQueued(t *testing.T) {
	o := reachAgentsOS(t)
	o.Windows[0].AgentQueued = 0
	o.Mode = app.WindowManagementMode
	o = pressKey(t, o, "x")
	got := o.RecentActions()
	rail := o.KeybindRegistry.GetSidebarAction("x")
	if len(got) == 0 || got[len(got)-1] != rail {
		t.Fatalf("x on a row with nothing queued ran %v, want the rail's %s", got, rail)
	}
	for _, a := range got {
		if a == config.ActionAgentCancelQueued {
			t.Fatalf("x ran %s with nothing queued", a)
		}
	}
}

// TestInboxReplyLineTakesEveryKey: while the reply line is open every
// printable key is text, including the Inbox's own keys, and esc closes it.
func TestInboxReplyLineTakesEveryKey(t *testing.T) {
	o := railOS(t)
	o.Windows[0].AgentState = "working"
	o.SetInboxVerbCaller(func(string, map[string]any, time.Duration) (json.RawMessage, error) { return nil, nil }, func() string { return "n" })
	if _, handled := o.SidebarAgentReply("", o.Windows[0].ID); !handled || !o.InboxReplyOpen() {
		t.Fatal("the reply line did not open")
	}
	for _, k := range []string{"d", "q", "z", "space", "j"} {
		o = pressKey(t, o, k)
	}
	if !o.InboxReplyOpen() || !o.ShowInbox {
		t.Fatal("an Inbox key closed the reply line")
	}
	o = pressKey(t, o, "esc")
	if o.InboxReplyOpen() || o.ShowInbox {
		t.Error("esc left the reply line or the Inbox the rail opened")
	}
}
