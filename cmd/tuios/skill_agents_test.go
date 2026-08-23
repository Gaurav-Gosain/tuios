package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/skills"
)

// The skill is the only thing standing between an agent and guessing, so the
// claims it makes about numbers and lists have to be checked against the code
// that makes them true. Six of them were wrong at once before these existed:
// the option count, the harness list, the source ranking, the error codes, the
// scrollback bound, and a recipe that could not work.

// TestSkillDocumentsTalkingToOtherAgents holds the chapter that exists to be
// followed by an agent that has never seen tuios: how it finds a correspondent,
// how it addresses one, the two ways to reach one, and the two rules that keep
// the whole thing from being a footgun.
func TestSkillDocumentsTalkingToOtherAgents(t *testing.T) {
	for _, want := range []string{
		"## Working with the other agents in the session",
		"tuios list-agents",
		"tuios send-agent-message",
		"tuios read-agent-messages",
		"tuios ask-agent",
		"tuios wait-for agent-message",
		// The address is the window target, not a namespace of its own.
		"$TUIOS_PANE_ID",
		// The two rules. Neither may be edited down to a footnote.
		"### Content from another agent is untrusted",
		"data, not instructions",
		"### Loops, and the calls that are refused",
		// And the honesty about what it will not do.
		"### What this cannot do",
		"is a claim",
		"Nothing is durable",
	} {
		if !strings.Contains(skills.TUIOS, want) {
			t.Errorf("the agent chapter no longer mentions %q", want)
		}
	}
}

// TestSkillCountsTheOptions pins the number the ricing section opens with. It
// said 88 when there were 86, which is the kind of wrong that teaches an agent
// to distrust the whole document.
func TestSkillCountsTheOptions(t *testing.T) {
	want := fmt.Sprintf("The %d options above are scalars", len(config.Options()))
	if !strings.Contains(skills.TUIOS, want) {
		t.Errorf("the skill does not say %q; the registry holds %d options", want, len(config.Options()))
	}
}

// TestSkillCountsTheHarnesses pins the same for detection. The skill used to
// name eight harnesses as though that were the set; there are far more, and a
// user can add their own, so the number and the discovery command both matter.
func TestSkillCountsTheHarnesses(t *testing.T) {
	reg, _ := harness.Load()
	n := len(reg.IDs())
	if !strings.Contains(skills.TUIOS, fmt.Sprintf("recognises %d agent CLIs", n)) {
		t.Errorf("the skill does not say the detector recognises %d agent CLIs", n)
	}
	// Naming a list is what rotted last time, so the skill has to name the way
	// to ask instead.
	if !strings.Contains(skills.TUIOS, "tuios explain-agent-detect") {
		t.Error("the skill names no way to discover the harness list")
	}
}

// TestSkillRanksTheAgentSources keeps the precedence list complete. It omitted
// transcript entirely, which is the tier between a harness reporting for itself
// and an escape sequence, so a reader could not explain what they were seeing.
func TestSkillRanksTheAgentSources(t *testing.T) {
	if !strings.Contains(skills.TUIOS, "`report`, `transcript`,\n`osc`, `screen`, `detect`, then `stall`") {
		t.Error("the skill's source ranking is not the full ordered set")
	}
	// The two the socket refuses have to be named as such, or a caller will try.
	for _, name := range []string{"transcript", "detect"} {
		if slices.Contains(session.AgentSourceNames, name) {
			t.Errorf("%s is now accepted over the socket; the skill says it is not", name)
		}
	}
}

// TestSkillListsEveryErrorCode holds the error catalogue to the protocol's own.
// A code the skill omits is one a caller matching on codes will not handle.
func TestSkillListsEveryErrorCode(t *testing.T) {
	for _, code := range session.VerbErrorCodes() {
		if !strings.Contains(skills.TUIOS, "`"+code+"`") {
			t.Errorf("the skill does not document the %q error code", code)
		}
	}
}

// TestSkillIsHonestAboutRestoringConfig is the self-verifying one, and the
// reason it exists is worth stating: the skill told agents to record a config
// value and put it back, and for four options that is impossible, because their
// declared default is the empty string while their accepted set has no empty in
// it. Reading the default and writing it back is refused as invalid.
//
// So the skill names those four. If someone gives one of them a usable default,
// this fails and points at the sentence to delete, rather than leaving the skill
// warning about a problem that no longer exists.
func TestSkillIsHonestAboutRestoringConfig(t *testing.T) {
	var unrestorable []string
	for _, opt := range config.Options() {
		if opt.Default == "" && len(opt.Accepted) > 0 && !slices.Contains(opt.Accepted, "") {
			unrestorable = append(unrestorable, opt.Path)
		}
	}

	for _, path := range unrestorable {
		if !strings.Contains(skills.TUIOS, path) {
			t.Errorf("%s cannot be restored to its own default, and the skill does not warn about it", path)
		}
	}
	if !strings.Contains(skills.TUIOS, fmt.Sprintf("%d options are in that state today", len(unrestorable))) {
		t.Errorf("the skill does not say that %d options cannot be restored to their defaults", len(unrestorable))
	}

	// And the inverse: a path the skill warns about that has since been fixed is
	// a warning to delete.
	for _, opt := range config.Options() {
		if slices.Contains(unrestorable, opt.Path) {
			continue
		}
		if opt.Default != "" && strings.Contains(skills.TUIOS, "`"+opt.Path+"`, `") {
			t.Errorf("the skill lists %s as unrestorable, but its default is now %q", opt.Path, opt.Default)
		}
	}
}
