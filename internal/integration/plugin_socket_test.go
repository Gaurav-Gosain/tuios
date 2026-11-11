package integration

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The plugins send each hook event to the daemon's agent-hook-event verb
// over its socket, and start tuios agent-hook only for a daemon without the
// verb. This is the wire between a plugin and a daemon of either age.
//
// How these could pass wrongly, written down first:
//   - The plugin could send over the socket and start tuios as well. The
//     stand-in tuios records every run, and with a daemon that answers it
//     records none.
//   - The fallback could be untested because the socket path always works.
//     A daemon that answers unknown_verb must get the first event once, and
//     tuios must run for it and for every event after it, with no second
//     connection.
//   - A permission reply could be read from the process path only. The
//     daemon's stdout carries the reply, and the plugin must hand it to
//     opencode's client.

// fakeHookDaemon is a unix socket that answers agent-hook-event lines.
type fakeHookDaemon struct {
	mu    sync.Mutex
	conns int
	calls []map[string]any
	// answer returns the response line for a call.
	answer func(call map[string]any) string
}

func startFakeHookDaemon(t *testing.T, answer func(map[string]any) string) (*fakeHookDaemon, string) {
	t.Helper()
	// Unix socket paths are short on macOS: a test temp folder is too long.
	dir, err := os.MkdirTemp("", "tsk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	d := &fakeHookDaemon{answer: answer}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.conns++
			d.mu.Unlock()
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var call map[string]any
				if json.Unmarshal(line, &call) != nil {
					return
				}
				d.mu.Lock()
				d.calls = append(d.calls, call)
				d.mu.Unlock()
				_, _ = conn.Write([]byte(d.answer(call) + "\n"))
			}()
		}
	}()
	return d, path
}

func (d *fakeHookDaemon) snapshot() (int, []map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns, append([]map[string]any(nil), d.calls...)
}

// runOpenCodePlugin renders the opencode plugin with a recording stand-in
// tuios, runs events through it under node with socket as TUIOS_SOCKET, and
// returns the recording folder and the file the client's permission reply
// is written to.
func runOpenCodePlugin(t *testing.T, socket string, events []map[string]any) (calls, replied string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in tuios is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	if err := os.Mkdir(calls, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "tuios")
	script := "#!/bin/sh\nf=$(mktemp \"" + calls + "/call.XXXXXX\")\n{ printf '%s\\n' \"$*\"; cat; } > \"$f\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tg := mustTarget(t, OpenCode)
	plugin := filepath.Join(dir, "plugin.mjs")
	if err := os.WriteFile(plugin, tg.format.(ownedFile).render(tg, fake), 0o644); err != nil {
		t.Fatal(err)
	}
	replied = filepath.Join(dir, "replied")
	evJSON, _ := json.Marshal(events)
	code := `import { writeFileSync } from "node:fs";
import { TuiosAgentState } from ` + jsString(plugin) + `;
const client = { permission: { reply: async (r) => { writeFileSync(` + jsString(replied) + `, JSON.stringify(r)); } } };
const hooks = await TuiosAgentState({ client });
for (const event of ` + string(evJSON) + `) {
  await hooks.event({ event });
}
// Let the reports queued last go out before the process ends.
await new Promise((r) => setTimeout(r, 1500));
`
	driver := filepath.Join(dir, "drive.mjs")
	if err := os.WriteFile(driver, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, driver)
	cmd.Env = append(os.Environ(), "TUIOS_ENV=1", "TUIOS_SOCKET="+socket, "TUIOS_AGENT=opencode")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return calls, replied
}

var openCodeSocketEvents = []map[string]any{
	{"type": "session.status", "properties": map[string]any{"sessionID": "ses_1", "status": map[string]any{"type": "busy"}}},
	{"type": "permission.asked", "properties": map[string]any{"sessionID": "ses_1", "id": "per_1", "permission": "bash", "title": "run ls"}},
	{"type": "session.status", "properties": map[string]any{"sessionID": "ses_1", "status": map[string]any{"type": "idle"}}},
}

// TestOpenCodePluginReportsOverTheSocket: a daemon with the verb gets every
// event, and no tuios process starts.
func TestOpenCodePluginReportsOverTheSocket(t *testing.T) {
	d, socket := startFakeHookDaemon(t, func(call map[string]any) string {
		params, _ := call["params"].(map[string]any)
		payload, _ := params["payload"].(string)
		if strings.Contains(payload, `"permission.asked"`) {
			return `{"id":1,"result":{"stdout":"{\"reply\":\"once\"}"}}`
		}
		return `{"id":1,"result":{"stdout":""}}`
	})
	calls, replied := runOpenCodePlugin(t, socket, openCodeSocketEvents)

	if got := readCalls(t, calls, "agent-hook"); len(got) != 0 {
		t.Fatalf("tuios agent-hook ran although the daemon answered:\n%s", strings.Join(got, "\n"))
	}
	_, got := d.snapshot()
	var events []string
	for _, call := range got {
		params, _ := call["params"].(map[string]any)
		if call["verb"] != "agent-hook-event" || params["harness"] != "opencode" {
			t.Errorf("call %v, want agent-hook-event for opencode", call)
		}
		if env, _ := params["env"].(map[string]any); env["TUIOS_AGENT"] != "opencode" {
			t.Errorf("call env %v, want TUIOS_AGENT forwarded", params["env"])
		}
		var p map[string]any
		payload, _ := params["payload"].(string)
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatalf("payload %q: %v", payload, err)
		}
		events = append(events, p["hook_event_name"].(string))
	}
	// The ask goes after the report before it; the report after it waits
	// only for the ask's start, so those two may land in either order.
	sorted := slices.Clone(events)
	slices.Sort(sorted)
	if len(events) != 3 || events[0] != "session.status" || strings.Join(sorted, ",") != "permission.asked,session.status,session.status" {
		t.Errorf("events over the socket %v, want session.status first, then permission.asked and session.status", events)
	}
	data, err := os.ReadFile(replied)
	if err != nil || !strings.Contains(string(data), `"reply":"once"`) {
		t.Fatalf("the reply the daemon printed did not reach opencode: %v %s", err, data)
	}
}

// TestOpenCodePluginFallsBackForAnOlderDaemon: a daemon that answers
// unknown_verb gets the first event once, and tuios agent-hook runs for it
// and every event after it.
func TestOpenCodePluginFallsBackForAnOlderDaemon(t *testing.T) {
	d, socket := startFakeHookDaemon(t, func(map[string]any) string {
		return `{"id":1,"error":{"code":"unknown_verb","message":"unknown verb agent-hook-event"}}`
	})
	calls, _ := runOpenCodePlugin(t, socket, openCodeSocketEvents)

	conns, got := d.snapshot()
	if conns != 1 || len(got) != 1 {
		t.Errorf("the older daemon got %d connections and %d calls, want 1 and 1", conns, len(got))
	}
	runs := readCalls(t, calls, "agent-hook")
	if len(runs) != 3 {
		t.Fatalf("tuios agent-hook ran %d times, want 3 (every event):\n%s", len(runs), strings.Join(runs, "\n"))
	}
	count := map[string]int{}
	for _, run := range runs {
		for _, name := range []string{"session.status", "permission.asked"} {
			if strings.Contains(run, `"hook_event_name":"`+name+`"`) {
				count[name]++
			}
		}
	}
	if count["session.status"] != 2 || count["permission.asked"] != 1 {
		t.Errorf("tuios agent-hook ran for %v, want 2 session.status and 1 permission.asked", count)
	}
}
