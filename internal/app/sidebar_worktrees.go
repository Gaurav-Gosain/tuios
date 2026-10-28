package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
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
// nothing about internal/session. A main-checkout record is a record only for
// a rail that has opted into it: group_main off reads it as no record at all,
// which is exactly the shape the sessions had before it existed.
func (m *OS) worktreeRef(info *session.WorktreeInfo) *sessiontree.WorktreeRef {
	if info == nil {
		return nil
	}
	if info.Main && !m.Settings.SidebarGroupMain {
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

// sidebarRepoGotoMain opens the repo group's session in the main checkout —
// the one whose directory is the repository itself, usually on main or
// master — and reports whether there was one. It is what a repo header's
// activation does once group_main gathers the main checkout in; without it,
// or when the group holds no main-checkout session, the header keeps its
// fold gesture.
func (m *OS) sidebarRepoGotoMain(repo string) bool {
	if !m.Settings.SidebarGroupMain {
		return false
	}
	for _, n := range m.BuildSessionTree().Sessions {
		if n.Kind == sessiontree.KindSession && n.Host == "" &&
			n.Worktree != nil && n.Worktree.Repo == repo && n.Worktree.Main {
			// Open the group, so the session the header pointed at is on
			// screen under it rather than hidden behind a count.
			if m.SidebarRepoCollapsed(repo) {
				m.SidebarToggleRepoCollapsed(repo)
			}
			m.sidebarSwitchSession(n.ID)
			m.sidebarFollowSession = n.ID
			return true
		}
	}
	return false
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
	rows := sessiontree.GroupBySpace(sessions, sessiontree.GroupByRepo)
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
// A Named session keeps its own title behind the mark instead, and the branch
// rides after it like on any other row (#1245): a name a person chose says
// more than the branch, and the documented promise is that a session with a
// custom name keeps it.
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
	if node.Named {
		return mark + printableTitle(node.Title), true
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
func (m *OS) sidebarRepoRow(node sessiontree.Node, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
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
	name := sidebarStyle(rowBg, fg).Bold(sidebarAttention(state)).
		Render(overlay.Truncate(printableTitle(node.Title), sidebarNameAvailIn(cw, rightW, indent)))
	gutter := sidebarGutter(false, state, rowBg, pal, &m.Settings)
	return sidebarComposeGroupRow(indent, gutter, glyph, name, right, cw, rowBg)
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
