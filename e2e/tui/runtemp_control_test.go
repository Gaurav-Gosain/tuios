package tuie2e

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRunTempLeak is the leak the guard has to catch. It does nothing unless
// TUIOS_E2E_LEAK_CONTROL is set, which only TestRunTempGuard does, in a child
// run of this binary: "leak" leaves a folder in the temp dir, "stuck" leaves one
// that cannot be removed.
func TestRunTempLeak(t *testing.T) {
	mode := os.Getenv("TUIOS_E2E_LEAK_CONTROL")
	if mode == "" {
		t.Skip("runs only as the child of TestRunTempGuard")
	}
	dir, err := os.MkdirTemp("", "leaked-on-purpose")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	if mode == "stuck" {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRunTempGuard runs the suite's own binary on TestRunTempLeak and checks the
// run's exit status and what it says. A clean child must pass and leave no folder,
// a leaking child must fail and name its folder, and neither may name a folder of
// this run, which is another run on the same machine to the child.
func TestRunTempGuard(t *testing.T) {
	if runTempRoot == "" {
		t.Fatal("the run has no temp folder, so the guard checks nothing")
	}
	// A folder of this run that the child must not blame or remove.
	other, err := os.MkdirTemp("", "another-runs-folder")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })

	child := func(mode string) (string, int) {
		cmd := exec.Command(os.Args[0], "-test.run", "^TestRunTempLeak$", "-test.count=1")
		cmd.Env = append(os.Environ(), "TUIOS_E2E=1", "TUIOS_E2E_BIN="+tuiosBin, "TUIOS_E2E_LEAK_CONTROL="+mode)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run the child: %v", err)
		}
		return string(out), code
	}
	// The child names its folder in its report; that one is cleaned up, never a glob.
	cleanStuck := func(report string) {
		m := regexp.MustCompile(`/tmp/tuios-e2e-run-\d+`).FindString(report)
		if m == "" {
			return
		}
		_ = filepath.WalkDir(m, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
		_ = os.RemoveAll(m)
	}

	t.Run("clean", func(t *testing.T) {
		out, code := child("")
		if code != 0 || strings.Contains(out, "left") {
			t.Fatalf("a child that leaves nothing exited %d:\n%s", code, out)
		}
	})
	for _, mode := range []string{"leak", "stuck"} {
		t.Run(mode, func(t *testing.T) {
			out, code := child(mode)
			defer cleanStuck(out)
			if code == 0 {
				t.Fatalf("the guard passed a child that left a folder:\n%s", out)
			}
			if !strings.Contains(out, "leaked-on-purpose") {
				t.Fatalf("the guard did not name the leaked folder:\n%s", out)
			}
			if strings.Contains(out, filepath.Base(other)) {
				t.Fatalf("the guard blamed a folder of another run:\n%s", out)
			}
			if _, err := os.Stat(other); err != nil {
				t.Fatalf("the child removed a folder of another run: %v", err)
			}
		})
	}
}
