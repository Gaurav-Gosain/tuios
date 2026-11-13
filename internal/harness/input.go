package harness

import "tuios.dev/harness"

// How tuios types a prompt into a harness, from the manifest's [input] block
// (harness.Input).

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
	// Images is Input.Images, path_text when the manifest says nothing.
	Images string
}

// DefaultInputProfile is the profile for a pane with no harness, or one whose
// manifest has no [input] block: carriage return, and bracketed paste when the
// pane asks for it.
func DefaultInputProfile() InputProfile {
	return InputProfile{SubmitKey: "\r", BracketedPaste: true, Images: harness.ImagesPathText}
}

// InputProfile returns the harness's input profile, or the default for an
// unknown id.
func (r *Registry) InputProfile(id string) InputProfile {
	p := DefaultInputProfile()
	m := r.Lookup(id)
	if m == nil {
		return p
	}
	if m.Input.Submit == harness.SubmitLF {
		p.SubmitKey = "\n"
	}
	if m.Input.BracketedPaste != nil {
		p.BracketedPaste = *m.Input.BracketedPaste
	}
	p.FocusBeforeSubmit = m.Input.FocusBeforeSubmit
	if m.Input.Images != "" {
		p.Images = m.Input.Images
	}
	return p
}
