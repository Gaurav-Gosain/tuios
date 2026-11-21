package app

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestAskpassDialogRecoversFromAFailedAnswer is the dialog after an
// answer-ask call that does not get through, as in a daemon that stalls past
// the call's timeout. The failure gives the keyboard back, and esc while an
// answer is on its way closes the dialog here without waiting for the
// daemon.
//
// Negative control: without askpassAnswered in applyInboxAskAnswered, sent
// stays true and every key, esc included, does nothing.
func TestAskpassDialogRecoversFromAFailedAnswer(t *testing.T) {
	m := newTestOS(newTestWindow(t, "askpass-0001", 40, 10))
	it := session.AttentionItem{ID: "1", RequestID: "r1", Secret: &session.SecretAsk{Kind: "passphrase", Prompt: "Enter passphrase"}}
	m.askpass = askpassState{open: &it, openedAt: time.Now().Add(-time.Second), sent: true}

	m.applyInboxAskAnswered(InboxAskAnsweredMsg{Name: "pane", Err: errors.New("the daemon did not answer")})
	if m.askpass.sent {
		t.Fatal("a failed answer left the dialog waiting for it")
	}
	_ = m.AskpassKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if string(m.askpass.buf) != "x" {
		t.Fatalf("the dialog took no key after a failed answer: %q", m.askpass.buf)
	}

	m.askpass.sent = true
	_ = m.AskpassKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.AskpassOpen() || m.askpass.hidden != "r1" || len(m.askpass.buf) != 0 {
		t.Fatalf("esc while an answer was on its way did not close the dialog: %+v", m.askpass)
	}
}
