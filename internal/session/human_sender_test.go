package session

import (
	"net"
	"testing"
	"time"
)

// attachTUI attaches a TUI client to session over socketPath, which is the
// daemon's own socket or its link socket, and returns the client.
func attachTUI(t *testing.T, socketPath, session string) *TUIClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := NewTUIClient()
	c.conn = conn
	if err := c.handshake("test", 80, 24, nil); err != nil {
		_ = conn.Close()
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.AttachSession(session, false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	return c
}

// sendAsHuman sends a message from human to window on conn, with nonce when it
// is not empty, and returns the send result and the message as a read gives it
// back.
func sendAsHuman(t *testing.T, conn *verbConn, session, window, nonce string) (map[string]any, map[string]any) {
	t.Helper()
	params := map[string]any{"session": session, "to": window, "from": AgentInboxHuman, "text": "yes, go ahead"}
	if nonce != "" {
		params["human_nonce"] = nonce
	}
	res := result(t, sendJSON(t, conn, 1, params))
	id := res["message_id"]
	for _, m := range readAll(t, conn, session) {
		if m["id"] == id {
			return res, m
		}
	}
	t.Fatalf("the message just sent is not in the ring")
	return nil, nil
}

// humanMark says which of the two marks a message carries.
func humanMark(m map[string]any) string {
	verified, _ := m["verified_human"].(bool)
	claimed, _ := m["claimed_human"].(bool)
	switch {
	case verified && claimed:
		return "both"
	case verified:
		return "verified"
	case claimed:
		return "claimed"
	default:
		return "neither"
	}
}

// TestHumanMailIsVerifiedOnlyWithALiveAttachNonce covers the forged human
// reply. Any process with the socket could send from=human and it was stored
// exactly like the person's own reply. Now it is verified only when it carries
// the nonce of a client attached to the session, and stored as a claim
// otherwise, so an agent can tell the two apart.
func TestHumanMailIsVerifiedOnlyWithALiveAttachNonce(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "mail")
	makeSessionWithWindow(t, d, "other")
	conn := dialVerb(t, sp)

	tui := attachTUI(t, sp, "mail")
	nonce := tui.HumanNonce()
	if len(nonce) != 2*humanNonceBytes {
		t.Fatalf("the attach reply carried nonce %q, want %d hex characters", nonce, 2*humanNonceBytes)
	}

	res, m := sendAsHuman(t, conn, "mail", a, nonce)
	if humanMark(res) != "verified" || humanMark(m) != "verified" {
		t.Errorf("a reply with the live attach nonce: send %s, stored %s, want verified", humanMark(res), humanMark(m))
	}

	for name, n := range map[string]string{"no nonce": "", "a wrong nonce": "00112233445566778899aabbccddeeff"} {
		if _, m := sendAsHuman(t, conn, "mail", a, n); humanMark(m) != "claimed" {
			t.Errorf("from human with %s is %s, want claimed", name, humanMark(m))
		}
	}

	// The nonce vouches for the session it was issued in, not for another.
	if _, m := sendAsHuman(t, conn, "other", "0", nonce); humanMark(m) != "claimed" {
		t.Errorf("a nonce from an attach to another session verified a message here: %s", humanMark(m))
	}

	// A message from a window says nothing about the person either way.
	res = result(t, sendJSON(t, conn, 2, map[string]any{"session": "mail", "to": AgentInboxHuman, "from": a, "text": "done", "human_nonce": nonce}))
	if humanMark(res) != "neither" {
		t.Errorf("a message from a window is marked %s, want neither", humanMark(res))
	}

	// Once the client detaches its nonce stops counting.
	_ = tui.Close()
	deadline := time.Now().Add(5 * time.Second)
	for d.verifyHumanNonce(nonce, d.manager.GetSession("mail").ID, false) {
		if time.Now().After(deadline) {
			t.Fatal("the nonce still verified after its client disconnected")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, m := sendAsHuman(t, conn, "mail", a, nonce); humanMark(m) != "claimed" {
		t.Errorf("a reply with the nonce of a closed attach is %s, want claimed", humanMark(m))
	}
}

// TestHumanMailOverALinkIsVerifiedAgainstALinkAttach covers mail from human
// that arrived from another machine. The hub relays the stream without reading
// it, so nothing in the request can vouch for the sender. What can is the
// nonce this daemon issued to a client attached through the link: that reply
// verifies, and every other from=human over the link is a claim, including one
// carrying a nonce issued to a local attach.
func TestHumanMailOverALinkIsVerifiedAgainstALinkAttach(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	link := dialLink(t, sp)
	local := dialVerb(t, sp)

	remoteTUI := attachTUI(t, LinkSocketPath(sp), "work")
	localTUI := attachTUI(t, sp, "work")

	if _, m := sendAsHuman(t, link, "work", a, ""); humanMark(m) != "claimed" {
		t.Errorf("from human over a link with no nonce is %s, want claimed", humanMark(m))
	}
	if _, m := sendAsHuman(t, link, "work", a, remoteTUI.HumanNonce()); humanMark(m) != "verified" {
		t.Errorf("a reply over the link with the link attach's nonce is %s, want verified", humanMark(m))
	}
	if _, m := sendAsHuman(t, link, "work", a, localTUI.HumanNonce()); humanMark(m) != "claimed" {
		t.Errorf("a reply over the link with a local attach's nonce is %s, want claimed", humanMark(m))
	}
	if _, m := sendAsHuman(t, local, "work", a, remoteTUI.HumanNonce()); humanMark(m) != "claimed" {
		t.Errorf("a local reply with the link attach's nonce is %s, want claimed", humanMark(m))
	}
}

// TestReattachIssuesAFreshNonce: the nonce belongs to one attach, so a client
// that attaches again gets a new one and the old one stops counting.
func TestReattachIssuesAFreshNonce(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "one")
	makeSessionWithWindow(t, d, "two")

	tui := attachTUI(t, sp, "one")
	first := tui.HumanNonce()
	if _, err := tui.AttachSession("two", false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if second := tui.HumanNonce(); second == "" || second == first {
		t.Fatalf("a second attach kept nonce %q (first %q)", second, first)
	}
	if d.verifyHumanNonce(first, d.manager.GetSession("one").ID, false) {
		t.Error("the first attach's nonce still verifies after the client moved on")
	}
}
