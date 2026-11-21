package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

const childQuestionHold = 10 * time.Minute

func childQuestionHoldFor() time.Duration {
	if d := testknob.Millis("child_hold_ms"); d > 0 {
		return d
	}
	return childQuestionHold
}

type childLink struct {
	parent, root PaneRef
	linked       bool
	asksParent   bool
	delegated    bool
}

func childLinkOf(w *WindowState) childLink {
	var l childLink
	if w.AgentLink != nil && w.StartedBy != nil {
		l.parent, l.root, l.linked = *w.StartedBy, w.AgentLink.Root, true
	}
	if r := w.Delegation; r != nil && r.Parent != nil && !delegateFinished(r.State) {
		l.delegated = true
	}
	l.asksParent = l.delegated || l.linked
	return l
}

func samePaneRef(a, b *PaneRef) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// SetChildQuestionsToParent applies [agents.mail] questions_to_parent.
// Turning it off releases every held question.
func (d *Daemon) SetChildQuestionsToParent(on bool) {
	a := d.attention
	if a == nil {
		return
	}
	a.parentFirst.Store(on)
	if on {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.sortedIDsLocked() {
		if it := a.items[id]; it.WithParentUntil != 0 {
			a.releaseParentLocked(it)
		}
	}
	a.armParentLocked()
}

func (a *attentionStore) parentHoldUntilLocked(key string) int64 {
	if cur := a.openOrSnoozedLocked(key); cur != nil && cur.Kind == AttentionQuestion {
		return cur.WithParentUntil
	}
	hold := a.parentHold
	if hold <= 0 {
		hold = childQuestionHoldFor()
	}
	return a.clock().Add(hold).UnixNano()
}

func (a *attentionStore) armParentLocked() {
	var next int64
	for _, it := range a.items {
		if it.WithParentUntil > 0 && (next == 0 || it.WithParentUntil < next) {
			next = it.WithParentUntil
		}
	}
	if next == a.parentAt && (next == 0) == (a.parentTimer == nil) {
		return
	}
	if a.parentTimer != nil {
		a.parentTimer.Stop()
		a.parentTimer = nil
	}
	a.parentGen++
	a.parentAt = next
	if next == 0 {
		return
	}
	gen := a.parentGen
	a.parentTimer = time.AfterFunc(max(time.Duration(next-a.clock().UnixNano()), 0), func() { a.parentFired(gen) })
}

func (a *attentionStore) parentFired(gen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen != a.parentGen {
		return
	}
	a.parentTimer, a.parentAt = nil, 0
	now := a.clock().UnixNano()
	for _, id := range a.sortedIDsLocked() {
		if it := a.items[id]; it.WithParentUntil > 0 && it.WithParentUntil <= now {
			a.releaseParentLocked(it)
		}
	}
	a.armParentLocked()
}

func (a *attentionStore) releaseParentLocked(it *AttentionItem) {
	a.rev++
	it.WithParentUntil = 0
	it.Seq = a.rev
	a.publish(attentionEvent(AttentionUpdated, *it))
	a.changedLocked()
}

func (a *attentionStore) releaseParentHold(sessionName, window string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.byKey[attentionKey(AttentionQuestion, sessionName, window, 0)]
	if !ok || a.items[id].WithParentUntil == 0 {
		return
	}
	a.releaseParentLocked(a.items[id])
	a.armParentLocked()
}

func (a *attentionStore) releaseChildrenOfLocked(window string) {
	if a.parentAt == 0 {
		return
	}
	hit := false
	for _, id := range a.sortedIDsLocked() {
		if it := a.items[id]; it.WithParentUntil != 0 && it.Parent != nil && it.Parent.Window == window {
			a.releaseParentLocked(it)
			hit = true
		}
	}
	if hit {
		a.armParentLocked()
	}
}

func (d *Daemon) childApprovalRefusal(caller string, target WindowState) string {
	if target.AgentLink == nil || target.ID == caller || agentBlockedBy(target) != harness.PromptKindApproval {
		return ""
	}
	return "window " + shortWindowID(target.ID) + " is a child agent waiting on an approval, and only the person answers it. " +
		"Ask the person with send-agent-message -w human"
}
