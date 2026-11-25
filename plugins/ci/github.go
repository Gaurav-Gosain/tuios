package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Run is one workflow run, as the runs API gives it.
type Run struct {
	ID       int64
	Workflow string
	Title    string
	Branch   string
	Event    string
	Actor    string
	SHA      string
	URL      string
	State    string
	Started  time.Time
	Updated  time.Time
	// PR is the number of the pull request the run belongs to, or 0.
	PR int
}

// Step is one step of a job.
type Step struct {
	Number    int
	Name      string
	State     string
	Started   time.Time
	Completed time.Time
}

// Job is one job of a run.
type Job struct {
	ID        int64
	RunID     int64
	Name      string
	Workflow  string
	State     string
	URL       string
	Started   time.Time
	Completed time.Time
	Steps     []Step
}

// state is one word for a run, a job or a step: running, queued, success,
// failure, cancelled, skipped or neutral.
func state(status, conclusion string) string {
	switch {
	case status == "in_progress":
		return "running"
	case status != "completed":
		return "queued"
	}
	switch conclusion {
	case "success":
		return "success"
	case "failure", "timed_out", "startup_failure":
		return "failure"
	case "cancelled":
		return "cancelled"
	case "skipped":
		return "skipped"
	case "action_required":
		return "queued"
	}
	return "neutral"
}

func active(s string) bool { return s == "running" || s == "queued" }

func when(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

var (
	csiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	oscRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?`)
)

func control(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }

// clean makes text from GitHub safe to draw: no escape sequences and no
// control characters, so a commit title or a log line cannot drive the
// terminal.
func clean(s string) string {
	if !strings.ContainsFunc(s, control) {
		return s
	}
	s = csiRE.ReplaceAllString(s, "")
	s = oscRE.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case control(r):
			return -1
		}
		return r
	}, s)
}

func parseRuns(body []byte) ([]Run, error) {
	var in struct {
		Runs []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Title      string `json:"display_title"`
			Branch     string `json:"head_branch"`
			Event      string `json:"event"`
			SHA        string `json:"head_sha"`
			URL        string `json:"html_url"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			Started    string `json:"run_started_at"`
			Created    string `json:"created_at"`
			Updated    string `json:"updated_at"`
			PRs        []struct {
				Number int `json:"number"`
			} `json:"pull_requests"`
			Actor struct {
				Login string `json:"login"`
			} `json:"actor"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("GitHub sent a list of runs that is not JSON: %w", err)
	}
	out := make([]Run, 0, len(in.Runs))
	for _, r := range in.Runs {
		started := r.Started
		if started == "" {
			started = r.Created
		}
		pr := 0
		if len(r.PRs) > 0 {
			pr = r.PRs[0].Number
		}
		out = append(out, Run{PR: pr,
			ID: r.ID, Workflow: clean(r.Name), Title: clean(r.Title), Branch: clean(r.Branch),
			Event: r.Event, Actor: clean(r.Actor.Login), SHA: r.SHA, URL: r.URL,
			State: state(r.Status, r.Conclusion), Started: when(started), Updated: when(r.Updated),
		})
	}
	return out, nil
}

func parseJobs(body []byte) ([]Job, error) {
	var in struct {
		Jobs []struct {
			ID         int64  `json:"id"`
			RunID      int64  `json:"run_id"`
			Name       string `json:"name"`
			Workflow   string `json:"workflow_name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			URL        string `json:"html_url"`
			Started    string `json:"started_at"`
			Completed  string `json:"completed_at"`
			Steps      []struct {
				Number     int    `json:"number"`
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				Started    string `json:"started_at"`
				Completed  string `json:"completed_at"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("GitHub sent a list of jobs that is not JSON: %w", err)
	}
	out := make([]Job, 0, len(in.Jobs))
	for _, j := range in.Jobs {
		job := Job{
			ID: j.ID, RunID: j.RunID, Name: clean(j.Name), Workflow: clean(j.Workflow), URL: j.URL,
			State: state(j.Status, j.Conclusion), Started: when(j.Started), Completed: when(j.Completed),
		}
		for _, s := range j.Steps {
			job.Steps = append(job.Steps, Step{
				Number: s.Number, Name: clean(s.Name), State: state(s.Status, s.Conclusion),
				Started: when(s.Started), Completed: when(s.Completed),
			})
		}
		out = append(out, job)
	}
	return out, nil
}

// Repo is one repository of the menu.
type Repo struct {
	Name        string
	Description string
	Archived    bool
	Pushed      time.Time
}

func parseRepos(body []byte) ([]Repo, error) {
	var in []struct {
		Name        string `json:"full_name"`
		Description string `json:"description"`
		Archived    bool   `json:"archived"`
		Pushed      string `json:"pushed_at"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("GitHub sent a list of repositories that is not JSON: %w", err)
	}
	out := make([]Repo, 0, len(in))
	for _, r := range in {
		out = append(out, Repo{Name: clean(r.Name), Description: clean(r.Description), Archived: r.Archived, Pushed: when(r.Pushed)})
	}
	return out, nil
}

var remoteRE = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^@/]+@)?github\.com(?::\d+)?[:/]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// repoFromRemote is owner/name for the URL of a git remote on github.com,
// or "" for any other URL.
func repoFromRemote(url string) string {
	m := remoteRE.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

// Line kinds of a job's log.
const (
	plainLine = iota
	groupLine
	errorLine
	warningLine
	commandLine
	noticeLine
)

// LogLine is one line of a job's log.
type LogLine struct {
	Text string
	// Styled is the line with its own SGR colours, or "" when it has none.
	Styled string
	Kind   int
	// At is when the runner wrote the line, or zero for a line with no
	// timestamp. The log's clock is finer than the one the jobs API gives a
	// step.
	At time.Time
}

var sgrRE = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// keepSGR is s with its SGR colours and nothing else that could drive the
// terminal.
func keepSGR(s string) string {
	var b strings.Builder
	last := 0
	for _, loc := range sgrRE.FindAllStringIndex(s, -1) {
		b.WriteString(clean(s[last:loc[0]]))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(clean(s[last:]))
	return b.String()
}

// PullRequest is what the pill in the build shows.
type PullRequest struct {
	Number int
	Title  string
	State  string // open, merged or closed.
}

func parsePull(body []byte) (PullRequest, error) {
	var in struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		At     string `json:"merged_at"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return PullRequest{}, fmt.Errorf("GitHub sent a pull request that is not JSON: %w", err)
	}
	st := in.State
	if in.Merged || in.At != "" {
		st = "merged"
	}
	return PullRequest{Number: in.Number, Title: clean(in.Title), State: st}, nil
}

var stampRE = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z) ?`)

var markers = []struct {
	prefix string
	kind   int
}{
	{"##[group]", groupLine}, {"##[error]", errorLine}, {"##[warning]", warningLine},
	{"##[command]", commandLine}, {"##[notice]", noticeLine}, {"##[section]", groupLine},
	// Some actions, actions/checkout among them, echo commands without the ##.
	{"[command]", commandLine},
}

func parseLog(raw []byte) []LogLine {
	text := strings.TrimPrefix(string(raw), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r", "")
	parts := strings.Split(text, "\n")
	lines := make([]LogLine, 0, len(parts))
	for _, part := range parts {
		var line LogLine
		if m := stampRE.FindStringSubmatchIndex(part); m != nil {
			line.At, _ = time.Parse(time.RFC3339Nano, part[m[2]:m[3]])
			part = part[m[1]:]
		}
		if strings.HasPrefix(part, "##[endgroup]") {
			continue
		}
		for _, mk := range markers {
			if rest, ok := strings.CutPrefix(part, mk.prefix); ok {
				part, line.Kind = rest, mk.kind
				break
			}
		}
		line.Text = clean(part)
		if strings.Contains(part, "\x1b[") {
			line.Styled = keepSGR(part)
		}
		lines = append(lines, line)
	}
	// The log ends in a newline, which Split turns into one empty line.
	if n := len(lines); n > 0 && lines[n-1].Text == "" && lines[n-1].At.IsZero() {
		lines = lines[:n-1]
	}
	return lines
}

type stepKind int

const (
	setUpStep stepKind = iota
	containersStep
	stopContainersStep
	preStep
	postStep
	completeStep
	userStep
)

func kindOf(name string, first bool) stepKind {
	switch {
	case first || name == "Set up job":
		return setUpStep
	case name == "Initialize containers":
		return containersStep
	case name == "Stop containers":
		return stopContainersStep
	case name == "Complete job":
		return completeStep
	case strings.HasPrefix(name, "Post "):
		return postStep
	case strings.HasPrefix(name, "Pre "):
		return preStep
	}
	return userStep
}

func marks(l LogLine, k stepKind) bool {
	switch k {
	case userStep:
		return l.Kind == groupLine && strings.HasPrefix(l.Text, "Run ")
	case postStep:
		return l.Text == "Post job cleanup."
	case containersStep:
		return l.Kind == groupLine && l.Text == "Checking docker version"
	case stopContainersStep:
		return strings.HasPrefix(l.Text, "Print service container logs") || strings.HasPrefix(l.Text, "Stop and remove container")
	case completeStep:
		return l.Text == "Cleaning up orphan processes" || l.Text == "Evaluate and set job outputs"
	}
	return false
}

// mapSteps gives the line each step starts on in its job's log, or -1 for a
// step that wrote nothing or whose start cannot be found. The API gives a
// step's times in whole seconds, cut off, so a step that ended at :08 may
// have written until :08.999. Several steps often start in the same second,
// so each step takes the next marker of its kind inside its time window.
func mapSteps(steps []Step, lines []LogLine) []int {
	starts := make([]int, len(steps))
	for i := range starts {
		starts[i] = -1
	}
	cursor := 0
	for i, s := range steps {
		if cursor >= len(lines) {
			break
		}
		if s.State == "skipped" || s.State == "queued" {
			continue
		}
		k := kindOf(s.Name, i == 0)
		if k == setUpStep {
			starts[i], cursor = 0, 1
			continue
		}
		if s.Started.IsZero() {
			continue
		}
		found, fallback := -1, -1
		for at := cursor; at < len(lines); at++ {
			l := lines[at]
			if l.At.IsZero() {
				continue
			}
			if !s.Completed.IsZero() && !l.At.Before(s.Completed.Add(time.Second)) {
				break
			}
			if l.At.Before(s.Started) {
				continue
			}
			if fallback < 0 {
				fallback = at
			}
			if marks(l, k) {
				found = at
				break
			}
		}
		if found < 0 {
			found = fallback
		}
		if found < 0 {
			continue
		}
		starts[i], cursor = found, found+1
	}
	return starts
}
