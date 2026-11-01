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

// The rail's worktree groups.
//
// A session whose directory is a linked git worktree is drawn under a row for
// its repository and labelled by its branch, rather than flat under its own
// name. Five sessions called tuios-feat-one to tuios-feat-five say nothing
// about each other in a flat list; under one "tuios" row labelled by branch
// they say what they are, and the repository can be folded shut when the work
// in it is not what the user is looking at.
//
// Everything here runs in the render path from the cached listing alone. The
// daemon detects the worktree and puts the record on the listing; nothing in
// this file runs git, opens a connection, or arms a timer.

// sidebarWorktreeGoneTag is what a child row shows when its worktree directory
// has been removed. The session is still listed, because the shell in it still
// runs, and the tag is what says the directory under it does not.
const sidebarWorktreeGoneTag = "gone"

// worktreeRef adapts the daemon's record to the shared row model, which knows
// nothing about internal/session. The main checkout's record comes through in
// both of the rail's layouts, because both group by it. See sidebarRepoGrouping.
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
	if info.Group != "" {
		// The first line, bounded: a row shows one line of it.
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

// sidebarSessionRows is the sessions section's own row list: the attached
// machine's sessions under their space's heading and, inside that, the worktree
// ones under their repository, with the members of a folded repository left
// out, then laid out by machine with the other machines' rows (see
// sidebarMachineRows).
//
// Only the sessions section reads it. The terminals and agents sections keep
// reading the ungrouped list, so folding a repository or a machine hides its
// rows here and never hides its panes or its waiting agents, which is the
// difference between tidying a list and losing work.
func (m *OS) sidebarSessionRows(sessions []sessiontree.Node) []sessiontree.Node {
	local := localSessionNodes(sessions)
	return m.sidebarMachineRows(m.sidebarGroupedRows(local), sessions[len(local):])
}

// sidebarGroupedRows groups one machine's sessions by space and, inside each
// space and inside the sessions in no space, gathers the worktree ones under
// their repository. A folded repository's members are left out, in every
// space they are in.
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

// sidebarRepoGrouping is how the rail gathers a repository's sessions. By
// default the main checkout's session is the repository's row. group_main gives
// the repository a row of its own, with the main checkout under it.
func (m *OS) sidebarRepoGrouping() func([]sessiontree.Node) []sessiontree.Node {
	if m.Settings.SidebarGroupMain {
		return sessiontree.GroupByRepo
	}
	return sessiontree.GroupByRepoCompact
}

// sidebarRepoOfRow is the repository a row folds, "" for a row that folds none:
// a repository's own row, or a main checkout's row that stands in for it.
func sidebarRepoOfRow(n sessiontree.Node) string {
	switch {
	case n.Kind == sessiontree.KindRepo:
		return n.ID
	case n.GroupHead:
		return n.Worktree.Repo
	}
	return ""
}

// sidebarHeadRepo is the repository whose row sessionID's row is, "" when that
// session's row does not stand in for a repository.
func (m *OS) sidebarHeadRepo(sessionID string) string {
	for _, n := range m.sidebarGroupedRows(localSessionNodes(m.BuildSessionTree().Sessions)) {
		if n.GroupHead && n.ID == sessionID {
			return n.Worktree.Repo
		}
	}
	return ""
}

// sidebarFoldMark is the mark that says a repository's row folds and which way
// it is folded now. While the keyboard cursor is on it the mark is reversed,
// the one way to set a single cell apart at every colour depth.
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

// sidebarComposeFoldRow is sidebarComposeGroupRow for a row with a fold mark.
// The mark takes the inset cell, so the figures stay on the column the figures
// of the rows around them are on.
func sidebarComposeFoldRow(indent int, gutter, glyph, name, right, fold string, cw int, bg color.Color) string {
	row := gutter + sidebarStyle(bg, nil).Render(strings.Repeat(" ", max(indent, 0))) +
		sidebarMarkCell(glyph, bg) + name
	gap := max(cw-lipgloss.Width(row)-lipgloss.Width(right)-lipgloss.Width(fold), 0)
	return sidebarFit(row+sidebarStyle(bg, nil).Render(strings.Repeat(" ", gap))+right+fold, cw, bg)
}

// sidebarFoldCells is what a fold mark costs a row's name: the mark and the
// blank in front of it.
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

// sidebarWorktreeLabel is the name a worktree session's row shows, its branch,
// and the tree mark in front of it that says whether the group goes on below.
// It reports false for a session that is not a worktree, whose row is
// unchanged.
//
// The two come back apart because they are drawn apart: the mark belongs to the
// tree and stays in its column, and only the name wears the current row's
// underline or pill, and only the name scrolls.
//
// A Named session keeps its own title behind the mark instead, and the branch
// rides after it like on any other row (#1245): a name a person chose says
// more than the branch, and the documented promise is that a session with a
// custom name keeps it.
//
// The branch is laundered like any other title, and the mark is not: the mark
// is one of ours, chosen by the glyph settings, and already ASCII in the mode
// that asks for ASCII.
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
		// A record with no branch is a detached head the daemon could not name.
		// The session's own title is better than an empty row.
		label = printableTitle(node.Title)
	}
	return mark, label, true
}

// railTreeMarkForCaps makes room for a pill's left cap in the blank the tree
// mark ends on, and reports the cells it took back. The name then starts on the
// column its siblings' names start on, so the current row moves nothing on the
// rail. A cap wider than that blank keeps the mark whole and takes its own cell.
func railTreeMarkForCaps(mark string, s *config.Settings) (string, int) {
	if s.GetRailRowStyle() != config.RailRowPill || lipgloss.Width(s.GetWindowPillLeft()) != 1 {
		return mark, 0
	}
	if trimmed, ok := strings.CutSuffix(mark, " "); ok {
		return trimmed, 1
	}
	return mark, 0
}

// sidebarRepoRow renders one repository's group header.
//
//	 · tuios
//	 ● tuios              3
//	^^ ^                  ^ members, right-aligned, while the group is shut
//	|| the roll-up of the members' states, while the group is shut
//	|gutter: severity when something inside a shut group wants a human
//
// An open group says nothing about its members' states: they are on screen
// underneath, each on its own row, and repeating the loudest of them on the
// parent would draw the same alarm twice. A shut group is the opposite case:
// its rows are not on screen, so the parent carries the worst of them and the
// count of what it is hiding.
func (m *OS) sidebarRepoRow(node sessiontree.Node, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	if d := m.SidebarDrag; d.Dragging && d.Repo && d.SessionID == node.ID && d.Space == node.Space {
		// A drag keeps the strongest band on the row riding the pointer.
		rowBg = pal.Surface
	}
	collapsed := m.SidebarRepoCollapsed(node.ID)

	right, rightW := "", 0
	if collapsed && node.WindowCount > 0 {
		// Ungated by the counts setting, unlike a window count: this number is
		// the only thing on the row saying how many sessions the fold is
		// holding, and a shut group with no number reads as an empty one.
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
	// A repository is an item that owns a subtree, not a heading: it wears a
	// session's glyph, rolls its members' states up, and steps in under its
	// machine with every other session row. Its own members hang off it on the
	// tree marks. See sidebarWorktreeLabel.
	indent := m.sidebarNodeIndent(node)
	nameW := sidebarNameAvailIn(cw, rightW, indent)
	if rightW == 0 {
		nameW = max(nameW-sidebarFoldCells, 1)
	}
	// A shut group that holds the attached session wears that session's mark,
	// in that session's colour, because the session's own row is not drawn.
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

// sessionNamed reports whether a person chose the label of the session called
// name: it has a display name, or a name tuios did not make up. Two names are
// made up: the generated session-N, and the <repo>-<branch> name that
// worktree new and fan give the session they make, which only repeats what
// the repository row and the branch already say.
func sessionNamed(name, display string, wt *session.WorktreeInfo) bool {
	if display != "" {
		return true
	}
	if session.IsGeneratedSessionName(name) {
		return false
	}
	return wt == nil || name != worktree.SessionName(wt.Repo, wt.Branch)
}
