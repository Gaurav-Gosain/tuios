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
	"github.com/Gaurav-Gosain/tuios/internal/oneline"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

const (
	approvalMaxPlan   = integration.MaxPlan
	approvalMaxTool   = 128
	approvalMaxTarget = 16 << 10
)

type riskSet struct {
	rules []risk.Rule
	home  string
}

func (a *attentionStore) setRisk(rules []risk.Rule) {
	home, _ := os.UserHomeDir()
	a.risk.Store(&riskSet{rules: slices.Clone(rules), home: home})
}

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

func (a *attentionStore) riskOfLine(line, root, runDir string) []risk.Hit {
	set := a.risk.Load()
	if set == nil || len(set.rules) == 0 || strings.HasPrefix(line, integration.PlanSummaryPrefix) {
		return nil
	}
	tool, text := risk.ParseSummary(line)
	clipped := oneline.Clipped(line)
	hits := risk.Match(set.rules, risk.Call{Tool: tool, Text: text, Root: root, RunDir: runDir, Home: set.home, Shown: true, Clipped: clipped, Resolve: realPath})
	if clipped || strings.Contains(line, risk.ShownRedacted) || strings.HasSuffix(line, harness.PartialSuffix) {
		hits = append(hits, risk.CutShortHit)
	}
	return hits
}

func stickyRisk(had, next []string) []string {
	out := slices.Clone(had)
	for _, r := range next {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

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

func sameRuleSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

func planDigest(plan string) string {
	sum := sha256.Sum256([]byte(plan))
	return hex.EncodeToString(sum[:])
}

func planLineCount(plan string) int {
	plan = strings.TrimRight(plan, "\n")
	if plan == "" {
		return 0
	}
	return strings.Count(plan, "\n") + 1
}

func approvalRoot(managedWorktree, startDir, agentDir string) string {
	for _, dir := range []string{resolveAbove(managedWorktree), startDir, agentDir} {
		if dir = usableFolder(dir); dir != "" {
			return dir
		}
	}
	return ""
}

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

func resolveAbove(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	return filepath.Join(realPath(filepath.Dir(p)), filepath.Base(p))
}

func managedWorktreePath(wt *WorktreeInfo) string {
	if wt != nil && wt.Managed {
		return wt.Path
	}
	return ""
}

func (s *Session) windowApprovalRoot(managedWorktree, windowID, agentSID string) string {
	return approvalRoot(managedWorktree, s.windowStartDir(windowID), s.agentStartDir(windowID, agentSID))
}

const agentStartDirsPerWindow = 16

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

func (d *Daemon) noteReportedAgentDir(sess *Session, target, agentSID, cwd string) {
	if !filepath.IsAbs(cwd) {
		return
	}
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

func (s *Session) agentStartDir(windowID, agentSID string) string {
	if windowID == "" || agentSID == "" {
		return ""
	}
	s.agentDirsMu.Lock()
	defer s.agentDirsMu.Unlock()
	return s.agentDirs[windowID][agentSID]
}

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

var errRiskUnacknowledged = errors.New("risk not acknowledged")

func riskUnacknowledgedError(rules []string, detail string) *verbError {
	return hintedVerbError(ErrVerbRiskUnacknowledged, "this call matched the risk rules "+strings.Join(rules, ", ")+", and an allow must acknowledge exactly those in risk_ack", &VerbHint{
		Param:    "risk_ack",
		Accepted: rules,
		Detail:   detail,
	})
}

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
