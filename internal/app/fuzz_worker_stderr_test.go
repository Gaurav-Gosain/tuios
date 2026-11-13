package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Go's fuzz coordinator starts each worker with its stderr on /dev/null. A
// worker that panics on a goroutine of its own, or stops on a fatal runtime
// error such as a concurrent map write, exits with status 2, and the run
// reports only "fuzzing process hung or terminated unexpectedly: exit status
// 2" with no stack. The input it writes does not always reproduce it, because
// the worker ran many inputs before that one. So each worker sends its stderr
// to a file named for the coordinator, and the coordinator prints what the
// files hold when FuzzModel ends.

// fuzzWorkerDir is where the workers of the coordinator with process id pid
// keep their stderr.
func fuzzWorkerDir(pid int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("tuios-fuzzworkers-%d", pid))
}

// The directory is computed before TestMain, because RunIsolated may move the
// temporary directory, and the coordinator must read the folder the workers
// wrote.
var fuzzWorkerStderrDir = fuzzWorkerDir(os.Getpid())

func init() {
	if !slices.Contains(os.Args[1:], "-test.fuzzworker") {
		return
	}
	dir := fuzzWorkerDir(os.Getppid())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.Create(filepath.Join(dir, fmt.Sprintf("worker-%d.log", os.Getpid())))
	if err != nil {
		return
	}
	redirectStderr(f)
}

// reportFuzzWorkerStderr logs what the workers of this coordinator wrote on
// stderr, and removes their files. A worker that ended normally wrote nothing.
func reportFuzzWorkerStderr(tb testing.TB) {
	tb.Helper()
	entries, err := os.ReadDir(fuzzWorkerStderrDir)
	if err != nil {
		return
	}
	defer func() { _ = os.RemoveAll(fuzzWorkerStderrDir) }()
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(fuzzWorkerStderrDir, e.Name()))
		if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		tb.Logf("fuzz worker %s wrote on stderr:\n%s", strings.TrimSuffix(e.Name(), ".log"), raw)
	}
}
