// Package harness is the tuios policy over tuios.dev/harness, the library
// that recognises coding-agent CLIs and reads their state from manifests.
//
// The library answers which agent a process is and what its screen, title
// and notifications say. This package holds what tuios does with that: the
// keys that answer a prompt, the command that resumes a conversation, the
// flags a new agent starts with, how a prompt is typed, the permission ladder
// per harness, the pane label a title gives, the limit text, and where a
// person's own manifests live. It re-exports the library names the rest of
// tuios uses, so a caller imports one package.
package harness

import (
	"os"
	"path/filepath"

	"tuios.dev/harness"
)

// Registry is the library's registry with the tuios policy methods on it.
type Registry struct {
	*harness.Registry
}

// Load builds the registry from the bundled manifests and the manifests in
// dirs. See harness.Load.
func Load(dirs ...string) (*Registry, []LoadError) {
	r, errs := harness.Load(dirs...)
	return &Registry{r}, errs
}

// SharedServer is harness.Registry.SharedServer. A nil registry knows no
// harness.
func (r *Registry) SharedServer(id string) bool {
	return r != nil && r.Registry.SharedServer(id)
}

// HasBoxRule is harness.Registry.HasBoxRule. A nil registry knows no
// harness.
func (r *Registry) HasBoxRule(id string) bool {
	return r != nil && r.Registry.HasBoxRule(id)
}

// BoxDraft is harness.Registry.BoxDraft. A nil registry knows no harness.
func (r *Registry) BoxDraft(id string, tail, shown []string) (draft, known bool) {
	if r == nil {
		return false, false
	}
	return r.Registry.BoxDraft(id, tail, shown)
}

// UserDir is where a user's own manifests live. It follows XDG, so a manifest
// dropped there is picked up on the next daemon start with no rebuild.
func UserDir() string {
	if dir := os.Getenv("TUIOS_HARNESS_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tuios", "harnesses")
}

// The library types tuios names.
type (
	Manifest     = harness.Manifest
	LoadError    = harness.LoadError
	ProcInfo     = harness.ProcInfo
	RuleReport   = harness.RuleReport
	DetectReport = harness.DetectReport
	ProcReport   = harness.ProcReport
	Transcript   = harness.Transcript
	Launch       = harness.Launch
	Input        = harness.Input
	Answers      = harness.Answers
	Answer       = harness.Answer
)

// The library constants tuios names.
const (
	PromptKindApproval  = harness.PromptKindApproval
	PromptKindQuestion  = harness.PromptKindQuestion
	PromptKindAuth      = harness.PromptKindAuth
	ActionApprove       = harness.ActionApprove
	ActionApproveAlways = harness.ActionApproveAlways
	ActionDeny          = harness.ActionDeny
	ActionChoose        = harness.ActionChoose
	ActionText          = harness.ActionText
	ChooseDigit         = harness.ChooseDigit
	ReaderJSONL         = harness.ReaderJSONL
	ImagesNone          = harness.ImagesNone
	ImagesPathPaste     = harness.ImagesPathPaste
	ImagesPathText      = harness.ImagesPathText
	PartialSuffix       = harness.PartialSuffix
	DefaultScreenLines  = harness.DefaultScreenLines
	MaxResumeSessionID  = harness.MaxResumeSessionID
)

// BaseName is harness.BaseName.
func BaseName(s string) string { return harness.BaseName(s) }

// IsInterpreter is harness.IsInterpreter.
func IsInterpreter(base string) bool { return harness.IsInterpreter(base) }

// PackageNamed is harness.PackageNamed.
func PackageNamed(token, name string) bool { return harness.PackageNamed(token, name) }

// BundledCanonical is harness.BundledCanonical.
func BundledCanonical(name string) (string, bool) { return harness.BundledCanonical(name) }

// Describe is harness.Describe.
func Describe(p ProcInfo) ProcReport { return harness.Describe(p) }

// ProgressText is harness.ProgressText.
func ProgressText(state, percent int) string { return harness.ProgressText(state, percent) }

// NotifyText is harness.NotifyText.
func NotifyText(title, body string) string { return harness.NotifyText(title, body) }

// GuessPromptKind is harness.GuessPromptKind.
func GuessPromptKind(text string) string { return harness.GuessPromptKind(text) }

// LaunchValueOK is harness.LaunchValueOK.
func LaunchValueOK(v string) bool { return harness.LaunchValueOK(v) }

// ValidResumeSessionID is harness.ValidResumeSessionID.
func ValidResumeSessionID(id string) bool { return harness.ValidResumeSessionID(id) }

// CleanPromptLine is harness.CleanPromptLine.
func CleanPromptLine(s string) string { return harness.CleanPromptLine(s) }
