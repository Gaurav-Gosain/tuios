package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSearchFoldsCaseByRune: a search must find and mark a match in a line
// where lowering a rune changes its length in bytes, as U+023A (2 bytes,
// lowers to 3) and the Kelvin sign U+212A (3 bytes, lowers to "k") do.
//
// How it could pass wrongly, written down first:
//   - The match could be counted and drawn on the wrong bytes: the marked
//     text must be the query itself.
//
// NEGATIVE CONTROL: with the offsets of strings.ToLower used on the
// original line, the first case panics with slice bounds out of range.
func TestSearchFoldsCaseByRune(t *testing.T) {
	for _, tc := range []struct{ line, query, marked string }{
		{"ȺȺ ok", "ok", "ok"},
		{"Kelvin is a sign", "k", "K"},
		{"Straße OK", "ok", "OK"},
	} {
		m := newModel(nil, "acme/shop", "")
		m.log = []LogLine{{Text: tc.line}}
		m.search.query = tc.query
		m.find()
		if len(m.search.matches) != 1 {
			t.Errorf("ASSERTION: %q in %q: %d matches, want 1", tc.query, tc.line, len(m.search.matches))
			continue
		}
		got := m.logText(m.log[0], true)
		if !strings.Contains(got, bold(tc.marked)) {
			t.Errorf("ASSERTION: %q in %q marks %q, want %q marked", tc.query, tc.line, got, tc.marked)
		}
	}
}

// TestApprovalIsNotRunning: a run that waits for a maintainer to approve
// it (the conclusion action_required, which GitHub gives a pull request
// from a fork) is not running. With only such a run, the plugin must not
// poll or tick, and must say what it waits for.
//
// NEGATIVE CONTROL: with action_required read as queued, the build counts
// as running and schedule arms a poll.
func TestApprovalIsNotRunning(t *testing.T) {
	runs, err := parseRuns([]byte(`{"workflow_runs":[{"id":1,"name":"Tests","event":"pull_request","head_sha":"abc",
		"status":"completed","conclusion":"action_required","run_started_at":"2026-11-25T10:00:00Z","updated_at":"2026-11-25T10:00:01Z"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(nil, "acme/shop", "")
	m.all = builds(runs)
	m.list = m.all
	m.key = m.all[0].Key
	if m.running() {
		t.Errorf("ASSERTION: a build that waits for approval counts as running")
	}
	if m.schedule() != nil {
		t.Errorf("ASSERTION: a build that waits for approval arms a poll")
	}
	if m.tick() != nil {
		t.Errorf("ASSERTION: a build that waits for approval ticks")
	}
	if text, _ := m.headline(m.all[0], forecast{}); !strings.Contains(text, "approve") {
		t.Errorf("ASSERTION: the headline %q does not say the build waits for approval", text)
	}
}

// TestLogIsBounded: the log of a job is read through a bound. Only the
// tail, which holds the failure, is kept, and the line cut by the bound is
// dropped.
//
// NEGATIVE CONTROL: with the whole output kept, the 12 MiB log comes back
// whole and the test fails.
func TestLogIsBounded(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nyes 'a line of a long log' | head -c 12582912\necho\necho 'the last line'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := &gh{bin: fake, kept: map[string]keptAnswer{}, slots: make(chan struct{}, 4)}
	raw, cut, err := g.log("acme/shop", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxLogBytes || !cut {
		t.Errorf("ASSERTION: the log kept %d bytes (cut %v), want at most %d and cut", len(raw), cut, maxLogBytes)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(raw)), "the last line") {
		t.Errorf("ASSERTION: the tail of the log is not kept")
	}
	if !strings.HasPrefix(string(raw), "a line of a long log\n") {
		t.Errorf("ASSERTION: the line that the bound cut is kept: %q", string(raw[:40]))
	}
}

// TestLogRefetchBacksOff: the log of a running job that did not grow is
// not read again on every poll. Each read that finds it the same doubles
// the wait, up to a minute.
//
// NEGATIVE CONTROL: without the wait, want asks for the log again at once
// and the test fails.
func TestLogRefetchBacksOff(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := newModel(nil, "acme/shop", "")
	m.now = func() time.Time { return now }
	start := now.Add(-time.Minute)
	m.runs = []Run{{ID: 1, Workflow: "Tests", Event: "push", SHA: "abc", State: "running", Started: start}}
	m.all = builds(m.runs)
	m.list, m.key = m.all, m.all[0].Key
	m.jobsOf[1] = &runJobs{jobs: []Job{{ID: 9, RunID: 1, Name: "test", State: "running", Started: start, Steps: []Step{{Name: "Run", State: "running", Started: start}}}}}
	m.asked[1] = true
	m.jobID = 9
	lines := []LogLine{{Text: "one"}, {Text: "two"}}
	m.logBusy = true
	m.onLog(logMsg{repo: "acme/shop", job: 9, lines: lines})
	m.logBusy = true
	m.onLog(logMsg{repo: "acme/shop", job: 9, lines: lines})
	m.logFinal = false
	m.fetchLog()
	if m.logBusy {
		t.Errorf("ASSERTION: a log that did not grow is read again at once")
	}
	now = now.Add(time.Minute)
	m.fetchLog()
	if !m.logBusy {
		t.Errorf("ASSERTION: the log is not read again after the wait")
	}
}
