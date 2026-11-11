package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The hook command tuios integration installs where the machine has nc -U or
// socat: a line of sh that forwards the event to the daemon's
// agent-hook-event verb instead of starting tuios (R12). It must do for the
// harness exactly what tuios agent-hook does.
//
// Every Claude Code fixture in internal/integration/testdata/hooks runs, in
// order, through both commands: tuios agent-hook (the command an older
// install has) in one pane, and the installed forwarder line in another. After
// each event the two must have printed the same, exited the same, and left
// their panes in the same agent state.
//
// How this could pass wrongly, written down first:
//   - The forwarder line could fall back to tuios agent-hook for every event,
//     and the two would agree trivially. The program installed is a wrapper
//     that records every start of tuios and the pane it ran in: the
//     forwarder's pane may start it only for the fixtures whose payload is
//     empty or not JSON.
//   - The installed command could be the plain one. The test reads it from
//     the settings file the install wrote and needs agent-hook-event in it.
//   - Pane ids differ between the two panes, so output naming the pane is
//     compared with each pane's own id put back as PANE.
//   - Fixtures that name the event on the command line are not what an
//     installed hook runs, so they are left out and listed in the artifact.
//
// The artifact is hook-forwarder.txt in the test's output folder: each
// fixture, both outputs and both states.

// forwarderScript runs one hook command in the pane it is typed into, with
// the fixture's variables, and writes the output and exit status to a file.
const forwarderScript = `#!/bin/sh
( if [ -s "$3" ]; then set -a; . "$3"; set +a; fi; sh "$4" < "$1" ) > "$2.tmp" 2>/dev/null
echo "exit=$?" >> "$2.tmp"
mv "$2.tmp" "$2"
`

type forwarderFixture struct {
	Name    string            `json:"name"`
	Event   string            `json:"event"`
	Env     map[string]string `json:"env"`
	Payload json.RawMessage   `json:"payload"`
	Raw     *string           `json:"raw"`
}

func TestInstalledForwarderMatchesTheHook(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	// The process detector clears a report's claim once the process that
	// reported has gone, and here that is a short-lived sh in each pane. Its
	// poll reaches the two panes at different times, so it is off: only the
	// hook path decides the state.
	cfgPath := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("[daemon]\nagent_autodetect = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const sess = "e2e-fwd"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	for _, name := range []string{"OLD", "NEW"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, name, "--no-focus"); err != nil {
			t.Fatalf("open pane %s: %v\n%s", name, err, out)
		}
	}
	oldWin, newWin := windowID(t, base, sess, "OLD"), windowID(t, base, sess, "NEW")

	// Install the Claude Code integration into a home of its own and read
	// the command it wrote.
	home := filepath.Join(base, "fwd-home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(base, "tuios-starts")
	wrapper := filepath.Join(base, "tuios-wrapper")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\necho \"$TUIOS_PANE_ID\" >> "+starts+"\nexec "+tuiosBin+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLIEnv(t, base, []string{"HOME=" + home}, "integration", "install", "claude-code", "--command", wrapper); err != nil {
		t.Fatalf("integration install claude-code: %v\n%s", err, out)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
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
	if err := json.Unmarshal(settings, &cfg); err != nil {
		t.Fatalf("the settings the install wrote: %v\n%s", err, settings)
	}
	installed := cfg.Hooks["PreToolUse"][0].Hooks[0].Command
	if !strings.Contains(installed, "agent-hook-event") {
		t.Skipf("no nc -U or socat on this machine, so the install wrote the plain command: %s", installed)
	}
	plain := installed[strings.LastIndex(installed, "exec ")+len("exec "):]
	plain = strings.TrimSuffix(plain, "'")

	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	script := write("run.sh", forwarderScript)
	oldCmd := write("old.cmd", plain+"\n")
	newCmd := write("new.cmd", installed+"\n")

	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "integration", "testdata", "hooks", "claude-code.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []forwarderFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}

	var artifact strings.Builder
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(artifactDir(t), "hook-forwarder.txt"), []byte(artifact.String()), 0o600)
	})
	n := 0
	run := func(window, cmdFile, payloadFile, envFile string) string {
		t.Helper()
		n++
		out := filepath.Join(dir, fmt.Sprintf("out-%d", n))
		line := "sh " + script + " " + payloadFile + " " + out + " " + envFile + " " + cmdFile + "\n"
		if o, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", window, line); err != nil {
			t.Fatalf("type into %s: %v\n%s", window, err, o)
		}
		var got string
		waitUntil(t, "the hook in "+window+" finishes", uiTimeout, func() (bool, string) {
			data, err := os.ReadFile(out)
			if err != nil {
				return false, err.Error()
			}
			got = string(data)
			return true, ""
		})
		return got
	}
	normalize := func(s, window string) string {
		s = strings.ReplaceAll(s, window, "PANE")
		return strings.ReplaceAll(s, window[:8], "PANE")
	}
	state := func(window string) agentStateJSON {
		st := readAgentState(t, base, sess, window)
		st.Message = normalize(st.Message, window)
		return st
	}

	ran, wantStarts := 0, 0
	for i, fx := range fixtures {
		if fx.Event != "" {
			fmt.Fprintf(&artifact, "skipped %q: it names the event on the command line\n\n", fx.Name)
			continue
		}
		payload := string(fx.Payload)
		if fx.Raw != nil {
			payload = *fx.Raw
		}
		if strings.TrimSpace(payload) == "" || !json.Valid([]byte(payload)) {
			wantStarts++
		}
		payloadFile := write(fmt.Sprintf("payload-%d.json", i), payload)
		var env strings.Builder
		keys := make([]string, 0, len(fx.Env))
		for k := range fx.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&env, "%s=%s\n", k, fx.Env[k])
		}
		envFile := write(fmt.Sprintf("env-%d", i), env.String())

		oldOut := normalize(run(oldWin, oldCmd, payloadFile, envFile), oldWin)
		newOut := normalize(run(newWin, newCmd, payloadFile, envFile), newWin)
		oldSt, newSt := state(oldWin), state(newWin)
		fmt.Fprintf(&artifact, "%s\n  tuios agent-hook: %q %+v\n  forwarder:        %q %+v\n\n", fx.Name, oldOut, oldSt, newOut, newSt)
		if oldOut != newOut {
			t.Errorf("%s: the forwarder printed %q, tuios agent-hook %q", fx.Name, newOut, oldOut)
		}
		if oldSt != newSt {
			t.Errorf("%s: the forwarder left %+v, tuios agent-hook %+v", fx.Name, newSt, oldSt)
		}
		ran++
	}
	if ran < 40 {
		t.Fatalf("only %d fixtures ran", ran)
	}

	// Every other event went over the socket.
	startsData, _ := os.ReadFile(starts)
	fromNew := strings.Count(string(startsData), newWin)
	fmt.Fprintf(&artifact, "fixtures run: %d, tuios starts from the forwarder's pane: %d (want %d), from the plain hook's pane: %d\n",
		ran, fromNew, wantStarts, strings.Count(string(startsData), oldWin))
	if fromNew != wantStarts {
		t.Errorf("the forwarder's pane started tuios %d times, want %d (only for an empty or broken payload)", fromNew, wantStarts)
	}
}
