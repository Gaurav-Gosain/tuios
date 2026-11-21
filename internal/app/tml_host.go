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

type tmlFar struct {
	mu     sync.Mutex
	attach *session.TUIClient
	client *session.VerbClient
}

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
			f.forget(c)
			if try > 0 || !session.VerbNotSent(err) {
				return nil, err
			}
		}
	}
}

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
	c, err := dial()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.client != nil && f.attach == attach {
		_ = c.Close()
		return f.client, nil
	}
	if f.client != nil {
		_ = f.client.Close()
	}
	f.client, f.attach = c, attach
	return c, nil
}

func (f *tmlFar) forget(c *session.VerbClient) {
	f.mu.Lock()
	if f.client == c {
		f.client, f.attach = nil, nil
	}
	f.mu.Unlock()
	_ = c.Close()
}

func (f *tmlFar) drop() {
	f.mu.Lock()
	c := f.client
	f.client, f.attach = nil, nil
	f.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (m *OS) tmlResync() {
	m.tml.mu.Lock()
	defer m.tml.mu.Unlock()
	for _, v := range m.tml.views {
		v.version, v.deferred, v.refreshing = 0, nil, false
	}
}

func (m *OS) tmlHostTooOld(w *terminal.Window) bool {
	return w != nil && w.Kind == session.WindowKindTML && m.tml.local == nil &&
		m.DaemonClient != nil && m.DaemonClient.TMLHostTooOld()
}

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
