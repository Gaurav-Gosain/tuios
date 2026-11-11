package session

import (
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// Questions from child agents a parent started with start-agent.
//
// A delegated child's question goes to its parent first (verb_delegate.go,
// attention_children.go). A child started with plain start-agent, which is how
// a lead starts a worker, is linked to its parent the same way but has no
// delegation record, so its question went to the person alone, in a pane
// nobody watched, and the lead that could answer it never heard of it.
//
// So a linked child's question is mailed to its parent with the prompt as
// peek-prompt reads it, once per prompt, and the Inbox holds it for the parent
// as it holds a delegated child's (childQuestionHold, then the person). The
// parent may answer it with respond (parentMayRespond). Never an approval or
// an auth prompt: those reach the person at once and only the person answers
// them (childApprovalRefusal). The person can always answer the question too.

// childAskTable remembers, per child window, the needs_input stamp whose
// question was mailed, so one prompt is mailed once.
type childAskTable struct {
	mu     sync.Mutex
	mailed map[string]int64
}

// first reports whether the question at stamp is new for window, and records it.
func (t *childAskTable) first(window string, stamp int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mailed == nil {
		t.mailed = make(map[string]int64)
	}
	if t.mailed[window] == stamp {
		return false
	}
	t.mailed[window] = stamp
	return true
}

// forget drops window's record, when it closes.
func (t *childAskTable) forget(window string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.mailed, window)
}

// noteChildQuestionEvent folds one session event into the child questions: an
// agent-state event of a linked child that is not a delegated task gets a look.
// It runs on the session's event sink with the state lock held, so the look
// runs off it.
func (d *Daemon) noteChildQuestionEvent(ev SessionEvent) {
	if ev.Type == EventWindowClosed {
		d.childAsks.forget(ev.Window)
		return
	}
	if ev.Type != EventAgentState || !ev.link.linked || ev.link.delegated || ev.State != AgentStateNeedsInput.Name() {
		return
	}
	go d.childQuestionLook(ev.Window)
}

// childQuestionLook mails a linked child's question to its parent, once per
// prompt. A question the parent cannot be told goes to the person at once.
func (d *Daemon) childQuestionLook(window string) {
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return
	}
	w, ok := findWindowState(sess.GetState(), window)
	if !ok || w.AgentLink == nil || w.StartedBy == nil || w.Delegation != nil || w.AgentState != AgentStateNeedsInput {
		return
	}
	if AgentKindReachesPerson(w.AgentKind) || !d.attention.parentFirst.Load() {
		return
	}
	if !d.childAsks.first(window, w.AgentStateAt) {
		return
	}
	q := d.delegateQuestionOf(sess, w)
	name := windowLabelOf(w)
	if name == "" {
		name = shortWindowID(w.ID)
	}
	body := strings.Join(q.Lines, "\n") + "\n\n" +
		"[tuios: answer it with tuios respond -s " + sess.Name() + " -w " + w.ID + " choose N, after tuios peek-prompt shows the choices. " +
		"The person is asked if it still waits in a while. An approval is never yours to answer.]"
	if d.mailParent(*w.StartedBy, "", sess, w, "Your agent "+name+" asks a question", body, "", "") == 0 {
		d.attention.releaseParentHold(sess.Name(), window)
	}
}

// parentMayRespond reports whether the pane window may answer target's
// prompt as its parent: target is a child agent it started, and the prompt is
// a question, not an approval or an auth prompt. respond still reads the live
// prompt and checks its kind again before it answers.
func parentMayRespond(window string, target WindowState) bool {
	return window != "" && target.AgentLink != nil && target.StartedBy != nil &&
		target.StartedBy.Window == window && agentBlockedBy(target) == harness.PromptKindQuestion
}
