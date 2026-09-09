package sessiontree

// A worktree session is a session whose directory is a linked git worktree.
// The daemon records which repository and which branch, and the surfaces group
// those sessions under a row for their repository instead of listing them flat.
// The grouping is the information: five sessions named after five branches of
// one repository say nothing about each other in a flat list, and everything
// about each other under one parent.
//
// This package is deliberately ignorant of internal/session, so the record
// arrives here as the three fields a row is drawn from rather than as the
// daemon's own type.

// WorktreeRef is what a session row needs to know about the worktree it sits
// in. Nil on a session that is not one.
type WorktreeRef struct {
	// Repo is the repository's name, which is what the parent row is titled.
	Repo string
	// Branch is the branch the worktree has checked out, which is what the
	// child row is labelled.
	Branch string
	// Group names the fan-out this session belongs to, empty for a worktree
	// made on its own. Carried for the surfaces that report it; the grouping
	// below is by repository, because that is the row a person looks for.
	Group string
	// Gone says the worktree directory no longer exists. The session is still
	// listed, because a shell whose directory was removed under it still runs.
	Gone bool
}

// GroupByRepo returns the session list with every worktree session gathered
// under a KindRepo parent row for its repository.
//
// The result is a flat run, not a nested tree: a parent is immediately
// followed by its members, which are the same session nodes with their own
// windows still on them. Every surface that walks the list by session (the
// terminals section, the agents section, the palette) therefore keeps working
// on the members without knowing this ran, and only the surface that draws
// rows has to know what a KindRepo node is.
//
// A parent lands where its first member sat, so a rail the user has dragged
// into an order keeps that order: the group appears where the eye already
// expects the first of its sessions. Members that sit further down are pulled
// up under it. A repository with one worktree session still gets a parent.
//
// Nodes with no record pass through untouched, in place. So do the federated
// rows: a session on another machine is drawn in its host group, and pulling
// one into a local repository group would say it is here.
func GroupByRepo(nodes []Node) []Node {
	members := map[string][]Node{}
	repos := make([]string, 0, len(nodes))
	for _, n := range nodes {
		repo := groupRepo(n)
		if repo == "" {
			continue
		}
		if _, seen := members[repo]; !seen {
			repos = append(repos, repo)
		}
		members[repo] = append(members[repo], n)
	}
	if len(repos) == 0 {
		return nodes
	}

	out := make([]Node, 0, len(nodes)+len(repos))
	drawn := make(map[string]bool, len(repos))
	for _, n := range nodes {
		repo := groupRepo(n)
		if repo == "" {
			out = append(out, n)
			continue
		}
		if drawn[repo] {
			continue // pulled up under the parent already
		}
		drawn[repo] = true
		group := members[repo]
		out = append(out, repoNode(repo, group))
		for i, child := range group {
			child.GroupLast = i == len(group)-1
			out = append(out, child)
		}
	}
	return out
}

// groupRepo is the repository a node is grouped under, or "" for a node that is
// not grouped at all.
func groupRepo(n Node) string {
	if n.Kind != KindSession || n.Host != "" || n.Worktree == nil {
		return ""
	}
	return n.Worktree.Repo
}

// repoNode builds the parent row for one repository.
//
// It carries the roll-up of its members' states so a collapsed group can still
// say that something inside it wants a human, and the member count so it can
// say how many rows it is hiding. Both are read only when the group is shut;
// an open group's children say it themselves.
func repoNode(repo string, members []Node) Node {
	node := Node{
		Kind:        KindRepo,
		ID:          repo,
		Title:       repo,
		WindowCount: len(members),
	}
	best := 0
	for _, m := range members {
		if r := AgentRank(m.AgentState, m.DoneSeen); r > best {
			node.AgentState, node.DoneSeen, best = m.AgentState, m.DoneSeen, r
		}
	}
	return node
}
