package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The dock cell. A focus runs "tuios-ci dock", which reads the build of
// the focused pane's commit, sets the cell and exits. While that build
// runs, it starts one "tuios-ci watch OWNER/REPO" for the repository,
// which keeps the cell up to date with the poll of the pane, says when a
// build it saw run ends, and exits when nothing it watches runs.

const (
	cellID = "build"
	// okFor is how long the cell says "CI ok" after a build the watcher
	// saw run passes.
	okFor = 30 * time.Second
	// cellEvery is how often the watcher moves the time left in the cell
	// between two polls.
	cellEvery = 10 * time.Second
)

// focus is the commit of the focused pane, as the last focus wrote it.
type focus struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

func stateDir() string { return os.Getenv("TUIOS_PLUGIN_STATE_DIR") }

func focusFile() string { return filepath.Join(stateDir(), "focus.json") }

func readFocus() focus {
	var f focus
	b, err := os.ReadFile(focusFile())
	if err == nil {
		_ = json.Unmarshal(b, &f)
	}
	return f
}

func writeFocus(f focus) {
	b, _ := json.Marshal(f)
	tmp := focusFile() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, focusFile())
	}
}

// focusOfFolder is the repository, branch and commit of the folder you
// work in.
func focusOfFolder() focus {
	dir := os.Getenv("TUIOS_PLUGIN_CWD")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	f := focus{Repo: repoOfFolder(), SHA: strings.ToLower(git("rev-parse", "HEAD"))}
	f.Branch = git("symbolic-ref", "--short", "HEAD")
	if f.Repo == "" || f.SHA == "" {
		return focus{}
	}
	return f
}

// lockFile is the lock of the watcher of repo.
func lockFile(repo string) string {
	return filepath.Join(stateDir(), "watch-"+strings.ReplaceAll(repo, "/", "-")+".lock")
}

// watcherOf is the process id of the watcher of repo, or 0 when none runs.
// A watcher holds the lock for as long as it runs, so a lock nobody holds
// is a watcher that ended.
func watcherOf(repo string) int {
	f, err := os.OpenFile(lockFile(repo), os.O_RDONLY, 0)
	if err != nil {
		return 0
	}
	defer f.Close()
	if !held(f) {
		return 0
	}
	b := make([]byte, 32)
	n, _ := f.Read(b)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b[:n])))
	return pid
}

// cell sets the dock cell, or clears it for "". It says nothing on an
// error: the cell is a hint, and the daemon logs the run.
func cell(text string) {
	bin := os.Getenv("TUIOS_BIN")
	if bin == "" {
		return
	}
	if text == "" {
		_ = exec.Command(bin, "plugin-cell", "clear", cellID).Run()
		return
	}
	// A tuios before plugin-cell --pane refuses the flag. Its cell then
	// shows the text with no click.
	if exec.Command(bin, "plugin-cell", "set", cellID, text, "--pane", "builds").Run() != nil {
		_ = exec.Command(bin, "plugin-cell", "set", cellID, text).Run()
	}
}

func notify(text, level string) {
	if bin := os.Getenv("TUIOS_BIN"); bin != "" {
		_ = exec.Command(bin, "plugin-notify", text, "--level", level).Run()
	}
}

const (
	red   = "\x1b[31m"
	green = "\x1b[32m"
	plain = "\x1b[39m"
)

// dockRun is "tuios-ci dock": one focus.
func dockRun() int {
	if stateDir() == "" {
		fmt.Fprintln(os.Stderr, "tuios-ci dock: tuios runs this on a focus. It needs TUIOS_PLUGIN_STATE_DIR.")
		return 2
	}
	f := focusOfFolder()
	writeFocus(f)
	if f.Repo == "" {
		cell("")
		return 0
	}
	// A watcher of the repository draws the cell itself. It reads the
	// focus again when it gets the signal.
	if pid := watcherOf(f.Repo); pid > 0 {
		if poke(pid) == nil {
			return 0
		}
	}
	g, err := newGH()
	if err != nil {
		cell("")
		return 0
	}
	b, ok, _, err := buildOf(g, f.Repo, f.SHA)
	switch {
	case err != nil || !ok:
		cell("")
	case active(b.State):
		cell(runningText(b, forecast{}))
		if err := startWatcher(f.Repo); err != nil {
			fmt.Fprintln(os.Stderr, "tuios-ci dock: "+err.Error())
		}
	case b.State == "failure":
		cell(failedText(firstFailure(g, f.Repo, b)))
	default:
		// A build that passed some time ago says nothing.
		cell("")
	}
	return 0
}

// buildOf is the build of one commit, and whether GitHub's answer changed
// since the last ask.
func buildOf(g *gh, repo, sha string) (Build, bool, bool, error) {
	body, changed, err := g.list("repos/" + repo + "/actions/runs?per_page=100&head_sha=" + sha)
	if err != nil {
		return Build{}, false, false, err
	}
	runs, err := parseRuns(body)
	if err != nil {
		return Build{}, false, false, err
	}
	for _, b := range builds(runs) {
		if b.IsBuild && b.SHA == sha && b.State != "skipped" {
			return b, true, changed, nil
		}
	}
	return Build{}, false, changed, nil
}

// startWatcher starts the watcher of repo and lets it run on. Its output
// goes nowhere, so it holds no pipe of the run that started it.
func startWatcher(repo string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "watch", repo)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func runningText(b Build, fc forecast) string {
	text := "CI running"
	switch {
	case fc.known && !fc.late:
		text = "CI " + dur(rounded(max(5, fc.total-fc.elapsed))) + " left"
	case fc.elapsed > 0:
		text = "CI running " + dur(fc.elapsed)
	}
	if b.Failing {
		return red + text + plain
	}
	return text
}

func failedText(f jobFailure) string {
	if f.test == "" {
		return red + "CI failed" + plain
	}
	return red + "CI failed: " + f.test + plain
}

// jobFailure is the first failure of a failed job.
type jobFailure struct {
	job, workflow, file string
	test, where, url    string
}

// failuresOf reads the log of each failed job of a build that ended. It
// costs a request per run and one per failed job.
func failuresOf(g *gh, repo string, b Build, most int) []jobFailure {
	var out []jobFailure
	for _, r := range b.Runs {
		if r.State != "failure" {
			continue
		}
		body, _, err := g.list(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, r.ID))
		if err != nil {
			continue
		}
		jobs, err := parseJobs(body)
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if j.State != "failure" {
				continue
			}
			f := jobFailure{job: j.Name, workflow: r.Workflow, file: r.File, url: j.URL}
			if raw, _, err := g.log(repo, j.ID); err == nil {
				for _, x := range findFailures(parseLog(raw), 0, -1) {
					if strings.HasPrefix(x.title, exitLinePrefix) {
						continue
					}
					f.test, f.where = x.title, x.location
					break
				}
			}
			out = append(out, f)
			if len(out) >= most {
				return out
			}
		}
	}
	return out
}

func firstFailure(g *gh, repo string, b Build) jobFailure {
	if fs := failuresOf(g, repo, b, 1); len(fs) > 0 {
		return fs[0]
	}
	return jobFailure{}
}

// cellTick moves the time left in the cell between two polls.
type cellTick struct{}

// watchFocus is a focus in the repository of the watcher.
type watchFocus struct{}

// watcher is the state of "tuios-ci watch" next to the model it drives.
type watcher struct {
	repo  string
	g     *gh
	shown string
	// seen are the commits of the builds the watcher saw run, and told
	// the ones that ended and that it has spoken about.
	seen, told map[string]bool
	text       string
	okUntil    time.Time
	quit       func()
}

// watchRun is "tuios-ci watch OWNER/REPO".
func watchRun(repo string) int {
	if stateDir() == "" || strings.Count(repo, "/") != 1 {
		fmt.Fprintln(os.Stderr, "tuios-ci watch: tuios starts this from a focus.")
		return 2
	}
	lock, err := os.OpenFile(lockFile(repo), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return 1
	}
	defer lock.Close()
	if !take(lock) {
		// Another watcher has the repository.
		return 0
	}
	_ = lock.Truncate(0)
	_, _ = lock.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	g, err := newGH()
	if err != nil {
		return 1
	}
	f := readFocus()
	sha := ""
	if f.Repo == repo {
		sha = f.SHA
	}
	m := newModel(g, repo, sha)
	m.headless = true
	msgs := make(chan Msg, 64)
	w := &watcher{repo: repo, g: g, seen: map[string]bool{}, told: map[string]bool{}}
	// observe runs in the loop, so the quit must not wait on the channel.
	w.quit = func() {
		select {
		case msgs <- quitMsg{}:
		default:
		}
	}
	m.observe = w.observe
	onPoke(func() { msgs <- watchFocus{} })
	go func() {
		for {
			time.Sleep(cellEvery)
			msgs <- cellTick{}
		}
	}()
	run(m, msgs)
	// "CI ok" stays a moment, then goes.
	if wait := time.Until(w.okUntil); wait > 0 {
		time.Sleep(wait)
		if f := readFocus(); f.Repo == repo && w.shown == "ok" {
			cell("")
		}
	}
	return 0
}

// focusOn moves the model to the commit of the focus, when it is in this
// repository.
func (m *model) focusOn() Cmd {
	f := readFocus()
	if f.Repo != m.repo || f.SHA == m.sha {
		return m.poll()
	}
	m.sha, m.key, m.jobPinned = f.SHA, f.SHA, false
	m.setJob(0)
	return batch(m.want(), m.poll())
}

// observe runs after each message the model handled.
func (w *watcher) observe(m *model) {
	if m.updated.IsZero() {
		return
	}
	f := readFocus()
	if f.Repo == w.repo && f.SHA != "" {
		for _, b := range m.all {
			if b.IsBuild && b.SHA == f.SHA && active(b.State) {
				w.seen[b.SHA] = true
			}
		}
	}
	running := false
	for _, b := range m.all {
		if !b.IsBuild || !w.seen[b.SHA] {
			continue
		}
		if active(b.State) {
			running = true
			continue
		}
		if !w.told[b.SHA] {
			w.told[b.SHA] = true
			w.tell(b)
		}
	}
	w.draw(m, f)
	if !running {
		w.quit()
	}
}

// tell says that a build the watcher saw run ended.
func (w *watcher) tell(b Build) {
	switch b.State {
	case "success":
		notify("Build passed: "+b.Title, "info")
	case "failure":
		f := firstFailure(w.g, w.repo, b)
		text := "Build failed"
		if f.test != "" {
			text += ": " + f.test
		}
		if f.file != "" {
			text += " in " + f.file
		}
		notify(text, "error")
	case "cancelled":
		notify("Build cancelled: "+b.Title, "warning")
	}
}

// draw sets the cell for the focused commit, when it is in this
// repository. Another repository's focus has its own cell.
func (w *watcher) draw(m *model, f focus) {
	if f.Repo != w.repo {
		return
	}
	var b Build
	found := false
	for _, x := range m.all {
		if x.IsBuild && x.SHA == f.SHA {
			b, found = x, true
			break
		}
	}
	text, kind := "", ""
	switch {
	case !found:
	case active(b.State):
		fc := forecast{}
		if m.key == b.Key {
			fc, _ = m.estimate(b)
		}
		text, kind = runningText(b, fc), "running"
	case b.State == "failure":
		if w.shown == "failure:"+b.SHA {
			return
		}
		text, kind = failedText(firstFailure(w.g, w.repo, b)), "failure:"+b.SHA
	case b.State == "success" && w.seen[b.SHA]:
		if w.shown != "ok" {
			w.okUntil = time.Now().Add(okFor)
		}
		text, kind = green+"CI ok"+plain, "ok"
	}
	if kind == w.shown && text == w.text {
		return
	}
	w.shown, w.text = kind, text
	cell(text)
}
