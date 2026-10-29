package webshell

import (
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// A fake git with one repository, the Go project in ProjectDir, and the
// worktrees made of it. Each checkout keeps the files of its last commit and
// a staging set, and compares them with the shared filesystem, so a file
// changed at the prompt shows up in git status and git diff like it would for
// real.

type gitCommit struct {
	hash    string
	message string
	when    time.Time
}

// checkout is one work tree of the repository: the main checkout, or a
// linked worktree on a branch of its own.
type checkout struct {
	root   string
	branch string
	// base is the branch a linked worktree was made from, "" for the main
	// checkout. Review diffs a worktree from where it left base.
	base string
	// fork is base's files, and forkHash its last commit, when the worktree
	// was made. Nil for the main checkout.
	fork     map[string]string
	forkHash string
	head     map[string]string // path relative to root -> content at HEAD
	staged   map[string]bool
	commits  []gitCommit // oldest first
}

// headHash is the checkout's last commit.
func (c *checkout) headHash() string {
	if len(c.commits) == 0 {
		return "0000000"
	}
	return c.commits[len(c.commits)-1].hash
}

var repo *checkout

func initRepo() {
	repo = &checkout{
		root:   ProjectDir,
		branch: "main",
		head: map[string]string{
			"go.mod":        helloGoMod,
			"main.go":       helloMain,
			"greet.go":      helloGreetHead,
			"greet_test.go": helloTest,
			"README.md":     helloReadme,
		},
		staged: map[string]bool{},
		commits: []gitCommit{
			{hash: "e41d7a2", message: "Initial commit", when: started.Add(-72 * time.Hour)},
			{hash: "3c9a0f1", message: "Say hello", when: started.Add(-50 * time.Hour)},
			{hash: "7d2b8e4", message: "Split greet into its own file", when: started.Add(-26 * time.Hour)},
			{hash: "a1f3c9e", message: "Add a test for greet", when: started.Add(-3 * time.Hour)},
		},
	}
}

// linked are the worktrees made since the tab opened, oldest first. Guarded
// by fsMu, like the files.
var linked []*checkout

// WorktreeDir is where tuios makes a worktree, as worktree.DefaultDir is on
// a real machine with no TUIOS_WORKTREE_DIR set.
const WorktreeDir = Home + "/.local/share/tuios/worktrees"

// checkouts is the main checkout, then every linked worktree. The caller
// holds fsMu.
func checkouts() []*checkout { return append([]*checkout{repo}, linked...) }

// checkoutAt is the checkout dir is in, or nil. The caller holds fsMu.
func checkoutAt(dir string) *checkout {
	var best *checkout
	for _, c := range checkouts() {
		if dir == c.root || strings.HasPrefix(dir, c.root+"/") {
			if best == nil || len(c.root) > len(best.root) {
				best = c
			}
		}
	}
	return best
}

// lockedCheckoutAt is checkoutAt for a caller that does not hold fsMu.
func lockedCheckoutAt(dir string) *checkout {
	fsMu.RLock()
	defer fsMu.RUnlock()
	return checkoutAt(dir)
}

// branchAt is the branch checked out where dir is, "" outside the repository.
func branchAt(dir string) string {
	fsMu.RLock()
	defer fsMu.RUnlock()
	if c := checkoutAt(dir); c != nil {
		return c.branch
	}
	return ""
}

// gitChange is one path that differs from HEAD.
type gitChange struct {
	path   string
	status string // "modified", "new file", "deleted"
	staged bool
}

// files lists the files under the checkout with their content. The
// caller holds fsMu.
func (c *checkout) files() map[string]string {
	out := map[string]string{}
	prefix := c.root + "/"
	for p, content := range files {
		if strings.HasPrefix(p, prefix) && !inLinkedBelow(c, p) {
			out[p[len(prefix):]] = content
		}
	}
	return out
}

// inLinkedBelow reports whether p is in another checkout inside c, which c
// does not track. The caller holds fsMu.
func inLinkedBelow(c *checkout, p string) bool {
	for _, o := range linked {
		if o != c && len(o.root) > len(c.root) && strings.HasPrefix(p, o.root+"/") {
			return true
		}
	}
	return false
}

// changes compares the checkout's working tree with its HEAD.
func (c *checkout) changes() []gitChange {
	fsMu.RLock()
	defer fsMu.RUnlock()
	return c.changesLocked()
}

// changesLocked is changes for a caller that holds fsMu.
func (c *checkout) changesLocked() []gitChange {
	work := c.files()
	var out []gitChange
	for p, content := range work {
		head, tracked := c.head[p]
		switch {
		case !tracked:
			out = append(out, gitChange{path: p, status: "new file", staged: c.staged[p]})
		case head != content:
			out = append(out, gitChange{path: p, status: "modified", staged: c.staged[p]})
		}
	}
	for p := range c.head {
		if _, ok := work[p]; !ok {
			out = append(out, gitChange{path: p, status: "deleted", staged: c.staged[p]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func cmdGit(s *shell, args []string) int {
	t := s.t
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		t.Print("usage: git <command>\r\n\r\n")
		rows := [][2]string{
			{"status", "what changed"},
			{"log", "the history (try --oneline)"},
			{"diff", "the changes, line by line"},
			{"add", "stage a file (git add . for all)"},
			{"commit", "commit what is staged (git commit -m \"msg\")"},
			{"restore", "throw a change away"},
			{"branch", "list branches"},
			{"worktree list", "the worktrees of the repository"},
		}
		t.Lay(func(cols int) []string { return table(rows, 2, green, cols) })
		return 0
	}
	if args[0] == "--version" || args[0] == "version" {
		t.Print("git version 2.99.0 (tuios web edition)\r\n")
		return 0
	}
	c := lockedCheckoutAt(s.cwd)
	if c == nil {
		s.fail("fatal: not a git repository. The demo repo is in " + bold + "~/projects/hello" + reset)
		return 128
	}
	switch args[0] {
	case "status", "st":
		return gitStatus(t, c, hasFlag(args, "-s", "--short"))
	case "log":
		return gitLog(t, c, args[1:])
	case "diff":
		return gitDiff(t, c, hasFlag(args, "--staged", "--cached"))
	case "add":
		return gitAdd(s, c, args[1:])
	case "commit":
		return gitCommitCmd(s, c, args[1:])
	case "restore", "checkout":
		return gitRestore(s, c, args[1:])
	case "branch":
		return gitBranch(t, c)
	case "worktree":
		return gitWorktree(s, args[1:])
	case "show":
		return gitShow(t, c)
	}
	s.fail("git: '" + args[0] + "' is not in this demo. Try git status, git log or git diff.")
	return 1
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		if slices.Contains(names, a) {
			return true
		}
	}
	return false
}

func gitStatus(t *TTY, c *checkout, short bool) int {
	changes := c.changes()
	if short {
		for _, ch := range changes {
			code := map[string]string{"modified": "M", "new file": "A", "deleted": "D"}[ch.status]
			switch {
			case ch.staged:
				t.Printf("%s%s%s  %s\r\n", green, code, reset, ch.path)
			case ch.status == "new file":
				t.Printf("%s??%s %s\r\n", red, reset, ch.path)
			default:
				t.Printf(" %s%s%s %s\r\n", red, code, reset, ch.path)
			}
		}
		return 0
	}
	t.Print("On branch " + bold + c.branch + reset + "\r\n")
	var staged, unstaged, untracked []gitChange
	for _, ch := range changes {
		switch {
		case ch.staged:
			staged = append(staged, ch)
		case ch.status == "new file":
			untracked = append(untracked, ch)
		default:
			unstaged = append(unstaged, ch)
		}
	}
	if len(changes) == 0 {
		t.Print("nothing to commit, working tree clean\r\n")
		return 0
	}
	if len(staged) > 0 {
		t.Print("\r\nChanges to be committed:\r\n")
		for _, ch := range staged {
			t.Printf("\t%s%-10s %s%s\r\n", green, ch.status+":", ch.path, reset)
		}
	}
	if len(unstaged) > 0 {
		t.Print("\r\nChanges not staged for commit:\r\n")
		t.Lay(func(cols int) []string {
			var lines []string
			for _, l := range wrapWords("(git add <file> to stage, git diff to see the change)", cols-2) {
				lines = append(lines, dim+"  "+l+reset)
			}
			return lines
		})
		for _, ch := range unstaged {
			t.Printf("\t%s%-10s %s%s\r\n", red, ch.status+":", ch.path, reset)
		}
	}
	if len(untracked) > 0 {
		t.Print("\r\nUntracked files:\r\n")
		for _, ch := range untracked {
			t.Printf("\t%s%s%s\r\n", red, ch.path, reset)
		}
	}
	return 0
}

func gitLog(t *TTY, co *checkout, args []string) int {
	oneline := hasFlag(args, "--oneline")
	fsMu.RLock()
	commits := append([]gitCommit(nil), co.commits...)
	branch := co.branch
	fsMu.RUnlock()
	limit := len(commits)
	for i, a := range args {
		if a == "-n" && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				limit = n
			}
		} else if strings.HasPrefix(a, "-") && len(a) > 1 {
			if n, err := strconv.Atoi(a[1:]); err == nil {
				limit = n
			}
		}
	}
	shown := 0
	for i := len(commits) - 1; i >= 0 && shown < limit; i-- {
		c := commits[i]
		ref := ""
		if i == len(commits)-1 {
			ref = " (" + cyan + bold + "HEAD -> " + green + branch + reset + yellow + ")"
		}
		if oneline {
			// One commit to a line, cut at the pane edge the way git's
			// pager shows it.
			line := yellow + c.hash + ref + reset + " " + c.message
			t.Lay(func(cols int) []string { return []string{fit(line, cols)} })
		} else {
			t.Printf("%scommit %s%s%s\r\n", yellow, c.hash, ref, reset)
			t.Print("Author: guest <guest@tuios.dev>\r\n")
			t.Print("Date:   " + c.when.Format("Mon Jan 2 15:04 2006") + "\r\n\r\n")
			t.Print("    " + c.message + "\r\n\r\n")
		}
		shown++
	}
	return 0
}

func gitDiff(t *TTY, co *checkout, staged bool) int {
	fsMu.RLock()
	work := co.files()
	head := make(map[string]string, len(co.head))
	maps.Copy(head, co.head)
	fsMu.RUnlock()
	for _, c := range co.changes() {
		if c.staged != staged || (!staged && c.status == "new file") {
			continue
		}
		old := head[c.path]
		cur := work[c.path]
		t.Printf("%sdiff --git a/%s b/%s%s\r\n", bold, c.path, c.path, reset)
		switch c.status {
		case "new file":
			t.Print(bold + "new file" + reset + "\r\n")
		case "deleted":
			t.Print(bold + "deleted file" + reset + "\r\n")
		}
		t.Printf("%s--- a/%s\r\n+++ b/%s%s\r\n", bold, c.path, c.path, reset)
		t.Print(unifiedHunks(lineDiff(old, cur)))
	}
	return 0
}

func gitAdd(s *shell, co *checkout, args []string) int {
	if len(args) == 0 {
		s.fail("Nothing specified, nothing added. Try " + bold + "git add ." + reset)
		return 1
	}
	all := hasFlag(args, ".", "-A", "--all")
	changes := co.changes()
	fsMu.Lock()
	defer fsMu.Unlock()
	for _, c := range changes {
		if all || hasFlag(args, c.path) {
			co.staged[c.path] = true
		}
	}
	return 0
}

func gitCommitCmd(s *shell, co *checkout, args []string) int {
	msg := ""
	all := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-m":
			if i+1 < len(args) {
				msg = args[i+1]
				i++
			}
		case "-a", "--all":
			all = true
		case "-am":
			all = true
			if i+1 < len(args) {
				msg = args[i+1]
				i++
			}
		}
	}
	if msg == "" {
		s.fail("The demo has no editor for the message. Use " + bold + "git commit -m \"your message\"" + reset)
		return 1
	}
	hash, n := co.commit(msg, func(c gitChange) bool { return c.staged || (all && c.status != "new file") })
	if n == 0 {
		s.fail("nothing to commit. Stage something first with " + bold + "git add" + reset)
		return 1
	}
	plural := "s"
	if n == 1 {
		plural = ""
	}
	s.t.Printf("[%s %s] %s\r\n %d file%s changed\r\n", co.branch, hash, msg, n, plural)
	return 0
}

// commit records every change take accepts as a new commit with msg, and
// returns its hash and how many files it took. It takes nothing and makes
// no commit when take accepts no change.
func (co *checkout) commit(msg string, take func(gitChange) bool) (string, int) {
	fsMu.Lock()
	defer fsMu.Unlock()
	work := co.files()
	n := 0
	for _, c := range co.changesLocked() {
		if !take(c) {
			continue
		}
		if c.status == "deleted" {
			delete(co.head, c.path)
		} else {
			co.head[c.path] = work[c.path]
		}
		delete(co.staged, c.path)
		n++
	}
	if n == 0 {
		return "", 0
	}
	hash := commitHash(co.branch, msg, len(co.commits))
	co.commits = append(co.commits, gitCommit{hash: hash, message: msg, when: time.Now()})
	return hash, n
}

// commitHash is a short hash for a new commit, the same for the same branch,
// message and place in the history.
func commitHash(branch, msg string, n int) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(branch + msg + strconv.Itoa(n)))
	return fmt.Sprintf("%07x", h.Sum32())[:7]
}

func gitRestore(s *shell, co *checkout, args []string) int {
	var targets []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		s.fail("Which file? Try " + bold + "git restore greet.go" + reset)
		return 1
	}
	fsMu.Lock()
	defer filesChanged()
	defer fsMu.Unlock()
	for _, p := range targets {
		rel := strings.TrimPrefix(resolve(s.cwd, p), co.root+"/")
		head, ok := co.head[rel]
		if !ok {
			s.t.Print(red + "error: " + p + " is not in the last commit" + reset + "\r\n")
			return 1
		}
		files[co.root+"/"+rel] = head
		delete(co.staged, rel)
	}
	return 0
}

func gitShow(t *TTY, co *checkout) int {
	fsMu.RLock()
	c := co.commits[len(co.commits)-1]
	fsMu.RUnlock()
	t.Printf("%scommit %s%s\r\nAuthor: guest <guest@tuios.dev>\r\nDate:   %s\r\n\r\n    %s\r\n", yellow, c.hash, reset, c.when.Format("Mon Jan 2 15:04 2006"), c.message)
	return 0
}

// gitBranch lists the branches, the way git branch does: * on the one
// checked out here, + on one checked out in another worktree.
func gitBranch(t *TTY, here *checkout) int {
	fsMu.RLock()
	all := checkouts()
	fsMu.RUnlock()
	sort.Slice(all, func(i, j int) bool { return all[i].branch < all[j].branch })
	for _, c := range all {
		switch {
		case c == here:
			t.Print("* " + green + c.branch + reset + "\r\n")
		default:
			t.Print("+ " + cyan + c.branch + reset + " " + dim + "(" + prettyPath(c.root) + ")" + reset + "\r\n")
		}
	}
	return 0
}

// gitWorktree is git worktree list. A worktree is made with ctrl+b U here,
// the way tuios makes one on a real machine.
func gitWorktree(s *shell, args []string) int {
	t := s.t
	switch argOr(args, 0, "list") {
	case "list":
		fsMu.RLock()
		all := checkouts()
		rows := make([][3]string, len(all))
		for i, c := range all {
			rows[i] = [3]string{prettyPath(c.root), c.headHash(), c.branch}
		}
		fsMu.RUnlock()
		width := 0
		for _, r := range rows {
			width = max(width, len(r[0]))
		}
		t.Lay(func(cols int) []string {
			var out []string
			for _, r := range rows {
				line := r[0] + strings.Repeat(" ", width-len(r[0])) + "  " + yellow + r[1] + reset + " [" + green + r[2] + reset + "]"
				out = append(out, fit(line, cols))
			}
			return out
		})
		return 0
	case "add":
		s.fail("In the demo, make a worktree with " + bold + "Ctrl+B U" + reset + " in this pane.")
		return 1
	}
	s.fail("git worktree " + args[0] + " is not in this demo. Try " + bold + "git worktree list" + reset)
	return 1
}

// RepoRoot is the main checkout of the repository dir is in, or "" outside
// it. From a linked worktree it is the main checkout too, as on a real
// machine.
func RepoRoot(dir string) string {
	ready()
	if lockedCheckoutAt(dir) == nil {
		return ""
	}
	return repo.root
}

// WorktreeAt is the root of the checkout dir is in and its branch, or empty
// strings outside the repository.
func WorktreeAt(dir string) (root, branch string) {
	ready()
	fsMu.RLock()
	defer fsMu.RUnlock()
	if c := checkoutAt(dir); c != nil {
		return c.root, c.branch
	}
	return "", ""
}

// NewWorktree makes a worktree of the repository at repoRoot on a new branch,
// where tuios puts one on a real machine, with the files of the last commit
// of the main checkout. It returns the worktree's folder and the session name
// tuios would give it.
func NewWorktree(repoRoot, branch string) (path, session string, err error) {
	ready()
	branch = strings.TrimSpace(branch)
	if repoRoot != repo.root {
		return "", "", fmt.Errorf("%s is not a git repository", prettyPath(repoRoot))
	}
	if err := validBranch(branch); err != nil {
		return "", "", err
	}
	path = WorktreeDir + "/" + baseName(repo.root) + "/" + worktree.Slug(branch)
	session = worktree.SessionName(baseName(repo.root), branch)
	fsMu.Lock()
	for _, c := range checkouts() {
		if c.branch == branch {
			fsMu.Unlock()
			return "", "", fmt.Errorf("a branch named %s already exists", branch)
		}
		if c.root == path {
			fsMu.Unlock()
			return "", "", fmt.Errorf("%s already exists", prettyPath(path))
		}
	}
	c := &checkout{
		root:     path,
		branch:   branch,
		base:     repo.branch,
		fork:     maps.Clone(repo.head),
		forkHash: repo.headHash(),
		head:     maps.Clone(repo.head),
		staged:   map[string]bool{},
		commits:  slices.Clone(repo.commits),
	}
	linked = append(linked, c)
	for rel, content := range c.head {
		files[path+"/"+rel] = content
		addParents(path + "/" + rel)
	}
	fsDirs[path] = true
	fsMu.Unlock()
	filesChanged()
	emit(Event{Type: EventWorktree, Data: map[string]any{"repo": baseName(repo.root), "branch": branch, "path": path}})
	return path, session, nil
}

// validBranch refuses the branch names git refuses, in the ways a person is
// likely to type one.
func validBranch(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("type a branch name")
	case strings.ContainsAny(name, " ~^:?*[\\") || strings.Contains(name, ".."),
		strings.HasPrefix(name, "-"), strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"),
		strings.HasSuffix(name, ".lock"), strings.HasSuffix(name, "."):
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	return nil
}
