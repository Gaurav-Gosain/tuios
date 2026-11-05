package session

import (
	"testing"
	"time"
)

// TestAnOptionLeftWaitingDuringAnAttachReachesTheAttachingClient lands a
// set-option inside an attach. The set-option finds no client, because the
// attaching one is not marked attached yet, and leaves its value waiting only
// after the attach has looked for waiting values. The test holds the route
// order the way set-option does across that gap, and releases it once the
// attach handler has returned.
//
// NEGATIVE CONTROL: with the check for waiting values made outside the route
// order (in handleAttach, before the goroutine starts), the attach finds
// nothing waiting, and the value never reaches the client that shows the
// session.
func TestAnOptionLeftWaitingDuringAnAttachReachesTheAttachingClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "opt")
	sess := d.manager.GetSession("opt")

	// set-option has found no client and holds the route order.
	sess.optionApplyMu.Lock()
	c := attachTUI(t, sp, "opt")
	waitForHandlerIdle(t, c)
	// set-option leaves its value waiting and lets go.
	sess.waitOption("appearance.border_style", "rounded", false)
	sess.optionApplyMu.Unlock()

	got := make(chan *RemoteCommandPayload, 4)
	c.OnRemoteCommand(func(p *RemoteCommandPayload) error {
		got <- p
		return c.SendCommandResult(p.RequestID, true, "")
	})
	c.StartReadLoop()

	select {
	case p := <-got:
		if p.CommandType != "set_config" || p.ConfigPath != "appearance.border_style" || p.ConfigValue != "rounded" {
			t.Fatalf("the client was sent %s %s=%s, want the waiting option", p.CommandType, p.ConfigPath, p.ConfigValue)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an option left waiting during the attach never reached the attached client")
	}
}
