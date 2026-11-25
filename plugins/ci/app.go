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
	gh  *gh
	scr *screen
	pal colors
	// next gathers the answers to the colour queries on their way.
	next []byte
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
	// logAgain asks for the log once more when the read on its way ends:
	// the job changed while it ran.
	logAgain bool
	// jumped is the job whose log opened at its first failure.
	jumped int64
	// confirm is an action that waits for its key a second time, such as a
	// rerun, which costs runner time.
	confirm    string
	confirmGen int
	// pulls are the pull requests of builds, by number.
	pulls map[int]PullRequest
	// search is the search in the log.
	search  search
	numbers bool // The line numbers of the log.
	// logGap is the wait before the log of a running job is read again,
	// and logNext the time it ends. A read that finds the log the same
	// doubles the wait.
	logGap  time.Duration
	logNext time.Time
	logCut  bool // Only the tail of the log is kept.
	// errGap is the wait before the builds are read again after an error
	// with nothing running.
	errGap time.Duration
	// names are the workflow and the name of the job of each key, from the
	// jobs of earlier builds.
	names   map[string][2]string
	logNote string
	starts  []int
	step    int // The step the person moved to with n and p, or -1.
	found   []failure
	failAt  int
	top     int  // The first log line in view.
	follow  bool // Keep the end of the log in view.

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
	tickGen  int
}

func newModel(g *gh, repo, sha string) *model {
	m := &model{gh: g, now: time.Now, sha: sha, step: -1, follow: true, interval: activeInterval}
	m.setRepo(repo)
	return m
}

func (m *model) setRepo(repo string) {
	// The counters go on, so a tick, a poll or a toast end of the repository
	// before matches nothing here.
	*m = model{gh: m.gh, scr: m.scr, pal: m.pal, now: m.now, sha: m.sha, width: m.width, height: m.height, step: -1, follow: true, interval: activeInterval,
		pollGen: m.pollGen + 1, tickGen: m.tickGen + 1, toastGen: m.toastGen + 1}
	m.repo = repo
	m.jobsOf = map[int64]*runJobs{}
	m.asked = map[int64]bool{}
	m.pulls = map[int]PullRequest{}
	m.names = map[string][2]string{}
	m.numbers = true
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
		cut   bool // Only the tail of the log came, within maxLogBytes.
		err   error
	}
	pollMsg struct{ gen int }
	pullMsg struct {
		repo string
		pr   PullRequest
	}
	// actedMsg is the end of an action: what to say, and whether to read
	// the builds again.
	actedMsg struct {
		say  string
		read bool
	}
	confirmEnd struct{ gen int }
	tickMsg    struct{ gen int }
	toastEnd   struct{ gen int }
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
	if !ok {
		return nil
	}
	if m.logBusy {
		m.logAgain = true
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
	// The log of a running job that did not grow waits before it is read
	// again: each read costs the whole log.
	if m.logJob == j.ID && active(j.State) && m.now().Before(m.logNext) {
		return nil
	}
	if m.logJob != j.ID {
		m.logNote = "Loading the log."
	}
	m.logBusy = true
	repo, g, id, final := m.repo, m.gh, j.ID, !active(j.State)
	return func() Msg {
		raw, cut, err := g.log(repo, id)
		if err != nil {
			return logMsg{repo: repo, job: id, err: err}
		}
		return logMsg{repo: repo, job: id, lines: parseLog(raw), final: final, cut: cut}
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
	m.tickGen++
	gen := m.tickGen
	return tick(time.Second, func(time.Time) Msg { return tickMsg{gen: gen} })
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
	m.jobID, m.step, m.jumped = id, -1, 0
	m.logGap, m.logNext, m.logCut = 0, time.Time{}, false
	m.search = search{}
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
	// The pull request of the build, for the pill. Asked once.
	if b.PR > 0 {
		if _, ok := m.pulls[b.PR]; !ok {
			m.pulls[b.PR] = PullRequest{Number: b.PR}
			repo, g, n := m.repo, m.gh, b.PR
			cmds = append(cmds, func() Msg {
				body, _, err := g.list(fmt.Sprintf("repos/%s/pulls/%d", repo, n))
				if err != nil {
					return nil
				}
				pr, err := parsePull(body)
				if err != nil {
					return nil
				}
				return pullMsg{repo: repo, pr: pr}
			})
		}
	}
	// The jobs of the build the default branch had before, for the column
	// that compares with it. They ended, so this asks once.
	if o, ok := m.before(b); ok {
		for _, r := range o.Runs {
			if r.State != "skipped" {
				cmds = append(cmds, m.fetchJobs(r))
			}
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
				w := j.Workflow
				if w == "" {
					w = r.Workflow
				}
				m.names[key] = [2]string{w, j.Name}
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
		// Back in view: read now, also when nothing ran a moment ago. Ask
		// for the colours too, in case the theme changed while away.
		if m.scr != nil {
			m.scr.askColors()
		}
		return m, m.poll()
	case schemeMsg:
		// The theme changed: ask for its colours.
		if m.scr != nil {
			m.scr.askColors()
		}
		return m, nil
	case colorMsg:
		m.next = append(m.next, msg.body...)
		return m, nil
	case colorsDone:
		var p palette
		parse(&p, m.next)
		m.next = nil
		if p.known {
			m.pal = newColors(p)
		}
		return m, nil
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
			// A toast: the request may be for an earlier build only.
			return m, m.say("Cannot read the jobs of a run: " + msg.err.Error())
		}
		if msg.changed {
			m.moved = true
		}
		was, _ := m.selectedJob()
		m.jobsOf[msg.run] = &runJobs{jobs: msg.jobs, final: msg.final}
		before := m.jobID
		m.pickJob()
		now, _ := m.selectedJob()
		var cmd Cmd
		switch {
		case m.jobID != before || m.logJob != m.jobID:
			cmd = m.fetchLog()
		case !active(now.State) && (active(was.State) || !m.logFinal):
			// The job ended since its log was read: read it to its end.
			m.logFinal = false
			cmd = m.fetchLog()
		}
		if m.logJob == m.jobID {
			// The steps may have moved on: place them and the failures again.
			m.annotate()
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
		if msg.gen != m.tickGen {
			return m, nil
		}
		m.ticking = false
		return m, m.tick()
	case pullMsg:
		if msg.repo == m.repo {
			m.pulls[msg.pr.Number] = msg.pr
		}
		return m, nil
	case actedMsg:
		cmd := m.say(msg.say)
		if msg.read {
			m.pollGen++
			m.polling = false
			cmd = batch(cmd, m.poll())
		}
		return m, cmd
	case confirmEnd:
		if msg.gen == m.confirmGen {
			m.confirm = ""
			m.toast = ""
		}
		return m, nil
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
		if m.running() {
			return m, m.schedule()
		}
		// Nothing is known to run, so no poll would come: try again later,
		// with a longer wait each time.
		m.errGap = min(max(2*m.errGap, activeInterval), 5*time.Minute)
		m.pollGen++
		gen := m.pollGen
		return m, tick(m.errGap, func(time.Time) Msg { return pollMsg{gen: gen} })
	}
	m.err, m.errGap = "", 0
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
	// The jobs of a run that left the list are not needed again.
	for id := range m.jobsOf {
		if !slices.ContainsFunc(m.runs, func(r Run) bool { return r.ID == id }) {
			delete(m.jobsOf, id)
			m.gh.forget(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", m.repo, id))
		}
	}
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
	again := m.logAgain
	m.logAgain = false
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
	switch {
	case len(msg.lines) != len(m.log) || m.logJob != msg.job:
		m.moved = true
		m.logGap = 0
	default:
		m.logGap = min(max(2*m.logGap, activeInterval), time.Minute)
	}
	m.logNext = m.now().Add(m.logGap)
	m.logCut = msg.cut
	m.log, m.logJob, m.logFinal, m.logNote = msg.lines, msg.job, msg.final, ""
	if j, _ := m.selectedJob(); !active(j.State) && !msg.final {
		// The read began while the job ran, and the job has ended since.
		m.logFinal = false
		again = true
	}
	m.annotate()
	m.find()
	switch {
	case len(m.found) > 0 && m.jumped != msg.job:
		// Open on the first failure, two lines below the top, so its whole
		// block is in view. Also when the job failed while you watched it.
		m.jumped = msg.job
		m.follow = false
		m.top = max(0, m.found[0].first-2)
	case m.follow:
		m.top = len(m.log)
	}
	m.clampLog()
	if again {
		return m, m.fetchLog()
	}
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
