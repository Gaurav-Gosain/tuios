package tuie2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The author tools: new makes a folder that loads, check says what is
// wrong, and dev reloads the folder when a file changes, with one question
// at its start. plugins/template itself passes check.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "Author tools".
func TestPluginsAuthorTools(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	cwd := workDirIn(t, base)
	exitCode := func(err error) int {
		var e *exec.ExitError
		if errors.As(err, &e) {
			return e.ExitCode()
		}
		return 0
	}

	template, _ := filepath.Abs(filepath.Join("..", "..", "plugins", "template"))
	if out, err := tuiosCLI(t, base, "plugins", "check", template); err != nil || !strings.Contains(out, "The plugin loads.") || strings.Contains(out, "warning") {
		t.Fatalf("check of plugins/template: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "new", "weather"); err != nil || !strings.Contains(out, "you.weather") {
		t.Fatalf("new: %v\n%s", err, out)
	}
	dir := filepath.Join(cwd, "weather")
	for _, f := range []string{"tuios-plugin.toml", "hello.sh", "README.md", ".github/workflows/plugin.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("new made no %s: %v", f, err)
		}
	}
	if out, err := tuiosCLI(t, base, "plugins", "check", "weather"); err != nil || !strings.Contains(out, "you.weather 0.1.0") {
		t.Fatalf("check of a new plugin: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "new", "weather"); err == nil || !strings.Contains(out, "exists") {
		t.Errorf("new over a folder: %v\n%s", err, out)
	}

	// check says what is wrong, and warns on an old key.
	manifest := filepath.Join(dir, "tuios-plugin.toml")
	good, _ := os.ReadFile(manifest)
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(manifest, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(strings.Replace(string(good), "hello.sh", "missing.sh", 1))
	if out, err := tuiosCLI(t, base, "plugins", "check", "weather"); exitCode(err) != 1 || !strings.Contains(out, "the script missing.sh is not in the folder") {
		t.Errorf("check of a missing script: %v\n%s", err, out)
	}
	write(strings.Replace(string(good), "api = 1", "schema = 1", 1))
	if out, err := tuiosCLI(t, base, "plugins", "check", "weather"); err != nil || !strings.Contains(out, "warning: schema is an old name") {
		t.Errorf("check of an old key: %v\n%s", err, out)
	}
	write("new_thing = 1\n" + string(good))
	if out, err := tuiosCLI(t, base, "plugins", "check", "weather"); exitCode(err) != 1 || !strings.Contains(out, "unknown key new_thing") {
		t.Errorf("check of an unknown key: %v\n%s", err, out)
	}
	write(string(good))

	// dev asks once, then reloads a changed manifest without a question.
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	env := make([]string, 0, len(xdgKeys))
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	term, err := tuitest.Start([]string{tuiosBin, "plugins", "dev", dir}, tuitest.WithInheritEnv(), tuitest.WithEnv(env...))
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	if err := term.WaitForText("Continue?", 30*time.Second); err != nil {
		t.Fatalf("dev did not ask: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("y\r"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Watching the folder", 30*time.Second); err != nil {
		t.Fatalf("dev did not start watching: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(1100 * time.Millisecond)
	extra := "\n[[process]]\nid = \"second\"\nrun = \"action\"\ntitle = \"Second\"\ncommand = [\"sh\", \"-c\", \"echo second-ran\"]\n"
	write(string(good) + extra)
	if err := term.WaitForText("Installed you.weather", 15*time.Second); err != nil {
		t.Fatalf("dev did not reload a changed manifest: %v\n%s", err, term.Snapshot())
	}
	// The daemon reuses its listing of the plugins for 2 s.
	runSecond := func() (string, error) {
		var out string
		var err error
		for range 30 {
			if out, err = tuiosCLI(t, base, "plugins", "run", "you.weather", "second", "--wait"); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		return out, err
	}
	if out, err := runSecond(); err != nil || !strings.Contains(out, "second-ran") {
		t.Fatalf("the new action after a reload: %v\n%s", err, out)
	}
	// A broken manifest is reported, and the fix is reloaded.
	write("api = 1\n")
	if err := term.WaitForText("The plugin cannot load, and it does not run", 15*time.Second); err != nil {
		t.Errorf("dev did not report a broken manifest: %v\n%s", err, term.Snapshot())
	}
	write(string(good) + extra)
	if out, err := runSecond(); err != nil || !strings.Contains(out, "second-ran") {
		t.Errorf("the fixed folder did not reload: %v\n%s", err, out)
	}
	_ = term.SendKeys("\x03")
}
