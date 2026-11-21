package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const sidebarWorktreeGoneTag = "gone"

func sidebarSetupTag(ref *sessiontree.WorktreeRef) string {
	if ref == nil {
		return ""
	}
	switch ref.Setup {
	case session.SetupPending, session.SetupRunning:
		return "setup"
	case session.SetupFailed:
		return "setup failed"
	}
	return ""
}

func (m *OS) worktreeRef(info *session.WorktreeInfo) *sessiontree.WorktreeRef {
	if info == nil {
		return nil
	}
	ref := &sessiontree.WorktreeRef{
		Repo:     info.Repo,
		Branch:   info.Branch,
		Group:    info.Group,
		FanID:    info.FanID,
		Gone:     info.Gone,
		Main:     info.Main,
		PR:       info.PR.Badge(),
		Path:     info.Path,
		RepoRoot: info.RepoRoot,
	}
	if info.PR != nil {
		ref.PRFailed = info.PR.Failed
	}
	if info.Setup != nil {
		ref.Setup = info.Setup.State
	}
	if info.Group != "" {
		line, _, _ := strings.Cut(strings.TrimSpace(info.Prompt), "\n")
		if len(line) > 200 {
			line = strings.ToValidUTF8(line[:200], "")
		}
		ref.Prompt = line
		ref.JudgeWindow = info.JudgeWindow
		if v := info.Verdict; v != nil {
			ref.Pick, ref.VerdictAt = v.Pick, v.At
		}
	}
	return ref
}

func (m *OS) sidebarSessionRows(sessions []sessiontree.Node) []sessiontree.Node {
	local := localSessionNodes(sessions)
	return m.sidebarMachineRows(m.sidebarGroupedRows(local), sessions[len(local):])
}

func (m *OS) sidebarGroupedRows(sessions []sessiontree.Node) []sessiontree.Node {
	rows := sessiontree.GroupBySpace(sessions, m.sidebarRepoGrouping())
	if len(m.SidebarCollapsedRepos) == 0 {
		return rows
	}
	out := make([]sessiontree.Node, 0, len(rows))
	for _, n := range rows {
		if n.Kind == sessiontree.KindSession && !n.GroupHead && n.Worktree != nil && m.SidebarCollapsedRepos[n.Worktree.Repo] {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (m *OS) sidebarRepoGrouping() func([]sessiontree.Node) []sessiontree.Node {
	if m.Settings.SidebarGroupMain {
		return sessiontree.GroupByRepo
	}
	return sessiontree.GroupByRepoCompact
}

func sidebarRepoOfRow(n sessiontree.Node) string {
	switch {
	case n.Kind == sessiontree.KindRepo:
		return n.ID
	case n.GroupHead:
		return n.Worktree.Repo
	}
	return ""
}

func (m *OS) sidebarHeadRepo(sessionID string) string {
	for _, n := range m.sidebarGroupedRows(localSessionNodes(m.BuildSessionTree().Sessions)) {
		if n.GroupHead && n.ID == sessionID {
			return n.Worktree.Repo
		}
	}
	return ""
}

func (m *OS) sidebarFoldMark(collapsed, cursor bool, bg color.Color, pal overlay.Palette) string {
	glyph := m.Settings.GetRailFoldOpenGlyph()
	if collapsed {
		glyph = m.Settings.GetRailFoldShutGlyph()
	}
	if cursor {
		return sidebarStyle(bg, pal.Fg).Reverse(true).Render(glyph)
	}
	return sidebarStyle(bg, pal.FgMute).Render(glyph)
}

func sidebarComposeFoldRow(indent int, gutter, glyph, name, right, fold string, cw int, bg color.Color) string {
	row := gutter + sidebarStyle(bg, nil).Render(strings.Repeat(" ", max(indent, 0))) +
		sidebarMarkCell(glyph, bg) + name
	gap := max(cw-lipgloss.Width(row)-lipgloss.Width(right)-lipgloss.Width(fold), 0)
	return sidebarFit(row+sidebarStyle(bg, nil).Render(strings.Repeat(" ", gap))+right+fold, cw, bg)
}

const sidebarFoldCells = 2

// SidebarRepoCollapsed reports whether a repository's group is folded shut.
func (m *OS) SidebarRepoCollapsed(repo string) bool {
	return m.SidebarCollapsedRepos[repo]
}

// SidebarToggleRepoCollapsed folds a repository's group shut, or opens it
// again, and remembers which it is. The set is keyed by repository name, so a
// group the user shut yesterday is still shut after a restart and after the
// sessions in it have been replaced.
func (m *OS) SidebarToggleRepoCollapsed(repo string) {
	if repo == "" {
		return
	}
	if m.SidebarCollapsedRepos[repo] {
		delete(m.SidebarCollapsedRepos, repo)
	} else {
		if m.SidebarCollapsedRepos == nil {
			m.SidebarCollapsedRepos = make(map[string]bool, 1)
		}
		m.SidebarCollapsedRepos[repo] = true
	}
	m.saveSidebarState()
}

func (m *OS) sidebarWorktreeLabel(node sessiontree.Node) (mark, label string, ok bool) {
	if node.Worktree == nil || node.GroupHead {
		return "", "", false
	}
	mark = m.Settings.GetRailTreeBranch()
	if node.GroupLast {
		mark = m.Settings.GetRailTreeLast()
	}
	if node.Named {
		return mark, printableTitle(node.Title), true
	}
	label = printableTitle(node.Worktree.Branch)
	if label == "" {
		label = printableTitle(node.Title)
	}
	return mark, label, true
}

func railTreeMarkForCaps(mark string, s *config.Settings) (string, int) {
	if s.GetRailRowStyle() != config.RailRowPill || lipgloss.Width(s.GetWindowPillLeft()) != 1 {
		return mark, 0
	}
	if trimmed, ok := strings.CutSuffix(mark, " "); ok {
		return trimmed, 1
	}
	return mark, 0
}

func (m *OS) sidebarRepoRow(node sessiontree.Node, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	if d := m.SidebarDrag; d.Dragging && d.Repo && d.SessionID == node.ID && d.Space == node.Space {
		rowBg = pal.Surface
	}
	collapsed := m.SidebarRepoCollapsed(node.ID)

	right, rightW := "", 0
	if collapsed && node.WindowCount > 0 {
		count := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(count)
		rightW = lipgloss.Width(count)
	}

	state := ""
	if collapsed {
		state = node.AgentState
	}
	glyph := sidebarQuietDot(rowBg, pal, &m.Settings)
	if agentStateIndicator(state) != "" {
		glyph = sidebarGlyph(state, node.DoneSeen, node.Subagents, rowBg, pal, &m.Settings)
	}

	fg := pal.FgDim
	if st.lit() {
		fg = pal.Fg
	}
	indent := m.sidebarNodeIndent(node)
	nameW := sidebarNameAvailIn(cw, rightW, indent)
	if rightW == 0 {
		nameW = max(nameW-sidebarFoldCells, 1)
	}
	holdsCurrent := collapsed && node.GroupCurrent
	nameMarked, capsW := railNameMarked(&m.Settings)
	nameMarked = nameMarked && holdsCurrent
	var tint color.Color
	if holdsCurrent {
		tint = m.sessionTint(m.sidebarCurrentSessionID(), m.rowGround(rowBg))
	}
	title := printableTitle(node.Title)
	name := ""
	gutter := sidebarGutterTinted(holdsCurrent, state, tint, rowBg, pal, &m.Settings)
	if nameMarked {
		name = railCurrentName(overlay.Truncate(title, max(nameW-capsW, 1)), tint, rowBg, pal, &m.Settings)
		gutter = sidebarGutterNameMarked(state, rowBg, pal, &m.Settings)
	} else {
		name = sidebarStyle(rowBg, fg).Bold(sidebarAttention(state)).Render(overlay.Truncate(title, nameW))
	}
	fold := m.sidebarFoldMark(collapsed, false, rowBg, pal)
	return sidebarComposeFoldRow(indent, gutter, glyph, name, right, fold, cw, rowBg)
}

func sessionNamed(name, display string, wt *session.WorktreeInfo) bool {
	if display != "" {
		return true
	}
	if session.IsGeneratedSessionName(name) {
		return false
	}
	return wt == nil || name != worktree.SessionName(wt.Repo, wt.Branch)
}
