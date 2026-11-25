package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Poll every few seconds while a build runs, and back off while nothing
// moves. With nothing running, the plugin asks nothing until you press r or
// come back to its pane.
const (
	activeInterval = 6 * time.Second
	slowestPoll    = 48 * time.Second
	// earlierBuilds is how many earlier builds' jobs the estimate reads.
	// Each costs a request per run, once.
	earlierBuilds = 6
)

type runJobs struct {
	jobs  []Job
	final bool // The run had ended, so its jobs do not change.
}

type model struct {
	gh   *gh
	scr  *screen
	now  func() time.Time
	repo string
	sha  string // From --sha: select the build of this commit when it comes.
	// extra is the commit whose runs --sha asked for alone, because it was
	// too old for the list. Its runs stay on each poll.
	extra string

	width, height int

	defaultBranch string
	runs          []Run
	all           []Build
	list          []Build
	key           string // The selected build.
	f             filters

	jobsOf    map[int64]*runJobs
	asked     map[int64]bool // Jobs asked for in this poll.
	jobID     int64
	jobPinned bool // The person picked a job, so a new failure does not move the selection.

	log      []LogLine
	logJob   int64
	logFinal bool
	logBusy  bool
	logNote  string
	starts   []int
	step     int // The step the person moved to with n and p, or -1.
	found    []failure
	failAt   int
	top      int  // The first log line in view.
	follow   bool // Keep the end of the log in view.

	tall, help bool
	picker     *picker

	err      string
	toast    string
	toastGen int
	updated  time.Time

	interval time.Duration
	moved    bool // Something changed since the last poll.
	pollGen  int
	polling  bool // A poll is on its way.
	ticking  bool
}

func newModel(g *gh, repo, sha string) *model {
	m := &model{gh: g, now: time.Now, sha: sha, step: -1, follow: true, interval: activeInterval}
	m.setRepo(repo)
	return m
}

func (m *model) setRepo(repo string) {
	*m = model{gh: m.gh, scr: m.scr, now: m.now, sha: m.sha, width: m.width, height: m.height, step: -1, follow: true, interval: activeInterval}
	m.repo = repo
	m.jobsOf = map[int64]*runJobs{}
	m.asked = map[int64]bool{}
}

// Messages from the commands that run gh.
type (
	repoMsg struct {
		repo, branch string
		err          error
	}
	runsMsg struct {
		repo    string
		runs    []Run
		extra   bool // The runs of one commit, for --sha.
		changed bool
		err     error
	}
	jobsMsg struct {
		repo    string
		run     int64
		jobs    []Job
		final   bool
		changed bool
		err     error
	}
	logMsg struct {
		repo  string
		job   int64
		lines []LogLine
		final bool
		err   error
	}
	pollMsg  struct{ gen int }
	tickMsg  struct{}
	toastEnd struct{ gen int }
)

func (m *model) Init() Cmd {
	if m.repo == "" {
		return m.openPicker()
	}
	return batch(m.askRepo(), m.poll())
}

func (m *model) askRepo() Cmd {
	repo, g := m.repo, m.gh
	return func() Msg {
		body, _, err := g.list("repos/" + repo)
		if err != nil {
			return repoMsg{repo: repo, err: err}
		}
		var in struct {
			Branch string `json:"default_branch"`
		}
		_ = json.Unmarshal(body, &in)
		return repoMsg{repo: repo, branch: in.Branch}
	}
}

// poll asks for the runs. The jobs and the log follow from the answer.
func (m *model) poll() Cmd {
	if m.polling || m.repo == "" {
		return nil
	}
	m.polling = true
	m.asked = map[int64]bool{}
	repo, g := m.repo, m.gh
	return func() Msg {
		body, changed, err := g.list("repos/" + repo + "/actions/runs?per_page=100")
		if err != nil {
			return runsMsg{repo: repo, err: err}
		}
		runs, err := parseRuns(body)
		return runsMsg{repo: repo, runs: runs, changed: changed, err: err}
	}
}

func (m *model) runsOfCommit(sha string) Cmd {
	m.extra = sha
	repo, g := m.repo, m.gh
	return func() Msg {
		body, _, err := g.list("repos/" + repo + "/actions/runs?per_page=100&head_sha=" + sha)
		if err != nil {
			return runsMsg{repo: repo, extra: true, err: err}
		}
		runs, err := parseRuns(body)
		return runsMsg{repo: repo, runs: runs, extra: true, changed: true, err: err}
	}
}

func (m *model) fetchJobs(r Run) Cmd {
	if m.asked[r.ID] {
		return nil
	}
	if k := m.jobsOf[r.ID]; k != nil && k.final {
		return nil
	}
	m.asked[r.ID] = true
	repo, g, final := m.repo, m.gh, !active(r.State)
	return func() Msg {
		body, changed, err := g.list(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, r.ID))
		if err != nil {
			return jobsMsg{repo: repo, run: r.ID, err: err}
		}
		jobs, err := parseJobs(body)
		return jobsMsg{repo: repo, run: r.ID, jobs: jobs, final: final, changed: changed, err: err}
	}
}

func (m *model) fetchLog() Cmd {
	j, ok := m.selectedJob()
	if !ok || m.logBusy {
		return nil
	}
	if j.State == "skipped" {
		m.log, m.logJob, m.logNote = nil, j.ID, "This job was skipped, so it has no log."
		return nil
	}
	if j.State == "queued" && len(j.Steps) == 0 {
		m.log, m.logJob, m.logNote = nil, j.ID, "This job waits for a runner."
		return nil
	}
	if m.logJob == j.ID && m.logFinal {
		return nil
	}
	if m.logJob != j.ID {
		m.logNote = "Loading the log."
	}
	m.logBusy = true
	repo, g, id, final := m.repo, m.gh, j.ID, !active(j.State)
	return func() Msg {
		raw, err := g.log(repo, id)
		if err != nil {
			return logMsg{repo: repo, job: id, err: err}
		}
		return logMsg{repo: repo, job: id, lines: parseLog(raw), final: final}
	}
}

// running says a build of the repository runs or waits for a runner.
func (m *model) running() bool {
	return slices.ContainsFunc(m.all, func(b Build) bool { return active(b.State) })
}

// schedule sets the next poll. It doubles the wait while nothing moves.
func (m *model) schedule() Cmd {
	if !m.running() {
		m.interval = activeInterval
		return nil
	}
	if m.moved {
		m.interval = activeInterval
	} else {
		m.interval = min(m.interval*2, slowestPoll)
	}
	m.moved = false
	m.pollGen++
	gen := m.pollGen
	return tick(m.interval, func(time.Time) Msg { return pollMsg{gen: gen} })
}

// tick moves the clock of a running build once a second.
func (m *model) tick() Cmd {
	b, ok := m.selected()
	if m.ticking || !ok || !active(b.State) {
		return nil
	}
	m.ticking = true
	return tick(time.Second, func(time.Time) Msg { return tickMsg{} })
}

func (m *model) selected() (Build, bool) {
	for _, b := range m.list {
		if b.Key == m.key {
			return b, true
		}
	}
	return Build{}, false
}

func jobKey(j Job, r Run) string {
	w := j.Workflow
	if w == "" {
		w = r.Workflow
	}
	return w + "/" + j.Name
}

// jobRow is one row of the timeline: a job, or one that the earlier builds
// say comes.
type jobRow struct {
	key string
	job Job
	run Run
	fc  jobForecast
}

// jobsOfBuild are the jobs of all the runs of a build, in the order the runs
// started.
func (m *model) jobsOfBuild(b Build) []jobRow {
	var rows []jobRow
	for _, r := range b.Runs {
		if r.State == "skipped" && b.State != "skipped" {
			continue
		}
		if k := m.jobsOf[r.ID]; k != nil {
			for _, j := range k.jobs {
				rows = append(rows, jobRow{key: jobKey(j, r), job: j, run: r})
			}
		}
	}
	return rows
}

func (m *model) selectedJob() (Job, bool) {
	b, ok := m.selected()
	if !ok {
		return Job{}, false
	}
	for _, row := range m.jobsOfBuild(b) {
		if row.job.ID == m.jobID {
			return row.job, true
		}
	}
	return Job{}, false
}

// pickJob selects the job worth looking at: the one that failed, else the
// one that runs, else the last one that did any work. A job the person
// picked stays picked.
func (m *model) pickJob() {
	b, ok := m.selected()
	if !ok {
		return
	}
	rows := m.jobsOfBuild(b)
	if m.jobPinned && slices.ContainsFunc(rows, func(r jobRow) bool { return r.job.ID == m.jobID }) {
		return
	}
	var id int64
	for _, r := range rows {
		if r.job.State == "failure" {
			id = r.job.ID
			break
		}
	}
	if id == 0 {
		for _, r := range rows {
			if r.job.State == "running" {
				id = r.job.ID
				break
			}
		}
	}
	if id == 0 {
		for _, r := range rows {
			if len(r.job.Steps) > 0 && r.job.State != "skipped" {
				id = r.job.ID
			}
		}
	}
	if id == 0 && len(rows) > 0 {
		id = rows[0].job.ID
	}
	m.setJob(id)
}

func (m *model) setJob(id int64) {
	if id == m.jobID {
		return
	}
	m.jobID, m.step = id, -1
	m.log, m.logJob, m.logFinal, m.logNote = nil, 0, false, ""
	m.starts, m.found, m.failAt, m.top, m.follow = nil, nil, 0, 0, true
}

// refilter applies the filters and keeps the selection when it can.
func (m *model) refilter() {
	m.list = shown(m.all, m.f, m.defaultBranch, m.key)
	key := m.key
	if m.sha != "" {
		for _, b := range m.list {
			if strings.HasPrefix(b.SHA, m.sha) {
				key, m.sha = b.Key, ""
				break
			}
		}
	}
	if !slices.ContainsFunc(m.list, func(b Build) bool { return b.Key == key }) {
		key = ""
		if len(m.list) > 0 {
			key = m.list[0].Key
		}
	}
	if key != m.key {
		m.key, m.jobPinned = key, false
		m.setJob(0)
	}
}

// want asks for what the selected build needs: the jobs of its runs, the
// jobs of earlier builds for the estimate while it runs, and the log.
func (m *model) want() Cmd {
	b, ok := m.selected()
	if !ok {
		return nil
	}
	var cmds []Cmd
	for _, r := range b.Runs {
		if r.State != "skipped" || b.State == "skipped" {
			cmds = append(cmds, m.fetchJobs(r))
		}
	}
	if active(b.State) {
		taken := 0
		for _, e := range likeBuilds(m.all, b, 16) {
			if e.State != "success" {
				continue
			}
			if taken++; taken > earlierBuilds {
				break
			}
			for _, r := range e.Runs {
				if r.State != "skipped" {
					cmds = append(cmds, m.fetchJobs(r))
				}
			}
		}
	}
	m.pickJob()
	cmds = append(cmds, m.fetchLog(), m.tick())
	return batch(cmds...)
}

// estimate is the forecast of the selected build.
func (m *model) estimate(b Build) (forecast, []Build) {
	like := likeBuilds(m.all, b, 16)
	s := spreadOf(like)
	if !active(b.State) {
		return forecast{known: s.known(), total: s.typical, low: s.low, high: s.top, samples: s.samples, elapsed: b.Seconds}, like
	}
	elapsed := max(0, m.now().Sub(b.Started).Seconds())
	var now []jobTime
	for _, r := range m.jobsOfBuild(b) {
		now = append(now, timeOf(r.job, r.key, b.Started))
	}
	var past []earlier
	taken := 0
	for _, e := range like {
		if e.State != "success" {
			continue
		}
		if taken++; taken > earlierBuilds {
			break
		}
		one, whole := earlier{}, true
		for _, r := range e.Runs {
			if r.State == "skipped" {
				continue
			}
			k := m.jobsOf[r.ID]
			if k == nil || !k.final {
				whole = false
				break
			}
			for _, j := range k.jobs {
				key := jobKey(j, r)
				one[key] = timeOf(j, key, e.Started)
			}
		}
		if whole && len(one) > 0 {
			past = append(past, one)
		}
	}
	return forecastOf(now, elapsed, s, past), like
}

func timeOf(j Job, key string, origin time.Time) jobTime {
	off := func(t time.Time) float64 {
		if t.IsZero() || origin.IsZero() {
			return -1
		}
		return max(0, t.Sub(origin).Seconds())
	}
	return jobTime{key: key, state: j.State, start: off(j.Started), end: off(j.Completed)}
}

// Update handles a message, then draws the frame.
func (m *model) Update(msg Msg) (*model, Cmd) {
	next, cmd := m.update(msg)
	if m.scr != nil && m.width > 0 {
		m.scr.draw(m.render(), m.width, m.height, m.windowTitle())
	}
	return next, cmd
}

func (m *model) update(msg Msg) (*model, Cmd) {
	switch msg := msg.(type) {
	case sizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampLog()
		return m, nil
	case focusMsg:
		// Back in view: read now, also when nothing ran a moment ago.
		return m, m.poll()
	case keyMsg:
		return m.key_(msg)
	case repoMsg:
		if msg.repo != m.repo {
			return m, nil
		}
		if msg.err != nil {
			m.err = "Cannot read " + m.repo + ": " + msg.err.Error()
			return m, nil
		}
		m.defaultBranch = msg.branch
		m.refilter()
		return m, m.want()
	case runsMsg:
		return m.onRuns(msg)
	case jobsMsg:
		if msg.repo != m.repo {
			return m, nil
		}
		if msg.err != nil {
			m.err = "Cannot read the jobs: " + msg.err.Error()
			return m, nil
		}
		if msg.changed {
			m.moved = true
		}
		m.jobsOf[msg.run] = &runJobs{jobs: msg.jobs, final: msg.final}
		before := m.jobID
		m.pickJob()
		var cmd Cmd
		if m.jobID != before || m.logJob != m.jobID {
			cmd = m.fetchLog()
		}
		return m, batch(cmd, m.tick())
	case logMsg:
		return m.onLog(msg)
	case pollMsg:
		if msg.gen != m.pollGen {
			return m, nil
		}
		return m, m.poll()
	case tickMsg:
		m.ticking = false
		return m, m.tick()
	case toastEnd:
		if msg.gen == m.toastGen {
			m.toast = ""
		}
		return m, nil
	case reposMsg:
		if m.picker != nil {
			m.picker.repos, m.picker.err, m.picker.loading = msg.repos, msg.err, false
			m.picker.narrow()
		}
		return m, nil
	}
	return m, nil
}

func (m *model) onRuns(msg runsMsg) (*model, Cmd) {
	if msg.repo != m.repo {
		return m, nil
	}
	if !msg.extra {
		m.polling = false
	}
	if msg.err != nil {
		m.err = "Cannot read the builds of " + m.repo + ": " + msg.err.Error()
		return m, m.schedule()
	}
	m.err = ""
	m.updated = m.now()
	if msg.changed {
		m.moved = true
	}
	if msg.extra {
		for _, r := range msg.runs {
			if !slices.ContainsFunc(m.runs, func(o Run) bool { return o.ID == r.ID }) {
				m.runs = append(m.runs, r)
			}
		}
	} else {
		var kept []Run
		// Runs of an old commit that --sha brought stay.
		for _, r := range m.runs {
			if !slices.ContainsFunc(msg.runs, func(o Run) bool { return o.ID == r.ID }) && m.extra != "" && r.SHA == m.extra {
				kept = append(kept, r)
			}
		}
		m.runs = append(slices.Clone(msg.runs), kept...)
	}
	slices.SortStableFunc(m.runs, func(a, b Run) int { return b.Started.Compare(a.Started) })
	m.all = builds(m.runs)
	m.refilter()
	var cmds []Cmd
	if m.sha != "" && !msg.extra && len(m.sha) == 40 && m.extra == "" {
		cmds = append(cmds, m.runsOfCommit(m.sha))
	}
	if j, ok := m.selectedJob(); ok && active(j.State) && m.logJob == j.ID {
		// A running job's log grows: ask again on each poll.
		m.logFinal = false
	}
	cmds = append(cmds, m.want(), m.schedule())
	return m, batch(cmds...)
}

func (m *model) onLog(msg logMsg) (*model, Cmd) {
	if msg.repo != m.repo {
		return m, nil
	}
	m.logBusy = false
	if msg.job != m.jobID {
		return m, m.fetchLog()
	}
	if msg.err != nil {
		j, _ := m.selectedJob()
		if active(j.State) {
			m.logNote = "GitHub has not published the log of this running job yet."
		} else {
			m.logNote = "Cannot read the log: " + msg.err.Error()
		}
		return m, nil
	}
	if len(msg.lines) != len(m.log) {
		m.moved = true
	}
	first := m.logJob != msg.job
	m.log, m.logJob, m.logFinal, m.logNote = msg.lines, msg.job, msg.final, ""
	m.annotate()
	switch {
	case first && len(m.found) > 0:
		// Open on the first failure, two lines below the top, so its whole
		// block is in view.
		m.follow = false
		m.top = max(0, m.found[0].first-2)
	case m.follow:
		m.top = len(m.log)
	}
	m.clampLog()
	return m, nil
}

// annotate ties the log to the steps and finds the failures, inside the
// failed step when the log says where it is.
func (m *model) annotate() {
	j, ok := m.selectedJob()
	if !ok {
		return
	}
	m.starts = mapSteps(j.Steps, m.log)
	m.found = nil
	if j.State == "success" || j.State == "skipped" {
		return
	}
	from, to := 0, -1
	for i, s := range j.Steps {
		if s.State == "failure" && m.starts[i] >= 0 {
			from, to = m.starts[i], m.stepEnd(i)
			break
		}
	}
	m.found = findFailures(m.log, from, to)
}

// stepEnd is the last line of step i.
func (m *model) stepEnd(i int) int {
	for k := i + 1; k < len(m.starts); k++ {
		if m.starts[k] >= 0 {
			return m.starts[k] - 1
		}
	}
	return len(m.log) - 1
}

func (m *model) say(s string) Cmd {
	m.toast = s
	m.toastGen++
	gen := m.toastGen
	return tick(4*time.Second, func(time.Time) Msg { return toastEnd{gen: gen} })
}

// link is the address of the selected job, else the build's run.
func (m *model) link() string {
	if j, ok := m.selectedJob(); ok {
		if j.URL != "" {
			return j.URL
		}
		if j.RunID != 0 && j.ID != 0 {
			return fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", m.repo, j.RunID, j.ID)
		}
	}
	if b, ok := m.selected(); ok {
		return b.URL
	}
	return ""
}
