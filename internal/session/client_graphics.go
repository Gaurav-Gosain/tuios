package session

import "fmt"

// ClientGraphicsPayload is the body of MsgClientGraphics: what the client's
// terminal draws, as the hello would have said had it known then.
type ClientGraphicsPayload struct {
	SixelGraphics bool `json:"sixel_graphics,omitempty"`
	KittyGraphics bool `json:"kitty_graphics,omitempty"`
	// SymbolImages says the client draws a pane's sixel image as block
	// glyphs when its terminal has neither protocol. See
	// HelloPayload.SymbolImages.
	SymbolImages bool `json:"symbol_images,omitempty"`
}

// handleClientGraphics records a client's graphics after the hello and
// recounts what the session's panes are told. An SSH client sends it when its
// terminal's DA1 answer arrives, which is after it attached.
func (d *Daemon) handleClientGraphics(cs *connState, msg *Message) error {
	var p ClientGraphicsPayload
	if err := msg.ParsePayload(&p); err != nil {
		return fmt.Errorf("invalid client graphics payload: %w", err)
	}
	cs.mu.Lock()
	cs.sixelGraphics = p.SixelGraphics
	cs.kittyGraphics = p.KittyGraphics
	cs.symbolImages = p.SymbolImages
	sessionID := cs.sessionID
	cs.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	if s := d.manager.GetSessionByID(sessionID); s != nil {
		s.SetGraphicsCapabilities(p.KittyGraphics, p.SixelGraphics)
	}
	d.refreshTreeOps(sessionID)
	return nil
}

// ReportGraphics tells the daemon what this client's terminal draws, when
// that is learned after the hello. It does nothing on a daemon that did not
// offer MsgClientGraphics.
func (c *TUIClient) ReportGraphics(sixel, kitty, symbols bool) error {
	if !c.graphicsSupported {
		return nil
	}
	msg, err := NewMessage(MsgClientGraphics, &ClientGraphicsPayload{SixelGraphics: sixel, KittyGraphics: kitty, SymbolImages: symbols})
	if err != nil {
		return err
	}
	return c.send(msg)
}
