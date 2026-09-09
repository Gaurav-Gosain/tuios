package app

import (
	"image/color"
	"strconv"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
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
// nothing about internal/session.
func worktreeRef(info *session.WorktreeInfo) *sessiontree.WorktreeRef {
	if info == nil {
		return nil
	}
	return &sessiontree.WorktreeRef{
		Repo:   info.Repo,
		Branch: info.Branch,
		Group:  info.Group,
		Gone:   info.Gone,
	}
}

// sidebarSessionRows is the sessions section's own row list: the attached
// machine's sessions with the worktree ones gathered under their repository
// and the members of a folded repository left out, then laid out by machine
// with the other machines' rows (see sidebarMachineRows).
//
// Only the sessions section reads it. The terminals and agents sections keep
// reading the ungrouped list, so folding a repository or a machine hides its
// rows here and never hides its panes or its waiting agents, which is the
// difference between tidying a list and losing work.
func (m *OS) sidebarSessionRows(sessions []sessiontree.Node) []sessiontree.Node {
	local := localSessionNodes(sessions)
	return m.sidebarMachineRows(m.sidebarRepoRows(local), sessions[len(local):])
}

// sidebarRepoRows gathers one machine's worktree sessions under their
// repository and leaves a folded repository's members out.
func (m *OS) sidebarRepoRows(sessions []sessiontree.Node) []sessiontree.Node {
	rows := sessiontree.GroupByRepo(sessions)
	if len(m.SidebarCollapsedRepos) == 0 {
		return rows
	}
	out := make([]sessiontree.Node, 0, len(rows))
	for _, n := range rows {
		if n.Kind == sessiontree.KindSession && n.Worktree != nil && m.SidebarCollapsedRepos[n.Worktree.Repo] {
			continue
		}
		out = append(out, n)
	}
	return out
}

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

// sidebarWorktreeLabel is the name a worktree session's row shows: its branch,
// behind the tree mark that says whether the group goes on below it. It
// reports false for a session that is not a worktree, whose row is unchanged.
//
// The branch is laundered like any other title, and the mark is not: the mark
// is one of ours, chosen by the glyph settings, and already ASCII in the mode
// that asks for ASCII.
func (m *OS) sidebarWorktreeLabel(node sessiontree.Node) (string, bool) {
	if node.Worktree == nil {
		return "", false
	}
	mark := m.Settings.GetRailTreeBranch()
	if node.GroupLast {
		mark = m.Settings.GetRailTreeLast()
	}
	label := printableTitle(node.Worktree.Branch)
	if label == "" {
		// A record with no branch is a detached head the daemon could not name.
		// The session's own title is better than an empty row.
		label = printableTitle(node.Title)
	}
	return mark + label, true
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
func (m *OS) sidebarRepoRow(node sessiontree.Node, cw int, pal overlay.Palette, hovered bool) string {
	var rowBg color.Color
	if hovered {
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
		glyph = sidebarGlyph(state, node.DoneSeen, rowBg, pal, &m.Settings)
	}

	fg := pal.FgDim
	if hovered {
		fg = pal.Fg
	}
	name := sidebarStyle(rowBg, fg).Bold(sidebarAttention(state)).
		Render(overlay.Truncate(printableTitle(node.Title), sidebarNameAvail(cw, rightW)))
	gutter := sidebarGutter(false, state, rowBg, pal, &m.Settings)
	return sidebarComposeRow(gutter, glyph, name, right, cw, rowBg)
}
