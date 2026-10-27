package webshell

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// tuios ship in the demo: commit and merge work on the fake repository the
// way they do on a real machine, and push and pr show what they would send.
// Nothing leaves the tab, and the output says so.

// demoRemote is the remote the demo repository pretends to push to.
const demoRemote = "https://github.com/you/hello"

// shipPRs are the pull requests tuios ship pr pretended to open, by branch.
// Guarded by fsMu.
var shipPRs = map[string]int{}

func tuiosShip(s *shell, args []string) int {
	sub := argOr(args, 0, "")
	co := lockedCheckoutAt(s.cwd)
	if sub == "" || sub == "help" || sub == "--help" || sub == "-h" {
		return shipOverview(s, co)
	}
	if co == nil {
		return s.fail("tuios ship works on a pane in a git repository. The demo repo is in " + bold + "~/projects/hello" + reset)
	}
	switch sub {
	case "commit":
		return shipCommit(s, co, args[1:])
	case "merge":
		return shipMerge(s, co)
	case "push":
		return shipOutbound(s, co, false)
	case "pr":
		return shipOutbound(s, co, true)
	case "status":
		return shipStatus(s, co)
	}
	return s.fail("tuios ship " + sub + " is not a command. Try " + bold + "tuios ship" + reset)
}

// shipOverview is what tuios ship prints with no command: the steps from
// this pane's worktree to a merged change.
func shipOverview(s *shell, co *checkout) int {
	t := s.t
	rows := [][2]string{
		{"tuios ship commit -m MSG", "commit every change on the branch"},
		{"tuios ship merge", "merge the branch into its base"},
		{"tuios ship push", "push the branch"},
		{"tuios ship pr", "push it and open a pull request"},
		{"tuios ship status", "the pull request and its checks"},
	}
	var head []string
	if co != nil {
		fsMu.RLock()
		branch, base, root, n := co.branch, co.base, co.root, len(co.changesLocked())
		fsMu.RUnlock()
		head = append(head, bold+"Ship "+branch+reset+" from "+prettyPath(root)+".")
		switch {
		case base == "":
			head = append(head, "This is the main checkout. Make a worktree with Ctrl+B U to ship a branch from it.")
		case n == 1:
			head = append(head, "It has 1 uncommitted change.")
		default:
			head = append(head, "It has "+strconv.Itoa(n)+" uncommitted changes.")
		}
	} else {
		head = append(head, "Take an agent's work from its worktree to a merged change.")
	}
	t.Lay(func(cols int) []string {
		var out []string
		for _, h := range head {
			out = append(out, wrapWords(h, cols)...)
		}
		out = append(out, "")
		out = append(out, table(rows, 2, green, cols)...)
		out = append(out, "")
		for _, l := range wrapWords("push and pr show what they send and ask first. In this demo nothing leaves the tab.", cols) {
			out = append(out, dim+l+reset)
		}
		return out
	})
	return 0
}

// shipCommit commits every change in the checkout, untracked files too, as
// tuios ship commit does.
func shipCommit(s *shell, co *checkout, args []string) int {
	msg := ""
	for i := 0; i < len(args); i++ {
		if (args[i] == "-m" || args[i] == "--message") && i+1 < len(args) {
			msg = args[i+1]
			i++
		}
	}
	if msg == "" {
		return s.fail("Give the message with -m. On a real machine, tuios takes the agent's last prompt when there is no -m.")
	}
	hash, n := co.commit(msg, func(gitChange) bool { return true })
	if n == 0 {
		return s.fail("Nothing to commit in " + prettyPath(co.root) + ".")
	}
	subject, _, _ := strings.Cut(msg, "\n")
	s.t.Printf("Committed %s on %s: %s\r\n", hash, co.branch, subject)
	return 0
}

// shipMerge merges a worktree's branch into its base in the main checkout,
// with the refusals tuios ship merge makes.
func shipMerge(s *shell, co *checkout) int {
	if co.base == "" {
		return s.fail("This pane is in the main checkout. Run tuios ship merge in a pane of a worktree.")
	}
	fsMu.Lock()
	main := repo
	var dirty []string
	for _, c := range main.changesLocked() {
		if c.status != "new file" {
			dirty = append(dirty, c.path)
		}
	}
	if len(dirty) > 0 {
		fsMu.Unlock()
		word := "changes"
		if len(dirty) == 1 {
			word = "change"
		}
		return s.fail(fmt.Sprintf("The main checkout %s has %d uncommitted %s: %s. Nothing was merged. Commit or restore them in the main checkout, then merge again.",
			prettyPath(main.root), len(dirty), word, strings.Join(dirty, ", ")))
	}
	forkAt := slices.IndexFunc(co.commits, func(c gitCommit) bool { return c.hash == co.forkHash })
	ahead := co.commits[forkAt+1:]
	if len(ahead) == 0 {
		fsMu.Unlock()
		s.t.Printf("%s already has every commit of %s. Nothing was merged.\r\n", main.branch, co.branch)
		return shipUncommittedNote(s, co)
	}
	// What the branch changed since it left the base, file by file.
	changed := map[string]bool{}
	for p, content := range co.head {
		if co.fork[p] != content {
			changed[p] = true
		}
	}
	for p := range co.fork {
		if _, ok := co.head[p]; !ok {
			changed[p] = true
		}
	}
	var conflicts []string
	for p := range changed {
		mainHas, mainOK := main.head[p]
		forkHas, forkOK := co.fork[p]
		if mainHas != forkHas || mainOK != forkOK {
			if branchHas, ok := co.head[p]; !ok || branchHas != mainHas {
				conflicts = append(conflicts, p)
			}
		}
	}
	if len(conflicts) > 0 {
		fsMu.Unlock()
		slices.Sort(conflicts)
		return s.fail("The merge conflicts in " + strings.Join(conflicts, ", ") + ". It was undone, and the main checkout is as it was.")
	}
	fastForward := main.headHash() == co.forkHash
	for p := range changed {
		if content, ok := co.head[p]; ok {
			main.head[p] = content
			files[main.root+"/"+p] = content
			addParents(main.root + "/" + p)
		} else {
			delete(main.head, p)
			delete(files, main.root+"/"+p)
		}
	}
	mode := "fast-forward"
	if fastForward {
		main.commits = slices.Clone(co.commits)
	} else {
		mode = "merge commit"
		main.commits = append(main.commits, ahead...)
		msg := "Merge branch '" + co.branch + "'"
		main.commits = append(main.commits, gitCommit{hash: commitHash(main.branch, msg, len(main.commits)), message: msg, when: time.Now()})
	}
	after := main.headHash()
	co.fork, co.forkHash = maps.Clone(co.head), co.headHash()
	fsMu.Unlock()
	filesChanged()
	word := "commits"
	if len(ahead) == 1 {
		word = "commit"
	}
	s.t.Printf("Merged %s into %s in %s: %s, %d %s, now at %s.\r\n", co.branch, main.branch, prettyPath(main.root), mode, len(ahead), word, after)
	return shipUncommittedNote(s, co)
}

// shipUncommittedNote says what a merge left behind in the worktree.
func shipUncommittedNote(s *shell, co *checkout) int {
	n := len(co.changes())
	if n == 0 {
		return 0
	}
	word := "changes"
	if n == 1 {
		word = "change"
	}
	s.t.Lay(func(cols int) []string {
		return wrapWords(fmt.Sprintf("The worktree still has %d uncommitted %s, which were not merged. Commit them with 'tuios ship commit -m MSG'.", n, word), cols)
	})
	return 0
}

// shipOutbound is tuios ship push, and with pr also tuios ship pr: what the
// command would send, and what it prints on a real machine once you say yes.
func shipOutbound(s *shell, co *checkout, pr bool) int {
	fsMu.Lock()
	branch, base, head := co.branch, co.base, co.headHash()
	number, ok := shipPRs[branch]
	if pr && !ok {
		number = 42 + len(shipPRs)
		shipPRs[branch] = number
	}
	fsMu.Unlock()
	if base == "" {
		base = branch
	}
	plan := []string{"push " + branch + " at " + head + " to origin (" + demoRemote + ")"}
	if pr {
		plan = append(plan, "open a pull request from "+branch+" into "+base+" with gh")
	}
	result := []string{"Pushed " + branch + " at " + head + " to origin (" + demoRemote + ")."}
	if pr {
		result = []string{
			"Pushed " + branch + " at " + head + " to origin.",
			"Opened pull request #" + strconv.Itoa(number) + ": " + demoRemote + "/pull/" + strconv.Itoa(number),
		}
	}
	s.t.Lay(func(cols int) []string {
		out := []string{"tuios ship will:"}
		for _, p := range plan {
			out = append(out, fitLines([]string{"  " + p}, cols)...)
		}
		out = append(out, "")
		for _, l := range wrapWords("This is a demo, so nothing leaves the tab. On a real machine, you say yes, and it prints:", cols) {
			out = append(out, dim+l+reset)
		}
		return append(out, fitLines(result, cols)...)
	})
	return 0
}

// shipStatus is tuios ship status: the pull request ship pr pretended to
// open, if any.
func shipStatus(s *shell, co *checkout) int {
	fsMu.RLock()
	branch := co.branch
	number, ok := shipPRs[branch]
	fsMu.RUnlock()
	if !ok {
		s.t.Print(branch + ": no pull request is known. Open one with 'tuios ship pr'.\r\n")
		return 0
	}
	s.t.Printf("%s: open (demo)\r\n%s/pull/%d\r\nChecks: 3 passed, 0 failed, 0 pending.\r\n", branch, demoRemote, number)
	return 0
}
