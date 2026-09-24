package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

// Risky approvals and plans: the daemon's half.
//
// The risk rules (internal/risk) mark an approval whose command matched one
// as risky. The mark is the daemon's, computed here and carried on the Inbox
// item, and so is the rule that follows from it: an allow of a risky call is
// only taken with risk_ack naming exactly the rules it matched. The Inbox
// sends that on the second press of the same key, so an allow is two
// deliberate presses; an older client sends none and cannot allow a risky call
// at all, which leaves it to the pane. That is fail closed.
//
// The rules run in two places:
//
//   - at request-approval, on the call the hook names (tool and target), else
//     on its line, for the hold;
//   - on every needs_input report of kind approval, on the line the pane
//     reported, for an approval nobody holds. tuios's own hooks report
//     "approve <Tool>: <what>", which is read as that tool and argument; any
//     other line is read as a command. The line is clipped, so a clipped one
//     is also marked cut short.
//
// respond, the peek's key presses, takes risk_ack under the same rule for its
// approving actions, and refuses them from a pane with the respond grant
// unless [agents.approvals.risk] panes_may_allow is set. A deny is always one
// press and never refused.
//
// A plan is held with its text, which get-approval serves while the hold runs.
// Its digest, plan_sha, is on the item, and an allow of a plan must name it:
// an answer made from one plan cannot approve another.

// Bounds on what request-approval takes.
const (
	// approvalMaxPlan bounds a plan's text: what the hook sends at most.
	approvalMaxPlan = integration.MaxPlan
	// approvalMaxTool bounds a tool name.
	approvalMaxTool = 128
	// approvalMaxTarget bounds a call's target, a command line or a path.
	approvalMaxTarget = 16 << 10
)

// riskSet is the rules the store marks approvals with, and the home directory
// a ~ in a command stands for.
type riskSet struct {
	rules []risk.Rule
	home  string
}

// setRisk replaces the rules.
func (a *attentionStore) setRisk(rules []risk.Rule) {
	home, _ := os.UserHomeDir()
	a.risk.Store(&riskSet{rules: slices.Clone(rules), home: home})
}

// riskOfCall matches the rules against a call: its tool and target when the
// hook named them, else its line.
func (a *attentionStore) riskOfCall(tool, target, line, root string) []risk.Hit {
	if tool == "" && target == "" {
		return a.riskOfLine(line, root)
	}
	set := a.risk.Load()
	if set == nil || len(set.rules) == 0 {
		return nil
	}
	return risk.Match(set.rules, risk.Call{Tool: tool, Text: target, Root: root, Home: set.home})
}

// riskOfLine matches the rules against the line a pane reported for its
// approval. A plan's line is its title, which is not a command.
//
// The hooks clip that line to integration.MaxMessage, so a risky part past
// the cut is not there to match. A clipped line is marked cut short on top of
// whatever the rules found, which holds the allow to two presses and keeps a
// pane with the respond grant from allowing it: fail closed.
func (a *attentionStore) riskOfLine(line, root string) []risk.Hit {
	set := a.risk.Load()
	if set == nil || len(set.rules) == 0 || strings.HasPrefix(line, integration.PlanSummaryPrefix) {
		return nil
	}
	tool, text := risk.ParseSummary(line)
	hits := risk.Match(set.rules, risk.Call{Tool: tool, Text: text, Root: root, Home: set.home})
	if integration.Clipped(line) {
		hits = append(hits, risk.CutShortHit)
	}
	return hits
}

// riskOn is the risk marked on a pane's blocking item, for respond.
func (a *attentionStore) riskOn(session, window string) []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.byKey[attentionKey(AttentionApproval, session, window, 0)]
	if !ok {
		return nil
	}
	return slices.Clone(a.items[id].Risk)
}

// sameRuleSet reports whether two lists name the same rules, in any order.
func sameRuleSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

// planDigest is the plan_sha of a plan's text.
func planDigest(plan string) string {
	sum := sha256.Sum256([]byte(plan))
	return hex.EncodeToString(sum[:])
}

// planLineCount is how many lines a plan has, not counting a final newline.
func planLineCount(plan string) int {
	plan = strings.TrimRight(plan, "\n")
	if plan == "" {
		return 0
	}
	return strings.Count(plan, "\n") + 1
}

// paneRoot is where a pane's work is rooted: its session's worktree, else the
// pane's working directory.
func paneRoot(st *SessionState, w WindowState) string {
	if st != nil && st.Worktree != nil && st.Worktree.Path != "" {
		return st.Worktree.Path
	}
	return w.Cwd
}

// errRiskUnacknowledged is an allow of a risky call without risk_ack naming
// exactly the rules it matched.
var errRiskUnacknowledged = errors.New("risk not acknowledged")

// riskUnacknowledgedError is the refusal for an allow of a risky call, naming
// the rules.
func riskUnacknowledgedError(rules []string, detail string) *verbError {
	return hintedVerbError(ErrVerbRiskUnacknowledged, "this call matched the risk rules "+strings.Join(rules, ", ")+", and an allow must acknowledge exactly those in risk_ack", &VerbHint{
		Param:    "risk_ack",
		Accepted: rules,
		Detail:   detail,
	})
}

// respondApproves reports whether a respond answer allows the call rather
// than refusing it. approve and approve_always do. A choice does unless it
// presses exactly the keys the prompt's deny answer presses, since the peek
// cannot tell what an option means. Typed text does too: in a menu it can
// pick an option like a digit, so it is held to the same rule.
func respondApproves(prompt harness.Prompt, action string, reply harness.Reply) bool {
	switch action {
	case harness.ActionDeny:
		return false
	case harness.ActionChoose:
		deny, err := prompt.Resolve(harness.ActionDeny, "")
		return err != nil || deny.Text != "" || !bytes.Equal(deny.Keys, reply.Keys)
	}
	return true
}

// respondRiskRefusal is respond's rule for a risky prompt: the pane's Inbox
// item carries the rules its call matched, and an answer that allows it needs
// risk_ack naming exactly those. From a pane answering under its respond
// grant, such an answer is refused outright unless the policy lets panes
// allow risky calls: an agent with the grant may deny one and never allow
// it. A deny is always taken.
func (d *Daemon) respondRiskRefusal(sess *Session, window string, prompt harness.Prompt, action string, reply harness.Reply, ack []string, byPane bool) *verbError {
	rules := d.attention.riskOn(sess.Name, window)
	if len(rules) == 0 || !respondApproves(prompt, action, reply) {
		return nil
	}
	if byPane && !d.approvalPolicy().PanesMayAllow {
		return hintedVerbError(ErrVerbForbidden, "this call matched the risk rules "+strings.Join(rules, ", ")+", and a pane may not allow a risky call", &VerbHint{
			Detail: "Nothing was pressed. A pane with the respond grant may deny a risky call and never allow one, unless [agents.approvals.risk] panes_may_allow is true. Ask the person with send-agent-message -w human.",
		})
	}
	if !sameRuleSet(ack, rules) {
		return riskUnacknowledgedError(rules, "Nothing was pressed. The Inbox's peek allows a risky call on a second press of the same key.")
	}
	return nil
}
