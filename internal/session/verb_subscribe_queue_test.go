package session

import "testing"

// TestSubscribeQueueIsCapped is a security boundary. The subscribe queue size
// comes from the client, and the daemon allocates the channel whole when the
// stream opens. Any connection with read scope, and a linked machine at the
// lowest link policy, can send it, so an uncapped size let one request take
// gigabytes and get the daemon OOM-killed with every pane in it.
func TestSubscribeQueueIsCapped(t *testing.T) {
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	result(t, c.call(t, `{"id":1,"verb":"subscribe","params":{"queue":100000}}`))

	d.events.mu.Lock()
	defer d.events.mu.Unlock()
	if len(d.events.subs) == 0 {
		t.Fatal("subscribe registered no subscription")
	}
	for sub := range d.events.subs {
		if got := cap(sub.ch); got > maxEventQueue {
			t.Fatalf("subscriber queue cap = %d, want at most %d", got, maxEventQueue)
		}
	}
}
