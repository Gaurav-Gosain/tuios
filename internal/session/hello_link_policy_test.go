package session

import "testing"

// TestHelloSaysTheDaemonHoldsLinksToAPolicy is a security boundary, so it
// runs in both builds. A link proxy that finds no link socket asks hello on
// the main socket (daemonHoldsLinkPolicy in link_dial.go). link_policy true
// makes it refuse the link. Without it, the proxy relays the other machine
// over the main socket, which is held to no link policy at all. A tuios-slim
// daemon opens no link socket, so it must say true too: then a full proxy
// refuses to reach it, which is the safe answer. pane_grants tells the CLI it
// may present a pane token, which a strict config depends on.
func TestHelloSaysTheDaemonHoldsLinksToAPolicy(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"hello","params":{"client":"test","protocol":1}}`))
	for _, key := range []string{"link_policy", "pane_grants"} {
		if v, ok := res[key].(bool); !ok || !v {
			t.Errorf("hello answered %s = %v, want true; the whole answer: %v", key, res[key], res)
		}
	}
}
