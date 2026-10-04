// Package ghpr reads and opens GitHub pull requests through the gh CLI.
//
// tuios holds no GitHub token and speaks no GitHub API. Everything here runs
// the person's own gh, with the person's own login, so what tuios can do on
// GitHub is exactly what gh can, and a person who never installed gh or
// never logged in gets a message that says so instead of a request that
// fails somewhere else.
//
// gh runs non-interactively: it never prompts, never pages, never checks for
// an update, and is killed when the caller's context ends.
package ghpr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PR states, lower case.
const (
	StateOpen   = "open"
	StateMerged = "merged"
	StateClosed = "closed"
)

// Check rollups.
const (
	ChecksPass    = "pass"
	ChecksFail    = "fail"
	ChecksPending = "pending"
)

// PR is what tuios keeps about a pull request.
type PR struct {
	// Number and URL name the pull request.
	Number int    `json:"number"`
	URL    string `json:"url"`
	// State is open, merged or closed.
	State string `json:"state"`
	// Checks rolls up the status checks: pass, fail or pending, empty when
	// the pull request has none.
	Checks string `json:"checks,omitempty"`
	// Passed, Failed and Pending count the checks.
	Passed  int `json:"passed,omitempty"`
	Failed  int `json:"failed,omitempty"`
	Pending int `json:"pending,omitempty"`
	// Branch is the branch the pull request is for.
	Branch string `json:"branch,omitempty"`
	// CheckedAt is when gh was last asked, in Unix nanoseconds.
	CheckedAt int64 `json:"checked_at,omitempty"`
}

// Final reports whether the pull request can change no more: it was merged
// or closed. A poll stops at a final state.
func (p *PR) Final() bool {
	return p != nil && (p.State == StateMerged || p.State == StateClosed)
}

// Same reports whether p and o say the same thing, ignoring when they were
// read.
func (p *PR) Same(o *PR) bool {
	if p == nil || o == nil {
		return p == o
	}
	a, b := *p, *o
	a.CheckedAt, b.CheckedAt = 0, 0
	return a == b
}

// Badge is the short form a rail row shows: "PR #12 open pass". It is empty
// for nil.
func (p *PR) Badge() string {
	if p == nil || p.Number == 0 {
		return ""
	}
	s := fmt.Sprintf("PR #%d %s", p.Number, p.State)
	if p.Checks != "" && p.State == StateOpen {
		s += " " + p.Checks
	}
	return s
}

// ErrNotInstalled reports that gh is not on PATH.
var ErrNotInstalled = errors.New("the gh CLI is not installed")

// ErrNoPR reports a branch with no pull request.
var ErrNoPR = errors.New("no pull request for the branch")

// Error is a gh call that failed, with what gh said.
type Error struct {
	Args   []string
	Stderr string
	Auth   bool
}

func (e *Error) Error() string {
	msg := e.Stderr
	if msg == "" {
		msg = "it exited with an error"
	}
	return "gh " + strings.Join(e.Args, " ") + ": " + msg
}

// Path is the gh on PATH, or ErrNotInstalled.
func Path() (string, error) {
	p, err := exec.LookPath("gh")
	if err != nil {
		return "", ErrNotInstalled
	}
	return p, nil
}

// run executes gh in dir and returns its stdout.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_PAGER=", "PAGER=cat",
		"NO_COLOR=1", "CLICOLOR=0", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("gh %s: %w", args[0], ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		low := strings.ToLower(msg)
		return "", &Error{
			Args:   args,
			Stderr: msg,
			Auth:   strings.Contains(low, "gh auth login") || strings.Contains(low, "not logged in") || strings.Contains(low, "authentication"),
		}
	}
	return stdout.String(), nil
}

// CheckAuth reports whether gh is installed and logged in to the host of the
// repository at dir.
func CheckAuth(ctx context.Context, dir string) error {
	_, err := run(ctx, dir, "auth", "status")
	var ge *Error
	if errors.As(err, &ge) {
		ge.Auth = true
	}
	return err
}

// viewFields are the fields View asks gh for.
const viewFields = "state,statusCheckRollup,url,number"

// View reads the pull request of branch in the repository at dir.
func View(ctx context.Context, dir, branch string) (PR, error) {
	if strings.HasPrefix(branch, "-") {
		return PR{}, fmt.Errorf("%q is not a branch", branch)
	}
	out, err := run(ctx, dir, "pr", "view", branch, "--json", viewFields)
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && strings.Contains(strings.ToLower(ge.Stderr), "no pull requests found") {
			return PR{}, ErrNoPR
		}
		return PR{}, err
	}
	pr, err := Parse([]byte(out))
	if err != nil {
		return PR{}, err
	}
	pr.Branch = branch
	return pr, nil
}

// Parse reads gh pr view --json state,statusCheckRollup,url,number.
func Parse(data []byte) (PR, error) {
	var raw struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
		State  string `json:"state"`
		Checks []struct {
			Typename   string `json:"__typename"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return PR{}, fmt.Errorf("gh printed no pull request: %w", err)
	}
	if raw.Number <= 0 {
		return PR{}, errors.New("gh printed a pull request with no number")
	}
	pr := PR{Number: raw.Number, URL: raw.URL, State: strings.ToLower(raw.State)}
	for _, c := range raw.Checks {
		switch checkOutcome(c.Typename, c.Status, c.Conclusion, c.State) {
		case ChecksPass:
			pr.Passed++
		case ChecksFail:
			pr.Failed++
		case ChecksPending:
			pr.Pending++
		}
	}
	switch {
	case pr.Failed > 0:
		pr.Checks = ChecksFail
	case pr.Pending > 0:
		pr.Checks = ChecksPending
	case pr.Passed > 0:
		pr.Checks = ChecksPass
	}
	return pr, nil
}

// checkOutcome classifies one entry of statusCheckRollup: a check run, which
// has a status and a conclusion, or a commit status, which has a state.
func checkOutcome(typename, status, conclusion, state string) string {
	if typename == "StatusContext" || (status == "" && state != "") {
		switch strings.ToUpper(state) {
		case "SUCCESS":
			return ChecksPass
		case "FAILURE", "ERROR":
			return ChecksFail
		default:
			return ChecksPending
		}
	}
	if strings.ToUpper(status) != "COMPLETED" {
		return ChecksPending
	}
	switch strings.ToUpper(conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return ChecksPass
	default:
		return ChecksFail
	}
}

// CreateOptions are what Create passes to gh pr create.
type CreateOptions struct {
	// Head is the branch the pull request is from, Base the one it is to.
	// An empty Base takes the repository's default branch.
	Head, Base string
	// Title and Body are the pull request's. With both empty, gh fills them
	// from the commits.
	Title, Body string
	Draft       bool
}

// CreateArgs is the gh command line Create runs, without the program.
func CreateArgs(o CreateOptions) []string {
	args := []string{"pr", "create", "--head", o.Head}
	if o.Base != "" {
		args = append(args, "--base", o.Base)
	}
	switch {
	case o.Title == "" && o.Body == "":
		args = append(args, "--fill")
	default:
		args = append(args, "--title", o.Title, "--body", o.Body)
	}
	if o.Draft {
		args = append(args, "--draft")
	}
	return args
}

// Create opens a pull request in the repository at dir and returns the URL
// gh printed.
func Create(ctx context.Context, dir string, o CreateOptions) (string, error) {
	if strings.HasPrefix(o.Head, "-") || strings.HasPrefix(o.Base, "-") {
		return "", fmt.Errorf("%q or %q is not a branch", o.Head, o.Base)
	}
	out, err := run(ctx, dir, CreateArgs(o)...)
	if err != nil {
		return "", err
	}
	url := ""
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "https://") || strings.HasPrefix(line, "http://") {
			url = line
		}
	}
	return url, nil
}
