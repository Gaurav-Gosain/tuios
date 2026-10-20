package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
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
func (a *attentionStore) riskOfCall(tool, target, line, root, runDir string) []risk.Hit {
	if tool == "" && target == "" {
		return a.riskOfLine(line, root, runDir)
	}
	set := a.risk.Load()
	if set == nil || len(set.rules) == 0 {
		return nil
	}
	return risk.Match(set.rules, risk.Call{Tool: tool, Text: target, Root: root, RunDir: runDir, Home: set.home, Resolve: realPath})
}

// riskOfLine matches the rules against the line a pane reported for its
// approval. A plan's line is its title, which is not a command.
//
// The hooks clip that line to integration.MaxMessage and redact what looks
// like a secret to "***", so a risky part past the cut or behind the stars is
// not there to match. Such a line is marked cut short on top of
// whatever the rules found, which holds the allow to two presses and keeps a
// pane with the respond grant from allowing it: fail closed.
func (a *attentionStore) riskOfLine(line, root, runDir string) []risk.Hit {
	set := a.risk.Load()
	if set == nil || len(set.rules) == 0 || strings.HasPrefix(line, integration.PlanSummaryPrefix) {
		return nil
	}
	tool, text := risk.ParseSummary(line)
	// Shown: the line is not the call. A path in it may stop at a redacted
	// run, or, in its last word, at the clip, and the rules must not read
	// that stop as where the path ends: a worktree path longer than the line
	// read as outside it. A "..." the line did not get from a clip is part
	// of the path, so Clipped is set only for a line Clip cut.
	clipped := integration.Clipped(line)
	hits := risk.Match(set.rules, risk.Call{Tool: tool, Text: text, Root: root, RunDir: runDir, Home: set.home, Shown: true, Clipped: clipped, Resolve: realPath})
	// A line cut to length, a run redacted from it, or a dialog that shows
	// only part of the call (harness.PartialSuffix) may hide what a rule
	// would match.
	if clipped || strings.Contains(line, risk.ShownRedacted) || strings.HasSuffix(line, harness.PartialSuffix) {
		hits = append(hits, risk.CutShortHit)
	}
	return hits
}

// stickyRisk is an open item's mark after a new one: the rules it had, then
// any the new one adds. A rule is never taken off while the item is open.
func stickyRisk(had, next []string) []string {
	out := slices.Clone(had)
	for _, r := range next {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
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

// approvalRoot is the folder the risk rules hold a pane's calls to. It is
// the first of these that is a usable folder (usableFolder):
//
//   - the session's worktree, when tuios made it;
//   - the folder the window's first process started in (noteStartDir);
//   - the folder the pane's agent started in, as its harness reported it on
//     the first report of its session (noteAgentStartDir).
//
// Each is fixed and resolved when it is recorded, so nothing the pane does
// later moves it. The pane's working folder is never used: the pane moves
// it with cd, or names any folder with an OSC 7 it prints. A worktree tuios
// did not make is not used either: it is detected from the first window's
// working folder (refreshWorktree), so it follows a cd into another
// repository.
//
// The root is resolved, so it compares with a resolved path. It is "" when
// no source is usable, and the risk rules then read every absolute path as
// outside (risk.Call.Root).
//
// It reads the file system, so it is called only for an approval, never on
// every state change. It takes no session lock: the event sink calls it
// with the state lock held, which is why it does not call windowFolder.
func approvalRoot(managedWorktree, startDir, agentDir string) string {
	for _, dir := range []string{managedWorktree, startDir, agentDir} {
		if dir = usableFolder(dir); dir != "" {
			return dir
		}
	}
	return ""
}

// usableFolder is dir when it is a folder a pane can be held to, else "".
// It is the check windowFolder makes for the files grant: the folder as it
// was recorded, resolved, still a folder that resolves to itself, narrower
// than the home folder (tooWideFolder) and owned by the daemon's user
// (ownedByDaemonUser). A managed worktree path is recorded unresolved, so it
// is cleaned and taken as it is; a link there fails the check.
func usableFolder(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || realPath(dir) != dir {
		return ""
	}
	if tooWideFolder(dir) || !ownedByDaemonUser(dir) {
		return ""
	}
	return dir
}

// managedWorktreePath is the session's worktree path when tuios made it,
// else "".
func managedWorktreePath(wt *WorktreeInfo) string {
	if wt != nil && wt.Managed {
		return wt.Path
	}
	return ""
}

// windowApprovalRoot is approvalRoot for one window of the session. agentSID
// is the window's agent session id (WindowState.AgentSessionID).
func (s *Session) windowApprovalRoot(managedWorktree, windowID, agentSID string) string {
	return approvalRoot(managedWorktree, s.windowStartDir(windowID), s.agentStartDir(windowID, agentSID))
}

// agentStartDirsPerWindow bounds how many agent sessions one window keeps a
// start folder for. A report names its session itself, so a pane could name
// a new one on every call.
const agentStartDirsPerWindow = 16

// noteAgentStartDir records the folder an agent session started in, the
// first time a report of that session names one. Later reports of the same
// session never move it. dir must already be resolved (realPath). The
// caller checks dir against the pane's own folder first (verbSetAgentState).
func (s *Session) noteAgentStartDir(windowID, agentSID, dir string) {
	if windowID == "" || agentSID == "" || dir == "" {
		return
	}
	s.agentDirsMu.Lock()
	defer s.agentDirsMu.Unlock()
	if s.agentDirs == nil {
		s.agentDirs = make(map[string]map[string]string)
	}
	byID := s.agentDirs[windowID]
	if byID == nil {
		byID = make(map[string]string)
		s.agentDirs[windowID] = byID
	}
	if _, ok := byID[agentSID]; ok || len(byID) >= agentStartDirsPerWindow {
		return
	}
	byID[agentSID] = dir
}

// noteReportedAgentDir keeps the folder a hook reported for an agent session
// as that session's start folder, the first time a report of the session
// names one. It is kept only when the window's agent is that session now,
// and the folder is the one the pane's process runs in, read from the
// process: a report that names some other folder records nothing. The
// folder is stored resolved (realPath).
func (d *Daemon) noteReportedAgentDir(sess *Session, target, agentSID, cwd string) {
	if !filepath.IsAbs(cwd) {
		return
	}
	// The report names its window as the caller did: an id, an index or a
	// name.
	st := sess.GetState()
	idx, err := findWindowStateIndex(st.Windows, target)
	if err != nil {
		return
	}
	w := st.Windows[idx]
	if w.AgentSessionID != agentSID || sess.agentStartDir(w.ID, agentSID) != "" {
		return
	}
	live := sess.paneRunDir(w.PTYID)
	if live == "" {
		return
	}
	dir := realPath(filepath.Clean(cwd))
	if dir != realPath(filepath.Clean(live)) {
		return
	}
	sess.noteAgentStartDir(w.ID, agentSID, dir)
}

// agentStartDir is the start folder recorded for an agent session in a
// window, or "".
func (s *Session) agentStartDir(windowID, agentSID string) string {
	if windowID == "" || agentSID == "" {
		return ""
	}
	s.agentDirsMu.Lock()
	defer s.agentDirsMu.Unlock()
	return s.agentDirs[windowID][agentSID]
}

// paneRunDir is the folder a pane's process runs in now: the folder read
// from the process, else the folder its shell last announced with OSC 7, else
// the folder it started in. A report never wins over the process, since the
// pane prints what it likes. A relative path in an approval is resolved
// against it (risk.Call.RunDir). It is "" for a pane on another machine, or
// one running ssh, or when no folder is known, and then every relative path
// is read as outside. It takes no session lock, so the event sink may call
// it with the state lock held; the process read is one system call.
func (s *Session) paneRunDir(ptyID string) string {
	if s == nil || ptyID == "" {
		return ""
	}
	v, ok := s.places.Load(ptyID)
	if !ok {
		return ""
	}
	p := v.(*PTY)
	if p.place.Elsewhere() != "" {
		return ""
	}
	if cwd, ok := p.ProcessCwd(); ok && cwd != "" {
		return cwd
	}
	return p.place.Cwd()
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
//
// The item's mark comes from what the pane reported, and a pane may report
// again. So for a pane, seen adds the rules the prompt on the screen matched
// (promptRisk), and either one refuses the allow. The person's risk_ack is
// still held to the item's mark, which is what the Inbox showed them.
func (d *Daemon) respondRiskRefusal(sess *Session, window string, prompt harness.Prompt, action string, reply harness.Reply, ack []string, byPane bool, seen []string) *verbError {
	rules := d.attention.riskOn(sess.Name(), window)
	if !respondApproves(prompt, action, reply) {
		return nil
	}
	if byPane && !d.approvalPolicy().PanesMayAllow {
		all := append(slices.Clone(rules), seen...)
		slices.Sort(all)
		if all = slices.Compact(all); len(all) == 0 {
			return nil
		}
		return hintedVerbError(ErrVerbForbidden, "this call matched the risk rules "+strings.Join(all, ", ")+", and a pane may not allow a risky call", &VerbHint{
			Detail: "Nothing was pressed. A pane with the respond grant may deny a risky call and never allow one, unless [agents.approvals.risk] panes_may_allow is true. Ask the person with send-agent-message -w human.",
		})
	}
	if len(rules) == 0 {
		return nil
	}
	if !sameRuleSet(ack, rules) {
		return riskUnacknowledgedError(rules, "Nothing was pressed. The Inbox's peek allows a risky call on a second press of the same key.")
	}
	return nil
}

// promptRisk is the rules the prompt on a pane's screen matches, read with
// the root and run folder the pane's approval line is marked with. Each line
// the rule read is matched as the pane's line would be, so a line the screen
// cut short is marked cut short too.
func (d *Daemon) promptRisk(sess *Session, w WindowState, prompt harness.Prompt) []string {
	st := sess.GetState()
	root := sess.windowApprovalRoot(managedWorktreePath(st.Worktree), w.ID, w.AgentSessionID)
	runDir := resolvedRunDir(sess.paneRunDir(w.PTYID))
	var names []string
	for _, line := range append([]string{prompt.Message}, prompt.Lines...) {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, risk.Names(d.attention.riskOfLine(line, root, runDir))...)
		}
	}
	return names
}
