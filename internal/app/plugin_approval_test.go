package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestPluginApprovalTakesOnlyTheTypedWord holds the approval dialog to its
// one way to allow: the typed word and Enter, after the dialog has settled.
// Review of #1006, B2: the dialog took keys as it opened, and Tab then Enter
// from the default row wrapped to Allow and allowed the plugin. A key or a
// click alone must never allow. Ways it could pass wrongly, written down
// first: the clock could not move (each case sets it), the caller could be
// called without the cursor ever reaching Allow (the Up, Up case reaches it),
// and a deny could be counted as an allow (the call records allow).
func TestPluginApprovalTakesOnlyTheTypedWord(t *testing.T) {
	now := time.Unix(1000, 0)
	pluginApprovalNow = func() time.Time { return now }
	defer func() { pluginApprovalNow = time.Now }()
	var calls []map[string]any
	pluginCaller = func(_ string, params map[string]any) error {
		calls = append(calls, params)
		return nil
	}
	defer func() { pluginCaller = callPluginMethod }()

	open := func() *OS {
		m := &OS{}
		m.setPluginAsks([]session.PluginApprovalAsk{{ID: "p", Name: "P", Trust: "t", Grants: []string{"write"}}})
		if !m.PluginApprovalOpen() {
			t.Fatal("the dialog did not open")
		}
		return m
	}
	press := func(m *OS, keys ...string) {
		for _, k := range keys {
			text := ""
			if len(k) == 1 {
				text = k
			}
			if cmd := m.PluginApprovalKey(k, text); cmd != nil {
				cmd()
			}
		}
	}
	allowed := func() bool {
		for _, c := range calls {
			if c["allow"] == true {
				return true
			}
		}
		return false
	}

	// Keys as it opens answer nothing, also the word.
	m := open()
	press(m, "a", "l", "l", "o", "w", "enter")
	if allowed() || !m.PluginApprovalOpen() {
		t.Fatalf("keys in the first half second answered the dialog: %v", calls)
	}

	// Keys meant for a shell, after it settled, allow nothing.
	now = now.Add(time.Second)
	m = open()
	now = now.Add(time.Second)
	press(m, "up", "up", "enter", "tab", "enter", "down", "down", "down", "up", "up", "up", "enter")
	if allowed() {
		t.Fatalf("keys alone allowed the plugin: %v", calls)
	}
	if m.pluginApproval.selected != pluginRowAllow {
		t.Fatalf("the cursor is on row %d, want Allow: the test did not reach it", m.pluginApproval.selected)
	}

	// A click on Allow asks for the word. A click on Deny needs a second.
	if cmd := m.PluginApprovalActivate(pluginRowAllow); cmd != nil || allowed() {
		t.Fatal("a click on Allow answered")
	}
	if cmd := m.PluginApprovalActivate(pluginRowDeny); cmd != nil {
		t.Fatal("one click on Deny answered")
	}

	// Up then Enter puts the cursor on Deny and asks again. A second
	// Enter denies.
	denied := func() bool {
		for _, c := range calls {
			if c["allow"] == false {
				return true
			}
		}
		return false
	}
	md := open()
	now = now.Add(time.Second)
	press(md, "up", "enter")
	if denied() {
		t.Fatal("Up then Enter denied the plugin without a second Enter")
	}
	press(md, "enter")
	if !denied() {
		t.Fatal("a second Enter on Deny did not deny")
	}

	// Keys a pane or a script sends through the client are not the
	// person's: send-keys of allow and Enter answers nothing.
	mr := open()
	now = now.Add(time.Second)
	mr.ProcessingRemoteKeys = true
	press(mr, "a", "l", "l", "o", "w", "enter")
	if allowed() {
		t.Fatal("keys sent through the client allowed the plugin")
	}
	mr.ProcessingRemoteKeys = false

	// The word and Enter allow it.
	press(m, "a", "l", "l", "o", "w", "enter")
	if !allowed() {
		t.Fatalf("allow and Enter did not allow: %v", calls)
	}
}
