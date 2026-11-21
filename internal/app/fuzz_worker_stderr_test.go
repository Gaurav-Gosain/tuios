package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fuzzWorkerDir(pid int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("tuios-fuzzworkers-%d", pid))
}

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
