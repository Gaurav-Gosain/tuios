package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ciFixtures writes the GitHub API answers that fakeGH serves, with times
// relative to now, so the plugin's own clock reads them as live.
//
// acme/shop has a running build on main (two workflows of one commit, with
// a deploy job that GitHub has not made yet), a failed pull request build,
// a run of an issue bot in which everything was skipped, a nightly run on
// main, and five earlier main builds that passed. acme/quiet has only a
// build that ended.
func ciFixtures(t *testing.T, dir string, now time.Time) {
	t.Helper()
	at := func(ago float64) string {
		return now.Add(-time.Duration(ago * float64(time.Second))).UTC().Format(time.RFC3339)
	}
	put := func(path string, v any) {
		t.Helper()
		var b []byte
		if s, ok := v.(string); ok {
			b = []byte(s)
		} else {
			var err error
			if b, err = json.Marshal(v); err != nil {
				t.Fatal(err)
			}
		}
		name := strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_", ",", "_").Replace(path)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	type obj = map[string]any
	run := func(repo string, id int, name, event, branch, sha, title string, start, end float64, conclusion string) obj {
		status, upd := "completed", at(end)
		if conclusion == "" {
			status, upd = "in_progress", at(start-1)
		}
		return obj{"id": id, "name": name, "display_title": title, "head_branch": branch, "event": event,
			"head_sha": sha, "html_url": fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, id),
			"status": status, "conclusion": conclusion, "run_started_at": at(start), "created_at": at(start),
			"updated_at": upd, "actor": obj{"login": "mira"}}
	}
	step := func(n int, name string, start, end float64, conclusion string) obj {
		s := obj{"number": n, "name": name, "status": "completed", "conclusion": conclusion, "started_at": at(start), "completed_at": at(end)}
		if conclusion == "" {
			s["status"], s["completed_at"] = "in_progress", nil
		}
		return s
	}
	job := func(repo string, run, id int, workflow, name string, start, end float64, conclusion string, steps ...obj) obj {
		j := obj{"id": id, "run_id": run, "name": name, "workflow_name": workflow, "status": "completed", "conclusion": conclusion,
			"html_url":   fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", repo, run, id),
			"started_at": at(start), "completed_at": at(end), "steps": steps}
		if conclusion == "" {
			j["status"], j["completed_at"] = "in_progress", nil
		}
		return j
	}
	withPR := func(r obj, n int) obj {
		r["pull_requests"] = []obj{{"number": n}}
		return r
	}
	jobs := func(repo string, run int, js ...obj) {
		put(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, run), obj{"jobs": js})
	}

	const shop = "acme/shop"
	put("repos/"+shop, obj{"full_name": shop, "default_branch": "main"})
	runs := []obj{
		run(shop, 101, "Tests", "push", "main", strings.Repeat("a", 40), "Change the checkout", 200, 0, ""),
		run(shop, 102, "Lint", "push", "main", strings.Repeat("a", 40), "Change the checkout", 200, 140, "success"),
		run(shop, 301, "Labeler", "issues", "main", strings.Repeat("c", 40), "Label the issue", 300, 299, "skipped"),
		withPR(run(shop, 201, "Tests", "pull_request", "gift-cards", strings.Repeat("b", 40), "Add gift cards", 900, 780, "failure"), 7),
		run(shop, 401, "Nightly", "schedule", "main", strings.Repeat("d", 40), "Nightly", 1500, 1300, "success"),
	}
	jobs(shop, 101,
		job(shop, 101, 1001, "Tests", "build", 195, 75, "success", step(1, "Set up job", 195, 194, "success")),
		job(shop, 101, 1002, "Tests", "test", 195, 0, "", step(1, "Set up job", 195, 194, "success"), step(2, "Run go test", 194, 0, "")))
	jobs(shop, 102, job(shop, 102, 1003, "Lint", "lint", 200, 140, "success", step(1, "Set up job", 200, 199, "success")))
	put(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", shop, 1002), at(195)+" Set up job\n"+at(194)+" ##[group]Run go test ./...\n"+at(150)+" ok  acme/shop/cart 0.4s\n")
	put(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", shop, 1001), at(195)+" Set up job\n"+at(80)+" built\n")

	// The failed build: the test runner names the failing test about a
	// hundred lines above the runner's exit line.
	t0 := 900.0
	// The failed job comes first, so a pick of the last job that did work
	// would show the build job.
	jobs(shop, 201,
		job(shop, 201, 2002, "Tests", "test", t0, t0-120, "failure",
			step(1, "Set up job", t0, t0-1, "success"), step(2, "Check out", t0-1, t0-2, "success"),
			step(3, "Run go test", t0-3, t0-110, "failure"), step(4, "Complete job", t0-111, t0-112, "success")),
		job(shop, 201, 2001, "Tests", "build", t0, t0-60, "success", step(1, "Set up job", t0, t0-1, "success")))
	var log strings.Builder
	line := func(ago float64, s string) { fmt.Fprintf(&log, "%s %s\n", at(ago), s) }
	line(t0, "Current runner version: '2.320.0'")
	line(t0-1, "##[group]Run actions/checkout@v4")
	line(t0-1, "Fetching the repository")
	line(t0-3, "##[group]Run go test ./...")
	for i := range 150 {
		line(t0-4, fmt.Sprintf("=== RUN   TestCart%03d", i))
		line(t0-4, fmt.Sprintf("--- PASS: TestCart%03d (0.00s)", i))
	}
	line(t0-50, "=== RUN   TestOrderTotal")
	line(t0-50, "--- FAIL: TestOrderTotal (0.01s)")
	line(t0-50, "    order_test.go:18: total is 9, want 10")
	for i := range 60 {
		line(t0-60, fmt.Sprintf("--- PASS: TestShip%03d (0.00s)", i))
	}
	line(t0-108, "\x1b[31mFAIL\x1b[0m acme/shop/order 0.2s")
	line(t0-109, "##[error]Process completed with exit code 1.")
	line(t0-111, "Cleaning up orphan processes")
	put(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", shop, 2002), log.String())
	put("repos/"+shop+"/pulls/7", obj{"number": 7, "title": "Add gift cards to the checkout", "state": "open"})
	put(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", shop, 2001), at(t0)+" Set up job\n"+at(t0-60)+" built\n")
	jobs(shop, 301)
	jobs(shop, 401, job(shop, 401, 4001, "Nightly", "nightly", 1500, 1300, "success"))

	// Five earlier main builds. Their runs end long after their jobs, so an
	// estimate from whole builds says about 6m 40s left, and one from the
	// jobs says about 4m 20s.
	for k := range 5 {
		start := 2000 + float64(k)*1000
		sha := fmt.Sprintf("%040d", k+1)
		tid, lid := 500+10*k, 501+10*k
		runs = append(runs,
			run(shop, tid, "Tests", "push", "main", sha, "Change "+strconv.Itoa(k+1), start, start-560-float64(k%3)*45, "success"),
			run(shop, lid, "Lint", "push", "main", sha, "Change "+strconv.Itoa(k+1), start, start-60, "success"))
		jobs(shop, tid,
			job(shop, tid, 9000+10*k, "Tests", "build", start-5, start-125, "success"),
			job(shop, tid, 9001+10*k, "Tests", "test", start-5, start-405, "success"),
			job(shop, tid, 9002+10*k, "Tests", "deploy", start-410, start-470, "success"))
		jobs(shop, lid, job(shop, lid, 9003+10*k, "Lint", "lint", start, start-60, "success"))
	}
	put("repos/"+shop+"/actions/runs?per_page=100", obj{"workflow_runs": runs})

	const quiet = "acme/quiet"
	put("repos/"+quiet, obj{"full_name": quiet, "default_branch": "main"})
	put("repos/"+quiet+"/actions/runs?per_page=100", obj{"workflow_runs": []obj{
		run(quiet, 701, "Tests", "push", "main", strings.Repeat("e", 40), "Quiet change", 4000, 3700, "success"),
	}})
	jobs(quiet, 701, job(quiet, 701, 7001, "Tests", "test", 4000, 3700, "success", step(1, "Set up job", 4000, 3999, "success")))
	put(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", quiet, 7001), at(4000)+" Set up job\n")

	put("user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member&page=1", []obj{
		{"full_name": shop, "description": "The shop", "pushed_at": at(200)},
		{"full_name": "acme/storefront-api", "description": "The API of the storefront", "pushed_at": at(7200)},
		{"full_name": quiet, "description": "Nothing runs here", "pushed_at": at(90000)},
		{"full_name": "acme/checkout-v1", "description": "The old checkout", "archived": true, "pushed_at": at(9000000)},
	})
}

// ciFinish rewrites the answers of acme/shop as if its running build had
// just failed in its test job, with the failing test a hundred lines above
// the runner's exit line.
func ciFinish(t *testing.T, dir string, now time.Time) {
	t.Helper()
	at := func(ago float64) string {
		return now.Add(-time.Duration(ago * float64(time.Second))).UTC().Format(time.RFC3339)
	}
	put := func(path string, b []byte) {
		t.Helper()
		name := strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_", ",", "_").Replace(path)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runsPath := filepath.Join(dir, "repos_acme_shop_actions_runs_per_page_100")
	raw, err := os.ReadFile(runsPath)
	if err != nil {
		t.Fatal(err)
	}
	var runs struct {
		Runs []map[string]any `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &runs); err != nil {
		t.Fatal(err)
	}
	for _, r := range runs.Runs {
		if r["id"] == float64(101) {
			r["status"], r["conclusion"], r["updated_at"] = "completed", "failure", at(5)
		}
	}
	b, _ := json.Marshal(runs)
	put("repos/acme/shop/actions/runs?per_page=100", b)
	step := func(n int, name string, start, end float64, conclusion string) map[string]any {
		return map[string]any{"number": n, "name": name, "status": "completed", "conclusion": conclusion, "started_at": at(start), "completed_at": at(end)}
	}
	jobs := map[string]any{"jobs": []map[string]any{
		{"id": 1001, "run_id": 101, "name": "build", "workflow_name": "Tests", "status": "completed", "conclusion": "success",
			"html_url": "https://github.com/acme/shop/actions/runs/101/job/1001", "started_at": at(260), "completed_at": at(140),
			"steps": []map[string]any{step(1, "Set up job", 260, 259, "success")}},
		{"id": 1002, "run_id": 101, "name": "test", "workflow_name": "Tests", "status": "completed", "conclusion": "failure",
			"html_url": "https://github.com/acme/shop/actions/runs/101/job/1002", "started_at": at(260), "completed_at": at(8),
			"steps": []map[string]any{step(1, "Set up job", 260, 259, "success"), step(2, "Run go test", 259, 10, "failure"), step(3, "Complete job", 9, 8, "success")}},
	}}
	b, _ = json.Marshal(jobs)
	put("repos/acme/shop/actions/runs/101/jobs?per_page=100", b)
	var log strings.Builder
	line := func(ago float64, s string) { fmt.Fprintf(&log, "%s %s\n", at(ago), s) }
	line(260, "Set up job")
	line(259, "##[group]Run go test ./...")
	for i := range 80 {
		line(200, fmt.Sprintf("--- PASS: TestCart%03d (0.00s)", i))
	}
	line(150, "--- FAIL: TestCheckout (0.01s)")
	line(150, "    checkout_test.go:31: paid twice")
	for i := range 120 {
		line(100, fmt.Sprintf("--- PASS: TestShip%03d (0.00s)", i))
	}
	line(11, "##[error]Process completed with exit code 1.")
	line(9, "Cleaning up orphan processes")
	put("repos/acme/shop/actions/jobs/1002/logs", []byte(log.String()))
}

// fakeGH is a gh that answers gh api from the files in $CI_FAKE_DIR/answers,
// with an ETag, and "not modified" for the ETag of the file. It writes each
// call to $CI_FAKE_DIR/calls.
const fakeGH = `#!/bin/sh
echo "$*" >> "$CI_FAKE_DIR/calls"
# A rerun or a browser: only the call counts.
[ "$1" = api ] || exit 0
inc=0 etag=
while [ $# -gt 1 ]; do
	case "$1" in
	--include) inc=1 ;;
	-H) shift; etag=${1#If-None-Match: } ;;
	esac
	shift
done
f="$CI_FAKE_DIR/answers/$(printf %s "$1" | tr '/?&=,' '_____')"
if [ ! -f "$f" ]; then
	echo "gh: Not Found (HTTP 404)" >&2
	exit 1
fi
tag="\"$(cksum < "$f" | cut -d' ' -f1)\""
if [ $inc = 1 ]; then
	if [ "$etag" = "$tag" ]; then
		printf 'HTTP/2.0 304 Not Modified\r\n\r\n'
		exit 1
	fi
	printf 'HTTP/2.0 200 OK\r\nEtag: %s\r\n\r\n' "$tag"
fi
cat "$f"
`

// TestPluginCI runs plugins/ci, the CI plugin, in a tuios pane against a
// fake gh on PATH that serves recorded API answers.
//
// How it could pass wrongly, written down first:
//   - The two runs of one commit could show as two builds that each look
//     right: the list must count 8 builds, and the build must name both
//     workflows.
//   - The estimate could come from whole builds, which is always there: the
//     earlier runs end long after their jobs, so that estimate says about
//     6m 30s and only the job forecast says about 4m 20s. The deploy job that GitHub
//     has not made must show as one to come.
//   - A failed build could open on its first job, or its log on the exit
//     line: the test job must be the one shown, with the failing test in
//     view and the exit line, a hundred lines lower, out of view.
//   - A filter could change the boxes and not the list: each one is checked
//     by the count of builds it leaves.
//   - A rerun could run on one key press, or not reach gh: one press of R
//     must call nothing, the second must call gh run rerun --failed.
//   - A search could count lines and not show them: the count, the first
//     match in view, and ctrl+n to the next.
//   - The plugin could poll with nothing running: a pane on a repository
//     where every build ended must make no gh call for ten seconds.
//
// NEGATIVE CONTROLS, each run once with the logic cut:
//   - builds() keyed by run: the count check fails.
//   - forecastOf with no earlier jobs: the estimate says about 6m and
//     fails.
//   - pickJob without the rule for a failed job: the log shows the build
//     job and fails.
//   - findFailures with only the exit line: the log opens on the exit line
//     and fails.
//   - shown() ignores activeOnly: A leaves 8 builds and fails.
//   - schedule() polls with nothing running: the quiet pane calls gh and
//     fails.
func TestPluginCI(t *testing.T) {
	requireReal(t, "it reads the CPU time of the plugin in a pane")
	plugin, err := filepath.Abs(filepath.Join("..", "..", "plugins", "ci"))
	if err != nil {
		t.Fatal(err)
	}
	fake := t.TempDir()
	bin := filepath.Join(fake, "bin")
	answers := filepath.Join(fake, "answers")
	for _, d := range []string{bin, answers} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The plugin's folder with its program built, as an install leaves it,
	// so the source folder stays clean.
	installed := filepath.Join(fake, "ci")
	manifest, err := os.ReadFile(filepath.Join(plugin, "tuios-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installed, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "tuios-plugin.toml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	ci := filepath.Join(installed, "bin", "tuios-ci")
	build := exec.Command("go", "build", "-trimpath", "-o", ci, ".")
	build.Dir = plugin
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the plugin: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	ciFixtures(t, answers, time.Now())
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CI_FAKE_DIR", fake)
	calls := func() string {
		b, _ := os.ReadFile(filepath.Join(fake, "calls"))
		return string(b)
	}
	t.Cleanup(func() { saveText(t, "ci-gh-calls", calls()) })

	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "plugins", "check", installed); err != nil {
		t.Fatalf("plugins check: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "ci", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 240, rows: 50, args: []string{"attach", "ci"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	var list struct {
		Windows []struct {
			ID   string `json:"window_id"`
			Name string `json:"display_name"`
		} `json:"windows"`
	}
	window := func(name string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, "list-windows", "-s", "ci", "--json")
		if err != nil || json.Unmarshal([]byte(out), &list) != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		for _, w := range list.Windows {
			if w.Name == name {
				return w.ID
			}
		}
		t.Fatalf("no window %s:\n%s", name, out)
		return ""
	}
	open := func(name, repo string) string {
		t.Helper()
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", "ci", "--", ci, repo); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		return window(name)
	}
	pane := open("shop", "acme/shop")
	// The plugin fills the screen, as it does for a person who zooms it.
	if out, err := tuiosCLI(t, base, "run-command", "-s", "ci", "Action", "toggle_zoom"); err != nil {
		t.Fatalf("zoom: %v\n%s", err, out)
	}
	screenOf := func(id string) func() string {
		return func() string {
			out, _ := tuiosCLI(t, base, "capture-pane", "-s", "ci", "-w", id)
			return out
		}
	}
	waitFor := func(what string, ok func(string) bool) string {
		t.Helper()
		return waitTextOf(t, what, uiTimeout*3, screenOf(pane), ok)
	}
	press := func(keys ...string) {
		t.Helper()
		for _, k := range keys {
			if out, err := tuiosCLI(t, base, "send-text", "-s", "ci", "-w", pane, k); err != nil {
				t.Fatalf("send-text %s: %v\n%s", k, err, out)
			}
		}
	}
	count := func(n int) func(string) bool {
		re := regexp.MustCompile(`BUILDS\s+\[?` + strconv.Itoa(n) + `\]?\s`)
		return func(s string) bool { return re.MatchString(s) }
	}

	// The running build: one row for both workflows, and the estimate from
	// the jobs, with the deploy still to come.
	// About 4m 20s left at the start, less as the test runs. The whole
	// builds say about 6m 30s.
	estimate := regexp.MustCompile(`About (4m [0-3]?0s|3m \d+s) left`)
	s := waitFor("the running build with its estimate", func(s string) bool {
		return estimate.MatchString(s) && strings.Contains(s, "deploy")
	})
	saveText(t, "ci-running", s)
	if err := term.WaitForText("deploy", uiTimeout); err != nil {
		t.Errorf("the attached screen does not show the estimate: %v", err)
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "ci-running")
	if !count(8)(s) || !strings.Contains(s, "Tests, Lint") {
		t.Errorf("ASSERTION: the runs of one commit are not one build of 8:\n%s", s)
	}
	if !strings.Contains(s, "◌ deploy") || !strings.Contains(s, "to come") {
		t.Errorf("ASSERTION: the deploy job that GitHub has not made is not shown as one to come:\n%s", s)
	}
	if strings.Contains(s, "Label the issue") || !strings.Contains(s, "Nightly") {
		t.Errorf("ASSERTION: the skipped build shows, or the nightly run is not a build of its own:\n%s", s)
	}

	// The failed build opens on the failed job, at the failing test.
	press("j")
	s = waitFor("the failed build at its failing test", func(s string) bool {
		return strings.Contains(s, "Failed after") && strings.Contains(s, "order_test.go")
	})
	saveText(t, "ci-failed", s)
	if err := term.WaitForText("order_test.go", uiTimeout); err != nil {
		t.Errorf("the attached screen does not show the failure: %v", err)
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "ci-failed")
	if !strings.Contains(s, "--- FAIL: TestOrderTotal") || !strings.Contains(s, "test · 1 failure") {
		t.Errorf("ASSERTION: the failed build does not open on the test job at the failing test:\n%s", s)
	}
	if strings.Contains(s, "Process completed with exit code") {
		t.Errorf("ASSERTION: the log opens on the exit line, not on the failing test:\n%s", s)
	}

	// c copies the link of the job.
	press("c")
	waitFor("the copied link", func(s string) bool {
		return strings.Contains(s, "Copied https://github.com/acme/shop/actions/runs/201/job/2002")
	})

	// The pull request of the build shows as a pill, and O opens it.
	waitFor("the pill of the pull request", func(s string) bool { return strings.Contains(s, "Open #7 Add gift cards to the checkout") })
	press("O")
	waitFor("the pull request opened", func(s string) bool { return strings.Contains(calls(), "pr view 7 --web -R acme/shop") })
	press("o")
	waitFor("the job opened", func(s string) bool { return strings.Contains(calls(), "run view 201 --job 2002 --web -R acme/shop") })

	// R runs the failed jobs again, but only on its second press: a rerun
	// costs runner time.
	press("R")
	waitFor("the question before a rerun", func(s string) bool { return strings.Contains(s, "Press R again") })
	if strings.Contains(calls(), "run rerun") {
		t.Errorf("ASSERTION: one press of R ran the jobs again:\n%s", calls())
	}
	press("R")
	waitFor("the rerun", func(s string) bool { return strings.Contains(s, "GitHub runs the failed jobs of 1 run(s) again") })
	if !strings.Contains(calls(), "run rerun 201 -R acme/shop --failed") {
		t.Errorf("ASSERTION: the second R did not run the failed jobs of run 201 again:\n%s", calls())
	}

	// / searches the log: the count of matches, the first one in view, and
	// ctrl+n steps to the next.
	press("/")
	for k := range strings.SplitSeq("TestShip01", "") {
		press(k)
	}
	s = waitFor("the matches of the search", func(s string) bool { return strings.Contains(s, "1 of 10 matches") })
	if !strings.Contains(s, "TestShip010") {
		t.Errorf("ASSERTION: the first match is not in view:\n%s", s)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "ci", "-w", pane, "Enter"); err != nil {
		t.Fatalf("send-keys Enter: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "ci", "-w", pane, "ctrl+n"); err != nil {
		t.Fatalf("send-keys ctrl+n: %v\n%s", err, out)
	}
	waitFor("the next match", func(s string) bool { return strings.Contains(s, "2 of 10 matches") })
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "ci", "-w", pane, "Escape"); err != nil {
		t.Fatalf("send-keys Escape: %v\n%s", err, out)
	}
	waitFor("the search closed", func(s string) bool { return !strings.Contains(s, "matches") })

	// The filters, each by the builds it leaves.
	press("S")
	s = waitFor("the skipped build with S", count(9))
	if !strings.Contains(s, "Label the issue") {
		t.Errorf("ASSERTION: S does not show the skipped build:\n%s", s)
	}
	press("S", "M")
	s = waitFor("the builds of main with M", count(6))
	if strings.Contains(s, "Add gift cards") || strings.Contains(s, "Nightly") {
		t.Errorf("ASSERTION: M keeps a build that is not of main:\n%s", s)
	}
	press("M", "A")
	s = waitFor("the running builds with A", count(1))
	if !strings.Contains(s, "Change the checkout") {
		t.Errorf("ASSERTION: A does not keep the running build:\n%s", s)
	}
	press("A")
	waitFor("all builds after A", count(8))
	press("j", "F")
	s = waitFor("the branch of the build with F", count(1))
	if !strings.Contains(s, "gift-cards") {
		t.Errorf("ASSERTION: F does not keep the branch of the selected build:\n%s", s)
	}
	press("F")
	waitFor("all builds again", count(8))

	// G lists the repositories, narrowed by what you type.
	press("G")
	waitFor("the menu of repositories", func(s string) bool { return strings.Contains(s, "acme/storefront-api") })
	press("a", "p", "i")
	s = waitFor("the menu narrowed to api", func(s string) bool { return !strings.Contains(s, "acme/quiet") })
	if !strings.Contains(s, "acme/storefront-api") {
		t.Errorf("ASSERTION: typing api does not keep acme/storefront-api:\n%s", s)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "ci", "-w", pane, "Escape"); err != nil {
		t.Fatalf("send-keys Escape: %v\n%s", err, out)
	}
	waitFor("the builds after esc", count(8))

	// Opening the same repository again, twice, while its build runs.
	for range 2 {
		press("G")
		waitFor("the menu of repositories", func(s string) bool { return strings.Contains(s, "acme/storefront-api") })
		press("s", "h", "o", "p")
		waitFor("the menu narrowed to shop", func(s string) bool { return !strings.Contains(s, "storefront-api") })
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "ci", "-w", pane, "Enter"); err != nil {
			t.Fatalf("send-keys Enter: %v\n%s", err, out)
		}
		waitFor("acme/shop again", func(s string) bool { return estimate.MatchString(s) && count(8)(s) })
	}
	// The clock after the switches: TestOldTickStartsNoSecondClock in the
	// plugin's module covers the tick of the repository before, which a
	// count of wakeups here cannot tell from the GC's.

	// The build ends while you watch it: the log is read again to its end,
	// and it opens at the failing test.
	ciFinish(t, answers, time.Now())
	// A focus report starts a poll at once, as a poll on the timer would.
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "ci", "-w", pane, "\\e[I"); err != nil {
		t.Fatalf("send-keys focus: %v\n%s", err, out)
	}
	s = waitFor("the ended build at its failing test", func(s string) bool {
		return strings.Contains(s, "Change the checkout") && strings.Contains(s, "Failed after") && strings.Contains(s, "checkout_test.go")
	})
	saveText(t, "ci-ended", s)
	if !strings.Contains(s, "--- FAIL: TestCheckout") || strings.Contains(s, "Process completed with exit code") {
		t.Errorf("ASSERTION: the build that ended while watched does not open at its failing test:\n%s", s)
	}

	// Nothing runs in acme/quiet: after the first read, no gh call and
	// almost no CPU.
	quiet := open("quiet", "acme/quiet")
	s = waitTextOf(t, "the quiet repository", uiTimeout*3, screenOf(quiet), func(s string) bool { return strings.Contains(s, "Passed in 5m 0s") })
	saveText(t, "ci-quiet", s)
	pid := pidOf(t, ci, "acme/quiet")
	// The first reads and the start of the Go runtime settle first.
	pause(t, "let the quiet pane settle after its first read", 3*time.Second)
	before := strings.Count(calls(), "acme/quiet")
	s0, c0 := procCost(pid)
	pause(t, "watch the plugin with nothing running", 10*time.Second)
	s1, c1 := procCost(pid)
	after := strings.Count(calls(), "acme/quiet")
	t.Logf("idle over 10 s: %d gh calls, %d context switches, %d CPU ticks", after-before, s1-s0, c1-c0)
	saveText(t, "ci-idle-cost", fmt.Sprintf("over 10 s with nothing running: %d gh calls, %d context switches, %d CPU ticks\n", after-before, s1-s0, c1-c0))
	if after != before {
		t.Errorf("ASSERTION: the plugin called gh %d times in 10 s with nothing running", after-before)
	}
	// A renderer on a ticker wakes the process up some 600 times in 10 s.
	if c1-c0 > 2 || s1-s0 > 50 {
		t.Errorf("ASSERTION: the plugin wakes up with nothing running: %d context switches and %d CPU ticks in 10 s", s1-s0, c1-c0)
	}
}

// pidOf is the pid of the process that runs bin with arg.
func pidOf(t *testing.T, bin, arg string) int {
	t.Helper()
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		b, _ := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if args := strings.Split(string(b), "\x00"); len(args) > 1 && args[0] == bin && args[1] == arg {
			return pid
		}
	}
	t.Fatalf("no process runs %s %s", bin, arg)
	return 0
}

// procCost is the context switches of all the threads of a process and
// its CPU ticks so far.
func procCost(pid int) (switches, ticks int) {
	p := "/proc/" + strconv.Itoa(pid)
	tasks, _ := os.ReadDir(p + "/task")
	for _, task := range tasks {
		status, _ := os.ReadFile(p + "/task/" + task.Name() + "/status")
		for f := range strings.SplitSeq(string(status), "\n") {
			if k, v, ok := strings.Cut(f, ":"); ok && strings.HasSuffix(k, "ctxt_switches") {
				n, _ := strconv.Atoi(strings.TrimSpace(v))
				switches += n
			}
		}
	}
	stat, _ := os.ReadFile(p + "/stat")
	s := string(stat)
	fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(fields) > 12 {
		u, _ := strconv.Atoi(fields[11])
		sy, _ := strconv.Atoi(fields[12])
		ticks = u + sy
	}
	return switches, ticks
}
