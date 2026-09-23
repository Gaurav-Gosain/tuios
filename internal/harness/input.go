package harness

import (
	"fmt"
	"strings"
)

// Input is how the daemon types a prompt into this harness: the key that
// submits it, whether a paste may be bracketed, and whether the harness needs
// to be told it has focus before it takes a submit.
//
// It is data rather than code because it differs by harness and changes by
// release, and a user whose harness submits on a different key should be able
// to say so in a file. Every field has a default that is right for a TUI in raw
// mode, so a manifest that says nothing gets the behaviour tuios had before
// this block existed.
//
// Source says where the values came from, in words, so a reader can tell a
// measured fact from a documented one and both from a guess.
type Input struct {
	// Submit is the key that submits a prompt: "cr" (carriage return, what
	// the Enter key sends in raw mode) or "lf" (line feed, Ctrl+J). Empty is
	// cr.
	Submit string `toml:"submit"`
	// BracketedPaste says whether the prompt may be sent as a bracketed paste
	// when the pane has DECSET 2004 on. Unset is true. false sends the text
	// raw even when the mode is on, for a harness that turns the mode on and
	// then mishandles a paste.
	BracketedPaste *bool `toml:"bracketed_paste"`
	// FocusBeforeSubmit sends a focus-in report (CSI I) before the prompt,
	// when the pane has focus reporting (DECSET 1004) on. A harness that
	// ignores a submit while it believes it is unfocused needs it.
	FocusBeforeSubmit bool `toml:"focus_before_submit"`
	// Source is where the values above came from.
	Source string `toml:"source"`
}

// Submit keys.
const (
	SubmitCR = "cr"
	SubmitLF = "lf"
)

// normalize checks the block and fills the default submit key.
func (in *Input) normalize() error {
	switch in.Submit = strings.ToLower(strings.TrimSpace(in.Submit)); in.Submit {
	case "":
		in.Submit = SubmitCR
	case SubmitCR, SubmitLF:
	default:
		return fmt.Errorf("submit %q (cr or lf)", in.Submit)
	}
	return nil
}

// InputProfile is what the prompt submitter needs from a harness's [input]
// block, with the defaults applied.
type InputProfile struct {
	// SubmitKey is the byte sequence that submits: "\r" or "\n".
	SubmitKey string
	// BracketedPaste is false when the harness must never get a bracketed
	// paste, whatever mode the pane is in.
	BracketedPaste bool
	// FocusBeforeSubmit asks for a focus-in report before the prompt.
	FocusBeforeSubmit bool
}

// DefaultInputProfile is the profile for a pane with no harness, or one whose
// manifest has no [input] block: carriage return, and bracketed paste when the
// pane asks for it.
func DefaultInputProfile() InputProfile {
	return InputProfile{SubmitKey: "\r", BracketedPaste: true}
}

// InputProfile returns the harness's input profile, or the default for an
// unknown id.
func (r *Registry) InputProfile(id string) InputProfile {
	p := DefaultInputProfile()
	m := r.Lookup(id)
	if m == nil {
		return p
	}
	if m.Input.Submit == SubmitLF {
		p.SubmitKey = "\n"
	}
	if m.Input.BracketedPaste != nil {
		p.BracketedPaste = *m.Input.BracketedPaste
	}
	p.FocusBeforeSubmit = m.Input.FocusBeforeSubmit
	return p
}
