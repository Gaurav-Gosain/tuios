package main

import (
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// gitChip is a pane's branch and its changes since HEAD, after its title:
// a click shows the Changes tab and the diffs. In a narrow pane, whose header the
// title and its controls fill, it shows its counts alone, or nothing.
func (a *App) gitChip(c *ui.Context, pal *palette, s *proto.Session, p *proto.Pane, plan headerPlan) {
	g := p.Git
	compact := plan.gitCompact
	summary := gitSummary(g)
	// It gives up its room before the title does: its branch shrinks
	// first, down to its counts.
	b := ui.ButtonBase(c.Key("git")).Height(24).Padding(0, 8).Gap(5).Radius(rRow).AlignItems(ui.Center).Shrink(0).
		Label(summary).Tooltip(summary + "\n" + a.tip(locales.GitShowChanges(), "changes.toggle"))
	if plan.sized {
		b.MaxWidth(plan.gitW)
	}
	if b.Hovered() || a.prefs.Side == sideChanges {
		b.Background(pal.rowHover)
	}
	b.Children(func() {
		ui.Icon(c, branchIcon).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
		if compact && g.Files > 0 {
			a.lineCounts(c, pal, g.Added, g.Removed, 12)
			return
		}
		ui.Text(c, branchName(g)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).MaxWidth(180)
		if st := gitState(g.State); st != "" {
			ui.Text(c, st).FontSize(fsSmall).TextColor(pal.attnText).SingleLine().Shrink(0)
		}
		a.lineCounts(c, pal, g.Added, g.Removed, 12)
	})
	if b.Clicked() {
		a.defer_(func() { a.toggleSide(sideChanges) })
	}
}

// branchName is a worktree's branch, or its commit while detached.
func branchName(g *proto.GitInfo) string {
	if g.Branch != "" {
		return g.Branch
	}
	if len(g.Head) >= 7 {
		return g.Head[:7]
	}
	return g.Head
}

// gitState names an operation in progress.
func gitState(st string) string {
	if say, ok := map[string]func() string{
		"merge": locales.GitMerging, "rebase": locales.GitRebasing, "cherry-pick": locales.GitCherryPicking, "revert": locales.GitReverting,
	}[st]; ok {
		return say()
	}
	return ""
}

// gitSummary says a worktree's status in a line: its branch, how far it
// is from its upstream, and what changed, as "fix-login ↑2 · 3 files +40 −2".
func gitSummary(g *proto.GitInfo) string {
	var b strings.Builder
	b.WriteString(branchName(g))
	if g.Ahead > 0 {
		b.WriteString(" ↑" + itoa(g.Ahead))
	}
	if g.Behind > 0 {
		b.WriteString(" ↓" + itoa(g.Behind))
	}
	if st := gitState(g.State); st != "" {
		b.WriteString(" · " + st)
	}
	if g.Files > 0 {
		b.WriteString(" · " + locales.ChangesFiles(g.Files))
		if g.Added > 0 {
			b.WriteString(" +" + itoa(g.Added))
		}
		if g.Removed > 0 {
			b.WriteString(" −" + itoa(g.Removed))
		}
	}
	return b.String()
}

// sessionGit is the worktree a session's row tells of: its lead pane's,
// else the one of the pane the user was in last, else any of its panes'.
func sessionGit(s *proto.Session) *proto.GitInfo {
	if s.Git != nil {
		return s.Git
	}
	if p := s.FocusPane(); p != nil && p.Git != nil {
		return p.Git
	}
	for i := range s.Panes {
		if s.Panes[i].Git != nil {
			return s.Panes[i].Git
		}
	}
	return nil
}

// offBranch is the branch to show by a session's name in the sidebar: one
// other than its repository's default, as an agent's in a worktree of its
// own; "" for the default, which every other session is on.
func offBranch(g *proto.GitInfo) string {
	if g == nil || g.Default == "" || g.Branch == g.Default {
		return ""
	}
	return branchName(g)
}
