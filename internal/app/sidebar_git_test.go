package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestDivergencePrintsNothingWhenNothingHasDrifted. A branch level with its
// upstream is the common case, so printing a pair of zeros against it would put
// a column on the rail that is only ever noise. Nothing to say, nothing said.
func TestDivergencePrintsNothingWhenNothingHasDrifted(t *testing.T) {
	if got := divergence(0, 0); got != "" {
		t.Errorf("a branch in step prints %q, want nothing", got)
	}
	for _, c := range []struct {
		ahead, behind int
		want          string
	}{
		{2, 0, "↑2"},
		{0, 3, "↓3"},
		{2, 3, "↑2 ↓3"},
		{12, 0, "↑12"},
	} {
		if got := divergence(c.ahead, c.behind); got != c.want {
			t.Errorf("divergence(%d, %d) is %q, want %q", c.ahead, c.behind, got, c.want)
		}
	}
}

// TestTheTwoHalvesOfTheFigureAreInkedApart. Commits you have and the upstream
// does not are not the same news as commits it has and you do not. One ink for
// both would make a row that needs a pull look like a row that needs a push.
func TestTheTwoHalvesOfTheFigureAreInkedApart(t *testing.T) {
	parts := splitDivergence("↑2 ↓3")
	if len(parts) != 2 {
		t.Fatalf("the figure split into %d parts, want 2: %+v", len(parts), parts)
	}
	if parts[0].behind {
		t.Error("the ahead half is marked as behind")
	}
	if !parts[1].behind {
		t.Error("the behind half is not marked as behind")
	}
	// The separator has to stay with one of them, or the two inked runs butt
	// together and the figure reads as one number.
	if joined := parts[0].text + parts[1].text; joined != "↑2 ↓3" {
		t.Errorf("the halves rejoin as %q, want the figure back unchanged", joined)
	}

	// One-sided figures are one part, so nothing draws an empty styled run.
	if got := splitDivergence("↑2"); len(got) != 1 || got[0].behind {
		t.Errorf("an ahead-only figure split into %+v", got)
	}
	if got := splitDivergence("↓3"); len(got) != 1 || !got[0].behind {
		t.Errorf("a behind-only figure split into %+v", got)
	}
}

// TestTheSectionSaysNothingAboutADirectoryOutsideARepository. The rows are what
// the rail budgets against, and a directory that is not in a repository has no
// rows, so the section is dropped rather than drawing an empty heading.
func TestTheSectionSaysNothingAboutADirectoryOutsideARepository(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.gitView = gitView{Dir: "/tmp/x", Found: false}
	if rows := m.gitRows(); len(rows) != 0 {
		t.Errorf("a directory outside a repository drew %d rows: %+v", len(rows), rows)
	}
}

// TestTheSectionDrawsTheRepositoryAndItsBranch, in that order, because the
// repository is the thing you act on and the branch is a fact about it.
func TestTheSectionDrawsTheRepositoryAndItsBranch(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.gitView = gitView{
		Dir:   "/src/tuios",
		Found: true,
		State: gitstate.State{
			Repo: "tuios", Branch: "main",
			Upstream: "refs/remotes/origin/main", Ahead: 2,
		},
	}

	rows := m.gitRows()
	if len(rows) != 2 {
		t.Fatalf("the section drew %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0].Kind != gitRowRepo || rows[0].Name != "tuios" {
		t.Errorf("the first row is %+v, want the repository", rows[0])
	}
	if rows[1].Kind != gitRowBranch || rows[1].Name != "main" {
		t.Errorf("the second row is %+v, want the branch", rows[1])
	}
	if rows[1].Right != "↑2" {
		t.Errorf("the branch row carries %q, want ↑2", rows[1].Right)
	}
}

// TestABranchWithNoUpstreamCarriesNoFigure. Zero and zero is what a branch in
// step reports, so a branch that follows nothing has to be distinguishable from
// one that is level. Otherwise the rail says "in step" about a branch that has
// nothing to be in step with.
func TestABranchWithNoUpstreamCarriesNoFigure(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.gitView = gitView{
		Dir: "/src/tuios", Found: true,
		State: gitstate.State{Repo: "tuios", Branch: "scratch"},
	}
	rows := m.gitRows()
	if len(rows) != 2 {
		t.Fatalf("the section drew %d rows, want 2", len(rows))
	}
	if rows[1].Right != "" {
		t.Errorf("a branch with no upstream carries %q", rows[1].Right)
	}
}

// TestTheLayoutNamesTheSection. The rail's sections are a layout the user
// writes, so the section arrives the same way every other one does, by being
// named, and is absent otherwise. There is no second switch for it.
func TestTheLayoutNamesTheSection(t *testing.T) {
	s := config.Global

	s.SidebarSections = "sessions,terminals"
	if sidebarLayoutHas(sidebarSectionGit, &s) {
		t.Error("a layout that does not name git has a git section")
	}

	s.SidebarSections = "sessions:25,terminals,git:20"
	if !sidebarLayoutHas(sidebarSectionGit, &s) {
		t.Error("a layout that names git has no git section")
	}
}

// TestAReadingForAnAbandonedDirectoryIsDropped. A reading is taken on a
// goroutine, so it can land after the focus has moved to a pane somewhere else.
// Applying it would put another directory's branch under this pane's name.
func TestAReadingForAnAbandonedDirectoryIsDropped(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.gitView.asked = "/src/tuios"

	m.ApplyGitState(GitStateMsg{
		Dir:   "/somewhere/else",
		Found: true,
		State: gitstate.State{Repo: "other", Branch: "main"},
	})
	if m.gitView.Found {
		t.Errorf("a reading for a directory the focus had left was applied: %+v", m.gitView.State)
	}

	// The positive half, so the guard is not just refusing everything.
	m.ApplyGitState(GitStateMsg{
		Dir:   "/src/tuios",
		Found: true,
		State: gitstate.State{Repo: "tuios", Branch: "main"},
	})
	if !m.gitView.Found || m.gitView.State.Repo != "tuios" {
		t.Errorf("the reading for the directory in hand was not applied: %+v", m.gitView)
	}
}

// TestTheBranchRowGivesWayBeforeTheFigure is the truncation rule. A branch cut
// to "feature/long-nam…" still says which branch. A figure cut from "↑12" to
// "↑1" says something false, so it is never the thing that shrinks.
func TestTheBranchRowGivesWayBeforeTheFigure(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.gitView = gitView{
		Dir: "/src/tuios", Found: true,
		State: gitstate.State{
			Repo:     "tuios",
			Branch:   "feature/a-really-quite-long-branch-name",
			Upstream: "refs/remotes/origin/main", Ahead: 12, Behind: 34,
		},
	}
	rows := m.gitRows()
	if len(rows) != 2 {
		t.Fatalf("the section drew %d rows, want 2", len(rows))
	}

	const cw = 28
	out := stripANSIForTrace(m.sidebarGitRow(rows[1], cw, theme.UI(), sidebarRowState{}))
	if !strings.Contains(out, "↑12 ↓34") {
		t.Errorf("the figure did not survive a narrow rail: %q", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the branch was not the thing that gave way: %q", out)
	}
}
