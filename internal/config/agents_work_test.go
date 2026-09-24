package config_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/pelletier/go-toml/v2"
)

// TestAgentWorkTablesDefault: a file that says nothing about the new tables
// reads as the plan's defaults.
func TestAgentWorkTablesDefault(t *testing.T) {
	var c config.AgentsConfig
	if !c.Approvals.PlansHeld() || !c.Approvals.Risk.BuiltinRules() || c.Approvals.Risk.PanesMayAllow {
		t.Errorf("approvals default to plans %v, builtin %v, panes_may_allow %v",
			c.Approvals.PlansHeld(), c.Approvals.Risk.BuiltinRules(), c.Approvals.Risk.PanesMayAllow)
	}
	r := c.Recap.Resolved()
	if r.Mode != config.RecapToast || r.Away != 10*time.Minute || !slices.Contains(r.TestPatterns, "go test") {
		t.Errorf("the recap defaults to %+v", r)
	}
	if c.Queue.MaxEntries() != 8 {
		t.Errorf("the queue holds %d by default, want 8", c.Queue.MaxEntries())
	}
	if d, ok := config.ParseAgentRestFold(""); !ok || d != time.Hour {
		t.Errorf("agent_rest_fold defaults to %v", d)
	}
	if config.DefaultSettings().SidebarAgentRestFold != time.Hour {
		t.Error("the settings do not default the fold to an hour")
	}
}

// TestAgentWorkTablesDecode reads the tables as the docs write them.
func TestAgentWorkTablesDecode(t *testing.T) {
	src := `
[agents.approvals]
hold_plans = false

[agents.approvals.risk]
builtin = false
panes_may_allow = true

[[agents.approvals.risk.rule]]
name = "kubectl apply"
tools = ["Bash"]
pattern = '\bkubectl\s+(apply|delete)\b'

[agents.recap]
mode = "inbox"
away = "30m"
test_patterns = ["just test"]

[agents.queue]
max = 12

[appearance.sidebar]
agent_rest_fold = "off"
`
	var cfg config.UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatal(err)
	}
	a := cfg.Agents
	if a.Approvals.PlansHeld() || a.Approvals.Risk.BuiltinRules() || !a.Approvals.Risk.PanesMayAllow {
		t.Errorf("approvals read as %+v", a.Approvals)
	}
	if len(a.Approvals.Risk.Rules) != 1 || a.Approvals.Risk.Rules[0].Name != "kubectl apply" || a.Approvals.Risk.Rules[0].Tools[0] != "Bash" {
		t.Errorf("rules read as %+v", a.Approvals.Risk.Rules)
	}
	r := a.Recap.Resolved()
	if r.Mode != config.RecapInbox || r.Away != 30*time.Minute || !slices.Equal(r.TestPatterns, []string{"just test"}) {
		t.Errorf("the recap reads as %+v", r)
	}
	if a.Queue.MaxEntries() != 12 || (config.QueueConfig{Max: 100}).MaxEntries() != config.MaxQueueMax {
		t.Errorf("the queue reads as %d, want 12, and 100 is held to %d", a.Queue.MaxEntries(), config.MaxQueueMax)
	}
	if d, ok := config.ParseAgentRestFold(cfg.Appearance.Sidebar.AgentRestFold); !ok || d != 0 {
		t.Errorf("off reads as %v (%v)", d, ok)
	}
	if res := config.ValidateConfig(&cfg); hasWarning(res, "") {
		t.Errorf("a valid file warned: %+v", res.Warnings)
	}
}

// TestAgentWorkTablesWarn: a value that reads as its default, and a rule that
// cannot be used, are reported.
func TestAgentWorkTablesWarn(t *testing.T) {
	var cfg config.UserConfig
	cfg.Agents.Recap = config.RecapConfig{Mode: "loud", Away: "soon"}
	cfg.Agents.Queue.Max = -1
	cfg.Agents.Approvals.Risk.Rules = []config.RiskRuleConfig{{Name: "", Pattern: "x"}, {Name: "broken", Pattern: "("}}
	cfg.Appearance.Sidebar.AgentRestFold = "a while"
	res := config.ValidateConfig(&cfg)
	for _, want := range []string{"mode", "away", "max", "rule[0]", "rule[1]", "agent_rest_fold"} {
		if !hasWarning(res, want) {
			t.Errorf("no warning for %s: %+v", want, res.Warnings)
		}
	}
	r := cfg.Agents.Recap.Resolved()
	if r.Mode != config.RecapToast || r.Away != config.DefaultRecapAway {
		t.Errorf("bad recap values read as %+v, want the defaults", r)
	}
}

func hasWarning(res *config.ValidationResult, key string) bool {
	for _, w := range res.Warnings {
		if !strings.HasPrefix(w.Field, "agents.") && w.Key != "agent_rest_fold" {
			continue
		}
		if key == "" || w.Key == key {
			return true
		}
	}
	return false
}

// TestSidebarAgentKeybindsFilledForOlderConfig: a config written before the
// agent rows had keys gets them, and the rail's own r and x keep their
// meaning.
func TestSidebarAgentKeybindsFilledForOlderConfig(t *testing.T) {
	cfg := writeConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n[keybindings.window_management]\nclose_window = [\"x\"]\n")
	r := config.NewKeybindRegistry(cfg)
	if got := r.GetAction("w"); got == "close_window" {
		t.Fatal("close_window still answers to w, so the fixture was never loaded")
	}
	for key, want := range map[string]string{
		"u": config.ActionAgentUnread,
		"z": config.ActionAgentSnooze,
		"r": config.ActionAgentReply,
		"v": config.ActionAgentReview,
		"x": config.ActionAgentCancelQueued,
	} {
		if got := r.GetSidebarAgentsAction(key); got != want {
			t.Errorf("GetSidebarAgentsAction(%q) = %q, want %q", key, got, want)
		}
	}
	if r.GetSidebarAction("r") != "rename" || r.GetSidebarAction("x") != "kill" {
		t.Error("the rail's own r and x lost their meaning")
	}
	for key, want := range map[string]string{"v": "prefix_review", "O": "prefix_next_finished"} {
		if got := r.GetPrefixAction(key); got != want {
			t.Errorf("GetPrefixAction(%q) = %q, want %q", key, got, want)
		}
	}
	for key, want := range map[string]string{"z": config.ActionInboxSnooze, "u": config.ActionInboxUndo, "S": config.ActionInboxShowSnoozed, "v": config.ActionInboxReview, "n": config.ActionInboxDenyReason, "J": config.ActionInboxDetailDown} {
		if got := r.GetInboxAction(key); got != want {
			t.Errorf("GetInboxAction(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestTheNewAgentPrefixLinesWaitForAnAgent: the two new prefix menu lines are
// agent lines, so the client leaves them out until an agent has been seen.
func TestTheNewAgentPrefixLinesWaitForAnAgent(t *testing.T) {
	var found int
	for _, k := range config.GetPrefixKeybindings("") {
		if k.Key == "v" || k.Key == "O" {
			found++
			if !config.IsAgentPrefixKeybinding(k) {
				t.Errorf("%q (%s) is not an agent line", k.Key, k.Description)
			}
		}
	}
	if found != 2 {
		t.Errorf("the prefix menu lists %d of v and O, want both", found)
	}
}
