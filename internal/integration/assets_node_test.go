package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const recorderScript = `#!/usr/bin/env node
import { appendFileSync } from "node:fs";
const argv = process.argv.slice(2).join(" ");
let stdin = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", d => { stdin += d; });
process.stdin.on("end", () => {
  appendFileSync(process.env.TUIOS_TEST_LOG, JSON.stringify({ argv, stdin: stdin.trim() }) + "\n");
  process.exit(0);
});
`

const driverScript = `import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

const [extPath, sessionFile, logPath] = process.argv.slice(2);
const sleep = ms => new Promise(r => setTimeout(r, ms));

const handlers = new Map();
const bus = new Map();
const pi = {
  on(name, fn) { (handlers.get(name) ?? handlers.set(name, []).get(name)).push(fn); },
  events: { on(name, fn) { (bus.get(name) ?? bus.set(name, []).get(name)).push(fn); } },
};
let leaf = "leaf0";
const ctx = {
  mode: "tui",
  isIdle: () => false,
  sessionManager: {
    getSessionId: () => "sess-pi-1",
    getSessionFile: () => sessionFile,
    getLeafId: () => leaf,
  },
};
const fire = (name, event) => handlers.get(name)?.forEach(fn => fn(event, ctx));
const emit = (name, payload) => bus.get(name)?.forEach(fn => fn(payload));

const logLines = () => readFileSync(logPath, "utf8").split("\n").filter(Boolean);
const gaveUp = [];
// A wait that never lands is recorded and the sequences go on: the fixed
// extension waits for nothing, so a gave-up marker is itself a failure the
// Go test reports alongside the assertions it then runs.
async function until(what, fn) {
  for (let i = 0; i < 400; i++) {
    let ok = false;
    try { ok = fn(); } catch {}
    if (ok) return;
    await sleep(25);
  }
  gaveUp.push(what);
  console.error("the harness gave up waiting for " + what);
  try { appendFileSync(logPath, JSON.stringify({ gaveup: what }) + "\n"); } catch {}
}
// Wait until the recorder log has stopped growing, so one sequence's reports
// are all in before the next starts. Reports leave the extension through one
// FIFO queue, so their order in the log is their order of firing.
async function settle() {
  let n = -1;
  for (let i = 0; i < 100; i++) {
    const cur = logLines().length;
    if (cur === n && cur > 0) return;
    n = cur;
    await sleep(50);
  }
}

mkdirSync(dirname(sessionFile), { recursive: true });
writeFileSync(sessionFile, "");
const mod = await import(extPath);
mod.default(pi);
fire("session_start", {});
await until("the session_start report", () => logLines().some(l => JSON.parse(l).stdin.includes("session_start")));

// 1. A management call ('status' and friends) is not a run: it must draw no
// start and no failure, whatever else it draws.
fire("tool_call", { toolName: "subagent", toolCallId: "c1", input: { action: "status", id: "x" } });
fire("tool_result", { toolName: "subagent", toolCallId: "c1", isError: false, content: [] });
await settle();

// 2. A good background single run: the launch's task text rides the
// async-started the tool call stashed it against.
fire("tool_call", { toolName: "subagent", toolCallId: "c2", input: { task: "T", async: true } });
emit("subagent:async-started", { id: "r1", toolCallId: "c2", agent: "scout", label: "T" });
emit("subagent:async-complete", { id: "r1", success: true });
fire("tool_result", { toolName: "subagent", toolCallId: "c2", isError: false, content: [{ type: "text", text: "ok" }] });
await settle();

// 3. A good background workflow: the launch event names no tool call, so the
// stash stays and the successful tool result must report nothing; the child
// ends with the parent's async-complete, by its agent when no step index
// was given.
fire("tool_call", { toolName: "subagent", toolCallId: "c3", input: { workflow: {}, async: true, task: "W" } });
emit("subagent:async-started", { id: "wf1", label: "wf" });
emit("subagent:child-status", { type: "subagent.child-status", status: "started", childRunId: "ch1", runId: "wf1", agent: "worker", label: "child task" });
emit("subagent:async-complete", { runId: "wf1", results: [{ status: "success", summary: "ok", index: 0 }] });
fire("tool_result", { toolName: "subagent", toolCallId: "c3", isError: false, content: [{ type: "text", text: "ok" }] });
await settle();

// 4. A foreground run (async: false under the default ladder) whose tool
// call id carries a '|', as OpenAI Responses call ids do: the reported id
// must keep to the daemon's alphabet, and the result must end the same row.
fire("tool_call", { toolName: "subagent", toolCallId: "call_abc|item_def", input: { task: "F", async: false } });
fire("tool_result", { toolName: "subagent", toolCallId: "call_abc|item_def", isError: false, content: [{ type: "text", text: "done" }] });
await settle();

// 5. Transcript interleaving: the leaf advances and a subagent report fires
// before the folder watcher's callback for the same append. The subagent
// reports drop their transcript fields and so must not count as a
// selection; the watcher's transcript_update must still be sent.
leaf = "leaf1";
appendFileSync(sessionFile, JSON.stringify({ advanced: true }) + "\n");
emit("subagent:async-started", { id: "sx", agent: "scout", label: "wedged" });
emit("subagent:async-complete", { id: "sx", success: true });
await until("a transcript_update after the wedged subagent reports", () => {
  const payloads = logLines().map(l => JSON.parse(l).stdin);
  const stop = payloads.findIndex(p => p.includes("subagent_stop") && p.includes('"agent_id":"sx"'));
  return stop >= 0 && payloads.slice(stop + 1).some(p => p.includes("transcript_update") && p.includes('"transcript_leaf":"leaf1"'));
});
await settle();
fire("session_shutdown", {});
await sleep(300);
process.exit(0);
`

// TestPiExtensionSubagentEventsInANodeHarness runs the managed pi extension
// (assets/pi/tuios-agent-state.ts) under node against pi-subagents 0.84.0
// event shapes. The e2e feeds `tuios agent-hook pi` by hand and so never
// exercises the TypeScript; this one loads the real file in a fake pi
// harness whose recorder stands in for the tuios command, and asserts on
// what the extension spawns it with. Five behaviours are pinned: a
// management call starts and fails nothing, a good background run starts
// and stops its own row and nothing else, a good background workflow
// reports no row for its tool call and ends its child with the parent's
// result, a foreground id keeps to the daemon's alphabet with the result
// ending the same row, and a subagent report wedged between a leaf advance
// and the watcher's callback does not suppress the parent's
// transcript_update. Set TUIOS_TEST_PI_EXTENSION_SOURCE to the path of
// another source for the extension — the pre-fix asset the negative
// controls in e2e/tui/NEGATIVE_CONTROLS.md were run against, from
// `git show HEAD:internal/integration/assets/pi/tuios-agent-state.ts`.
func TestPiExtensionSubagentEventsInANodeHarness(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH; the pi extension harness needs it to run the TypeScript")
	}
	out, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Skipf("node --version failed: %v", err)
	}
	digits := strings.Split(strings.TrimPrefix(strings.TrimSpace(string(out)), "v"), ".")
	major, _ := strconv.Atoi(digits[0])
	minor := 0
	if len(digits) > 1 {
		minor, _ = strconv.Atoi(digits[1])
	}
	var flags []string
	switch {
	case major > 22 || (major == 22 && minor >= 6):
	case major == 22:
		flags = []string{"--experimental-strip-types"}
	default:
		t.Skipf("node %s predates type stripping (22.6); the harness needs a newer node", strings.TrimSpace(string(out)))
	}

	var source []byte
	if path := os.Getenv("TUIOS_TEST_PI_EXTENSION_SOURCE"); path != "" {
		if source, err = os.ReadFile(path); err != nil {
			t.Fatalf("reading TUIOS_TEST_PI_EXTENSION_SOURCE: %v", err)
		}
	} else {
		source = assets()[piExtensionTemplate]
	}
	dir := t.TempDir()
	recorder := filepath.Join(dir, "recorder.mjs")
	driver := filepath.Join(dir, "driver.mjs")
	ext := filepath.Join(dir, "tuios-agent-state.ts")
	sessionFile := filepath.Join(dir, "session", "session.jsonl")
	log := filepath.Join(dir, "reports.jsonl")
	write := func(path string, body []byte, exec bool) {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if exec && runtime.GOOS != "windows" {
			if err := os.Chmod(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(recorder, []byte(recorderScript), true)
	write(driver, []byte(driverScript), false)
	write(ext, renderSource(source, piTargetOf(), recorder), false)

	cmd := exec.Command(node, append(flags, driver, ext, sessionFile, log)...)
	cmd.Env = append(os.Environ(),
		"TUIOS_ENV=1",
		"PI_CODING_AGENT_DIR="+filepath.Join(dir, "no-pi-agent"),
		"TUIOS_TEST_LOG="+log,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the node harness failed: %v\n%s", err, out)
	}

	lines := strings.Split(strings.TrimRight(readOrFatal(t, log), "\n"), "\n")
	payloads := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		var record struct {
			Argv   string `json:"argv"`
			Stdin  string `json:"stdin"`
			Gaveup string `json:"gaveup"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("recorder line %d does not parse: %v", i+1, err)
		}
		if record.Gaveup != "" {
			t.Errorf("the harness gave up: %s", record.Gaveup)
			continue
		}
		if want := "agent-hook pi --integration " + strconv.Itoa(piTargetOf().Version); record.Argv != want {
			t.Errorf("recorder line %d was spawned with argv %q, want %q", i+1, record.Argv, want)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(record.Stdin), &payload); err != nil {
			t.Fatalf("recorder line %d carries a payload that does not parse: %v", i+1, err)
		}
		payloads = append(payloads, payload)
	}
	event := func(p map[string]any) string { s, _ := p["hook_event_name"].(string); return s }
	agent := func(p map[string]any) string { s, _ := p["agent_id"].(string); return s }
	field := func(p map[string]any, key string) string { s, _ := p[key].(string); return s }
	count := func(name, id string) int {
		n := 0
		for _, p := range payloads {
			if event(p) == name && agent(p) == id {
				n++
			}
		}
		return n
	}
	only := func(name, id string) map[string]any {
		for _, p := range payloads {
			if event(p) == name && agent(p) == id {
				return p
			}
		}
		return nil
	}

	if len(payloads) == 0 {
		t.Fatal("the extension reported nothing at all")
	}
	if first := payloads[0]; event(first) != "session_start" ||
		field(first, "session_id") != "sess-pi-1" || field(first, "transcript_path") != sessionFile {
		t.Errorf("the first report is %s for session %q at %q, want a session_start for sess-pi-1 at the session file",
			event(first), field(first, "session_id"), field(first, "transcript_path"))
	}

	if n := count("subagent_start", "fg:c1"); n != 0 {
		t.Errorf("a subagent management call drew %d start reports, want none", n)
	}
	if n := count("subagent_update", "fg:c1"); n != 0 {
		t.Errorf("a subagent management call drew %d update reports, want none", n)
	}

	for i, p := range payloads {
		if field(p, "outcome") == "failed" {
			t.Errorf("report %d is a %s with outcome failed: no sequence fails a run", i+1, event(p))
		}
	}

	if n := count("subagent_start", "r1"); n != 1 {
		t.Errorf("the background run drew %d start reports, want 1", n)
	} else if start := only("subagent_start", "r1"); field(start, "agent_type") != "scout" || field(start, "text") != "T" {
		t.Errorf("the background run started as type %q text %q, want scout and T", field(start, "agent_type"), field(start, "text"))
	}
	if n := count("subagent_stop", "r1"); n != 1 {
		t.Errorf("the background run drew %d stop reports, want 1", n)
	} else if stop := only("subagent_stop", "r1"); field(stop, "text") != "done" {
		t.Errorf("the background run stopped with text %q, want done", field(stop, "text"))
	}
	if n := count("subagent_start", "fg:c2"); n != 0 {
		t.Errorf("the background run's tool result drew %d start reports for its own call id, want none", n)
	}

	for _, p := range payloads {
		if id := agent(p); id == "fg:c3" || id == "c3" {
			t.Errorf("the workflow's tool call drew a %s report for %q, want none", event(p), id)
		}
	}
	if n := count("subagent_start", "wf1"); n != 1 {
		t.Errorf("the workflow drew %d start reports for the parent run, want 1", n)
	}
	if n := count("subagent_start", "ch1"); n != 1 {
		t.Errorf("the workflow drew %d start reports for its child, want 1", n)
	} else if start := only("subagent_start", "ch1"); field(start, "agent_type") != "worker" || field(start, "text") != "child task" {
		t.Errorf("the workflow's child started as type %q text %q, want worker and child task", field(start, "agent_type"), field(start, "text"))
	}
	if n := count("subagent_stop", "ch1"); n != 1 {
		t.Errorf("the workflow drew %d stop reports for its child, want 1", n)
	} else if stop := only("subagent_stop", "ch1"); field(stop, "text") != "done" {
		t.Errorf("the workflow's child stopped with text %q, want done", field(stop, "text"))
	}
	if n := count("subagent_stop", "wf1"); n != 1 {
		t.Errorf("the workflow drew %d stop reports for the parent run, want 1", n)
	}

	id := "fg:call_abc_item_def"
	if n := count("subagent_start", id); n != 1 {
		t.Errorf("the foreground run drew %d start reports for %q, want 1", n, id)
	}
	if n := count("subagent_stop", id); n != 1 {
		t.Errorf("the foreground run drew %d stop reports for %q, want 1", n, id)
	} else if stop := only("subagent_stop", id); field(stop, "text") != "done" {
		t.Errorf("the foreground run stopped with text %q, want done", field(stop, "text"))
	}
	sane := regexp.MustCompile(`^fg:[A-Za-z0-9_.:@-]+$`)
	for _, p := range payloads {
		if got := agent(p); strings.HasPrefix(got, "fg:") && !sane.MatchString(got) {
			t.Errorf("the foreground id %q does not keep to the daemon's alphabet", got)
		}
	}
	if !ValidSubagentID(id) {
		t.Errorf("the sanitised foreground id %q is not one the daemon keeps", id)
	}

	stop := -1
	for i, p := range payloads {
		if event(p) == "subagent_stop" && agent(p) == "sx" {
			stop = i
		}
	}
	if stop < 0 {
		t.Fatal("the wedged subagent never stopped, so the interleaving was not exercised")
	}
	for _, p := range payloads[stop+1:] {
		if event(p) == "transcript_update" && field(p, "transcript_leaf") == "leaf1" {
			return
		}
	}
	t.Errorf("no transcript_update for leaf1 followed the wedged subagent reports: the watcher's report was suppressed")
}

func piTargetOf() *Target {
	for _, x := range targets {
		if x.ID == Pi {
			return x
		}
	}
	panic("no pi target")
}

func readOrFatal(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}
