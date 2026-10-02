//go:build !slim

package session

import "testing"

// TestALinkTheHubDidNotVouchForCannotVerify covers the other half of the link
// rule. A stream the hub did not vouch for arrives on the plain link socket:
// its caller runs inside one of the hub's panes, or the hub predates the
// check. An attach through it is issued no nonce, and a reply sent over it
// with the nonce of a vouched link attach is still a claim, so an agent on the
// hub cannot borrow the person's standing on this machine.
func TestALinkTheHubDidNotVouchForCannotVerify(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	plain := dialLink(t, sp)

	plainTUI := attachTUI(t, LinkSocketPath(sp), "work")
	if n := plainTUI.HumanNonce(); n != "" {
		t.Errorf("an attach over the plain link socket was issued nonce %q", n)
	}
	vouchedTUI := attachTUI(t, LinkHumanSocketPath(sp), "work")
	if vouchedTUI.HumanNonce() == "" {
		t.Fatal("an attach over the link-human socket was issued no nonce")
	}
	if _, m := sendAsHuman(t, plain, "work", a, vouchedTUI.HumanNonce()); humanMark(m) != "claimed" {
		t.Errorf("a reply over the plain link socket with a vouched attach's nonce is %s, want claimed", humanMark(m))
	}
}
