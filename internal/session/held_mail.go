package session

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

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

func (d *Daemon) onMailHeld(host string, held federation.MailHeld) {
	d.attention.noteHeldMail(host, held.Count, heldMailWhy(host, held.Status))
}

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
