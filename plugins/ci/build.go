package main

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// Build is a commit: every run that a push, a pull request or a dispatch
// started for it, rolled into one. It is running while any of them runs,
// red when any failed, and lasts from the first run's start to the last
// run's end. Runs that something else started, such as a schedule or a
// comment, also attach to the head of the branch. Rolled in, a nightly job
// would stretch a four minute build to half a day, so each of those is a
// build of its own.
type Build struct {
	Key       string
	IsBuild   bool
	SHA       string
	Title     string
	Branch    string
	Event     string
	Actor     string
	URL       string
	State     string
	Failing   bool // A run failed while others still run.
	Started   time.Time
	Ended     time.Time // Zero while the build runs.
	Seconds   float64   // Zero while the build runs.
	Workflows []string
	Runs      []Run
}

func buildEvent(e string) bool {
	switch e {
	case "push", "pull_request", "pull_request_target", "workflow_dispatch", "merge_group":
		return true
	}
	return false
}

// rollUp is the state of several runs together. A skipped run says nothing
// unless every run was skipped.
func rollUp(runs []Run) (string, bool) {
	var running, queued, failed, cancelled, done, counted int
	for _, r := range runs {
		if r.State == "skipped" {
			continue
		}
		counted++
		switch r.State {
		case "running":
			running++
		case "queued":
			queued++
		case "failure":
			failed++
		case "cancelled":
			cancelled++
		}
		if !active(r.State) {
			done++
		}
	}
	switch {
	case counted == 0:
		return "skipped", false
	case running > 0 || (queued > 0 && done > 0):
		return "running", failed > 0
	case queued > 0:
		return "queued", failed > 0
	case failed > 0:
		return "failure", true
	case cancelled > 0:
		return "cancelled", false
	}
	return "success", false
}

// builds rolls runs, newest first, into builds, newest first.
func builds(runs []Run) []Build {
	var order []string
	grouped := map[string][]Run{}
	for _, r := range runs {
		key := "run:" + strconv.FormatInt(r.ID, 10)
		if buildEvent(r.Event) && r.SHA != "" {
			key = r.SHA
		}
		if _, ok := grouped[key]; !ok {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], r)
	}
	out := make([]Build, 0, len(order))
	for _, key := range order {
		members := grouped[key]
		// Oldest first, so the jobs of a build read in the order they started.
		slices.SortStableFunc(members, func(a, b Run) int { return a.Started.Compare(b.Started) })
		st, failing := rollUp(members)
		b := Build{Key: key, IsBuild: !strings.HasPrefix(key, "run:"), SHA: members[0].SHA, State: st, Runs: members}
		var lead *Run
		for i := range members {
			r := &members[i]
			if r.State == "skipped" && st != "skipped" {
				continue
			}
			if !slices.Contains(b.Workflows, r.Workflow) {
				b.Workflows = append(b.Workflows, r.Workflow)
			}
			// The run to name the build after: the one that failed, else
			// the one still going, else the first.
			if lead == nil || (r.State == "failure" && lead.State != "failure") ||
				(active(r.State) && !active(lead.State) && lead.State != "failure") {
				lead = r
			}
			if b.Started.IsZero() || r.Started.Before(b.Started) {
				b.Started = r.Started
			}
			if r.Updated.After(b.Ended) {
				b.Ended = r.Updated
			}
		}
		if lead == nil {
			lead = &members[0]
		}
		b.Title, b.Branch, b.Event, b.Actor, b.URL = lead.Title, lead.Branch, lead.Event, lead.Actor, lead.URL
		if active(st) {
			b.Ended = time.Time{}
			b.Failing = failing
		} else if !b.Started.IsZero() && b.Ended.After(b.Started) {
			b.Seconds = b.Ended.Sub(b.Started).Round(time.Second).Seconds()
		}
		out = append(out, b)
	}
	return out
}

// filters are the toggles of the bar at the top.
type filters struct {
	branch      string // Only the builds of this branch. M sets the default branch, F the branch of the build.
	activeOnly  bool   // A: only the builds that run or wait for a runner.
	showSkipped bool   // S: the builds in which every run was skipped too.
}

// shown is the list the filters leave. The build in keep stays with A on,
// so the build you watch does not leave the list at the moment it ends.
func shown(all []Build, f filters, defaultBranch, keep string) []Build {
	mainOnly := f.branch != "" && f.branch == defaultBranch
	var out []Build
	for _, b := range all {
		if !f.showSkipped && b.State == "skipped" {
			continue
		}
		if f.branch != "" && b.Branch != f.branch {
			continue
		}
		// The builds of main: a scheduled job that runs on main is not one.
		if mainOnly && !b.IsBuild {
			continue
		}
		if f.activeOnly && !active(b.State) && b.Key != keep {
			continue
		}
		out = append(out, b)
	}
	return out
}
