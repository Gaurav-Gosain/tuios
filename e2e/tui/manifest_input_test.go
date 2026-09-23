package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The manifest engine end to end, through the binary a person runs: a harness
// manifest dropped in the user manifest directory, a fake agent on PATH that
// the manifest recognises, and fan typing a prompt into it. The manifest
// proves the agent idle with a rule reading only the last line of the screen,
// and says the agent submits on a line feed. The fake agent reads its input
// byte by byte with the terminal's CR to LF translation off, so the bytes it
// prints are the bytes tuios wrote.
//
// Negative control: against a binary without the engine, the loader refuses
// the manifest's bottom_non_empty_lines region, fan refuses the agent name, and
// the fan step fails. A binary with the region but without the [input] block
// submits with a carriage return, so the pane shows B:0d and never B:0a.

const fakeAgentManifest = `schema_version = 1
id = "tuiosfakeagent"
[detect]
comm  = ["tuiosfakeagent"]
argv0 = ["tuiosfakeagent"]
[screen]
enabled = true
lines   = 4
[[screen.rule]]
state    = "idle"
priority = 1
region   = "bottom_non_empty_lines(1)"
regex    = ['^fakeagent ready>$']
[input]
submit = "lf"
source = "e2e"
`

// fakeAgentScript turns bracketed paste on, draws its prompt, and prints every
// byte it reads as B:<hex>.
const fakeAgentScript = `#!/bin/sh
stty -icanon -icrnl -echo min 1
printf '\033[?2004h'
printf 'fakeagent ready>\n'
while :; do
  b=$(dd bs=1 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
  [ -n "$b" ] && printf 'B:%s\n' "$b"
done
`

func TestUserManifestInputReachesTheAgent(t *testing.T) {
	base, repo := fanFixture(t)
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tuiosfakeagent"), []byte(fakeAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	manifests := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "harnesses")
	if err := os.MkdirAll(manifests, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifests, "tuiosfakeagent.toml"), []byte(fakeAgentManifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "doctor", "agents"); err != nil || !strings.Contains(out, "Manifest tuiosfakeagent is loaded from") {
		t.Fatalf("doctor agents does not list the user manifest: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "1", "--agent", "tuiosfakeagent", "--repo", repo, "--name", "try/input", "hi"); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}

	var pane string
	deadline := time.Now().Add(45 * time.Second)
	for {
		out, err := tuiosCLI(t, base, "capture-pane", "-s", "repo-try-input")
		if err == nil {
			pane = out
			if strings.Contains(pane, "B:0a") || strings.Contains(pane, "B:0d") {
				break
			}
		}
		if time.Now().After(deadline) {
			rows := worktreeRows(t, base, "--group", "try/input")
			t.Fatalf("the agent never received the prompt: %v\n%s", rows, pane)
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Give the rest of the bytes a moment to be printed.
	time.Sleep(time.Second)
	pane, _ = tuiosCLI(t, base, "capture-pane", "-s", "repo-try-input")
	for _, want := range []string{"B:1b", "B:68", "B:69", "B:0a"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the agent did not read %s:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "B:0d") {
		t.Errorf("the prompt was submitted with a carriage return, not the line feed the manifest names:\n%s", pane)
	}
}
