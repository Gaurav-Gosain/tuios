package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
)

// TML surfaces of a session on another machine. See session/tml_surface.go,
// "Surfaces over a link", for the far daemon's half.
//
// A client attached through a host is a client of the far daemon: its attach
// goes over the link, and the far daemon pushes its surfaces on it as it does
// to a client of its own machine. So the views, the drawing and the keys are
// the ones tml.go has for any surface. Only the calls differ: tml-input,
// tml-get and tml-place go to the far daemon, on a connection over the link
// that tmlFar keeps.

// tmlFar is the verb connection a client attached through a host sends its
// TML calls on. It is kept, so a key or a click costs one round trip over the
// link, not a new stream and two handshakes. The first call opens it, so a
// session with no surface opens none, and it belongs to one attach: a
// reconnect or a switch dials a new one. The zero value is ready.
type tmlFar struct {
	mu     sync.Mutex
	attach *session.TUIClient
	client *session.VerbClient
}

// caller is the call for the far daemon of attach. dial opens a verb
// connection to that daemon through the link.
func (f *tmlFar) caller(attach *session.TUIClient, dial agentMailDial) inboxVerbCall {
	return func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
		for try := 0; ; try++ {
			c, err := f.conn(attach, dial)
			if err != nil {
				return nil, err
			}
			raw, err := c.CallWithTimeout(verb, params, timeout)
			if err == nil {
				return raw, nil
			}
			if _, refused := errors.AsType[*session.VerbCallError](err); refused {
				return nil, err
			}
			// The connection is gone: the link dropped, or the far daemon
			// closed it. A call that never reached the far daemon goes once
			// more on a new connection. One that may have run does not, so
			// an emit never reaches the owner twice.
			f.forget(c)
			if try > 0 || !session.VerbNotSent(err) {
				return nil, err
			}
		}
	}
}

// conn is the connection for attach, dialed when none is held for it.
func (f *tmlFar) conn(attach *session.TUIClient, dial agentMailDial) (*session.VerbClient, error) {
	f.mu.Lock()
	if f.client != nil && f.attach == attach {
		c := f.client
		f.mu.Unlock()
		return c, nil
	}
	old := f.client
	f.client, f.attach = nil, nil
	f.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	// Dialed outside the lock: the dial crosses the link.
	c, err := dial()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.client != nil && f.attach == attach {
		// Another call dialed first.
		_ = c.Close()
		return f.client, nil
	}
	if f.client != nil {
		_ = f.client.Close()
	}
	f.client, f.attach = c, attach
	return c, nil
}

// forget closes c, and stops holding it when it is the connection held.
func (f *tmlFar) forget(c *session.VerbClient) {
	f.mu.Lock()
	if f.client == c {
		f.client, f.attach = nil, nil
	}
	f.mu.Unlock()
	_ = c.Close()
}

// drop closes the connection held. A call on it fails, and the next call
// dials. It never waits for a call.
func (f *tmlFar) drop() {
	f.mu.Lock()
	c := f.client
	f.client, f.attach = nil, nil
	f.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// tmlResync makes every view take the next push of its surface whole: the
// far daemon's surface is the truth. It runs when a link came back, and the
// attach that came back pushes every open surface whole, so a value this
// client set and could not send goes back to the far daemon's.
func (m *OS) tmlResync() {
	m.tml.mu.Lock()
	defer m.tml.mu.Unlock()
	for _, v := range m.tml.views {
		v.version, v.deferred, v.refreshing = 0, nil, false
	}
}

// tmlHostTooOld reports whether w is a TML window of a far daemon that does
// not serve surfaces over a link. See renderTMLHostTooOld.
func (m *OS) tmlHostTooOld(w *terminal.Window) bool {
	return w != nil && w.Kind == session.WindowKindTML && m.tml.local == nil &&
		m.DaemonClient != nil && m.DaemonClient.TMLHostTooOld()
}

// renderTMLHostTooOld draws a TML window of a far daemon that does not serve
// surfaces over a link. It says why the surface is not here and what to do.
// The keys still go to the window's placeholder program, so q closes the
// surface on the far machine.
func (m *OS) renderTMLHostTooOld(window *terminal.Window, dim int) string {
	cw, ch := max(window.ContentWidth(), 1), max(window.ContentHeight(), 1)
	dx, dy, iw, ih := tmldraw.Inset(cw, ch)
	iw, ih = max(iw, 1), max(ih, 1)
	host := cmp.Or(m.AttachedHost, "the other machine")
	pal := theme.UI()
	parts := []struct {
		text  string
		style lipgloss.Style
	}{
		{"tuios on " + host + " is too old to send this surface here.", lipgloss.NewStyle().Foreground(pal.Warning).Bold(true)},
		{"Update it with 'tuios hosts sync " + host + "', then restart its daemon.", lipgloss.NewStyle().Foreground(pal.Fg)},
		{"Press q to close the surface.", lipgloss.NewStyle().Foreground(pal.FgDim)},
	}
	var lines []string
	for _, p := range parts {
		for _, l := range tmlWrapWords(p.text, iw) {
			lines = append(lines, p.style.Render(l))
		}
	}
	lines = lines[:min(len(lines), ih)]
	for len(lines) < ih {
		lines = append(lines, "")
	}
	out := tmldraw.Pad(strings.Join(lines, "\n"), dx, dy, cw, ch)
	cacheRender(window, out, 0, 0, dim)
	return out
}
