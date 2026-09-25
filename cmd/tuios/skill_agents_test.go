package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The skill is the only thing standing between an agent and guessing, so the
// claims it makes about numbers and lists have to be checked against the code
// that makes them true. Six of them were wrong at once before these existed:
// the option count, the harness list, the source ranking, the error codes, the
// scrollback bound, and a recipe that could not work.

// TestSkillListsEveryErrorCode holds the error catalogue to the protocol's own.
// A code the skill omits is one a caller matching on codes will not handle.
func TestSkillListsEveryErrorCode(t *testing.T) {
	for _, code := range session.VerbErrorCodes() {
		if !strings.Contains(skillText(t, "errors"), "`"+code+"`") {
			t.Errorf("the skill does not document the %q error code", code)
		}
	}
}

// TestSkillIsHonestAboutRestoringConfig is the self-verifying one, and the
// reason it exists is worth stating: the skill told agents to record a config
// value and put it back, and for a few options that is impossible, because their
// declared default is the empty string while their accepted set has no empty in
// it. Reading the default and writing it back is refused as invalid.
//
// So the skill names them. If someone gives one of them a usable default,
// this fails and points at the sentence to delete, rather than leaving the skill
// warning about a problem that no longer exists.
func TestSkillIsHonestAboutRestoringConfig(t *testing.T) {
	var unrestorable []string
	for _, opt := range config.Options() {
		// A colour option takes empty whatever its keyword list says (empty is
		// how it is unset), so an empty default is one it can be set back to.
		if opt.Default == "" && len(opt.Accepted) > 0 && !slices.Contains(opt.Accepted, "") && !opt.Color {
			unrestorable = append(unrestorable, opt.Path)
		}
	}

	for _, path := range unrestorable {
		if !strings.Contains(skillText(t, "config"), path) {
			t.Errorf("%s cannot be restored to its own default, and the skill does not warn about it", path)
		}
	}
	if !strings.Contains(skillText(t, "config"), fmt.Sprintf("%d options are in that state today", len(unrestorable))) {
		t.Errorf("the skill does not say that %d options cannot be restored to their defaults", len(unrestorable))
	}

	// And the inverse: a path the skill warns about that has since been fixed is
	// a warning to delete.
	//
	// Read out of the warning sentence rather than looked for in the document.
	// A backticked comma-separated list is the shape of every option list in the
	// skill, and searching the whole text for one made the ricing table's
	// `appearance.gap`, `appearance.panel_padding` read as a restore warning.
	warned := warnedUnrestorable(skillText(t, "config"))
	for _, opt := range config.Options() {
		if slices.Contains(unrestorable, opt.Path) {
			continue
		}
		if opt.Default != "" && slices.Contains(warned, opt.Path) {
			t.Errorf("the skill lists %s as unrestorable, but its default is now %q", opt.Path, opt.Default)
		}
	}
}

// warnedUnrestorable returns the option paths named by the skill's restore
// warning: the backticked names between "in that state today:" and the end of
// that sentence, and nothing from any other list in the document.
func warnedUnrestorable(skill string) []string {
	const marker = "options are in that state today:"
	i := strings.Index(skill, marker)
	if i < 0 {
		return nil
	}
	rest := skill[i+len(marker):]
	// The enumeration is one sentence, so it ends at the first full stop that
	// closes a name.
	if end := strings.Index(rest, "`. "); end >= 0 {
		rest = rest[:end+1]
	}
	var out []string
	for _, m := range regexp.MustCompile("`([a-z0-9_.]+)`").FindAllStringSubmatch(rest, -1) {
		out = append(out, m[1])
	}
	return out
}
