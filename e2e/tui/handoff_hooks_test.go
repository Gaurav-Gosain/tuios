package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Hooks across a live handoff. A hook runs as a child of the daemon, and one
// started late in the handoff would still run at the exec: the new image never
// waits for it, and it stays a zombie of the daemon's pid once it exits. So
// every hook fired once the handoff has shut the pipe holds waits on them. A
// put-back starts the waiting ones; the exec ends them unstarted
// (docs/design/live-handoff.md, section 7.3).
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// defunctChildren is every zombie child of pid.
func defunctChildren(t *testing.T, pid int) []string {
	t.Helper()
	var out []string
	for _, c := range childCommands(t, pid) {
		if strings.Contains(c, "<defunct>") {
			out = append(out, c)
		}
	}
	return out
}

// TestHandoffHoldsHooks configures an after-workspace-switch hook that sleeps
// 3 s and then appends to a file, and switches workspace, on a connection
// opened before the handoff, inside a 1.5 s hold between the manifest and the
// exec.
//
//   - "exec": the handoff goes through. Once the hook would have ended, the
//     daemon must have no zombie child, and the hook must not have run.
//   - "put back": the binary is replaced in the hold, so the handoff is put
//     back. The held hook must run then.
//
// How it could pass wrongly: the switch could be answered by the new image,
// whose hook is its own child and is waited for. The test checks the old
// image answered it.
func TestHandoffHoldsHooks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		putBack bool
	}{{"exec", false}, {"put back", true}} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			mark := filepath.Join(base, "hook-ran")
			writeConfig(t, base, "[hooks]\nafter-workspace-switch = \"sleep 3; echo ran >> "+mark+"\"\n")
			startHandoffDaemon(t, base)
			const sess = "e2e-hooks"
			newCommandWindow(t, base, sess, "a", false, "sh", "-c", "exec sleep 600")
			params := map[string]any{"hold_ms": 1500}
			var second string
			if tc.putBack {
				second = copyTuios(t)
				params["binary"] = second
			}

			early := dialEarly(t, base)
			img := helloImage(t, base)
			_ = mustCall(t, base, "test-handoff", params)
			if _, err := early.Call("select-workspace", map[string]any{"session": sess, "workspace": 2}); err != nil {
				t.Fatalf("switch workspace in the hold: %v", err)
			}
			inHold := answeredBy(t, early) == img.Instance
			if tc.putBack {
				replaceWithNonTuios(t, second)
			}
			// The hook takes 3 s from the switch, and a put-back starts it
			// 1.5 s in.
			time.Sleep(5 * time.Second)
			var ran []byte
			for deadline := time.Now().Add(5 * time.Second); tc.putBack && len(ran) == 0 && time.Now().Before(deadline); {
				time.Sleep(100 * time.Millisecond)
				ran, _ = os.ReadFile(mark)
			}
			after := helloImage(t, base)
			zombies := defunctChildren(t, after.PID)
			ran, _ = os.ReadFile(mark)

			writeHandoffArtifact(t, map[string]any{
				"instance_before": img.Instance, "instance_after": after.Instance, "switch_in_hold": inHold,
				"defunct": zombies, "hook_output": string(ran),
			})
			if !inHold {
				t.Fatalf("the switch was answered by the new image, so the test exercised nothing")
			}
			if after.PID != img.PID {
				t.Fatalf("the daemon's pid changed from %d to %d", img.PID, after.PID)
			}
			if tc.putBack {
				if after.Instance != img.Instance {
					t.Fatalf("the handoff went through, so nothing was put back")
				}
				if strings.Count(string(ran), "ran") != 1 {
					t.Errorf("ASSERTION: the hook held during the handoff ran %d times after the put-back, want once", strings.Count(string(ran), "ran"))
				}
				return
			}
			if after.Instance == img.Instance {
				t.Fatalf("the handoff did not go through")
			}
			if len(zombies) > 0 {
				t.Errorf("ASSERTION: the daemon has zombie children after the handoff: %v", zombies)
			}
			if len(ran) > 0 {
				t.Errorf("ASSERTION: a hook fired in the handoff's hold ran across the exec: %q", ran)
			}
		})
	}
}
