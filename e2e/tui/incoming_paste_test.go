package tuie2e

import (
	"bytes"
	"encoding/base64"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// A bracketed-paste burst arriving from the OUTER terminal (ESC[200~ ... ESC[201~)
// is passthrough input, not a clipboard operation. fcitx5 and other IMEs commit
// text wrapped exactly this way, and the burst a real PTY delivers is byte for
// byte what tuios receives here. It must reach the focused pane and must NOT
// raise the "Pasted N characters" notification that a real clipboard paste does.
// This is the regression from issue #113.
//
// The payload is multi-byte CJK on purpose: nothing in the paste path may assume
// one byte per character.
func TestIncomingBracketedPasteReachesPaneSilently(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// The inner /bin/sh does not enable bracketed paste, so tuios forwards the
	// text raw and the shell echoes it at the prompt. Sending the outer markers
	// is what a real terminal (or fcitx5) does; tuios's input parser turns them
	// into a single paste event.
	if err := term.Type("\x1b[200~中文\x1b[201~"); err != nil {
		t.Fatalf("send bracketed paste burst: %v", err)
	}

	if err := term.WaitForText("中文", shellTimeout); err != nil {
		t.Fatalf("the pasted CJK text never reached the pane: %v\n%s", err, term.Snapshot())
	}
	if got := dockRow(term.Screen()); strings.Contains(got, "Pasted") {
		t.Fatalf("an incoming terminal paste raised a %q notification; IME/terminal "+
			"paste must be silent (issue #113)\ndock row: %q\n%s", "Pasted", got, term.Snapshot())
	}
	alive(t, term, "after an incoming bracketed paste")
}

// osc52Responder mirrors the PTY output stream and, once armed, answers the
// first OSC 52 clipboard read query with a fixed payload, exactly as a real
// terminal with a populated clipboard would. This is what lets the harness
// exercise tuios's OWN clipboard paste end to end: Ctrl+Shift+V emits the query,
// the terminal answers, and tuios pastes the answer.
type osc52Responder struct {
	mu      sync.Mutex
	buf     []byte
	term    *tuitest.Terminal
	replyB  string
	armed   bool
	replied bool
}

func (r *osc52Responder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	// An OSC 52 read query is ESC ] 52 ; <selection> ; ? <terminator>. Matching
	// the "]52;" introducer and a trailing "?" is enough to recognize it without
	// depending on the selection byte or terminator.
	if r.armed && !r.replied && r.term != nil &&
		bytes.Contains(r.buf, []byte("]52;")) && bytes.Contains(r.buf, []byte("?")) {
		r.replied = true
		term, reply := r.term, r.replyB
		// Answer off the reader goroutine so this Write does not block.
		go func() { _ = term.Type("\x1b]52;c;" + reply + "\x07") }()
	}
	return len(p), nil
}

func (r *osc52Responder) arm(term *tuitest.Terminal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.term = term
	r.buf = r.buf[:0]
	r.armed = true
}

// TUIOS's own clipboard paste (Ctrl+Shift+V, which reads the clipboard over
// OSC 52) is a genuine clipboard operation: it pastes the clipboard content AND
// notifies. This is the other side of issue #113 and must keep working, so the
// two paths are proven distinct: incoming terminal paste is silent, clipboard
// paste notifies.
func TestClipboardPasteStillPastesAndNotifies(t *testing.T) {
	const clip = "CLIPPASTEXYZ"
	resp := &osc52Responder{replyB: base64.StdEncoding.EncodeToString([]byte(clip))}

	term, _ := start(t, startOpts{cols: 120, rows: 40, out: resp})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	resp.arm(term)

	// Ctrl+Shift+V in the Kitty CSI u encoding: 'v' is 118, modifier 6 is
	// ctrl+shift. tuios reads this as a clipboard-paste request and emits the
	// OSC 52 query the responder answers.
	if err := term.SendKeys(tuitest.Key("\x1b[118;6u")); err != nil {
		t.Fatalf("send ctrl+shift+v: %v", err)
	}

	if err := term.WaitForText(clip, shellTimeout); err != nil {
		t.Fatalf("the clipboard content never reached the pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "Pasted")
	}, uiTimeout); err != nil {
		t.Fatalf("a real clipboard paste did not raise the \"Pasted\" notification; the "+
			"clipboard path must stay distinct from the silent incoming-paste path: %v\n%s",
			err, term.Snapshot())
	}
	alive(t, term, "after a real clipboard paste")
}
