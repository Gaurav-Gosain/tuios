package harness

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// Launch is how a new agent of this harness is started with a choice the
// person made: the model, the effort and the permission mode, each as the
// flag the harness takes and the values it accepts. Claude Code's is
//
//	model_flag      = "--model"
//	models          = ["fable", "opus", "sonnet", "haiku"]
//	effort_flag     = "--effort"
//	efforts         = ["low", "medium", "high", "xhigh", "max"]
//	permission_flag = "--permission-mode"
//	permissions     = ["manual", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"]
//	risky_permissions = ["bypassPermissions"]
//
// A harness with no table, or no flag for an option, takes no such choice:
// start-agent refuses it by name rather than guess a flag.
//
// It also says how to ask the harness whether it is signed in, for the New
// agent sheet: SignedInArgs is run on the harness's own program, never on a
// program the manifest names, and its output is read only for the match.
type Launch struct {
	// ModelFlag takes a model name. Models are names to suggest; any other
	// name that is one plain word is passed too, since a harness learns new
	// models faster than a manifest does.
	ModelFlag string   `toml:"model_flag"`
	Models    []string `toml:"models"`
	// EffortFlag takes one of Efforts.
	EffortFlag string   `toml:"effort_flag"`
	Efforts    []string `toml:"efforts"`
	// PermissionFlag takes one of Permissions. RiskyPermissions are the
	// ones that turn approvals off, shown with a warning and never chosen
	// for the person. DefaultPermission is the mode the harness uses with
	// no flag, so a choice of it passes no flag.
	PermissionFlag    string   `toml:"permission_flag"`
	Permissions       []string `toml:"permissions"`
	RiskyPermissions  []string `toml:"risky_permissions"`
	DefaultPermission string   `toml:"default_permission"`
	// SignedInArgs are the arguments that make the harness say whether it
	// is signed in, and SignedInMatch the pattern its output matches when it
	// is. Both or neither.
	SignedInArgs  []string `toml:"signed_in_args"`
	SignedInMatch string   `toml:"signed_in_match"`
	// AddDirFlag gives the harness one more directory it may work in, once
	// per directory (Claude Code's --add-dir). A start-agent preset's
	// add_dirs go through it; a harness without one refuses them.
	AddDirFlag string `toml:"add_dir_flag"`
	// Source is where the flags and values came from, in words.
	Source string `toml:"source"`

	signedIn *regexp.Regexp
}

// launchValueMax bounds a model name, an effort or a permission mode.
const launchValueMax = 128

// LaunchValueOK reports whether v can follow a launch flag as one argument:
// not empty, not a flag itself, no space or control character, and short.
// The argv is exec'd with no shell, so this is about what the harness reads,
// not about quoting.
func LaunchValueOK(v string) bool {
	if v == "" || len(v) > launchValueMax || v[0] == '-' {
		return false
	}
	return !strings.ContainsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// normalize checks the table when the manifest loads.
func (l *Launch) normalize() error {
	l.Source = strings.TrimSpace(l.Source)
	for _, f := range []struct{ name, flag string }{{"model_flag", l.ModelFlag}, {"effort_flag", l.EffortFlag}, {"permission_flag", l.PermissionFlag}, {"add_dir_flag", l.AddDirFlag}} {
		if f.flag != "" && (len(f.flag) < 2 || f.flag[0] != '-' || strings.Contains(f.flag, "=") || !safeResumeToken(f.flag)) {
			return errfmt.Errorf("%s %q: a flag is - or -- and then letters, digits and _ . / : + -", f.name, f.flag)
		}
	}
	for _, list := range []struct {
		name, flag string
		values     []string
	}{{"models", l.ModelFlag, l.Models}, {"efforts", l.EffortFlag, l.Efforts}, {"permissions", l.PermissionFlag, l.Permissions}} {
		if len(list.values) > 0 && list.flag == "" {
			return errfmt.Errorf("%s are listed with no flag to pass them", list.name)
		}
		if len(list.values) > 32 {
			return errfmt.Errorf("%s list %d values, limit 32", list.name, len(list.values))
		}
		for _, v := range list.values {
			if !LaunchValueOK(v) {
				return errfmt.Errorf("%s value %q: one word, not starting with -", list.name, v)
			}
		}
	}
	if l.EffortFlag != "" && len(l.Efforts) == 0 {
		return errfmt.Errorf("effort_flag %q lists no efforts", l.EffortFlag)
	}
	if l.PermissionFlag != "" && len(l.Permissions) == 0 {
		return errfmt.Errorf("permission_flag %q lists no permissions", l.PermissionFlag)
	}
	for _, p := range l.RiskyPermissions {
		if !slices.Contains(l.Permissions, p) {
			return errfmt.Errorf("risky_permissions %q is not in permissions", p)
		}
	}
	if l.DefaultPermission != "" && !slices.Contains(l.Permissions, l.DefaultPermission) {
		return errfmt.Errorf("default_permission %q is not in permissions", l.DefaultPermission)
	}
	if slices.Contains(l.RiskyPermissions, l.DefaultPermission) && l.DefaultPermission != "" {
		return errfmt.Errorf("default_permission %q is a risky one", l.DefaultPermission)
	}
	if (len(l.SignedInArgs) == 0) != (l.SignedInMatch == "") {
		return errfmt.Errorf("signed_in_args and signed_in_match go together")
	}
	if len(l.SignedInArgs) > 8 {
		return errfmt.Errorf("signed_in_args has %d arguments, limit 8", len(l.SignedInArgs))
	}
	for _, a := range l.SignedInArgs {
		if !safeResumeToken(a) {
			return errfmt.Errorf("signed_in_args %q: letters, digits and _ . / : = + - only", a)
		}
	}
	if l.SignedInMatch != "" {
		re, err := regexp.Compile(l.SignedInMatch)
		if err != nil {
			return errfmt.Errorf("signed_in_match: %w", err)
		}
		l.signedIn = re
	}
	return nil
}

// LaunchChoice is what the person chose for a new agent. An empty field is
// the harness's own default.
type LaunchChoice struct {
	Model, Effort, Permission string
}

// Empty reports whether nothing was chosen.
func (c LaunchChoice) Empty() bool { return c.Model == "" && c.Effort == "" && c.Permission == "" }

// LaunchArgs is the argv the choice adds after the harness's program, or why
// the harness cannot take it. m may be nil, for a program no manifest knows.
func LaunchArgs(m *Manifest, c LaunchChoice) ([]string, error) {
	if c.Empty() {
		return nil, nil
	}
	name := "this program"
	var l Launch
	if m != nil {
		name, l = m.DisplayName, m.Launch
	}
	var out []string
	add := func(what, flag, v string, values []string, free bool) error {
		if v == "" {
			return nil
		}
		if flag == "" {
			return errfmt.Errorf("%s takes no %s option that tuios knows", name, what)
		}
		if !LaunchValueOK(v) {
			return errfmt.Errorf("%s %q: one word, not starting with -", what, v)
		}
		if !free && !slices.Contains(values, v) {
			return errfmt.Errorf("%s %q is not one %s takes: %s", what, v, name, strings.Join(values, ", "))
		}
		out = append(out, flag, v)
		return nil
	}
	if err := add("model", l.ModelFlag, c.Model, l.Models, true); err != nil {
		return nil, err
	}
	if err := add("effort", l.EffortFlag, c.Effort, l.Efforts, false); err != nil {
		return nil, err
	}
	if c.Permission != "" && c.Permission == l.DefaultPermission {
		return out, nil
	}
	if err := add("permission mode", l.PermissionFlag, c.Permission, l.Permissions, false); err != nil {
		return nil, err
	}
	return out, nil
}

// SignedIn reports whether output, what SignedInArgs printed, says the
// harness is signed in. ok is false when the manifest has no check.
func (l *Launch) SignedIn(output []byte) (signedIn, ok bool) {
	if l.signedIn == nil {
		return false, false
	}
	return l.signedIn.Match(output), true
}

// RiskyPermission reports whether mode turns approvals off.
func (l Launch) RiskyPermission(mode string) bool { return slices.Contains(l.RiskyPermissions, mode) }
