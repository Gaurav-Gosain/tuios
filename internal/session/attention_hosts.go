package session

import (
	"cmp"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

const hostAttentionMaxItems = 256

const hostAttentionMaxOptions = 16

var hostItemIDPattern = lazyre.New(`^[A-Za-z0-9._-]{1,64}$`)

func hostItemID(host, id string) string { return host + ":" + id }

var hostCloseReasons = []string{
	AttentionClosedResolved, AttentionClosedSeen, AttentionClosedRead,
	AttentionClosedDismissed, AttentionClosedWindow, AttentionClosedSession,
	AttentionClosedEvicted, AttentionClosedSnoozed,
}

func sanitizeHostItem(host string, in AttentionItem, now time.Time) (AttentionItem, bool) {
	if in.Host != "" || AttentionKindRank(in.Kind) == len(AttentionKindNames) || !hostItemIDPattern().MatchString(in.ID) {
		return AttentionItem{}, false
	}
	if in.Kind == AttentionOutbox || in.Kind == AttentionHeldMail {
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
	if nowNano := now.UnixNano(); out.Since <= 0 || out.Since > nowNano {
		out.Since = nowNano
	}
	for i, o := range in.Options {
		if i == hostAttentionMaxOptions {
			break
		}
		out.Options = append(out.Options, attentionText(o, 80))
	}
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
	for _, id := range a.hostSnoozedIDsLocked(host) {
		if !keep[id] {
			a.dropHostSnoozedLocked(id, AttentionClosedResolved)
		}
	}
	for id := range a.hostHidden {
		if hostOfItemID(id) == host && !keep[id] {
			delete(a.hostHidden, id)
		}
	}
}

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
		if _, asleep := a.hostSnoozed[it.ID]; asleep {
			reason := raw.Closed
			if !slices.Contains(hostCloseReasons, reason) {
				reason = AttentionClosedResolved
			}
			a.dropHostSnoozedLocked(it.ID, reason)
			return
		}
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

func (a *attentionStore) upsertHostLocked(it AttentionItem, fromListing bool) {
	if hidden, ok := a.hostHidden[it.ID]; ok {
		if it.remoteSeq <= hidden {
			return
		}
		delete(a.hostHidden, it.ID)
	}
	if asleep, ok := a.hostSnoozed[it.ID]; ok {
		if it.remoteSeq <= asleep.remoteSeq {
			return
		}
		delete(a.hostSnoozed, it.ID)
		a.armWakeLocked()
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

func (a *attentionStore) hideHostLocked(id string) {
	it, ok := a.hostItems[id]
	if !ok {
		return
	}
	a.hostHidden[id] = it.remoteSeq
	a.closeHostLocked(id, AttentionClosedDismissed)
}

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

func (a *attentionStore) hostDrop(host, reason string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.hostIDsLocked(host) {
		a.closeHostLocked(id, reason)
	}
	for _, id := range a.hostSnoozedIDsLocked(host) {
		a.dropHostSnoozedLocked(id, reason)
	}
	for id := range a.hostHidden {
		if hostOfItemID(id) == host {
			delete(a.hostHidden, id)
		}
	}
}

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

func (a *attentionStore) hostSnoozedIDsLocked(host string) []string {
	var ids []string
	for id, it := range a.hostSnoozed {
		if it.Host == host {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func hostOfItemID(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[:i]
		}
	}
	return ""
}
