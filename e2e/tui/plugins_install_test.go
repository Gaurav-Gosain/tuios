package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// tuios plugins install, offline: a bare repository under the test's temp
// folder, reached over file://, holds a probe plugin on four branches. On
// "main" its build writes dist/out and its one action prints that file. On
// "broken" its build command fails. On "escape" herdr-plugin.toml is a link
// to a manifest outside the repository, and on "linkdir" the folder plug is
// a link to a folder outside it. The outside manifest's build writes a
// marker file, so a build that ran outside the checkout shows.
//
// Negative controls (NEGATIVE_CONTROLS.md, "tuios plugins install"): with
// the build error ignored, the broken branch gets a lock entry; with the
// removal of the new checkout cut from the build failure, the broken branch
// leaves its folder; with the managed origin cut from Discover, the
// installed plugin is not listed and plugin.enable fails; with the Inside
// check cut from loadInside, the escape branch builds outside the
// repository; with the cat-file check cut, the linkdir branch fails on the
// Inside check instead; with the fetched check cut from the failure path,
// install from the lock file removes a checkout it did not fetch.

const probeManifest = `id = "e2e.probe"
name = "Probe"
version = "1.0.0"
min_herdr_version = "0.9.3"

[[build]]
command = ["sh", "-c", "%BUILD%"]

[[actions]]
id = "ping"
title = "Ping"
command = ["sh", "-c", "cat dist/out && echo pong"]
`

// probeRepo makes the bare repository and returns its file:// URL, the
// commit of main, and the folder outside it.
func probeRepo(t *testing.T) (string, string, string) {
	t.Helper()
	work := testutil.GitRepo(t)
	outside := t.TempDir()
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
	write("mkdir -p dist && echo built-by-install > dist/out")
	testutil.Git(t, work, "commit", "-q", "-m", "probe")
	main := testutil.Git(t, work, "rev-parse", "HEAD")
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
	bare := filepath.Join(t.TempDir(), "probe.git")
	testutil.Git(t, work, "clone", "-q", "--bare", work, bare)
	return "file://" + bare, main, outside
}

// TestPluginsInstallFromGit installs the probe from the bare repository with
// a daemon running. The broken branch leaves no lock entry, no checkout and
// no enable. main is fetched at the commit the branch names, built in its
// checkout, pinned in plugins.lock, listed as managed and on, and its action
// prints what the build wrote. The lock file and the list are kept as
// artifacts.
func TestPluginsInstallFromGit(t *testing.T) {
	url, mainCommit, outside := probeRepo(t)
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "probe", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	const id = "e2e.probe"
	lockPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "plugins.lock")
	managed := filepath.Join(xdgDir(base, "XDG_DATA_HOME"), "tuios", "plugins", id)
	art := artifactDir(t)

	// The broken build: refused, and nothing is left behind.
	out, err := tuiosCLI(t, base, "plugins", "install", url+"@broken", "--yes")
	if err == nil || !strings.Contains(out, "the build command failed") || !strings.Contains(out, "The plugin is not installed") {
		t.Fatalf("install of the broken branch: %v\n%s", err, out)
	}
	if data, err := os.ReadFile(lockPath); err == nil && strings.Contains(string(data), id) {
		t.Fatalf("a failed build wrote a lock entry:\n%s", data)
	}
	if ents, _ := os.ReadDir(managed); len(ents) != 0 {
		t.Fatalf("a failed build left %d folders in %s", len(ents), managed)
	}
	if cfg, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")); strings.Contains(string(cfg), id) {
		t.Fatalf("a failed build enabled the plugin:\n%s", cfg)
	}
	if log, _ := os.ReadFile(filepath.Join(pluginState(base, id), "build.log")); !strings.Contains(string(log), "the build breaks") {
		t.Errorf("build.log does not hold the failed build's output:\n%s", log)
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
		if ents, _ := os.ReadDir(managed); len(ents) != 0 {
			t.Fatalf("install of %s left %d folders in %s", c.name, len(ents), managed)
		}
	}

	// main without a build: the checkout is there and the lock entry says
	// it is not built. Install from the lock file has no terminal to ask in
	// and must leave the checkout it did not fetch.
	if out, err := tuiosCLI(t, base, "plugins", "install", url+"@main", "--yes", "--no-build", "--no-enable"); err != nil {
		t.Fatalf("install of main without a build: %v\n%s", err, out)
	}
	checkout := filepath.Join(managed, mainCommit[:12])
	if out, err := tuiosCLI(t, base, "plugins", "install"); err == nil || !strings.Contains(out, "no terminal") {
		t.Fatalf("install from the lock file without a terminal: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(checkout, "herdr-plugin.toml")); err != nil {
		t.Fatalf("install from the lock file removed the checkout it did not fetch: %v", err)
	}

	// main: built beside that checkout, swapped in, pinned and enabled.
	out, err = tuiosCLI(t, base, "plugins", "install", url+"@main", "--yes")
	if err != nil {
		t.Fatalf("install of main: %v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(art, "install.txt"), []byte(out), 0o644)
	for _, want := range []string{"Install e2e.probe 1.0.0", "mkdir -p dist", "actions  ping", "tuios enables the plugin", "Installed e2e.probe at " + mainCommit[:12], "is enabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("the install output lacks %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("no lock file: %v", err)
	}
	_ = os.WriteFile(filepath.Join(art, "plugins.lock"), data, 0o644)
	// The lock file is TOML. Each key is read off its own line, so the test
	// needs no TOML parser of its own.
	field := func(key string) string {
		m := regexp.MustCompile(`(?m)^` + key + ` = ['"]?([^'"\n]*)['"]?$`).FindStringSubmatch(string(data))
		if m == nil {
			return ""
		}
		return m[1]
	}
	if n := strings.Count(string(data), "[[plugin]]"); n != 1 {
		t.Fatalf("plugins.lock has %d entries, want 1:\n%s", n, data)
	}
	if field("version") != "1" || field("id") != id || field("format") != "herdr" || field("source") != url || field("ref") != "main" ||
		field("commit") != mainCommit || len(field("tree")) != 40 || !strings.HasPrefix(field("approved"), "sha256:") || field("built") != "true" {
		t.Fatalf("plugins.lock does not pin main at %s:\n%s", mainCommit, data)
	}
	built, err := os.ReadFile(filepath.Join(managed, mainCommit[:12], "dist", "out"))
	if err != nil || strings.TrimSpace(string(built)) != "built-by-install" {
		t.Fatalf("the build did not run in the checkout: %v %q", err, built)
	}

	out, err = tuiosCLI(t, base, "plugins", "list", "--json")
	if err != nil {
		t.Fatalf("plugins list: %v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(art, "list.json"), []byte(out), 0o644)
	var entries []struct {
		ID      string          `json:"plugin_id"`
		Origin  string          `json:"origin"`
		Enabled bool            `json:"enabled"`
		Err     json.RawMessage `json:"error"`
		Lock    *struct {
			Commit string `json:"commit"`
		} `json:"lock"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("plugins list --json: %v\n%s", err, out)
	}
	if len(entries) != 1 || entries[0].ID != id || entries[0].Origin != "managed" || !entries[0].Enabled ||
		entries[0].Err != nil || entries[0].Lock == nil || entries[0].Lock.Commit != mainCommit {
		t.Fatalf("plugins list does not show the probe managed and on:\n%s", out)
	}
	if table, _ := tuiosCLI(t, base, "plugins", "list"); !strings.Contains(table, "managed") || !strings.Contains(table, mainCommit[:12]) {
		t.Errorf("the plugins list table lacks the source and the pin:\n%s", table)
	}

	out, err = tuiosCLI(t, base, "plugins", "run", id, "ping", "--wait")
	if err != nil || !strings.Contains(out, "built-by-install") || !strings.Contains(out, "pong") {
		t.Fatalf("plugins run ping: %v\n%s", err, out)
	}
}
