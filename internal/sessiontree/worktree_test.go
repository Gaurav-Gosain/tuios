package sessiontree

import (
	"strings"
	"testing"
)

// wtSession is one worktree session node, as the app builds them.
func wtSession(name, repo, branch string) Node {
	return Node{
		Kind:     KindSession,
		ID:       name,
		Title:    name,
		Worktree: &WorktreeRef{Repo: repo, Branch: branch},
	}
}

// rowNames renders a grouped list as "kind:id" lines, which is what every
// claim below is about: what row landed where.
func rowNames(nodes []Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		kind := "session"
		if n.Kind == KindRepo {
			kind = "repo"
		}
		out = append(out, kind+":"+n.ID)
	}
	return out
}

func TestGroupByRepoPutsEveryWorktreeSessionUnderItsRepository(t *testing.T) {
	nodes := []Node{
		{Kind: KindSession, ID: "plain", Title: "plain"},
		wtSession("tuios-feat-one", "tuios", "feat/one"),
		wtSession("docs-fix", "docs", "fix/typo"),
		wtSession("tuios-feat-two", "tuios", "feat/two"),
	}

	got := rowNames(GroupByRepo(nodes))
	want := []string{
		"session:plain",
		// The parent lands where its first member sat, and the member further
		// down is pulled up under it.
		"repo:tuios", "session:tuios-feat-one", "session:tuios-feat-two",
		"repo:docs", "session:docs-fix",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("grouped rows are\n\t%v\nwant\n\t%v", got, want)
	}
}

func TestGroupByRepoLeavesUngroupedSessionsAlone(t *testing.T) {
	nodes := []Node{
		{Kind: KindSession, ID: "plain", Title: "plain"},
		{Kind: KindHost, ID: "\x00host/build", Title: "build", Host: "build"},
		// A session on another machine carries no local repository row: it is
		// drawn in its host group.
		{Kind: KindSession, ID: "\x00host/build:api", Host: "build", Worktree: &WorktreeRef{Repo: "tuios", Branch: "main"}},
	}

	got := GroupByRepo(nodes)
	for _, n := range got {
		if n.Kind == KindRepo {
			t.Fatalf("a repository row was made for %v, which holds no local worktree session", rowNames(got))
		}
	}
	if len(got) != len(nodes) {
		t.Errorf("grouping changed a list with nothing to group: %v", rowNames(got))
	}
}

func TestGroupByRepoMarksTheLastMemberOfEachGroup(t *testing.T) {
	nodes := []Node{
		wtSession("a", "tuios", "feat/one"),
		wtSession("b", "tuios", "feat/two"),
		wtSession("c", "docs", "fix/typo"),
	}

	last := map[string]bool{}
	for _, n := range GroupByRepo(nodes) {
		if n.Kind == KindSession {
			last[n.ID] = n.GroupLast
		}
	}
	if last["a"] {
		t.Error("the first of two members is marked last, so it would close the group early")
	}
	if !last["b"] {
		t.Error("the last member of the tuios group is not marked, so the group never closes")
	}
	if !last["c"] {
		t.Error("the only member of the docs group is not marked last")
	}
}

func TestGroupByRepoRollsUpTheWorstMemberState(t *testing.T) {
	nodes := []Node{
		wtSession("a", "tuios", "feat/one"),
		wtSession("b", "tuios", "feat/two"),
		wtSession("c", "tuios", "feat/three"),
	}
	nodes[0].AgentState = "working"
	nodes[1].AgentState = "errored"
	nodes[2].AgentState = "done"

	var parent Node
	for _, n := range GroupByRepo(nodes) {
		if n.Kind == KindRepo {
			parent = n
		}
	}
	if parent.AgentState != "errored" {
		t.Errorf("the group rolled up to %q, want errored: a shut group must show the worst of what it hides", parent.AgentState)
	}
	if parent.WindowCount != 3 {
		t.Errorf("the group counts %d members, want 3", parent.WindowCount)
	}
	if parent.Title != "tuios" {
		t.Errorf("the group is titled %q, want the repository name", parent.Title)
	}
}

func TestGroupByRepoRollUpPrefersAnUnseenDoneOverWorking(t *testing.T) {
	// The same ladder AgentRank sets for a session over its windows: a finished
	// pane nobody has looked at outranks one still working, and drops below it
	// once seen.
	nodes := []Node{
		wtSession("a", "tuios", "feat/one"),
		wtSession("b", "tuios", "feat/two"),
	}
	nodes[0].AgentState = "working"
	nodes[1].AgentState, nodes[1].DoneSeen = "done", false

	for _, n := range GroupByRepo(nodes) {
		if n.Kind == KindRepo && n.AgentState != "done" {
			t.Errorf("the group rolled up to %q, want the unseen done", n.AgentState)
		}
	}

	nodes[1].DoneSeen = true
	for _, n := range GroupByRepo(nodes) {
		if n.Kind == KindRepo && n.AgentState != "working" {
			t.Errorf("with the done pane seen the group rolled up to %q, want working", n.AgentState)
		}
	}
}

func TestBuildSessionCarriesTheWorktreeRecord(t *testing.T) {
	tree := Build([]SessionInput{
		{Name: "tuios-feat-one", Worktree: &WorktreeRef{Repo: "tuios", Branch: "feat/one", Gone: true}},
	})
	if len(tree.Sessions) != 1 {
		t.Fatalf("built %d sessions, want 1", len(tree.Sessions))
	}
	wt := tree.Sessions[0].Worktree
	if wt == nil {
		t.Fatal("the built session dropped its worktree record, so no surface can group it")
	}
	if wt.Repo != "tuios" || wt.Branch != "feat/one" || !wt.Gone {
		t.Errorf("the record came through as %+v, want repo tuios, branch feat/one, gone", *wt)
	}
}
