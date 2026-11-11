package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExecTimeoutIsAnOption: [daemon] exec_timeout_seconds bounds a command
// run with exec when the caller names no timeout. It was a 30 second literal
// in the daemon, so the file could not change it.
//
// The daemon is the one the config file starts, and the command is one that
// would run far longer than the option allows.
//
// How this could pass wrongly, written down first:
//   - The command could end on its own. sleep 20 runs for 20 seconds, and
//     the test wants it stopped in under 10.
//   - A daemon that ignores the file waits 30 seconds, which is past the 30
//     second wait of tuios call, so the call fails, or past the 10 second
//     bound. Either one fails the test.
//   - The reply could say timed_out for a reason other than the timeout. A
//     command that is stopped by the timeout has no exit status, so the test
//     checks that too.
//
// It leaves the verb's reply and the time it took as JSON under
// TUIOS_E2E_FRAMES.
func TestExecTimeoutIsAnOption(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[daemon]\nexec_timeout_seconds = 2\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-exec-timeout", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}

	start := time.Now()
	res := mustCall(t, base, "exec", map[string]any{"line": "sleep 20"})
	took := time.Since(start)

	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		b, _ := json.MarshalIndent(map[string]any{"reply": res, "took_ms": took.Milliseconds()}, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, "exec-timeout-option.json"), b, 0o644)
	}
	if res["timed_out"] != true {
		t.Fatalf("exec of sleep 20 with exec_timeout_seconds = 2 did not time out: %v", res)
	}
	if res["exit"] != nil {
		t.Errorf("a command the timeout stopped has exit %v, want none", res["exit"])
	}
	if took > 10*time.Second {
		t.Errorf("exec took %s; with exec_timeout_seconds = 2 it should stop in about 2 seconds", took.Round(time.Millisecond))
	}
}
