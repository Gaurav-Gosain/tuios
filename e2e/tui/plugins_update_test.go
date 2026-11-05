package tuie2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// tuios plugins update, offline, against a bare repository over file://. The
// plugin is in the folder plug, so a commit outside it is not an update. Its
// action prints the file out.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "tuios plugins update".

const updateManifest = `id = "e2e.up"
name = "Update probe"
version = "1.0.0"
min_herdr_version = "0.9.3"

[[actions]]
id = "say"
title = "Say"
command = ["cat", "out"]
`

func TestPluginsUpdate(t *testing.T) {
	const id = "e2e.up"
	work := testutil.GitRepo(t)
	plug := filepath.Join(work, "plug")
	if err := os.MkdirAll(plug, 0o755); err != nil {
		t.Fatal(err)
	}
	commit := func(msg string, files map[string]string) string {
		t.Helper()
		writeFiles(t, plug, files)
		testutil.Git(t, work, "add", "-A")
		testutil.Git(t, work, "commit", "-q", "-m", msg)
		return testutil.Git(t, work, "rev-parse", "HEAD")
	}
	first := commit("first", map[string]string{"herdr-plugin.toml": updateManifest, "out": "one\n"})
	testutil.Git(t, work, "tag", "v1")
	bare := filepath.Join(t.TempDir(), "up.git")
	testutil.Git(t, work, "clone", "-q", "--bare", work, bare)
	testutil.Git(t, work, "remote", "add", "up", bare)
	push := func(args ...string) {
		t.Helper()
		testutil.Git(t, work, append([]string{"push", "-q", "up"}, args...)...)
	}
	branch := testutil.Git(t, work, "rev-parse", "--abbrev-ref", "HEAD")
	url := "file://" + bare

	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "up", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	lockPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "plugins.lock")
	pin := func() string { b, _ := os.ReadFile(lockPath); return string(b) }
	say := func(want string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "plugins", "run", id, "say", "--wait"); err != nil || !strings.Contains(out, want) {
			t.Fatalf("the action: %v, want %q\n%s", err, want, out)
		}
	}
	update := func(args ...string) (string, error) {
		t.Helper()
		return tuiosCLI(t, base, append([]string{"plugins", "update"}, args...)...)
	}
	art := artifactDir(t)
	if out, err := tuiosCLI(t, base, "plugins", "install", url+"@"+branch, "--subdir", "plug", "--yes"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	say("one")

	// Nothing new.
	if out, err := update("--yes"); err != nil || !strings.Contains(out, id+" is up to date") {
		t.Fatalf("update with nothing new: %v\n%s", err, out)
	}

	// A commit outside the plugin's folder is not an update.
	if err := os.WriteFile(filepath.Join(work, "NOTES"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "-q", "-m", "not the plugin")
	push(branch)
	if out, err := update(id, "--yes"); err != nil || !strings.Contains(out, "is up to date") {
		t.Fatalf("update after a commit outside the folder: %v\n%s", err, out)
	}

	// install from the same source and ref names update.
	if out, err := tuiosCLI(t, base, "plugins", "install", url+"@"+branch, "--subdir", "plug", "--yes"); err == nil || !strings.Contains(out, "tuios plugins update "+id) {
		// The tree is the same, but the commit moved.
		t.Errorf("install of an installed plugin: %v, want the update command\n%s", err, out)
	}

	// New code, with a subject that holds an escape. --check changes nothing.
	second := commit("second \x1b[31mred", map[string]string{"out": "two\n"})
	push(branch)
	before := pin()
	out, err := update("--check")
	_ = os.WriteFile(filepath.Join(art, "check.txt"), []byte(out), 0o644)
	if err != nil || !strings.Contains(out, second[:7]) || !strings.Contains(out, "second") {
		t.Fatalf("update --check: %v\n%s", err, out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("a commit subject reached the terminal with its escape:\n%q", out)
	}
	if pin() != before {
		t.Fatal("update --check changed plugins.lock")
	}
	say("one")
	out, err = update(id, "--yes")
	_ = os.WriteFile(filepath.Join(art, "update.txt"), []byte(out), 0o644)
	if err != nil || !strings.Contains(out, "Updated "+id) || !strings.Contains(out, "runs from the new folder") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(pin(), second) {
		t.Fatalf("plugins.lock does not pin the new commit:\n%s", pin())
	}
	say("two")

	// A change in what runs: --yes is not enough, and with no terminal
	// nothing changes. In a terminal the lines show, and yes moves the pin.
	third := commit("third", map[string]string{
		"herdr-plugin.toml": strings.ReplaceAll(updateManifest, `["cat", "out"]`, `["sh", "-c", "cat out; echo extra"]`),
	})
	push(branch)
	before = pin()
	out, err = update(id, "--yes")
	if err == nil || !strings.Contains(out, "What the plugin runs or asks for changes") || !strings.Contains(out, "+ action   say: sh -c") || !strings.Contains(out, "- action   say: cat out") {
		t.Fatalf("update with a trust change under --yes: %v\n%s", err, out)
	}
	if pin() != before {
		t.Fatal("a trust change moved the pin with no answer")
	}
	env := make([]string, 0, len(xdgKeys))
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	term, err := tuitest.Start([]string{tuiosBin, "plugins", "update", id, "--yes"}, tuitest.WithInheritEnv(), tuitest.WithEnv(env...))
	if err != nil {
		t.Fatalf("start update in a terminal: %v", err)
	}
	defer term.Close()
	if err := term.WaitForText("Continue?", 30*time.Second); err != nil {
		t.Fatalf("update with a trust change did not ask in a terminal, also with --yes: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("y\r"); err != nil {
		t.Fatal(err)
	}
	waitFile(t, lockPath, func(s string) bool { return strings.Contains(s, third) }, "the pin of the approved update")

	// A force-push asks again, also with --yes, though the manifest is the
	// one approved.
	testutil.Git(t, work, "reset", "-q", "--hard", third+"~1")
	rewritten := commit("rewritten", map[string]string{
		"herdr-plugin.toml": strings.ReplaceAll(updateManifest, `["cat", "out"]`, `["sh", "-c", "cat out; echo extra"]`),
		"out":               "rewritten\n",
	})
	push("--force", branch)
	before = pin()
	out, err = update(id, "--yes")
	if err == nil || !strings.Contains(out, "history of this source was rewritten") || strings.Contains(pin(), rewritten) {
		t.Fatalf("update after a force-push under --yes: %v\n%s", err, out)
	}
	if pin() != before {
		t.Fatal("a force-push moved the pin with no answer")
	}
	if out, err := tuiosCLI(t, base, "plugins", "uninstall", id, "--yes"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}

	// A moved tag asks again, also when the new commit is a descendant.
	if out, err := tuiosCLI(t, base, "plugins", "install", url+"@v1", "--subdir", "plug", "--yes"); err != nil {
		t.Fatalf("install of the tag: %v\n%s", err, out)
	}
	if !strings.Contains(pin(), first) {
		t.Fatalf("the tag does not pin the first commit:\n%s", pin())
	}
	testutil.Git(t, work, "tag", "-f", "v1", rewritten)
	push("--force", "v1")
	before = pin()
	if out, err := update("--yes"); err == nil || !strings.Contains(out, "The tag v1 now points to a different commit") || pin() != before {
		t.Fatalf("update of a moved tag under --yes: %v\n%s", err, out)
	}

	// A tag that is gone, with a branch of its name on a descendant that
	// keeps the approved manifest, asks again too.
	testutil.Git(t, work, "reset", "-q", "--hard", first)
	replaced := commit("replaced", map[string]string{"out": "replaced\n"})
	testutil.Git(t, work, "tag", "-d", "v1")
	push(":refs/tags/v1", replaced+":refs/heads/v1")
	if out, err := update("--yes"); err == nil || !strings.Contains(out, "The tag v1 is gone") || pin() != before {
		t.Fatalf("update of a tag replaced by a branch under --yes: %v\n%s", err, out)
	}

	// A moved tag installed by its full name asks again too.
	if out, err := tuiosCLI(t, base, "plugins", "uninstall", id, "--yes"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	testutil.Git(t, work, "tag", "v1", first)
	push("v1")
	if out, err := tuiosCLI(t, base, "plugins", "install", url+"@refs/tags/v1", "--subdir", "plug", "--yes"); err != nil {
		t.Fatalf("install of the tag by its full name: %v\n%s", err, out)
	}
	if ref := field(pin)("ref"); ref != "refs/tags/v1" || !strings.Contains(pin(), first) {
		t.Fatalf("the tag by its full name does not pin the first commit:\n%s", pin())
	}
	moved := commit("moved", map[string]string{"out": "moved\n"})
	testutil.Git(t, work, "tag", "-f", "v1", moved)
	push("--force", "v1")
	before = pin()
	if out, err := update("--yes"); err == nil || !strings.Contains(out, "now points to a different commit") || pin() != before {
		t.Fatalf("update of a moved tag by its full name under --yes: %v\n%s", err, out)
	}

	// Offline: exit 3, and the pin stays.
	if err := os.Rename(bare, bare+".gone"); err != nil {
		t.Fatal(err)
	}
	out, err = update("--yes")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || !strings.Contains(out, "stays at "+first[:12]) || pin() != before {
		t.Fatalf("update with the source gone: %v, want exit 3\n%s", err, out)
	}
}
