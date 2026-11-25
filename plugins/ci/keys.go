package main

import (
	"slices"
	"strings"
)

func (m *model) key_(k keyMsg) (*model, Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, quit
	}
	if m.picker != nil {
		return m.pickerKey(k)
	}
	if m.search.editing {
		m.searchKey(k)
		return m, nil
	}
	// The second press of an armed action runs it.
	if m.confirm != "" {
		action := m.confirm
		m.confirm, m.toast = "", ""
		if s == action {
			switch action {
			case "R":
				return m, m.rerun(false)
			case "ctrl+r":
				return m, m.rerun(true)
			}
		}
	}
	if m.help {
		if s == "?" || s == "esc" || s == "q" {
			m.help = false
		}
		return m, nil
	}
	switch s {
	case "q":
		return m, quit
	case "?":
		m.help = true
	case "esc":
		if m.search.query != "" {
			m.search = search{}
		} else {
			m.tall = false
		}
	case "j", "down":
		return m, m.moveBuild(1)
	case "k", "up":
		return m, m.moveBuild(-1)
	case "l", "right", "tab":
		return m, m.moveJob(1)
	case "h", "left", "shift+tab":
		return m, m.moveJob(-1)
	case "n":
		m.moveStep(1)
	case "p":
		m.moveStep(-1)
	case "e":
		m.moveFailure(1)
	case "E":
		m.moveFailure(-1)
	case "c":
		link := m.link()
		if link == "" {
			return m, m.say("This build has no link yet.")
		}
		if m.scr != nil {
			m.scr.copy(link)
		}
		return m, m.say("Copied " + link)
	case "z":
		m.tall = !m.tall
	case "M", "m":
		if m.f.branch != "" && m.f.branch == m.defaultBranch {
			m.f.branch = ""
		} else {
			m.f.branch = m.defaultBranch
		}
		return m, m.refiltered()
	case "F", "f":
		if m.f.branch != "" {
			m.f.branch = ""
		} else if b, ok := m.selected(); ok {
			m.f.branch = b.Branch
		}
		return m, m.refiltered()
	case "A", "a":
		m.f.activeOnly = !m.f.activeOnly
		// Turned on, it shows what runs, not the build you were on.
		if b, ok := m.selected(); m.f.activeOnly && ok && !active(b.State) {
			m.key = ""
		}
		return m, m.refiltered()
	case "S", "s":
		m.f.showSkipped = !m.f.showSkipped
		return m, m.refiltered()
	case "R":
		return m, m.ask("R", "Press R again, or click again, to run the failed jobs of this build again.")
	case "ctrl+r":
		return m, m.ask("ctrl+r", "Press ctrl+r again, or click again, to run all the jobs of this build again.")
	case "o":
		return m, m.openJob()
	case "O":
		return m, m.openPR()
	case "/":
		m.search = search{editing: true}
	case "ctrl+n":
		m.stepMatch(1)
	case "ctrl+p":
		m.stepMatch(-1)
	case "#":
		m.numbers = !m.numbers
	case "r", "space":
		m.pollGen++
		if j, ok := m.selectedJob(); ok && active(j.State) {
			m.logFinal = false
		}
		return m, m.poll()
	case "G":
		return m, m.openPicker()
	case "pgup":
		m.scroll(-m.logRows() + 1)
	case "pgdown":
		m.scroll(m.logRows() - 1)
	case "home":
		m.follow, m.top = false, 0
	case "end":
		m.follow, m.top = true, len(m.log)
		m.clampLog()
	}
	return m, nil
}

func (m *model) refiltered() Cmd {
	m.listScrolled = false
	m.refilter()
	return m.want()
}

func (m *model) moveBuild(d int) Cmd {
	i := slices.IndexFunc(m.list, func(b Build) bool { return b.Key == m.key })
	n := i + d
	if n < 0 || n >= len(m.list) {
		return nil
	}
	m.key, m.jobPinned, m.listScrolled = m.list[n].Key, false, false
	m.setJob(0)
	return m.want()
}

func (m *model) moveJob(d int) Cmd {
	b, ok := m.selected()
	if !ok {
		return nil
	}
	rows := m.jobsOfBuild(b)
	if len(rows) == 0 {
		return nil
	}
	i := slices.IndexFunc(rows, func(r jobRow) bool { return r.job.ID == m.jobID })
	n := (i + d + len(rows)) % len(rows)
	m.jobPinned = true
	m.setJob(rows[n].job.ID)
	return m.fetchLog()
}

// moveStep shows where the next or previous step that wrote something
// ends, at the bottom of the view, because what a step writes last is its
// outcome.
func (m *model) moveStep(d int) {
	if len(m.starts) == 0 {
		return
	}
	for n := m.step + d; n >= 0 && n < len(m.starts); n += d {
		if m.starts[n] >= 0 {
			m.showStep(n)
			return
		}
	}
}

// showStep selects step n and shows where it ends in the log.
func (m *model) showStep(n int) {
	m.step, m.follow, m.stepsScrolled = n, false, false
	m.top = m.stepEnd(n) - m.logRows() + 1
	m.clampLog()
}

func (m *model) moveFailure(d int) {
	if len(m.found) == 0 {
		return
	}
	m.failAt = (m.failAt + d + len(m.found)) % len(m.found)
	m.follow = false
	m.top = max(0, m.found[m.failAt].first-2)
	m.clampLog()
}

func (m *model) scroll(d int) {
	m.top += d
	m.follow = false
	m.clampLog()
	if m.top >= len(m.log)-m.logRows() {
		m.follow = true
	}
}

func (m *model) clampLog() {
	last := max(0, len(m.log)-m.logRows())
	m.top = min(max(0, m.top), last)
}

// picker is the menu of repositories.
type picker struct {
	input   string
	repos   []Repo
	shown   []Repo
	at      int
	loading bool
	err     error
}

type reposMsg struct {
	repos []Repo
	err   error
}

func (m *model) openPicker() Cmd {
	m.picker = &picker{loading: true}
	g := m.gh
	return func() Msg {
		var all []Repo
		for page := 1; page <= 3; page++ {
			body, _, err := g.list("user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member&page=" + itoa(page))
			if err != nil {
				return reposMsg{repos: all, err: err}
			}
			repos, err := parseRepos(body)
			if err != nil {
				return reposMsg{repos: all, err: err}
			}
			all = append(all, repos...)
			if len(repos) < 100 {
				break
			}
		}
		return reposMsg{repos: all}
	}
}

// narrow keeps the repositories that have every word typed, so "acme api"
// finds acme/storefront-api.
func (p *picker) narrow() {
	words := strings.Fields(strings.ToLower(p.input))
	p.shown = p.shown[:0]
	for _, r := range p.repos {
		hay := strings.ToLower(r.Name + " " + r.Description)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			p.shown = append(p.shown, r)
		}
	}
	p.at = min(p.at, max(0, len(p.shown)-1))
}

func (m *model) pickerKey(k keyMsg) (*model, Cmd) {
	p := m.picker
	switch k.String() {
	case "esc":
		if m.repo != "" {
			m.picker = nil
		}
	case "up", "ctrl+p":
		p.at = max(0, p.at-1)
	case "down", "ctrl+n":
		p.at = min(max(0, len(p.shown)-1), p.at+1)
	case "backspace":
		if p.input != "" {
			r := []rune(p.input)
			p.input = string(r[:len(r)-1])
			p.narrow()
		}
	case "enter":
		repo := ""
		if len(p.shown) > 0 {
			repo = p.shown[p.at].Name
		} else if strings.Count(strings.TrimSpace(p.input), "/") == 1 {
			// A whole owner/name that is not in the list, such as a public
			// repository of someone else, opens as typed.
			repo = strings.TrimSpace(p.input)
		}
		if repo == "" {
			return m, nil
		}
		m.picker = nil
		m.sha = ""
		m.setRepo(repo)
		return m, batch(m.askRepo(), m.poll())
	default:
		if t := k.text; t != "" && !strings.ContainsFunc(t, control) {
			p.input += t
			p.narrow()
		}
	}
	return m, nil
}
