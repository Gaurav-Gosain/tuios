package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A bare tuios is always the build under test.
//
// Panes type tuios by name (tuios set-agent-state, tuios respond, tuios
// send-agent-message), and so do the scripts stand-in agents run. Each found
// whatever tuios came first on PATH: on a developer's machine the installed
// one, an older build talking to the test daemon; on CI, which has none,
// nothing at all. Tests passed on the one and failed on the other.
//
// So the suite puts a folder holding a tuios link to the build under test
// first on its own PATH, which every daemon, client and pane it starts
// inherits (ownTuiosOnPath), and fails a test that hands a child process a
// PATH on which another tuios comes first (ownTuiosGuard): one that is not the
// build under test and lives outside the temporary folder, as an installed one
// does. A test that swaps
// tuiosBin for another build keeps the link on the suite's build: it runs that
// build by path, never by name.

// ownTuiosDir is the folder ownTuiosOnPath puts first on PATH.
var ownTuiosDir string

// ownTuiosOnPath links tuiosBin as tuios in a folder of its own, puts that
// folder first on the process's PATH and checks a bare tuios finds it. It
// returns a cleanup.
func ownTuiosOnPath() (func(), error) {
	dir, err := os.MkdirTemp("", "tuios-e2e-own")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	link := filepath.Join(dir, "tuios")
	if err := os.Symlink(tuiosBin, link); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		cleanup()
		return nil, err
	}
	ownTuiosDir = dir
	if got, err := exec.LookPath("tuios"); err != nil || got != link {
		cleanup()
		return nil, fmt.Errorf("a bare tuios finds %q, not the build under test at %s", got, link)
	}
	return cleanup, nil
}

// ownTuiosGuard fails t when path, a PATH a test hands a child process, puts
// a tuios ahead of the suite's own that the suite did not build or copy: one
// outside the temporary folder, where every build and copy of this suite
// lives. A PATH with no tuios on it passes: a bare tuios then fails visibly,
// the same on every machine.
func ownTuiosGuard(t failer, path string) {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		tmp = os.TempDir()
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if filepath.Clean(dir) == ownTuiosDir {
			return
		}
		cand := filepath.Join(dir, "tuios")
		st, err := os.Stat(cand)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		real, err := filepath.EvalSymlinks(cand)
		if err != nil {
			real = cand
		}
		if own, err := filepath.EvalSymlinks(tuiosBin); (err == nil && real == own) || strings.HasPrefix(real, tmp+string(filepath.Separator)) {
			return
		}
		t.Fatalf("e2e guard: a test runs with PATH where %s comes before the build under test, so a bare tuios typed into a pane runs that one. Build the PATH from os.Getenv(\"PATH\"), which starts with the suite's own tuios, or leave %s out", cand, dir)
	}
}

// ownTuiosEnvGuard is ownTuiosGuard for the PATH in env, a child's
// environment as KEY=VALUE entries, the last PATH winning.
func ownTuiosEnvGuard(t failer, env []string) {
	t.Helper()
	path, found := "", false
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path, found = v, true
		}
	}
	if found {
		ownTuiosGuard(t, path)
	}
}

// guardRecorder is a failer that records a Fatalf instead of stopping.
type guardRecorder struct{ failed string }

func (r *guardRecorder) Helper() {}
func (r *guardRecorder) Fatalf(format string, args ...any) {
	if r.failed == "" {
		r.failed = fmt.Sprintf(format, args...)
	}
}

// TestABareTuiosInAPaneIsTheBuildUnderTest: a pane's shell and a script a
// stand-in runs both find the build under test as a bare tuios, and the guard
// refuses a PATH where an installed tuios comes first.
//
// How it could pass wrongly, written down first:
//   - the pane could find the build only because the machine has none: the
//     test compares the resolved path with tuiosBin, not that one was found;
//   - the guard could pass everything: a tuios outside the temporary folder,
//     as an installed one is, must be refused, and the suite's own folder and
//     a copy under the temporary folder must not.
//
// NEGATIVE CONTROL: without the ownTuiosOnPath call in runE2E, the pane on a
// machine with tuios installed resolves the installed one, and on CI none.
func TestABareTuiosInAPaneIsTheBuildUnderTest(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "own"
	mustCLI(t, base, "new", sess, "--detach")
	out := filepath.Join(base, "which")
	script := filepath.Join(base, "standin.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nreadlink -f \"$(command -v tuios)\" > \"$1\"\n"), 0o755); err != nil { //nolint:gosec // a pane runs it
		t.Fatal(err)
	}
	mustCLI(t, base, "send-text", "-s", sess, "readlink -f \"$(command -v tuios)\" > "+out+".pane; sh "+script+" "+out+".script\n")
	want, err := filepath.EvalSymlinks(tuiosBin)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{out + ".pane", out + ".script"} {
		got := strings.TrimSpace(waitForFileText(t, f, func(s string) bool { return strings.TrimSpace(s) != "" }, "the pane never said which tuios it runs"))
		if got != want {
			t.Errorf("ASSERTION: a bare tuios in %s resolves to %s, want the build under test %s", filepath.Ext(f), got, want)
		}
	}

	// An installed tuios lives outside the temporary folder: one under the
	// package folder stands in for it.
	installed, err := os.MkdirTemp(".", "installed-tuios-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(installed) })
	installed, _ = filepath.Abs(installed)
	if err := os.WriteFile(filepath.Join(installed, "tuios"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // never run
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(copied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tuiosBin, filepath.Join(copied, "tuios")); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	for _, c := range []struct {
		path   string
		refuse bool
	}{
		{installed + sep + os.Getenv("PATH"), true},
		{ownTuiosDir + sep + installed, false},
		{copied + sep + installed, false},
		{"/nonexistent" + sep + "/also-not-there", false},
	} {
		r := &guardRecorder{}
		ownTuiosGuard(r, c.path)
		if (r.failed != "") != c.refuse {
			t.Errorf("ASSERTION: the guard on PATH %q refused=%v, want %v (%s)", c.path, r.failed != "", c.refuse, r.failed)
		}
	}
}
