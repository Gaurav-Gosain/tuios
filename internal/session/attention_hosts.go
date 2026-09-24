package session

import (
	"cmp"
	"regexp"
	"slices"
	"time"
)

// Inbox items from other machines.
//
// A hub follows the queue of every linked host (host_fleet.go) and mirrors it
// here, so the one Inbox the person reads covers every machine. A mirrored item
// is the host's own item with host set and an id of this daemon's making, the
// host's name, a colon and the host's id, which cannot collide with an id of
// this machine's: those are plain numbers and a host name holds no colon.
//
// What a host sends is data from another machine and it is treated that way. An
// item is only ever displayed: nothing a host sends here runs a command, types
// into a pane or marks anything on this machine. Its text is cleaned and cut the
// way a local summary is, its kind has to be one this build knows, and a host
// can hold at most hostAttentionMaxItems rows here, kept apart from this
// machine's items so a busy or hostile host cannot evict one of them.
//
// Items from a host are not saved. The host keeps its own queue, and a restart
// here lists it again.
//
// Dismissing a host item hides it here and nowhere else. Whether the person has
// dealt with something is a fact about the person reading, so a second hub
// linked to the same host, or a client attached there, still sees it. The item
// comes back when the host changes it.

// hostAttentionMaxItems bounds the rows one host can place in this Inbox.
const hostAttentionMaxItems = 256

// hostAttentionMaxOptions bounds the options a mirrored item keeps.
const hostAttentionMaxOptions = 16

// hostItemIDPattern is what a host's own item id may be. A daemon issues plain
// numbers; the pattern is wider than that so a later id scheme still mirrors,
// and narrow enough that an id is never more than a token.
var hostItemIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// hostItemID is the id a mirrored item has here.
func hostItemID(host, id string) string { return host + ":" + id }

// hostCloseReasons are the close reasons a host's close is reported with. A
// reason this build does not know reads as resolved.
var hostCloseReasons = []string{
	AttentionClosedResolved, AttentionClosedSeen, AttentionClosedRead,
	AttentionClosedDismissed, AttentionClosedWindow, AttentionClosedSession,
	AttentionClosedEvicted, AttentionClosedSnoozed,
}

// sanitizeHostItem turns an item a host sent into the item mirrored here, or
// reports that it is not one.
func sanitizeHostItem(host string, in AttentionItem, now time.Time) (AttentionItem, bool) {
	// An item the host mirrors from a machine of its own is that machine's to
	// report, so only the host's own items are taken.
	if in.Host != "" || AttentionKindRank(in.Kind) == len(AttentionKindNames) || !hostItemIDPattern.MatchString(in.ID) {
		return AttentionItem{}, false
	}
	// Mail waiting to leave that machine is about its links, not about
	// anything on it a person here can act on, and dismissing it here would
	// discard nothing there.
	if in.Kind == AttentionOutbox {
		return AttentionItem{}, false
	}
	out := AttentionItem{
		ID:            hostItemID(host, in.ID),
		Kind:          in.Kind,
		Host:          host,
		Session:       attentionText(in.Session, 128),
		Window:        attentionText(in.Window, 128),
		Workspace:     min(max(in.Workspace, 0), 999),
		Harness:       attentionText(in.Harness, 64),
		Name:          attentionText(in.Name, attentionMaxSummary),
		Summary:       attentionText(in.Summary, attentionMaxSummary),
		Since:         in.Since,
		Thread:        in.Thread,
		Count:         min(max(in.Count, 0), 1<<20),
		CompletionSeq: in.CompletionSeq,
		remoteSeq:     in.Seq,
	}
	if out.Session == "" {
		return AttentionItem{}, false
	}
	// A wait that starts in the future is a clock on another machine, and the
	// row would show a negative wait. The host's own start is kept otherwise,
	// since the whole wait is what the row reports.
	if nowNano := now.UnixNano(); out.Since <= 0 || out.Since > nowNano {
		out.Since = nowNano
	}
	for i, o := range in.Options {
		if i == hostAttentionMaxOptions {
			break
		}
		out.Options = append(out.Options, attentionText(o, 80))
	}
	// The host's risk marks and the length of its plan are shown here as the
	// host gave them. They are display only: an item of another machine is
	// never answered from this one.
	for i, r := range in.Risk {
		if i == hostAttentionMaxOptions {
			break
		}
		out.Risk = append(out.Risk, attentionText(r, 64))
	}
	if in.Kind == AttentionPlan {
		out.PlanLines = min(max(in.PlanLines, 0), 1<<20)
	}
	return out, true
}

// hostReplace makes the mirror of one host's queue the listing the host just
// gave. Items it holds are opened or updated, and items the mirror holds that
// it does not are closed as resolved: the host dealt with them while nobody was
// listening. It clears the stale mark, since the listing is current.
func (a *attentionStore) hostReplace(host string, items []AttentionItem) {
	if a == nil {
		return
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	keep := make(map[string]bool, len(items))
	for _, raw := range items {
		it, ok := sanitizeHostItem(host, raw, now)
		if !ok {
			continue
		}
		keep[it.ID] = true
		a.upsertHostLocked(it, true)
	}
	for _, id := range a.hostIDsLocked(host) {
		if !keep[id] {
			a.closeHostLocked(id, AttentionClosedResolved)
		}
	}
	for id := range a.hostHidden {
		if hostOfItemID(id) == host && !keep[id] {
			delete(a.hostHidden, id)
		}
	}
}

// hostApply folds one attention event from a host into the mirror.
func (a *attentionStore) hostApply(host, action string, raw AttentionItem) {
	if a == nil {
		return
	}
	it, ok := sanitizeHostItem(host, raw, time.Now())
	if !ok {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch action {
	case AttentionOpened, AttentionUpdated:
		a.upsertHostLocked(it, false)
	case AttentionClosed:
		cur, ok := a.hostItems[it.ID]
		if !ok {
			delete(a.hostHidden, it.ID)
			return
		}
		if it.remoteSeq != 0 && it.remoteSeq < cur.remoteSeq {
			return
		}
		reason := raw.Closed
		if !slices.Contains(hostCloseReasons, reason) {
			reason = AttentionClosedResolved
		}
		a.closeHostLocked(it.ID, reason)
	}
}

// upsertHostLocked opens or updates a mirrored item. The caller holds mu. A
// change older than the one the mirror holds is dropped, and a hidden item
// stays hidden until the host changes it. fromListing says the item came from
// a listing, whose copy is current even at the same revision, which is what
// clears a stale mark.
func (a *attentionStore) upsertHostLocked(it AttentionItem, fromListing bool) {
	if hidden, ok := a.hostHidden[it.ID]; ok {
		if it.remoteSeq <= hidden {
			return
		}
		delete(a.hostHidden, it.ID)
	}
	if cur, ok := a.hostItems[it.ID]; ok {
		if it.remoteSeq < cur.remoteSeq || (it.remoteSeq == cur.remoteSeq && !fromListing && !cur.Stale) {
			return
		}
		it.Seq = cur.Seq
		if attentionSame(*cur, it) && cur.Since == it.Since {
			cur.remoteSeq = it.remoteSeq
			return
		}
		a.rev++
		it.Seq = a.rev
		*cur = it
		a.publish(attentionEvent(AttentionUpdated, *cur))
		return
	}
	if len(a.hostIDsLocked(it.Host)) >= hostAttentionMaxItems {
		return
	}
	a.rev++
	it.Seq = a.rev
	item := it
	a.hostItems[item.ID] = &item
	a.publish(attentionEvent(AttentionOpened, item))
}

// closeHostLocked closes a mirrored item. The caller holds mu.
func (a *attentionStore) closeHostLocked(id, reason string) {
	it, ok := a.hostItems[id]
	if !ok {
		return
	}
	delete(a.hostItems, id)
	a.rev++
	closed := *it
	closed.Seq = a.rev
	closed.Closed = reason
	a.publish(attentionEvent(AttentionClosed, closed))
}

// hideHostLocked is a dismiss of a mirrored item: it closes here and stays
// hidden until the host changes it. The caller holds mu.
func (a *attentionStore) hideHostLocked(id string) {
	it, ok := a.hostItems[id]
	if !ok {
		return
	}
	a.hostHidden[id] = it.remoteSeq
	a.closeHostLocked(id, AttentionClosedDismissed)
}

// hostStale marks every item of a host stale, with when the host was last
// heard from. The link is down, so the rows are the host's last word.
func (a *attentionStore) hostStale(host string, seenAt int64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.hostIDsLocked(host) {
		cur := a.hostItems[id]
		if cur.Stale {
			continue
		}
		cur.Stale, cur.SeenAt = true, seenAt
		a.rev++
		cur.Seq = a.rev
		a.publish(attentionEvent(AttentionUpdated, *cur))
	}
}

// hostFresh clears the stale mark of every item of a host. It follows a resume
// that replayed exactly what was missed, which makes the rows current again.
func (a *attentionStore) hostFresh(host string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.hostIDsLocked(host) {
		cur := a.hostItems[id]
		if !cur.Stale {
			continue
		}
		cur.Stale, cur.SeenAt = false, 0
		a.rev++
		cur.Seq = a.rev
		a.publish(attentionEvent(AttentionUpdated, *cur))
	}
}

// hostDrop closes every item of a host and forgets what was hidden, for a host
// that left the table or now names another machine.
func (a *attentionStore) hostDrop(host, reason string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.hostIDsLocked(host) {
		a.closeHostLocked(id, reason)
	}
	for id := range a.hostHidden {
		if hostOfItemID(id) == host {
			delete(a.hostHidden, id)
		}
	}
}

// hostIDsLocked lists a host's mirrored ids in a stable order, so a sweep
// publishes in the same order every time. The caller holds mu.
func (a *attentionStore) hostIDsLocked(host string) []string {
	var ids []string
	for id, it := range a.hostItems {
		if it.Host == host {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(x, y string) int {
		return cmp.Compare(a.hostItems[x].Seq, a.hostItems[y].Seq)
	})
	return ids
}

// hostOfItemID is the host part of a mirrored item's id.
func hostOfItemID(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[:i]
		}
	}
	return ""
}
