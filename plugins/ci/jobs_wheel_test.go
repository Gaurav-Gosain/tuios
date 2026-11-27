package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// TestWheelScrollsTheJobs: a build with more jobs than the jobs area holds.
// The wheel over the jobs must move them, as it moves the builds, the steps
// and the log. A key that moves the selected job must bring it into view
// again.
//
// How it could pass wrongly, written down first:
//   - The wheel could reach another part under the pointer, such as the
//     steps: the test checks that the first job leaves the jobs area and a
//     later one shows.
//   - The scroll could take the JOBS head with it: the head must stay.
//   - A scrolled view could hide the selected job for good: l must bring
//     the job it selects into view.
//
// NEGATIVE CONTROL: with the jobs area drawn without its wheel, the wheel
// reaches no part, the first job stays in view and the test fails.
func TestWheelScrollsTheJobs(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := newModel(nil, "acme/shop", "")
	m.now = func() time.Time { return now }
	m.width, m.height = 160, 44
	start := now.Add(-time.Hour)
	end := now.Add(-time.Minute)
	m.runs = []Run{{ID: 1, Workflow: "Tests", Event: "push", SHA: "abc", State: "success", Started: start, Updated: end}}
	m.all = builds(m.runs)
	m.list, m.key = m.all, m.all[0].Key
	var jobs []Job
	for i := range 30 {
		jobs = append(jobs, Job{ID: int64(100 + i), RunID: 1, Name: fmt.Sprintf("job-%02d", i), State: "success",
			Started: start.Add(time.Duration(i) * time.Minute), Completed: start.Add(time.Duration(i+1) * time.Minute)})
	}
	m.jobsOf[1] = &runJobs{jobs: jobs, final: true}
	m.asked[1] = true
	m.jobID = 100

	jobsArea := func() string {
		screen := strings.Split(m.frame(), "\n")
		top := m.areaTop("jobs")
		if top < 0 {
			t.Fatalf("the screen has no jobs area:\n%s", ansi.Strip(strings.Join(screen, "\n")))
		}
		h := m.layout().jobsH
		return ansi.Strip(strings.Join(screen[top:min(len(screen), top+h)], "\n"))
	}

	before := jobsArea()
	if !strings.Contains(before, "job-00") || strings.Contains(before, "job-29") {
		t.Fatalf("the test needs more jobs than the area holds, with job-00 in view first:\n%s", before)
	}
	top := m.areaTop("jobs")
	x := -1
	for _, r := range m.rects {
		if r.zone < len(m.drawn) && m.drawn[r.zone].area == "jobs" && r.y == top+4 {
			x = r.x0 + 10
		}
	}
	if x < 0 {
		t.Fatal("no jobs row to point at")
	}
	for range 3 {
		m.mouse(mouseMsg{x: x, y: top + 4, wheel: 1, button: 3})
	}
	after := jobsArea()
	if os.Getenv("CI_FRAME") != "" {
		_ = os.WriteFile(os.Getenv("CI_FRAME"), []byte(after+"\n"), 0o644)
	}
	if strings.Contains(after, "job-00") || !strings.Contains(after, "job-09") {
		t.Errorf("ASSERTION: the wheel did not scroll the jobs, the area shows:\n%s", after)
	}
	if !strings.Contains(after, "JOBS") {
		t.Errorf("ASSERTION: the scroll took the JOBS head away:\n%s", after)
	}

	// Up again, past the top, stops at the first job.
	for range 10 {
		m.mouse(mouseMsg{x: x, y: top + 4, wheel: -1, button: 3})
	}
	if back := jobsArea(); !strings.Contains(back, "job-00") {
		t.Errorf("ASSERTION: the wheel up did not come back to the first job:\n%s", back)
	}

	// Scroll down, then a key selects the last job: it must be in view.
	for range 3 {
		m.mouse(mouseMsg{x: x, y: top + 4, wheel: 1, button: 3})
	}
	m.moveJob(-1) // From the first job, back wraps to the last.
	if last := jobsArea(); !strings.Contains(last, "job-29") {
		t.Errorf("ASSERTION: the job the key selected is out of view:\n%s", last)
	}
}
