package session

import (
	"regexp"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// The theme event.
//
// A theme is a client's: each TUI client has its own, and it changes on the
// client, through the theme picker, the settings page, the light and dark pair
// or a theme file. A program that follows the tuios theme had to poll for it
// or ride the after-theme-change hook, which waits until the picker closes.
// So a client reports its theme to the daemon with MsgClientTheme on every
// change, previews from the theme picker and the theme editor included, and
// the daemon publishes it as a theme event. A follower can repaint while the
// person browses themes, and the last event, with preview false, is the theme
// that stays.
//
// The event is opt-in: it reaches only a subscription that names it in types,
// so a subscriber written before it, which names no types, sees nothing new.

// ClientThemePayload is the body of MsgClientTheme: the theme a client shows.
type ClientThemePayload struct {
	// Theme is the theme id, or "none" when no theme is in force and the
	// terminal's own colours show.
	Theme string
	// Background is the background panes are drawn on, as #rrggbb, or empty
	// when the client does not know it yet.
	Background string
	// Light says the background is light, by the rule the mode 2031 notice
	// uses. It means nothing when Background is empty. A plain bool, since
	// gob does not send a pointer to false.
	Light bool
	// Preview says the theme picker or the theme editor shows the theme on
	// the way to a pick. It is not in force until a report with Preview false.
	Preview bool
}

// ThemeEvent is the theme a theme event reports.
type ThemeEvent struct {
	ID         string `json:"id"`
	Background string `json:"background,omitempty"`
	Light      *bool  `json:"light,omitempty"`
	Preview    bool   `json:"preview"`
}

// maxClientThemeFrame bounds a MsgClientTheme frame. The payload is a theme
// id, a colour and two flags.
const maxClientThemeFrame = 1024

// maxThemeIDBytes bounds the theme id a client reports.
const maxThemeIDBytes = 128

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// handleClientTheme publishes the theme a client reports as a theme event in
// the client's session.
func (d *Daemon) handleClientTheme(cs *connState, msg *Message) error {
	var p ClientThemePayload
	if err := msg.ParsePayload(&p); err != nil {
		return errfmt.Errorf("invalid client theme payload: %w", err)
	}
	if p.Theme == "" || len(p.Theme) > maxThemeIDBytes {
		return errfmt.Errorf("client theme id must have 1 to %d bytes", maxThemeIDBytes)
	}
	if p.Background != "" && !hexColour.MatchString(p.Background) {
		return errfmt.Errorf("client theme background %q is not #rrggbb", p.Background)
	}
	cs.mu.Lock()
	sessionID, clientID, pid := cs.sessionID, cs.clientID, cs.peerPID
	cs.mu.Unlock()
	ev := streamEvent{
		Type:     EventTheme,
		ClientID: clientID,
		PID:      pid,
		Theme:    &ThemeEvent{ID: p.Theme, Background: p.Background, Preview: p.Preview},
	}
	if p.Background != "" {
		ev.Theme.Light = &p.Light
	}
	if sess := d.manager.GetSessionByID(sessionID); sess != nil {
		ev.Session = sess.Name()
	}
	d.events.publish(ev)
	return nil
}

// ReportTheme tells the daemon the theme this client shows. It sends nothing
// to a daemon whose welcome did not set ThemeEvents, which would refuse the
// type.
func (c *TUIClient) ReportTheme(p ClientThemePayload) error {
	if c == nil || !c.themeEvents {
		return nil
	}
	msg, err := NewMessage(MsgClientTheme, &p)
	if err != nil {
		return err
	}
	return c.send(msg)
}
