// Command tuios-ci shows the GitHub Actions builds of a repository in a
// tuios pane: the time a running build has left, its jobs on a timeline,
// and the log of a failed job opened at the test that failed. Its design
// follows omaci by Jankees van Woezik (MIT).
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

const usage = `Usage: tuios-ci [OWNER/REPO] [--sha SHA]

Shows the GitHub Actions builds of a repository. Without OWNER/REPO, it
shows the repository of the folder you work in. --sha selects the build of
one commit. You need the gh CLI, logged in.
`

func main() {
	var repo, sha string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			return
		case a == "--sha" && i+1 < len(args):
			i++
			sha = args[i]
		case strings.HasPrefix(a, "--sha="):
			sha = strings.TrimPrefix(a, "--sha=")
		case !strings.HasPrefix(a, "-") && repo == "" && strings.Count(a, "/") == 1:
			repo = a
		default:
			fmt.Fprintf(os.Stderr, "tuios-ci: %s is not an argument it knows.\n\n%s", a, usage)
			os.Exit(2)
		}
	}
	g, err := newGH()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tuios-ci: "+err.Error()+".")
		os.Exit(1)
	}
	if repo == "" {
		repo = repoOfFolder()
	}
	if repo == "" {
		repo = lastRepo()
	}
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		fmt.Fprintln(os.Stderr, "tuios-ci: run it in a terminal: "+err.Error())
		os.Exit(1)
	}
	m := newModel(g, repo, strings.ToLower(sha))
	m.scr = &screen{out: os.Stdout}
	m.scr.start()
	chunks := make(chan []byte, 16)
	go readInput(chunks)
	pal, rest := queryPalette(os.Stdout, chunks, 500*time.Millisecond)
	m.pal = newColors(pal)
	msgs := make(chan Msg, 64)
	watchSize(msgs)
	go readKeys(msgs, chunks, rest)
	restore := func() {
		m.scr.stop()
		_ = term.Restore(os.Stdin.Fd(), state)
	}
	defer func() {
		// A panic leaves the terminal as it found it, then says what broke.
		if r := recover(); r != nil {
			restore()
			fmt.Fprintf(os.Stderr, "tuios-ci stopped on an error: %v\n%s", r, debug.Stack())
			os.Exit(1)
		}
	}()
	final := run(m, msgs)
	restore()
	if final.repo != "" {
		saveRepo(final.repo)
	}
}

// repoOfFolder is the GitHub repository of the folder you work in: the
// folder of the focused pane when tuios runs the plugin.
func repoOfFolder() string {
	dir := os.Getenv("TUIOS_PLUGIN_CWD")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	out, err := exec.Command("git", "-C", dir, "remote").Output()
	if err != nil {
		return ""
	}
	remotes := strings.Fields(string(out))
	// origin first, then the others in the order git lists them.
	if i := slices.Index(remotes, "origin"); i > 0 {
		remotes = append([]string{"origin"}, slices.Delete(remotes, i, i+1)...)
	}
	for _, r := range remotes {
		url, err := exec.Command("git", "-C", dir, "remote", "get-url", r).Output()
		if err == nil {
			if repo := repoFromRemote(string(url)); repo != "" {
				return repo
			}
		}
	}
	return ""
}

// The name of the repository you watched last is the one thing the plugin
// writes, in its state folder, so the next pane opens on it outside a
// checkout.
func stateFile() string {
	dir := os.Getenv("TUIOS_PLUGIN_STATE_DIR")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "last-repo")
}

func lastRepo() string {
	if f := stateFile(); f != "" {
		b, _ := os.ReadFile(f)
		if r := strings.TrimSpace(string(b)); strings.Count(r, "/") == 1 {
			return r
		}
	}
	return ""
}

func saveRepo(repo string) {
	if f := stateFile(); f != "" {
		_ = os.MkdirAll(filepath.Dir(f), 0o700)
		_ = os.WriteFile(f, []byte(repo+"\n"), 0o600)
	}
}
