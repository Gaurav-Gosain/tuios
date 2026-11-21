package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tuios integration status says what differs when an install is not the one
// this tuios writes, rather than "out of date (v3, this tuios installs v3)",
// which reads as a contradiction and gives the person nothing to act on.
//
// The same version can hold a different install in three ways a person meets:
// the hooks were written where nc or socat was on PATH and status runs where
// neither is (or the other way round), so one side forwards to the daemon and
// the other starts tuios; and an event's hook was removed by hand.
//
// How this could pass wrongly, written down first:
//   - The install could already be current, and the status would say
//     "current" with no difference to name. Each case first checks that the
//     verdict is "out of date".
//   - The verdict could drop the version and still name nothing. Each case
//     needs its own words for what differs.
//   - The PATH without nc could still find one through a later PATH entry.
//     The env sets PATH last, to a folder that holds only sh.
//
// The artifact is integration-status.txt in the test's output folder: each
// case's status output.
func TestIntegrationStatusNamesWhatDiffers(t *testing.T) {
	base := t.TempDir()
	noSocket := filepath.Join(base, "bin-sh-only")
	if err := os.MkdirAll(noSocket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", filepath.Join(noSocket, "sh")); err != nil {
		t.Fatal(err)
	}
	withSocket := os.Getenv("PATH")
	var artifact strings.Builder
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(artifactDir(t), "integration-status.txt"), []byte(artifact.String()), 0o600)
	})
	sameVersion := regexp.MustCompile(`v(\d+), this tuios installs v(\d+)`)

	run := func(home, path string, args ...string) string {
		t.Helper()
		env := []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=", "PATH=" + path}
		out, err := tuiosCLIEnv(t, base, env, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	newHome := func(name string) string {
		t.Helper()
		home := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		return home
	}
	check := func(name, out string, want ...string) {
		t.Helper()
		fmt.Fprintf(&artifact, "== %s\n%s\n", name, out)
		if !strings.Contains(out, "out of date") {
			t.Fatalf("%s: the install should read as out of date:\n%s", name, out)
		}
		if m := sameVersion.FindStringSubmatch(out); m != nil && m[1] == m[2] {
			t.Errorf("%s: the verdict names the same version on both sides and nothing else:\n%s", name, out)
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: the verdict does not say %q:\n%s", name, w, out)
			}
		}
	}
	installedCommand := func(home string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg.Hooks["PreToolUse"][0].Hooks[0].Command
	}

	fwd := newHome("home-forwarder")
	run(fwd, withSocket, "integration", "install", "claude-code", "--command", tuiosBin)
	if !strings.Contains(installedCommand(fwd), "agent-hook-event") {
		t.Skip("no nc -U or socat on this machine, so the install cannot write the forwarder line")
	}
	if out := run(fwd, withSocket, "integration", "status", "claude-code", "--command", tuiosBin); !strings.Contains(out, "current") {
		t.Fatalf("status right after the install is not current:\n%s", out)
	}
	check("forwarder hooks, no nc or socat on PATH",
		run(fwd, noSocket, "integration", "status", "claude-code", "--command", tuiosBin),
		"the hooks send events to the daemon", "no nc or socat")

	plain := newHome("home-plain")
	run(plain, noSocket, "integration", "install", "claude-code", "--command", tuiosBin)
	if strings.Contains(installedCommand(plain), "agent-hook-event") {
		t.Fatalf("an install with no socket tool on PATH wrote the forwarder line")
	}
	check("plain hooks, nc on PATH",
		run(plain, withSocket, "integration", "status", "claude-code", "--command", tuiosBin),
		"the hooks start tuios for each event")

	settings := filepath.Join(fwd, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc["hooks"].(map[string]any), "Stop")
	data, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, data, 0o600); err != nil {
		t.Fatal(err)
	}
	check("Stop hook removed",
		run(fwd, withSocket, "integration", "status", "claude-code", "--command", tuiosBin),
		"no hook for Stop")
}
