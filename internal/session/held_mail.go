package session

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Mail another machine keeps for this one.
//
// A machine whose link to this one is down keeps the mail it has for this
// machine in its outbox (host_outbox.go), and its own Inbox says so. This
// machine's person used to hear nothing, though this machine's own link to
// that one is often up: two machines that link each other, one of whose
// links waits for a Tailscale sign-in. Mail between them sat for hours.
//
// So a daemon answers the hello of a link connection with mail_waiting: how
// many messages it holds for the machine that connection named, and its own
// link's status for it. The name is the one the caller gave itself on the
// link, matched against this daemon's [hosts] name for it, so a third machine
// asking learns nothing about mail for anyone else. The answer carries no
// sender, subject or text. The asking machine keeps it on the link
// (federation.MailHeld) and raises one Inbox item, which closes when the count
// goes to zero or that link goes down.

// mailWaitingFor is the hello's mail_waiting for a link connection, or nil
// when nothing waits for its machine.
func (d *Daemon) mailWaitingFor(cs *connState) map[string]any {
	if cs == nil || !cs.viaLink || cs.linkPeer == "" {
		return nil
	}
	n := d.outbox.count(cs.linkPeer)
	if n == 0 {
		return nil
	}
	out := map[string]any{"count": n}
	if d.federation != nil {
		if r, ok := d.federation.Report(cs.linkPeer); ok {
			out["status"] = r.Status
		}
	}
	return out
}

// onMailHeld is federation's OnMailHeld: what a linked machine said it holds
// for this one.
func (d *Daemon) onMailHeld(host string, held federation.MailHeld) {
	d.attention.noteHeldMail(host, held.Count, heldMailWhy(host, held.Status))
}

// heldMailWhy is why the far machine's link to this one is down, said from
// this machine's side. The far machine's own reason names its sign-in page,
// which is for its person, so this machine words it from the status alone.
func heldMailWhy(host string, status federation.Status) string {
	switch status {
	case federation.StatusApproval:
		return "Its link here waits for someone to sign in to Tailscale on " + host + "."
	case federation.StatusUnreachable, federation.StatusConnecting, federation.StatusReconnecting:
		return "Its link here cannot reach this machine now."
	case federation.StatusNoDaemon, federation.StatusNoBinary, federation.StatusIncompatible:
		return "Its link here cannot start tuios on this machine."
	}
	return ""
}

// noteHeldMail opens, updates or closes the held-mail item for host. A count
// the person dismissed stays dismissed until it changes.
func (a *attentionStore) noteHeldMail(host string, count int, why string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := "held\x00" + host
	if count <= 0 {
		delete(a.heldDismissed, host)
		a.closeKeyLocked(key, AttentionClosedResolved)
		return
	}
	if n, ok := a.heldDismissed[host]; ok && n == count {
		return
	}
	delete(a.heldDismissed, host)
	words := "1 message for this machine waits on " + host
	if count > 1 {
		words = strconv.Itoa(count) + " messages for this machine wait on " + host
	}
	if why != "" {
		words += ". " + why
	}
	a.upsertLocked(AttentionItem{
		Kind:    AttentionHeldMail,
		ForHost: host,
		Name:    attentionText(host, attentionMaxSummary),
		Summary: attentionText(words, attentionMaxSummary),
		Count:   count,
	})
}

// noteHeldDismissed records that the person dismissed host's held-mail item
// at count.
func (a *attentionStore) noteHeldDismissed(host string, count int) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.heldDismissed == nil {
		a.heldDismissed = map[string]int{}
	}
	a.heldDismissed[host] = count
}
