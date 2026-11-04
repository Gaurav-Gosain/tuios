package session

import (
	"os"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// Child agents in the Inbox.
//
// A child agent is a pane another agent started (agent_parent.go). What waits
// in it reaches the Inbox like any pane's, with two differences:
//
//   - Every item about a child carries its parent and its root pane, so a
//     client can show a chain's items under the root ("2 waiting in
//     children"). The fields come off the session event, which carries the
//     link (childLink), so the queue reads no session state under its lock.
//
//   - A question from a delegated child, a task delegate started with a
//     parent to answer to, goes to the parent agent first: delegate mails it
//     (verb_delegate.go), and the Inbox holds the item for childQuestionHold
//     with WithParentUntil set. A held item is open, listed and can be
//     answered by a person who looks, but it is not counted and raises no
//     alert. When the time comes with the child still asking, the item is
//     released: the field clears in an update, and every surface counts and
//     alerts on it as on a new item. An answer by the parent moves the child
//     off needs_input, which closes the item as any answer does. The parent
//     pane closing, a mail that found no parent, or the route turned off
//     releases it at once.
//
// One timer releases the held questions: armed for the earliest, and only
// while one is held. A daemon with no held question runs none.
//
// An approval is never held, and never the parent's to answer. A parent that
// could answer its child's approval could widen itself through the child: the
// child's grants are a subset of the parent's (child_clamp.go), but what the
// harness asks before a call is the one gate the parent does not hold. So no
// pane may type into a child agent's approval or answer it with respond
// (childApprovalRefusal), whatever its grants. The person answers it, in the
// Inbox or in the pane.

// childQuestionHold is how long a delegated child's question waits for its
// parent agent before the person is asked.
const childQuestionHold = 10 * time.Minute

// childQuestionHoldFor is childQuestionHold, or the TUIOS_E2E_CHILD_HOLD_MS an
// E2E test sets, so the release can be shown without ten minutes.
func childQuestionHoldFor() time.Duration {
	if os.Getenv("TUIOS_E2E") == "1" {
		if ms, err := strconv.Atoi(os.Getenv("TUIOS_E2E_CHILD_HOLD_MS")); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return childQuestionHold
}

// childLink is what the Inbox reads of a window's place in a chain of
// agents, copied onto the session event.
type childLink struct {
	parent, root PaneRef
	// linked says the window is a child agent: parent and root are set.
	linked bool
	// delegated says a delegated task with a parent to answer to is under
	// way in the window, so its question goes to the parent first.
	delegated bool
}

// childLinkOf reads w's link.
func childLinkOf(w *WindowState) childLink {
	var l childLink
	if w.AgentLink != nil && w.StartedBy != nil {
		l.parent, l.root, l.linked = *w.StartedBy, w.AgentLink.Root, true
	}
	if r := w.Delegation; r != nil && r.Parent != nil && !delegateFinished(r.State) {
		l.delegated = true
	}
	return l
}

// samePaneRef reports whether two optional pane references name one pane.
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

// parentHoldUntilLocked is the WithParentUntil a delegated child's question
// under key gets: the one the open or snoozed question already has, which is
// zero once released, or a new wait. The caller holds mu.
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

// armParentLocked sets the release timer for the earliest held question, or
// stops it when none is held. The caller holds mu.
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
	// A timer whose Stop came too late finds a newer generation and does
	// nothing, as in armWakeLocked.
	a.parentGen++
	a.parentAt = next
	if next == 0 {
		return
	}
	gen := a.parentGen
	a.parentTimer = time.AfterFunc(max(time.Duration(next-a.clock().UnixNano()), 0), func() { a.parentFired(gen) })
}

// parentFired is the release timer of generation gen firing: every held
// question whose time has come is released, and the timer set for the next.
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

// releaseParentLocked makes a held question the person's. The update is what
// counts it and alerts on it. The caller holds mu, and arms the timer again.
func (a *attentionStore) releaseParentLocked(it *AttentionItem) {
	a.rev++
	it.WithParentUntil = 0
	it.Seq = a.rev
	a.publish(attentionEvent(AttentionUpdated, *it))
	a.changedLocked()
}

// releaseParentHold releases the question held for window's parent, if one
// is: the parent could not be told.
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

// releaseChildrenOfLocked releases the questions held for the agent in
// window, which closed. The caller holds mu.
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

// childApprovalRefusal is why no pane may answer target's prompt, "" when
// one may: target is a child agent waiting on an approval, which is the
// person's alone to answer. caller is the calling pane's window. A plan is an
// approval here too: its pane's blocked_by stays approval. respond also reads
// the prompt on the screen (verb_respond.go).
func (d *Daemon) childApprovalRefusal(caller string, target WindowState) string {
	if target.AgentLink == nil || target.ID == caller || agentBlockedBy(target) != harness.PromptKindApproval {
		return ""
	}
	return "window " + shortWindowID(target.ID) + " is a child agent waiting on an approval, and only the person answers it. " +
		"Ask the person with send-agent-message -w human"
}
