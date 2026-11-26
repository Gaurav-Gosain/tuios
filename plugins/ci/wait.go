package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const waitUsage = `Usage: tuios-ci wait [OWNER/REPO] [--sha SHA] [--start DURATION]

Waits for the build of a commit to end: HEAD of the folder you work in, or
SHA. It prints one line for each failed job, with the failing test, its
file and line, and the link of the job.

It exits 0 when the build passed, 1 when it failed and 2 when the commit
has no build. --start is how long to wait for a build to start after a
push. The default is 1m.
`

// waitRun is "tuios-ci wait".
func waitRun(args []string) int {
	repo, sha, start := "", "", time.Minute
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			fmt.Print(waitUsage)
			return 0
		case a == "--sha" && i+1 < len(args):
			i++
			sha = args[i]
		case a == "--start" && i+1 < len(args):
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil || d < 0 {
				fmt.Fprintf(os.Stderr, "tuios-ci wait: %s is not a duration such as 30s or 2m.\n", args[i])
				return 2
			}
			start = d
		case !strings.HasPrefix(a, "-") && repo == "" && strings.Count(a, "/") == 1:
			repo = a
		default:
			fmt.Fprintf(os.Stderr, "tuios-ci wait: %s is not an argument it knows.\n\n%s", a, waitUsage)
			return 2
		}
	}
	if repo == "" || sha == "" {
		f := focusOfFolder()
		if repo == "" {
			repo = f.Repo
		}
		if sha == "" {
			sha = f.SHA
		}
	}
	if repo == "" {
		fmt.Fprintln(os.Stderr, "tuios-ci wait: this folder has no GitHub repository. Name one as OWNER/REPO.")
		return 2
	}
	if sha == "" {
		fmt.Fprintln(os.Stderr, "tuios-ci wait: this folder has no commit. Name one with --sha.")
		return 2
	}
	sha = strings.ToLower(sha)
	g, err := newGH()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tuios-ci wait: "+err.Error()+".")
		return 2
	}
	began := time.Now()
	var gap time.Duration
	for {
		b, ok, changed, err := buildOf(g, repo, sha)
		switch {
		case err != nil && time.Since(began) >= start:
			fmt.Fprintln(os.Stderr, "tuios-ci wait: cannot read the builds of "+repo+": "+err.Error())
			return 2
		case err != nil:
			fmt.Fprintln(os.Stderr, "tuios-ci wait: cannot read the builds of "+repo+": "+err.Error()+". It tries again.")
		case !ok && time.Since(began) >= start:
			fmt.Printf("No build of %s in %s.\n", short(sha), repo)
			return 2
		case ok && !active(b.State):
			return report(g, repo, b)
		}
		// The same back-off as the pane: GitHub does not count an answer
		// that did not change, and the wait doubles up to 48 seconds.
		switch {
		case !ok:
			gap = min(activeInterval, max(time.Second, start-time.Since(began)))
		case changed:
			gap = activeInterval
		default:
			gap = min(gap*2, slowestPoll)
		}
		time.Sleep(gap)
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// report prints the end of a build and gives the exit code.
func report(g *gh, repo string, b Build) int {
	if b.State == "success" {
		fmt.Printf("Passed: %s (%s) in %s.\n", b.Title, short(b.SHA), dur(b.Seconds))
		return 0
	}
	fmt.Printf("Failed: %s (%s).\n", b.Title, short(b.SHA))
	fs := failuresOf(g, repo, b, 20)
	for _, f := range fs {
		line := "✗ " + f.job
		if f.file != "" {
			line += " in " + f.file
		}
		if f.test != "" {
			line += ": " + f.test
		}
		if f.where != "" {
			line += " at " + f.where
		}
		if f.url != "" {
			line += " " + f.url
		}
		fmt.Println(line)
	}
	if len(fs) == 0 {
		fmt.Printf("The build ended as %s. %s\n", b.State, b.URL)
	}
	return 1
}
