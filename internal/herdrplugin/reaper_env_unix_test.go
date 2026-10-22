//go:build unix

package herdrplugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestReaperKeepsTheCommandsGodebug: the reaper starts with
// GODEBUG=disablethp=1 for its own memory, and the plugin command must not
// inherit it. A GODEBUG the person set reaches the command as it was. This
// guards the environment of every plugin command, so it is kept as a unit
// test beside the reaper it covers.
func TestReaperKeepsTheCommandsGodebug(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, env, want string
	}{
		{"unset", "", "unset"},
		{"the person's", "GODEBUG=madvdontneed=1", "set:madvdontneed=1"},
		{"the person's disablethp", "GODEBUG=disablethp=1", "set:disablethp=1"},
		{"empty", "GODEBUG=", "set:"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := NewRunner()
			r.Reaper = []string{self, ReaperArg}
			dir := t.TempDir()
			out := filepath.Join(dir, "godebug")
			var env []string
			if c.env != "" {
				env = []string{c.env}
			} else if v, ok := os.LookupEnv("GODEBUG"); ok {
				t.Setenv("GODEBUG", v)
				_ = os.Unsetenv("GODEBUG")
			}
			script := `if [ "${GODEBUG+x}" = x ]; then echo "set:$GODEBUG"; else echo unset; fi >` + out + `; sleep 1`
			p := &Plugin{PluginID: "t.env", PluginRoot: dir}
			if _, perr := r.Start(Job{Plugin: p, Command: []string{"sh", "-c", script}, Env: env}); perr != nil {
				t.Fatal(perr)
			}
			defer r.StopPlugin(p.PluginID)
			deadline := time.Now().Add(5 * time.Second)
			for {
				b, _ := os.ReadFile(out)
				if got := strings.TrimSpace(string(b)); strings.HasSuffix(string(b), "\n") {
					if got != c.want {
						t.Errorf("the command saw GODEBUG %q, want %q", got, c.want)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the command never wrote its GODEBUG")
				}
				time.Sleep(20 * time.Millisecond)
			}
			// The reaper itself runs with disablethp=1. It has run the
			// command, so it is past its own exec.
			if runtime.GOOS == "linux" {
				r.mu.Lock()
				var sid int
				for s := range r.owners {
					sid = s
				}
				r.mu.Unlock()
				environ, _ := os.ReadFile("/proc/" + strconv.Itoa(sid) + "/environ")
				var godebug string
				for kv := range strings.SplitSeq(string(environ), "\x00") {
					if v, ok := strings.CutPrefix(kv, "GODEBUG="); ok {
						godebug = v
					}
				}
				if !strings.HasSuffix(godebug, "disablethp=1") {
					t.Errorf("the reaper runs with GODEBUG=%q, want disablethp=1 last", godebug)
				}
			}
		})
	}
}
