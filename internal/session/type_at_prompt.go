package session

import (
	"errors"
	"strings"
)

// TypeAtPromptPayload is the body of MsgTypeAtPrompt. It names a directory,
// not text: the daemon builds the cd line itself (CdLine), so the request can
// type nothing but a cd.
type TypeAtPromptPayload struct {
	PTYID string `json:"pty_id"`
	Dir   string `json:"dir"`
	// Clear adds "&& clear" after the cd, as a loaded layout does.
	Clear bool `json:"clear,omitempty"`
}

// CdLine is the command that moves a shell to dir, without the Enter, or
// false when dir must not be typed at all.
//
// The directory is single-quoted, which is correct for a POSIX shell. The
// shell in a pane is not always one: fish reads \' inside single quotes as an
// escaped quote, so the POSIX escape for a quote ends the quoting there and
// the rest of the name runs as commands. No single quoting is right for every
// shell, so a folder whose name holds a quote, a backslash or a control
// character is refused. A relative path is refused too.
func CdLine(dir string) (string, bool) {
	if dir == "" || !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, `'\`) {
		return "", false
	}
	if strings.ContainsFunc(dir, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) }) {
		return "", false
	}
	return "cd '" + dir + "'", true
}

// PromptTypedPayload is the body of MsgPromptTyped.
type PromptTypedPayload struct {
	// Typed says the cd was written.
	Typed bool `json:"typed"`
	// Refused, when set, says why it was not.
	Refused string `json:"refused,omitempty"`
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
	pgid, ok := foregroundPGID(pid)
	return ok && pgid == pid
}

// foregroundPGID is readForegroundPGID, held in a variable so a test can play
// a platform where the kernel does not say.
var foregroundPGID = readForegroundPGID

// handleTypeAtPrompt types a cd into a pane of the attached session if the
// pane's shell is at its prompt, and says whether it did.
//
// It carries no more authority than MsgInput: the connection must be attached
// to the session the pane is in, a link needs write, and a caller that is a
// pane is held to the same grant check as its keystrokes (refuseTypingInto).
// It carries less, because the daemon builds the line from the directory, so
// the request can only ever type one cd.
func (d *Daemon) handleTypeAtPrompt(cs *connState, msg *Message) error {
	var p TypeAtPromptPayload
	if err := msg.ParsePayload(&p); err != nil {
		return d.reply(cs, msg, MsgPromptTyped, &PromptTypedPayload{Refused: "invalid request"})
	}
	out := PromptTypedPayload{}
	line, ok := CdLine(p.Dir)
	var sess *Session
	if cs.sessionID != "" {
		sess = d.manager.GetSessionByID(cs.sessionID)
	}
	var pty *PTY
	if sess != nil {
		pty = sess.GetPTY(p.PTYID)
	}
	switch {
	case !ok:
		out.Refused = "the folder name cannot be typed safely"
	case pty == nil:
		out.Refused = "no such pane in the attached session"
	default:
		if why := d.refuseTypingInto(cs, sess, p.PTYID); why != "" {
			out.Refused = why
			break
		}
		if !shellAtPrompt(pty) {
			out.Refused = "the pane's shell is not at its prompt"
			break
		}
		if p.Clear {
			line += " && clear"
		}
		if _, err := pty.Write([]byte(line + "\r")); err == nil {
			out.Typed = true
			sess.TouchActive()
		}
	}
	return d.reply(cs, msg, MsgPromptTyped, &out)
}

// CdAtPrompt asks the daemon to type a cd to dir into a pane only if the
// pane's shell is at its prompt, and reports whether it did. A daemon that
// predates the request gets none, and the answer is ErrTypeAtPromptUnsupported.
func (c *TUIClient) CdAtPrompt(ptyID, dir string, clear bool) (bool, error) {
	if !c.typeAtPromptSupported {
		return false, ErrTypeAtPromptUnsupported
	}
	msg, err := NewMessage(MsgTypeAtPrompt, &TypeAtPromptPayload{PTYID: ptyID, Dir: dir, Clear: clear})
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
	if !out.Typed && out.Refused != "" {
		return false, errors.New(out.Refused)
	}
	return out.Typed, nil
}
