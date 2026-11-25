package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// search is a search in the log of the selected job.
type search struct {
	editing bool   // The person types the query.
	query   string // What the log is searched for.
	matches []int  // The lines that hold it, in order.
	at      int    // The match in view.
}

// find fills the matches of the query in the log.
func (m *model) find() {
	s := &m.search
	s.matches = s.matches[:0]
	if s.query == "" {
		return
	}
	q := strings.ToLower(s.query)
	for i, l := range m.log {
		if strings.Contains(strings.ToLower(l.Text), q) {
			s.matches = append(s.matches, i)
		}
	}
	s.at = min(s.at, max(0, len(s.matches)-1))
}

// showMatch puts the match in view, a third of the way down the log.
func (m *model) showMatch() {
	if len(m.search.matches) == 0 {
		return
	}
	m.follow = false
	m.top = max(0, m.search.matches[m.search.at]-m.logRows()/3)
	m.clampLog()
}

func (m *model) stepMatch(d int) {
	n := len(m.search.matches)
	if n == 0 {
		return
	}
	m.search.at = (m.search.at + d + n) % n
	m.showMatch()
}

// searchKey handles a key while the person types a query.
func (m *model) searchKey(k keyMsg) {
	s := &m.search
	switch k.name {
	case "esc":
		*s = search{}
		return
	case "enter":
		s.editing = false
		return
	case "backspace":
		if r := []rune(s.query); len(r) > 0 {
			s.query = string(r[:len(r)-1])
		}
	case "ctrl+n":
		m.stepMatch(1)
		return
	case "ctrl+p":
		m.stepMatch(-1)
		return
	default:
		if k.text == "" || strings.ContainsFunc(k.text, control) {
			return
		}
		s.query += k.text
	}
	s.at = 0
	m.find()
	m.showMatch()
}

// ask arms an action that runs when its key comes a second time within
// a few seconds.
func (m *model) ask(action, question string) Cmd {
	m.confirm = action
	m.confirmGen++
	gen := m.confirmGen
	m.toast = question
	return tick(4*time.Second, func(time.Time) Msg { return confirmEnd{gen: gen} })
}

// rerun asks GitHub to run the failed jobs of the selected build again, or
// all its jobs.
func (m *model) rerun(all bool) Cmd {
	b, ok := m.selected()
	if !ok {
		return nil
	}
	var ids []int64
	for _, r := range b.Runs {
		if active(r.State) || r.State == "skipped" {
			continue
		}
		if all || r.State == "failure" || r.State == "cancelled" {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return m.say("This build has no failed run to run again.")
	}
	repo, g := m.repo, m.gh
	return func() Msg {
		for _, id := range ids {
			args := []string{"run", "rerun", strconv.FormatInt(id, 10), "-R", repo}
			if !all {
				args = append(args, "--failed")
			}
			if err := g.act(args...); err != nil {
				return actedMsg{say: "GitHub did not run it again: " + err.Error()}
			}
		}
		what := "the failed jobs"
		if all {
			what = "all the jobs"
		}
		return actedMsg{say: fmt.Sprintf("GitHub runs %s of %d run(s) again.", what, len(ids)), read: true}
	}
}

// openJob opens the selected job, else the run, on GitHub.
func (m *model) openJob() Cmd {
	b, ok := m.selected()
	if !ok || len(b.Runs) == 0 {
		return nil
	}
	repo, g := m.repo, m.gh
	args := []string{"run", "view", strconv.FormatInt(b.Runs[0].ID, 10), "--web", "-R", repo}
	if j, ok := m.selectedJob(); ok && j.ID != 0 {
		args = []string{"run", "view", strconv.FormatInt(j.RunID, 10), "--job", strconv.FormatInt(j.ID, 10), "--web", "-R", repo}
	}
	return func() Msg {
		if err := g.act(args...); err != nil {
			return actedMsg{say: "Cannot open the browser: " + err.Error()}
		}
		return actedMsg{say: "Opened the job on GitHub."}
	}
}

// openPR opens the pull request of the selected build on GitHub.
func (m *model) openPR() Cmd {
	b, ok := m.selected()
	if !ok || b.PR == 0 {
		return m.say("This build has no pull request.")
	}
	repo, g, n := m.repo, m.gh, b.PR
	return func() Msg {
		if err := g.act("pr", "view", strconv.Itoa(n), "--web", "-R", repo); err != nil {
			return actedMsg{say: "Cannot open the browser: " + err.Error()}
		}
		return actedMsg{say: fmt.Sprintf("Opened pull request #%d on GitHub.", n)}
	}
}
