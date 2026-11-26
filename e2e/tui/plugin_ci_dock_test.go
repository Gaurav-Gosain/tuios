package tuie2e

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestPluginCIDock runs the dock cell of plugins/ci against the fake gh of
// TestPluginCI: the focus of a pane in a checkout sets the cell from the
// build of its commit, one watcher per repository follows a running build
// and says once when it ends, a click opens the builds on that commit, and
// tuios-ci wait gives 0, 1 or 2.
//
// How it could pass wrongly, written down first:
//   - The message could come from the focus that reads an ended build, not
//     from the end the watcher saw: after the end, more focuses must bring
//     no second message, and a build that failed before any focus must
//     bring none.
//   - A watcher could stay after the build: no tuios-ci process may be left
//     once the build ended and the focus moved.
//   - Each focus could start a watcher: there must be one at a time.
//   - The click could open the builds on the newest build: the pane must
//     name the commit of the cell, so the click is checked on a commit that
//     is not the newest.
func TestPluginCIDock(t *testing.T) {
	requireReal(t, "it reads the processes of the plugin")
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
	// The plugin's folder with its program built and no build command, as
	// an install leaves it.
	installed := filepath.Join(fake, "ci")
	manifest, err := os.ReadFile(filepath.Join(plugin, "tuios-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	noBuild := regexp.MustCompile(`(?s)\[\[build\]\]\ncommand = [^\n]*\n`).ReplaceAll(manifest, nil)
	if err := os.MkdirAll(filepath.Join(installed, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "tuios-plugin.toml"), noBuild, 0o644); err != nil {
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
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CI_FAKE_DIR", fake)
	t.Cleanup(func() {
		b, _ := os.ReadFile(filepath.Join(fake, "calls"))
		saveText(t, "ci-dock-gh-calls", string(b))
	})

	// Checkouts of acme/shop: the running build, a build that failed, a
	// commit with no build, and a build that will pass. Each has a commit
	// of its own, so each has its own commit id.
	checkout := func(name string) (string, string) {
		t.Helper()
		dir := testutil.GitRepo(t)
		testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/shop.git")
		testutil.Git(t, dir, "commit", "-q", "--allow-empty", "-m", name)
		return dir, testutil.Git(t, dir, "rev-parse", "HEAD")
	}
	work, workSHA := checkout("work")
	gift, giftSHA := checkout("gift")
	none, noneSHA := checkout("none")
	okDir, okSHA := checkout("ok")
	passed := strings.Repeat("0", 39) + "1"
	now := time.Now()
	runsFile := filepath.Join(answers, "repos_acme_shop_actions_runs_per_page_100")
	swap := strings.NewReplacer(strings.Repeat("a", 40), workSHA, strings.Repeat("b", 40), giftSHA)
	// answer rewrites the fixtures: the commits of the checkouts in place
	// of the made-up ones, a workflow file for each run, the build that
	// will pass in the state okState ("" for none yet), and the runs of
	// each commit on their own.
	answer := func(okState string) {
		t.Helper()
		b, err := os.ReadFile(runsFile)
		if err != nil {
			t.Fatal(err)
		}
		var in struct {
			Runs []map[string]any `json:"workflow_runs"`
		}
		if err := json.Unmarshal([]byte(swap.Replace(string(b))), &in); err != nil {
			t.Fatal(err)
		}
		var runs []map[string]any
		for _, r := range in.Runs {
			if r["id"] == float64(801) {
				continue
			}
			r["path"] = ".github/workflows/" + strings.ToLower(r["name"].(string)) + ".yml"
			if r["name"] == "Tests" {
				r["path"] = ".github/workflows/ci.yml"
			}
			runs = append(runs, r)
		}
		at := func(ago float64) string {
			return now.Add(-time.Duration(ago * float64(time.Second))).UTC().Format(time.RFC3339)
		}
		ok := map[string]any{"id": 801, "name": "Tests", "path": ".github/workflows/ci.yml", "display_title": "Add the receipt", "head_branch": "receipt",
			"event": "push", "head_sha": okSHA, "html_url": "https://github.com/acme/shop/actions/runs/801", "status": "in_progress", "conclusion": "",
			"run_started_at": at(60), "created_at": at(60), "updated_at": at(59), "actor": map[string]any{"login": "mira"}}
		if okState == "success" {
			ok["status"], ok["conclusion"], ok["updated_at"] = "completed", "success", at(1)
		}
		if okState != "" {
			runs = append([]map[string]any{ok}, runs...)
		}
		put := func(name string, v any) {
			t.Helper()
			b, _ := json.Marshal(v)
			if err := os.WriteFile(filepath.Join(answers, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		put(filepath.Base(runsFile), map[string]any{"workflow_runs": runs})
		for _, sha := range []string{workSHA, giftSHA, noneSHA, okSHA, passed} {
			of := []map[string]any{}
			for _, r := range runs {
				if r["head_sha"] == sha {
					of = append(of, r)
				}
			}
			put(filepath.Base(runsFile)+"_head_sha_"+sha, map[string]any{"workflow_runs": of})
		}
		put("repos_acme_shop_actions_runs_801_jobs_per_page_100", map[string]any{"jobs": []map[string]any{}})
	}
	ciFixtures(t, answers, now)
	answer("")

	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "dock", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "install", installed, "--yes"); err != nil || !strings.Contains(out, "build") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 200, rows: 40, args: []string{"attach", "dock"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	art := artifactDir(t)
	ids := map[string]string{}
	open := func(name, dir string) {
		t.Helper()
		out, err := tuiosCLI(t, base, "new-window", name, "-s", "dock", "--print-id", "--cwd", dir, "--", "sleep", "900")
		if err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		ids[name] = strings.TrimSpace(out)
	}
	focusOn := func(name string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "focus-window", "-s", "dock", ids[name]); err != nil {
			t.Fatalf("focus-window %s: %v\n%s", name, err, out)
		}
	}
	dock := func() string { return dockRows(term.Screen()) }
	waitDock := func(what string, ok func(string) bool) string {
		t.Helper()
		deadline := time.Now().Add(3 * uiTimeout)
		for {
			s := dock()
			if ok(s) {
				return s
			}
			if time.Now().After(deadline) {
				log, _ := tuiosCLI(t, base, "plugins", "log", "tuios.ci", "--limit", "10")
				t.Fatalf("ASSERTION: %s:\n%s\n%s", what, log, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	has := func(text string) func(string) bool { return func(s string) bool { return strings.Contains(s, text) } }
	noCell := func(s string) bool { return !strings.Contains(s, "CI ") }
	// procs are the processes of the plugin's program whose arguments
	// start with args.
	procs := func(args ...string) []int {
		var out []int
		dirs, _ := os.ReadDir("/proc")
		for _, d := range dirs {
			pid, err := strconv.Atoi(d.Name())
			if err != nil {
				continue
			}
			b, _ := os.ReadFile("/proc/" + d.Name() + "/cmdline")
			got := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
			if got[0] != ci || len(got) < len(args)+1 {
				continue
			}
			if slices.Equal(got[1:len(args)+1], args) {
				out = append(out, pid)
			}
		}
		return out
	}
	watchers := func() int { return len(procs("watch")) }
	waitGone := func(what string) {
		t.Helper()
		deadline := time.Now().Add(3 * uiTimeout)
		for len(procs()) > 0 {
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s: tuios-ci processes %v are alive", what, procs())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	left := regexp.MustCompile(`CI \d+m \d+s left`)

	// The running build: the focus sets the cell, and one watcher moves it
	// to the time left.
	open("work", work)
	s := waitDock("the cell of the running build", left.MatchString)
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "cell-running")
	saveText(t, "ci-dock-running", s)
	if n := watchers(); n != 1 {
		t.Fatalf("ASSERTION: %d watchers for one running build, want 1", n)
	}
	open("plain", t.TempDir())
	waitDock("no cell outside a checkout", noCell)
	focusOn("work")
	waitDock("the cell back on the running build", left.MatchString)
	focusOn("plain")
	focusOn("work")
	waitDock("the cell after more focuses", left.MatchString)
	if n := watchers(); n != 1 {
		t.Fatalf("ASSERTION: %d watchers after three focuses on one build, want 1", n)
	}
	if strings.Contains(term.Snapshot(), "CI: Build") {
		t.Fatalf("ASSERTION: a message while the build runs:\n%s", term.Snapshot())
	}

	// A build that failed before any focus: the cell names the test, no
	// watcher starts and no message comes. A click on the cell opens the
	// builds on its commit, which is not the newest build.
	open("gift", gift)
	waitDock("the cell of the failed build", has("CI failed: TestOrderTot"))
	if n := watchers(); n != 1 {
		t.Fatalf("ASSERTION: a build that ended started a watcher: %d watchers", n)
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "cell-failed")
	x, y, ok := dockCellFind(term.Screen(), "CI failed")
	if !ok {
		t.Fatalf("the cell is not on the dock:\n%s", term.Snapshot())
	}
	windows := func() []string {
		t.Helper()
		var list struct {
			Windows []struct {
				ID string `json:"window_id"`
			} `json:"windows"`
		}
		out, err := tuiosCLI(t, base, "list-windows", "-s", "dock", "--json")
		if err != nil || json.Unmarshal([]byte(out), &list) != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		var ids []string
		for _, w := range list.Windows {
			ids = append(ids, w.ID)
		}
		return ids
	}
	before := windows()
	mouseClick(t, term, x+1, y, tuitest.MouseLeft, 0)
	pane := regexp.MustCompile(`BUILD\s+gift-cards · not on main · ` + giftSHA[:7])
	deadline := time.Now().Add(3 * uiTimeout)
	for !pane.MatchString(term.Snapshot()) {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the click did not open the builds on %s:\n%s", giftSHA[:7], term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	saveText(t, "ci-dock-click", term.Snapshot())
	// The pane of the builds is a tuios-ci too. Close it, so the idle
	// check below counts only what the dock started.
	for _, id := range windows() {
		if !slices.Contains(before, id) {
			if out, err := tuiosCLI(t, base, "close-window", "-s", "dock", id); err != nil {
				t.Fatalf("close the pane of the builds: %v\n%s", err, out)
			}
		}
	}
	if strings.Contains(term.Snapshot(), "CI: Build") {
		t.Fatalf("ASSERTION: a message for a build that ended before any focus:\n%s", term.Snapshot())
	}

	// The build ends while the watcher runs: one message, and the cell
	// names the failing test.
	ciFinish(t, answers, now)
	answer("")
	focusOn("plain")
	focusOn("work")
	if err := term.WaitForText("CI: Build failed: TestCheckout in ci.yml", 3*uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no message at the end of the build: %v\n%s", err, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "message")
	// The message hides the cells on the right while it shows. An error
	// stays until esc.
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(30 * time.Second)
	for strings.Contains(term.Snapshot(), "CI: Build failed") {
		if time.Now().After(deadline) {
			t.Fatalf("the message stays:\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	waitDock("the cell of the build that failed", has("CI failed: TestCheckout"))
	focusOn("plain")
	waitGone("the build ended and the focus moved")
	// More focuses on the ended builds bring no other message.
	for range 2 {
		focusOn("work")
		waitDock("the cell of the ended build", has("CI failed: TestCheckout"))
		focusOn("gift")
		waitDock("the cell of the other failed build", has("CI failed: TestOrderTot"))
	}
	pause(t, "give a second message the time to show", 2*time.Second)
	if strings.Contains(term.Snapshot(), "CI: Build failed") {
		t.Fatalf("ASSERTION: a focus on an ended build brought a message again:\n%s", term.Snapshot())
	}
	if n := len(procs()); n != 0 {
		t.Fatalf("ASSERTION: focuses on ended builds left %d tuios-ci processes", n)
	}

	// A commit with no build clears the cell.
	open("none", none)
	waitDock("no cell for a commit with no build", noCell)

	// A build that passes while the watcher runs: "CI ok" for a moment,
	// then no cell.
	answer("running")
	open("ok", okDir)
	waitDock("the cell of the build that will pass", has("CI "))
	answer("success")
	focusOn("plain")
	focusOn("ok")
	if err := term.WaitForText("CI: Build passed: Add the receipt", 3*uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no message when the build passed: %v\n%s", err, term.Snapshot())
	}
	// The message hides the cell for a moment, and CI ok stays for 30
	// seconds, so the cell shows after the message.
	waitDock("CI ok after the pass", has("CI ok"))
	deadline = time.Now().Add(45 * time.Second)
	for strings.Contains(dock(), "CI ok") {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: CI ok stays:\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	focusOn("plain")
	waitGone("the passed build ended and the focus moved")
	saveText(t, "ci-dock-idle", "no tuios-ci process after the builds ended and the focus moved\n")

	// tuios-ci wait.
	wait := func(sha string) (int, string) {
		t.Helper()
		b, err := exec.Command(ci, "wait", "acme/shop", "--sha", sha, "--start", "0").CombinedOutput()
		var ee *exec.ExitError
		switch {
		case err == nil:
			return 0, string(b)
		case errors.As(err, &ee):
			return ee.ExitCode(), string(b)
		}
		t.Fatalf("tuios-ci wait: %v\n%s", err, b)
		return -1, ""
	}
	if code, out := wait(giftSHA); code != 1 || !strings.Contains(out, "TestOrderTotal") || !strings.Contains(out, "order_test.go:18") || !strings.Contains(out, "/job/2002") {
		t.Errorf("ASSERTION: wait on the failed build: exit %d, want 1 with the test, the file and the job\n%s", code, out)
	} else {
		saveText(t, "ci-wait-failed", out)
	}
	if code, out := wait(passed); code != 0 || !strings.Contains(out, "Passed") {
		t.Errorf("ASSERTION: wait on a build that passed: exit %d, want 0\n%s", code, out)
	}
	if code, out := wait(noneSHA); code != 2 {
		t.Errorf("ASSERTION: wait on a commit with no build: exit %d, want 2\n%s", code, out)
	}
}
