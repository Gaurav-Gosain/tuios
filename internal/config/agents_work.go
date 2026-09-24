package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The tables of the agent review, triage, queue and approval work:
//
//	[agents.approvals]
//	hold_plans = true
//
//	[agents.approvals.risk]
//	builtin = true
//	panes_may_allow = false
//
//	[[agents.approvals.risk.rule]]
//	name = "kubectl apply"
//	tools = ["Bash"]
//	pattern = '\bkubectl\s+(apply|delete)\b'
//
//	[agents.recap]
//	mode = "toast"
//	away = "10m"
//	test_patterns = ["go test", "pytest"]
//
//	[agents.queue]
//	max = 8
//
// They are file-plane config like the rest of [agents]: read from the file and
// again when it changes, and never settable with set-option, so a pane cannot
// switch the risk rules off through tuios. Every field has a default, so a
// file without any of them behaves as the defaults below say.

// RiskConfig is the [agents.approvals.risk] table.
type RiskConfig struct {
	// Builtin keeps the rules tuios ships. Unset means true.
	Builtin *bool `toml:"builtin,omitempty"`
	// PanesMayAllow lets a pane holding the respond grant allow an approval
	// that matched a rule. Off by default: such a pane may deny it and not
	// allow it, and only the person allows it.
	PanesMayAllow bool `toml:"panes_may_allow,omitempty"`
	// Rules are rules of the person's own, added to the shipped ones.
	Rules []RiskRuleConfig `toml:"rule,omitempty"`
}

// RiskRuleConfig is one [[agents.approvals.risk.rule]].
type RiskRuleConfig struct {
	// Name is what the Inbox and the risk list call the rule.
	Name string `toml:"name"`
	// Tools are the tool names the rule applies to. Empty applies it to
	// every tool.
	Tools []string `toml:"tools,omitempty"`
	// Pattern is an RE2 regular expression matched against each command
	// segment of the call.
	Pattern string `toml:"pattern"`
}

// BuiltinRules reports whether the shipped risk rules apply.
func (r RiskConfig) BuiltinRules() bool {
	return r.Builtin == nil || *r.Builtin
}

// PlansHeld reports whether plans are held for the Inbox, for the harnesses
// Enabled names.
func (c ApprovalsConfig) PlansHeld() bool {
	return c.HoldPlans == nil || *c.HoldPlans
}

// Recap modes: where the away recap is shown.
const (
	// RecapToast shows it in the dock when the person comes back to a pane,
	// as well as in the Inbox and agent-log.
	RecapToast = "toast"
	// RecapInbox shows it only in the Inbox's Finished detail and agent-log.
	RecapInbox = "inbox"
	// RecapOff shows it only in agent-log.
	RecapOff = "off"
)

// RecapModes lists the valid values for agents.recap.mode.
var RecapModes = []string{RecapToast, RecapInbox, RecapOff}

// DefaultRecapAway is how long the person must have been away from a pane
// for the dock to show a recap when they come back.
const DefaultRecapAway = 10 * time.Minute

// DefaultRecapTestPatterns are the commands the recap reads as a test run.
var DefaultRecapTestPatterns = []string{
	"go test", "npm test", "pnpm test", "yarn test", "bun test", "pytest",
	"cargo test", "make test", "jest", "vitest", "mvn test", "gradle test",
}

// RecapConfig is the [agents.recap] table.
type RecapConfig struct {
	// Mode is one of RecapModes. Empty means toast.
	Mode string `toml:"mode,omitempty"`
	// Away is a Go duration ("10m"). Empty means DefaultRecapAway.
	Away string `toml:"away,omitempty"`
	// TestPatterns replaces DefaultRecapTestPatterns when set.
	TestPatterns []string `toml:"test_patterns,omitempty"`
}

// ResolvedRecap is RecapConfig with its defaults applied and bad values
// replaced by them.
type ResolvedRecap struct {
	Mode         string
	Away         time.Duration
	TestPatterns []string
}

// Resolved applies the defaults. A value validation warns about reads as its
// default.
func (c RecapConfig) Resolved() ResolvedRecap {
	out := ResolvedRecap{Mode: RecapToast, Away: DefaultRecapAway, TestPatterns: DefaultRecapTestPatterns}
	if m := strings.ToLower(strings.TrimSpace(c.Mode)); slices.Contains(RecapModes, m) {
		out.Mode = m
	}
	if d, err := time.ParseDuration(strings.TrimSpace(c.Away)); err == nil && d > 0 {
		out.Away = d
	}
	if len(c.TestPatterns) > 0 {
		out.TestPatterns = slices.Clone(c.TestPatterns)
	}
	return out
}

// Queue bounds: how many messages one pane's delivery queue may hold.
const (
	DefaultQueueMax = 8
	MaxQueueMax     = 64
)

// QueueConfig is the [agents.queue] table.
type QueueConfig struct {
	// Max is how many messages one pane's queue holds. Zero means the
	// default, 8; values past 64 read as 64.
	Max int `toml:"max,omitempty"`
}

// MaxEntries is Max with its default and bound applied.
func (c QueueConfig) MaxEntries() int {
	switch {
	case c.Max <= 0:
		return DefaultQueueMax
	case c.Max > MaxQueueMax:
		return MaxQueueMax
	}
	return c.Max
}

// validateAgentWork warns about the values of the tables above that read as
// their default, and about risk rules that cannot be used.
func validateAgentWork(cfg *UserConfig, result *ValidationResult) {
	warn := func(field, key, msg string) {
		result.Warnings = append(result.Warnings, ValidationError{Field: field, Key: key, Message: msg})
	}
	recap := cfg.Agents.Recap
	if m := strings.ToLower(strings.TrimSpace(recap.Mode)); m != "" && !slices.Contains(RecapModes, m) {
		warn("agents.recap", "mode", fmt.Sprintf("'%s' is not a valid value (allowed: %s); read as %s", recap.Mode, strings.Join(RecapModes, ", "), RecapToast))
	}
	if a := strings.TrimSpace(recap.Away); a != "" {
		if d, err := time.ParseDuration(a); err != nil || d <= 0 {
			warn("agents.recap", "away", fmt.Sprintf("'%s' is not a duration such as 10m; read as %s", recap.Away, DefaultRecapAway))
		}
	}
	if q := cfg.Agents.Queue.Max; q < 0 || q > MaxQueueMax {
		warn("agents.queue", "max", fmt.Sprintf("%d is outside 1 to %d; read as %d", q, MaxQueueMax, cfg.Agents.Queue.MaxEntries()))
	}
	for i, rule := range cfg.Agents.Approvals.Risk.Rules {
		key := fmt.Sprintf("rule[%d]", i)
		if strings.TrimSpace(rule.Name) == "" {
			warn("agents.approvals.risk", key, "a rule has no name, so the Inbox could not say which rule matched; ignored")
			continue
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil || strings.TrimSpace(rule.Pattern) == "" {
			warn("agents.approvals.risk", key, fmt.Sprintf("rule %q has no pattern RE2 can compile; ignored", rule.Name))
		}
	}
}

// Durations of appearance.sidebar.agent_rest_fold.
const (
	// DefaultAgentRestFold is how long an agent row rests before the rail
	// folds it into one line.
	DefaultAgentRestFold = time.Hour
	// AgentRestFoldOff turns folding off.
	AgentRestFoldOff = "off"
)

// ParseAgentRestFold reads appearance.sidebar.agent_rest_fold: a Go duration,
// or off (or 0) for never. ok is false for anything else, which reads as the
// default.
func ParseAgentRestFold(v string) (d time.Duration, ok bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "":
		return DefaultAgentRestFold, true
	case AgentRestFoldOff, "0":
		return 0, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return DefaultAgentRestFold, false
	}
	return d, true
}
