// Wire fixtures from OpenCode v2.0.23's public event and CLI plugin contracts.
// Failures this exercises: missing default export, server-side pane reporting,
// V1 event envelopes, lost session IDs on replies, unrelated-session events,
// and listeners/replies surviving unload.
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { readdir, readFile } from "node:fs/promises";
import childProcess from "node:child_process";
import { syncBuiltinESMExports } from "node:module";

const dir = process.argv[2];
const reports = [];
const spawn = childProcess.spawn;
childProcess.spawn = (...args) => {
  const child = spawn(...args);
  const end = child.stdin.end;
  child.stdin.end = function(input, ...rest) {
    reports.push(JSON.parse(input));
    return end.call(this, input, ...rest);
  };
  return child;
};
syncBuiltinESMExports();
const { flush } = await import(pathToFileURL(join(dir, "node_modules/solid-js/index.js")));
async function until(predicate) {
  for (let i = 0; i < 200; i++) {
    if (await predicate()) return;
    await delay(10);
  }
  assert.fail("timed out waiting for plugin reports");
}
const legacy = await import(pathToFileURL(join(dir, "plugins/tuios-agent-state.js")));
assert.equal(typeof legacy.default?.id, "string", "V2 requires a default definition");
assert.equal(typeof legacy.default.setup, "function");
assert.equal(legacy.default.server, legacy.TuiosAgentState, "V1 still uses its own hooks");
await legacy.default.setup({});
const mod = await import(pathToFileURL(join(dir, "plugins/tuios-agent-state/tui.js")));
let listener;
let stopped = false;
const replies = [];
let route = { type: "session", sessionID: "ses_main" };
let pending = [{ id: "frm_existing", sessionID: "ses_main", title: "Already waiting" }];
let status = "idle";
const ctx = {
  ui: { router: { current: () => route } },
  data: {
    listen(fn) { listener = fn; return () => { stopped = true; }; },
    session: {
      get: (id) => ({ id, model: { id: "test-model" }, cost: 0.25 }),
      sync: async () => {},
      status: () => status,
      permission: { list: () => [], sync: async () => {} },
      form: { list: () => pending, sync: async () => {} },
      message: {
        get: () => ({ type: "assistant", content: [
          { type: "tool", id: "call_read", name: "read", state: { status: "running", input: { path: "/project/README.md" } } },
        ] }),
      },
    },
  },
  client: { permission: { reply: async (input) => { replies.push(input); } } },
};
const stop = await mod.default.setup(ctx);
assert.equal(typeof listener, "function");
await until(() => reports.some((r) => r.hook_event_name === "question.asked" && r.title === "Already waiting"));
pending = [];
// The client's active-session snapshot hydrates separately from session data.
status = "running";
flush();
await until(() => reports.some((r) => r.session_id === "ses_main" && r.status === "busy"));
route = { type: "home" };
flush();
await until(() => reports.some((r) => r.session_id === "ses_main" && r.hook_event_name === "session.deleted"));
status = "idle";
route = { type: "session", sessionID: "ses_switched" };
flush();
await until(() => reports.some((r) => r.session_id === "ses_switched" && r.hook_event_name === "session.created"));
route = { type: "session", sessionID: "ses_main" };
flush();
await delay(50);
reports.length = 0;
// OpenCode delivers a batch without awaiting individual listeners. A previous
// turn's terminal event must not overtake the next turn's busy event.
listener({ details: { type: "session.execution.interrupted", data: { sessionID: "ses_main" } } });
listener({ details: { type: "session.execution.started", data: { sessionID: "ses_main" } } });
await until(() => reports.filter((r) => r.hook_event_name === "session.status").length === 2);
assert.deepEqual(reports.filter((r) => r.hook_event_name === "session.status").map((r) => r.status), ["idle", "busy"]);
for (const event of [
  { type: "session.status", data: { sessionID: "ses_main", status: { type: "busy" } } },
  { type: "session.usage.updated", data: { sessionID: "ses_main", cost: 0.5 } },
  { type: "session.usage.updated", data: { sessionID: "ses_main", cost: 0.75 } },
  { type: "session.execution.succeeded", data: { sessionID: "ses_main" } },
]) await listener({ details: event });
await until(async () => {
  const calls = await Promise.all((await readdir(join(dir, "calls"))).map((name) => readFile(join(dir, "calls", name), "utf8")));
  return calls.some((call) => call.includes("--turn-end") && call.includes('"cost":0.75'));
});
const calls = await Promise.all((await readdir(join(dir, "calls"))).map((name) => readFile(join(dir, "calls", name), "utf8")));
const totals = calls.filter((call) => call.startsWith("agent-statusline"));
assert(totals.some((call) => call.includes('"cost":0.75')), "V2 usage is a cumulative session total");
assert(totals.some((call) => call.includes("--turn-end") && call.includes('"cost":0.75')), "turn-end flushes the latest total");
assert(!totals.some((call) => call.includes('"cost":1.25')), "do not sum successive session totals");
const event = { type: "permission.asked", data: {
  id: "per_read", sessionID: "ses_main", action: "read", resources: ["/project/README.md"],
  source: { type: "tool", messageID: "msg_test", id: "call_read" },
} };
const batchStart = reports.length;
listener({ details: { ...event, data: { ...event.data, id: "per_batched" } } });
listener({ details: { type: "permission.replied", data: { sessionID: "ses_main", requestID: "per_batched", reply: "once" } } });
await until(() => reports.slice(batchStart).filter((r) => r.hook_event_name?.startsWith("permission.")).length === 2);
assert.deepEqual(reports.slice(batchStart).filter((r) => r.hook_event_name?.startsWith("permission.")).map((r) => r.hook_event_name), ["permission.asked", "permission.replied"]);
await listener({ details: { ...event, data: { ...event.data, sessionID: "ses_other" } } });
await delay(50);
assert.equal(replies.length, 0, "another pane's permission must not be answered");
await listener({ details: event });
await until(() => replies.length > 0);
assert.deepEqual(replies, [{ sessionID: "ses_main", requestID: "per_read", reply: "once" }]);
await listener({ details: { ...event, data: { ...event.data, id: "per_disposed" } } });
stop();
assert.equal(stopped, true, "unload must unsubscribe");
await delay(100);
assert.equal(replies.length, 1, "unload must not send an outstanding answer");
