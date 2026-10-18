package tuie2e

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// tuios plugins install, offline: a bare repository under the test's temp
// folder, reached over file://, holds a probe plugin on five branches. On
// "main" its build writes its folder to dist/where and "built-by-install" to
// dist/out, and its one action prints dist/out. "next" is main with one
// more commit. On "broken" the build fails. On "escape" herdr-plugin.toml is
// a link to a manifest outside the repository, and on "linkdir" the folder
// plug is a link to a folder outside it. The outside manifest's build
// writes a marker file, so a build that ran outside the checkout shows.
// While the file slow exists, the build sleeps, so the test can kill an
// install during the build.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "tuios plugins
// install".

const probeManifest = `id = "e2e.probe"
name = "Probe"
version = "1.0.0"
min_herdr_version = "0.9.3"

[[build]]
command = ["sh", "-c", "%BUILD%"]

[[startup]]
command = ['sh', '-c', 'pwd >> "$HERDR_PLUGIN_STATE_DIR/startup.log"; exec sleep 600']

[[actions]]
id = "ping"
title = "Ping"
command = ["sh", "-c", "cat dist/out && echo pong"]

[[panes]]
id = "hold"
title = "Hold"
placement = "tab"
command = ["sh", "-c", "exec sleep 600"]
`

// probeRepo makes the bare repository and returns its file:// URL, the
// commits of main and next, and the folder outside it.
func probeRepo(t *testing.T) (url, main, next, outside string) {
	t.Helper()
	work := testutil.GitRepo(t)
	outside = t.TempDir()
	if real, err := filepath.EvalSymlinks(outside); err == nil {
		outside = real
	}
	evil := strings.ReplaceAll(probeManifest, "%BUILD%", "touch "+filepath.Join(outside, "ran"))
	if err := os.WriteFile(filepath.Join(outside, "herdr-plugin.toml"), []byte(evil), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(build string) {
		body := strings.ReplaceAll(probeManifest, "%BUILD%", build)
		if err := os.WriteFile(filepath.Join(work, "herdr-plugin.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, work, "add", "herdr-plugin.toml")
	}
	// With the file slow in place the build stops in a sleep, so a test can
	// kill the install during the build. sleep writes its pid first.
	slow := filepath.Join(outside, "slow")
	write("test -e " + slow + " && echo $$ > " + slow + ".pid && exec sleep 30; mkdir -p dist && pwd > dist/where && echo built-by-install > dist/out")
	testutil.Git(t, work, "commit", "-q", "-m", "probe")
	main = testutil.Git(t, work, "rev-parse", "HEAD")
	testutil.Git(t, work, "checkout", "-q", "-b", "broken")
	write("echo the build breaks >&2; exit 3")
	testutil.Git(t, work, "commit", "-q", "-m", "break the build")
	link := func(branch, name, target string) {
		testutil.Git(t, work, "checkout", "-q", "main")
		testutil.Git(t, work, "checkout", "-q", "-b", branch)
		_ = os.Remove(filepath.Join(work, name))
		if err := os.Symlink(target, filepath.Join(work, name)); err != nil {
			t.Skip("no symlinks here:", err)
		}
		testutil.Git(t, work, "add", "-A")
		testutil.Git(t, work, "commit", "-q", "-m", branch)
	}
	link("escape", "herdr-plugin.toml", filepath.Join(outside, "herdr-plugin.toml"))
	link("linkdir", "plug", outside)
	// next is main with one more commit that leaves the plugin alone: a
	// reinstall at a new commit with the same build.
	testutil.Git(t, work, "checkout", "-q", "main")
	testutil.Git(t, work, "checkout", "-q", "-b", "next")
	if err := os.WriteFile(filepath.Join(work, "NOTES"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "NOTES")
	testutil.Git(t, work, "commit", "-q", "-m", "next")
	next = testutil.Git(t, work, "rev-parse", "HEAD")
	bare := filepath.Join(t.TempDir(), "probe.git")
	testutil.Git(t, work, "clone", "-q", "--bare", work, bare)
	return "file://" + bare, main, next, outside
}

// TestPluginsInstallFromGit installs the probe from the bare repository with
// a daemon running. Each install builds into a new folder, and the lock
// entry names the folder that runs. Links out of the repository are
// refused. A kill at the prompt does not block the next install. main
// installs, builds in the folder the lock entry names, and runs. A failed
// build and a kill during the build of next leave the lock entry and the
// running plugin as they were. The next install of next works, and only its
// folder is left. The lock file and the list are kept as artifacts.
func TestPluginsInstallFromGit(t *testing.T) {
	url, mainCommit, nextCommit, outside := probeRepo(t)
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "probe", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	const id = "e2e.probe"
	lockPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "plugins.lock")
	data := xdgDir(base, "XDG_DATA_HOME")
	managed := filepath.Join(data, "tuios", "plugins", id)
	art := artifactDir(t)
	readLock := func() string {
		b, _ := os.ReadFile(lockPath)
		return string(b)
	}
	readState := func() string {
		b, _ := os.ReadFile(filepath.Join(data, "tuios", "plugins", "%state.toml"))
		return string(b)
	}
	ping := func(when string) {
		t.Helper()
		out, err := tuiosCLI(t, base, "plugins", "run", id, "ping", "--wait")
		if err != nil || !strings.Contains(out, "built-by-install") || !strings.Contains(out, "pong") {
			t.Fatalf("plugins run ping %s: %v\n%s", when, err, out)
		}
	}
	// checkFolders checks that the build folders of the plugin are the
	// ones named, the first being the live one, whose build ran in it.
	checkFolders := func(live string, also ...string) {
		t.Helper()
		folder := filepath.Join(data, "tuios", filepath.FromSlash(live))
		where, _ := os.ReadFile(filepath.Join(folder, "dist", "where"))
		if want, _ := filepath.EvalSymlinks(folder); strings.TrimSpace(string(where)) != want {
			t.Errorf("the build ran in %q, want the folder the state file names, %s", strings.TrimSpace(string(where)), want)
		}
		want := []string{path.Base(live)}
		for _, d := range also {
			want = append(want, path.Base(d))
		}
		slices.Sort(want)
		if got := foldersIn(managed); !slices.Equal(got, want) {
			t.Errorf("%s holds %v, want %v", managed, got, want)
		}
	}
	// checkStartup waits until the plugin's startup last ran from the
	// folder the state file names.
	checkStartup := func(live string) {
		t.Helper()
		want, _ := filepath.EvalSymlinks(filepath.Join(data, "tuios", filepath.FromSlash(live)))
		deadline := time.Now().Add(10 * time.Second)
		for {
			lines := strings.Fields(readPluginFile(base, id, "startup.log"))
			if len(lines) > 0 && lines[len(lines)-1] == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the startup did not run from %s. startup.log:\n%s", want, strings.Join(lines, "\n"))
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// A link out of the repository, as the manifest or as the plugin
	// folder: refused before any build, and nothing is left behind.
	for _, c := range []struct{ name, want string }{
		{"escape", "outside the repository"},
		{"linkdir", "has no folder plug"},
	} {
		args := []string{"plugins", "install", url + "@" + c.name, "--yes"}
		if c.name == "linkdir" {
			args = append(args, "--subdir", "plug")
		}
		out, err := tuiosCLI(t, base, args...)
		if err == nil || !strings.Contains(out, c.want) {
			t.Fatalf("install of %s: %v, want %q\n%s", c.name, err, c.want, out)
		}
		if _, err := os.Stat(filepath.Join(outside, "ran")); err == nil {
			t.Fatalf("install of %s ran a build outside the repository", c.name)
		}
		if n := len(foldersIn(managed)); n != 0 || strings.Contains(readLock(), id) {
			t.Fatalf("install of %s left %d folders in %s, or a lock entry:\n%s", c.name, n, managed, readLock())
		}
	}

	// A kill at the prompt, while the install lock is held: the next
	// install must still work.
	killInstallAt(t, base, "Continue?", url+"@main")

	// main: built in the folder the state file names, pinned and enabled.
	out, err := tuiosCLI(t, base, "plugins", "install", url+"@main", "--yes")
	if err != nil {
		t.Fatalf("install of main after a kill at the prompt: %v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(art, "install.txt"), []byte(out), 0o644)
	for _, want := range []string{"Install e2e.probe 1.0.0", "mkdir -p dist", "actions  ping", "tuios enables the plugin", "Installed e2e.probe at " + mainCommit[:12], "is enabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("the install output lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(readLock(), "[[plugin]]"); n != 1 {
		t.Fatalf("plugins.lock has %d entries, want 1:\n%s", n, readLock())
	}
	lock := field(readLock)
	if lock("version") != "1" || lock("id") != id || lock("format") != "herdr" || lock("source") != url || lock("ref") != "main" ||
		lock("commit") != mainCommit || len(lock("tree")) != 40 || !strings.HasPrefix(lock("approved"), "sha256:") ||
		lock("dir") != "" || lock("built") != "" {
		t.Fatalf("plugins.lock does not pin main at %s, or holds what is local to this machine:\n%s", mainCommit, readLock())
	}
	state := field(readState)
	if state("commit") != mainCommit || state("built") != "true" || !strings.HasPrefix(state("dir"), "plugins/"+id+"/"+mainCommit[:12]+"-") {
		t.Fatalf("the state file does not name the build of main:\n%s", readState())
	}
	mainDir := state("dir")
	checkFolders(mainDir)
	checkStartup(mainDir)

	out, err = tuiosCLI(t, base, "plugins", "list", "--json")
	if err != nil {
		t.Fatalf("plugins list: %v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(art, "list.json"), []byte(out), 0o644)
	entries := listEntries(out)
	if len(entries) != 1 || entries[0].ID != id || entries[0].Origin != "managed" || !entries[0].Enabled ||
		entries[0].Err != nil || entries[0].Lock == nil || entries[0].Lock.Commit != mainCommit {
		t.Fatalf("plugins list does not show the probe managed and on:\n%s", out)
	}
	if table, _ := tuiosCLI(t, base, "plugins", "list"); !strings.Contains(table, "managed") || !strings.Contains(table, mainCommit[:12]) {
		t.Errorf("the plugins list table lacks the source and the pin:\n%s", table)
	}
	ping("after the install of main")
	// A plugin pane opened from the build of main. Its folder stays while
	// the pane is open.
	if out, err := tuiosCLI(t, base, "plugins", "open", id, "hold", "--placement", "tab"); err != nil {
		t.Fatalf("plugins open hold: %v\n%s", err, out)
	}

	// A failed build: the lock file, the state file and the running plugin
	// stay as they were, and the failed build folder is gone.
	lockBefore, stateBefore := readLock(), readState()
	out, err = tuiosCLI(t, base, "plugins", "install", url+"@broken", "--yes")
	if err == nil || !strings.Contains(out, "the build command failed") || !strings.Contains(out, "The plugin is not installed") {
		t.Fatalf("install of the broken branch: %v\n%s", err, out)
	}
	if readLock() != lockBefore || readState() != stateBefore {
		t.Fatalf("a failed build changed plugins.lock or the state file:\n%s\n%s", readLock(), readState())
	}
	if log, _ := os.ReadFile(filepath.Join(pluginState(base, id), "build.log")); !strings.Contains(string(log), "the build breaks") {
		t.Errorf("build.log does not hold the failed build's output:\n%s", log)
	}
	checkFolders(mainDir)
	ping("after a failed build")

	// A kill during the build of next: the lock file, the state file and
	// the running plugin stay as they were.
	if err := os.WriteFile(filepath.Join(outside, "slow"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	killInstallAt(t, base, "Running in", url+"@next", "--yes")
	_ = os.Remove(filepath.Join(outside, "slow"))
	if pid, err := os.ReadFile(filepath.Join(outside, "slow.pid")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(pid))); err == nil {
			if p, err := os.FindProcess(n); err == nil {
				_ = p.Kill()
			}
		}
	}
	if readLock() != lockBefore || readState() != stateBefore {
		t.Fatalf("a killed install changed plugins.lock or the state file:\n%s\n%s", readLock(), readState())
	}
	ping("after a kill during the build")

	// next: a new folder. The plugin restarts from it, config.toml keeps it
	// enabled, and the build of main stays as the previous one. The folder
	// of the killed build goes.
	if out, err := tuiosCLI(t, base, "plugins", "install", url+"@next", "--yes"); err != nil {
		t.Fatalf("install of next after a kill during the build: %v\n%s", err, out)
	}
	nextDir := state("dir")
	if lock("commit") != nextCommit || state("commit") != nextCommit || nextDir == mainDir ||
		state("prev_dir") != mainDir || state("prev_commit") != mainCommit {
		t.Fatalf("plugins.lock and the state file do not name next, with main before it:\n%s\n%s", readLock(), readState())
	}
	checkFolders(nextDir, mainDir)
	checkStartup(nextDir)
	if cfg, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")); !strings.Contains(string(cfg), id) {
		t.Errorf("config.toml lost the plugin after the restart:\n%s", cfg)
	}
	ping("after the install of next")

	// An install killed between the state write and the lock write leaves
	// the state a commit ahead of plugins.lock. The plugin then runs from
	// the previous folder, the one plugins.lock names.
	nextLock := readLock()
	if err := os.WriteFile(lockPath, []byte(strings.ReplaceAll(nextLock, nextCommit, mainCommit)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ = tuiosCLI(t, base, "plugins", "list", "--json")
	if got := listEntries(out); len(got) != 1 || got[0].Plugin == nil || got[0].Plugin.Root != filepath.Join(data, "tuios", filepath.FromSlash(mainDir)) {
		t.Fatalf("with the state ahead of plugins.lock, the plugin does not run from the folder of main:\n%s", out)
	}
	if err := os.WriteFile(lockPath, []byte(nextLock), 0o600); err != nil {
		t.Fatal(err)
	}

	// main again, at a new folder: the live one and the one before it stay,
	// and the first build of main stays too, because the plugin pane runs
	// from it.
	if out, err := tuiosCLI(t, base, "plugins", "install", url+"@main", "--yes"); err != nil {
		t.Fatalf("install of main again: %v\n%s", err, out)
	}
	checkFolders(state("dir"), nextDir, mainDir)
	checkStartup(state("dir"))
	_ = os.WriteFile(filepath.Join(art, "plugins.lock"), []byte(readLock()), 0o644)
	_ = os.WriteFile(filepath.Join(art, "state.toml"), []byte(readState()), 0o644)
}

// listEntry is one plugin of plugins list --json.
type listEntry struct {
	ID      string          `json:"plugin_id"`
	Origin  string          `json:"origin"`
	Enabled bool            `json:"enabled"`
	Err     json.RawMessage `json:"error"`
	Lock    *struct {
		Commit string `json:"commit"`
	} `json:"lock"`
	Plugin *struct {
		Root string `json:"plugin_root"`
	} `json:"plugin"`
}

func listEntries(out string) []listEntry {
	var entries []listEntry
	_ = json.Unmarshal([]byte(out), &entries)
	return entries
}

// field reads one key of a TOML file off its own line, so the test needs no
// TOML parser of its own. read gives the file.
func field(read func() string) func(string) string {
	return func(key string) string {
		m := regexp.MustCompile(`(?m)^` + key + ` = ['"]?([^'"\n]*)['"]?$`).FindStringSubmatch(read())
		if m == nil {
			return ""
		}
		return m[1]
	}
}

// foldersIn lists the folders in dir, sorted.
func foldersIn(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// killInstallAt runs tuios plugins install with args in a terminal, waits
// until want is on the screen, and kills the process.
func killInstallAt(t *testing.T, base, want string, args ...string) {
	t.Helper()
	env := make([]string, 0, len(xdgKeys))
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	term, err := tuitest.Start(append([]string{tuiosBin, "plugins", "install"}, args...), tuitest.WithInheritEnv(), tuitest.WithEnv(env...))
	if err != nil {
		t.Fatalf("start tuios plugins install: %v", err)
	}
	defer term.Close()
	if err := term.WaitForText(want, 30*time.Second); err != nil {
		t.Fatalf("tuios plugins install never showed %q: %v\n%s", want, err, term.Snapshot())
	}
	p, err := os.FindProcess(term.Pid())
	if err == nil {
		err = p.Kill()
	}
	if err != nil {
		t.Fatalf("kill tuios plugins install: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
}
