package session

import "errors"

// TypeAtPromptPayload is the body of MsgTypeAtPrompt.
type TypeAtPromptPayload struct {
	PTYID string `json:"pty_id"`
	Text  string `json:"text"`
}

// PromptTypedPayload is the body of MsgPromptTyped.
type PromptTypedPayload struct {
	// Typed says the text was written.
	Typed bool `json:"typed"`
}

// ErrTypeAtPromptUnsupported is what TypeAtPrompt returns for a daemon that
// does not answer MsgTypeAtPrompt. The caller types nothing.
var ErrTypeAtPromptUnsupported = errors.New("the daemon cannot check a pane's prompt")

// shellAtPrompt reports whether the pane's own shell holds its terminal: the
// kernel's foreground process group is the shell's. It fails closed. A pane
// whose shell has exited, or a platform where the kernel does not say, is not
// at a prompt, whatever the pane last reported.
func shellAtPrompt(pty *PTY) bool {
	if pty == nil || pty.IsExited() {
		return false
	}
	pid := pty.ShellPID()
	if pid <= 0 {
		return false
	}
	pgid, ok := readForegroundPGID(pid)
	return ok && pgid == pid
}

// handleTypeAtPrompt writes text to a pane of the attached session if the
// pane's shell is at its prompt, and says whether it did.
//
// It carries the authority MsgInput carries and no more: the connection must
// be attached to the session the pane is in, and a link needs write.
func (d *Daemon) handleTypeAtPrompt(cs *connState, msg *Message) error {
	var p TypeAtPromptPayload
	if err := msg.ParsePayload(&p); err != nil {
		return d.sendMessage(cs, MsgPromptTyped, &PromptTypedPayload{})
	}
	typed := false
	if cs.sessionID != "" {
		if sess := d.manager.GetSessionByID(cs.sessionID); sess != nil {
			if pty := sess.GetPTY(p.PTYID); pty != nil && shellAtPrompt(pty) {
				if _, err := pty.Write([]byte(p.Text)); err == nil {
					typed = true
					sess.TouchActive()
				}
			}
		}
	}
	return d.sendMessage(cs, MsgPromptTyped, &PromptTypedPayload{Typed: typed})
}

// TypeAtPrompt asks the daemon to write text to a pane only if the pane's
// shell is at its prompt, and reports whether it did. A daemon that predates
// the request gets none, and the answer is ErrTypeAtPromptUnsupported.
func (c *TUIClient) TypeAtPrompt(ptyID, text string) (bool, error) {
	if !c.typeAtPromptSupported {
		return false, ErrTypeAtPromptUnsupported
	}
	msg, err := NewMessage(MsgTypeAtPrompt, &TypeAtPromptPayload{PTYID: ptyID, Text: text})
	if err != nil {
		return false, err
	}
	resp, err := c.sendAndWaitResponse(msg, MsgPromptTyped, MsgError)
	if err != nil {
		return false, err
	}
	if resp.Type != MsgPromptTyped {
		return false, errors.New("the daemon refused the request")
	}
	var out PromptTypedPayload
	if err := resp.ParsePayload(&out); err != nil {
		return false, err
	}
	return out.Typed, nil
}
