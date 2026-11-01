package sessiontree

import "slices"

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
	// FanID tells two fans of one name apart. Empty for a remote session
	// and for a record from before the field.
	FanID string
	// Gone says the worktree directory no longer exists. The session is still
	// listed, because a shell whose directory was removed under it still runs.
	Gone bool
	// Main says the session sits in the repository's main checkout, the
	// directory holding the shared .git, rather than in a worktree split off
	// it. Its repo row points here: activating the header opens this session.
	Main bool
	// PR is the short form of the branch's pull request ("PR #12 open
	// pass"), empty when there is none. The agent rows of the session draw
	// it as their pr token.
	PR string
	// PRFailed is how many of the pull request's checks failed.
	PRFailed int
	// Path is the worktree's directory on its machine, and RepoRoot its
	// repository's main checkout. A fan is one group in one repository.
	Path     string
	RepoRoot string
	// Prompt is the first line of the task a fan gave the session, empty
	// when no fan made it.
	Prompt string
	// Pick is the session a judge picked among the fan's attempts, and
	// VerdictAt when, 0 with no verdict. JudgeWindow is the pane of the
	// judge started for the fan, empty when none runs.
	Pick        string
	VerdictAt   int64
	JudgeWindow string
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

// GroupByRepoCompact is GroupByRepo without the line a repository's own row
// costs when a session can stand in for it: the first session in the main
// checkout becomes the repository's row, and the group's other sessions follow
// it. The result has a KindRepo row only for a repository with no main checkout
// session in nodes, because there is nothing to attach that row to.
//
// A repository whose only session is its main checkout is a plain session, so
// it loses its record on the returned row. Selecting and numbering read the
// session's name and neither looks at the record.
func GroupByRepoCompact(nodes []Node) []Node {
	// The rail draws this list on every rebuild of a frame, and a Node is large,
	// so the rows GroupByRepo allocated are rewritten in place. A group only ever
	// loses rows here, so the write position never passes the read position. With
	// no group GroupByRepo hands back the caller's slice, and nothing is written.
	rows := GroupByRepo(nodes)
	w := 0
	for i := 0; i < len(rows); {
		if rows[i].Kind != KindRepo {
			if w != i {
				rows[w] = rows[i]
			}
			w++
			i++
			continue
		}
		end := i + 1
		for end < len(rows) && !rows[end-1].GroupLast {
			end++
		}
		w = compactGroup(rows, w, i, end)
		i = end
	}
	return rows[:w]
}

// compactGroup rewrites one repository's rows, rows[repo:end] with its KindRepo
// row first, at rows[w:], and returns the position after them. The first main
// checkout session is pulled up as the head.
func compactGroup(rows []Node, w, repo, end int) int {
	members := rows[repo+1 : end]
	head := slices.IndexFunc(members, func(n Node) bool { return n.Worktree.Main })
	if head < 0 {
		return w + copy(rows[w:], rows[repo:end])
	}
	lead := members[head]
	if len(members) == 1 {
		lead.Worktree, lead.GroupLast = nil, false
		rows[w] = lead
		return w + 1
	}
	lead.GroupHead, lead.GroupLast = true, false
	lead.GroupCount = len(members) - 1
	state, doneSeen, current := rows[repo].AgentState, rows[repo].DoneSeen, rows[repo].GroupCurrent
	lead.GroupState, lead.GroupDoneSeen, lead.GroupCurrent = state, doneSeen, current
	if !lead.Named {
		lead.Title = rows[repo].Title
	}
	rest := w + 1
	// The rows before the head move first, then the rows after it, so neither
	// overwrites a row still to be read. The lead was copied out above.
	rest += copy(rows[rest:], members[:head])
	rest += copy(rows[rest:], members[head+1:])
	rows[w] = lead
	rows[rest-1].GroupLast = true
	if rest-2 > w {
		rows[rest-2].GroupLast = false
	}
	return rest
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
		node.GroupCurrent = node.GroupCurrent || m.IsCurrent
	}
	return node
}
