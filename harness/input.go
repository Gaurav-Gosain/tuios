package harness

import (
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
	// Images is how the harness takes an image the person attached to a
	// queued reply: "path_paste" (the file's path in a paste of its own,
	// which the harness reads as an attached image), "path_text" (the path
	// in the text, for the agent to open), or "none" (refused). Empty is
	// path_text.
	Images string `toml:"images"`
	// ImagesSource is where the images value came from: measured, or not.
	ImagesSource string `toml:"images_source"`
	// Source is where the values above came from.
	Source string `toml:"source"`
}

// Ways a harness takes an image, Input.Images.
const (
	ImagesPathPaste = "path_paste"
	ImagesPathText  = "path_text"
	ImagesNone      = "none"
)

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
		return errorf("submit %q (cr or lf)", in.Submit)
	}
	switch in.Images = strings.ToLower(strings.TrimSpace(in.Images)); in.Images {
	case "":
		in.Images = ImagesPathText
	case ImagesPathPaste, ImagesPathText, ImagesNone:
	default:
		return errorf("images %q (path_paste, path_text or none)", in.Images)
	}
	return nil
}
