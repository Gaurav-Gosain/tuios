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
)

// A fake git with one repository, the Go project in ProjectDir. It keeps the
// files of the last commit and a staging set, and compares them with the
// shared filesystem, so a file changed at the prompt shows up in git status
// and git diff like it would for real.

type gitCommit struct {
	hash    string
	message string
	when    time.Time
}

var repo = struct {
	head    map[string]string // path relative to ProjectDir -> content at HEAD
	staged  map[string]bool
	commits []gitCommit // oldest first
}{
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

// gitChange is one path that differs from HEAD.
type gitChange struct {
	path   string
	status string // "modified", "new file", "deleted"
	staged bool
}

// repoFiles lists the files under the repository with their content. The
// caller holds fsMu.
func repoFiles() map[string]string {
	out := map[string]string{}
	prefix := ProjectDir + "/"
	for p, c := range files {
		if strings.HasPrefix(p, prefix) {
			out[p[len(prefix):]] = c
		}
	}
	return out
}

// gitChanges compares the working tree with HEAD.
func gitChanges() []gitChange {
	fsMu.RLock()
	defer fsMu.RUnlock()
	work := repoFiles()
	var out []gitChange
	for p, c := range work {
		head, tracked := repo.head[p]
		switch {
		case !tracked:
			out = append(out, gitChange{path: p, status: "new file", staged: repo.staged[p]})
		case head != c:
			out = append(out, gitChange{path: p, status: "modified", staged: repo.staged[p]})
		}
	}
	for p := range repo.head {
		if _, ok := work[p]; !ok {
			out = append(out, gitChange{path: p, status: "deleted", staged: repo.staged[p]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func inRepo(cwd string) bool {
	return cwd == ProjectDir || strings.HasPrefix(cwd, ProjectDir+"/")
}

func cmdGit(s *shell, args []string) int {
	t := s.t
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		t.Print("usage: git <command>\r\n\r\n")
		for _, l := range [][2]string{
			{"status", "what changed"},
			{"log", "the history (try --oneline)"},
			{"diff", "the changes, line by line"},
			{"add", "stage a file (git add . for all)"},
			{"commit", "commit what is staged (git commit -m \"msg\")"},
			{"restore", "throw a change away"},
			{"branch", "list branches"},
		} {
			t.Printf("  %s%-8s%s %s\r\n", green, l[0], reset, l[1])
		}
		return 0
	}
	if args[0] == "--version" || args[0] == "version" {
		t.Print("git version 2.99.0 (tuios web edition)\r\n")
		return 0
	}
	if !inRepo(s.cwd) {
		s.fail("fatal: not a git repository. The demo repo is in " + bold + "~/projects/hello" + reset)
		return 128
	}
	switch args[0] {
	case "status", "st":
		return gitStatus(t, hasFlag(args, "-s", "--short"))
	case "log":
		return gitLog(t, args[1:])
	case "diff":
		return gitDiff(t, hasFlag(args, "--staged", "--cached"))
	case "add":
		return gitAdd(s, args[1:])
	case "commit":
		return gitCommitCmd(s, args[1:])
	case "restore", "checkout":
		return gitRestore(s, args[1:])
	case "branch":
		t.Print("* " + green + "main" + reset + "\r\n")
		return 0
	case "show":
		return gitShow(t)
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

func gitStatus(t *TTY, short bool) int {
	changes := gitChanges()
	if short {
		for _, c := range changes {
			code := map[string]string{"modified": "M", "new file": "A", "deleted": "D"}[c.status]
			switch {
			case c.staged:
				t.Printf("%s%s%s  %s\r\n", green, code, reset, c.path)
			case c.status == "new file":
				t.Printf("%s??%s %s\r\n", red, reset, c.path)
			default:
				t.Printf(" %s%s%s %s\r\n", red, code, reset, c.path)
			}
		}
		return 0
	}
	t.Print("On branch " + bold + "main" + reset + "\r\n")
	var staged, unstaged, untracked []gitChange
	for _, c := range changes {
		switch {
		case c.staged:
			staged = append(staged, c)
		case c.status == "new file":
			untracked = append(untracked, c)
		default:
			unstaged = append(unstaged, c)
		}
	}
	if len(changes) == 0 {
		t.Print("nothing to commit, working tree clean\r\n")
		return 0
	}
	if len(staged) > 0 {
		t.Print("\r\nChanges to be committed:\r\n")
		for _, c := range staged {
			t.Printf("\t%s%-10s %s%s\r\n", green, c.status+":", c.path, reset)
		}
	}
	if len(unstaged) > 0 {
		t.Print("\r\nChanges not staged for commit:\r\n")
		t.Print(dim + "  (git add <file> to stage, git diff to see the change)" + reset + "\r\n")
		for _, c := range unstaged {
			t.Printf("\t%s%-10s %s%s\r\n", red, c.status+":", c.path, reset)
		}
	}
	if len(untracked) > 0 {
		t.Print("\r\nUntracked files:\r\n")
		for _, c := range untracked {
			t.Printf("\t%s%s%s\r\n", red, c.path, reset)
		}
	}
	return 0
}

func gitLog(t *TTY, args []string) int {
	oneline := hasFlag(args, "--oneline")
	limit := len(repo.commits)
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
	fsMu.RLock()
	commits := append([]gitCommit(nil), repo.commits...)
	fsMu.RUnlock()
	shown := 0
	for i := len(commits) - 1; i >= 0 && shown < limit; i-- {
		c := commits[i]
		ref := ""
		if i == len(commits)-1 {
			ref = " (" + cyan + bold + "HEAD -> " + green + "main" + reset + yellow + ")"
		}
		if oneline {
			t.Printf("%s%s%s%s %s\r\n", yellow, c.hash, ref, reset, c.message)
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

func gitDiff(t *TTY, staged bool) int {
	fsMu.RLock()
	work := repoFiles()
	head := make(map[string]string, len(repo.head))
	maps.Copy(head, repo.head)
	fsMu.RUnlock()
	for _, c := range gitChanges() {
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

func gitAdd(s *shell, args []string) int {
	if len(args) == 0 {
		s.fail("Nothing specified, nothing added. Try " + bold + "git add ." + reset)
		return 1
	}
	all := hasFlag(args, ".", "-A", "--all")
	changes := gitChanges()
	fsMu.Lock()
	defer fsMu.Unlock()
	for _, c := range changes {
		if all || hasFlag(args, c.path) {
			repo.staged[c.path] = true
		}
	}
	return 0
}

func gitCommitCmd(s *shell, args []string) int {
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
	changes := gitChanges()
	fsMu.Lock()
	work := repoFiles()
	n := 0
	for _, c := range changes {
		if !c.staged && !(all && c.status != "new file") {
			continue
		}
		if c.status == "deleted" {
			delete(repo.head, c.path)
		} else {
			repo.head[c.path] = work[c.path]
		}
		delete(repo.staged, c.path)
		n++
	}
	var hash string
	if n > 0 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(msg + strconv.Itoa(len(repo.commits))))
		hash = fmt.Sprintf("%07x", h.Sum32())[:7]
		repo.commits = append(repo.commits, gitCommit{hash: hash, message: msg, when: time.Now()})
	}
	fsMu.Unlock()
	if n == 0 {
		s.fail("nothing to commit. Stage something first with " + bold + "git add" + reset)
		return 1
	}
	plural := "s"
	if n == 1 {
		plural = ""
	}
	s.t.Printf("[main %s] %s\r\n %d file%s changed\r\n", hash, msg, n, plural)
	return 0
}

func gitRestore(s *shell, args []string) int {
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
	defer fsMu.Unlock()
	for _, p := range targets {
		rel := strings.TrimPrefix(resolve(s.cwd, p), ProjectDir+"/")
		head, ok := repo.head[rel]
		if !ok {
			s.t.Print(red + "error: " + p + " is not in the last commit" + reset + "\r\n")
			return 1
		}
		files[ProjectDir+"/"+rel] = head
		delete(repo.staged, rel)
	}
	return 0
}

func gitShow(t *TTY) int {
	fsMu.RLock()
	c := repo.commits[len(repo.commits)-1]
	fsMu.RUnlock()
	t.Printf("%scommit %s%s\r\nAuthor: guest <guest@tuios.dev>\r\nDate:   %s\r\n\r\n    %s\r\n", yellow, c.hash, reset, c.when.Format("Mon Jan 2 15:04 2006"), c.message)
	return 0
}
