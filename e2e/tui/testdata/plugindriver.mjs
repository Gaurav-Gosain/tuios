// plugindriver loads a plugin tuios installed for a harness and stands in for
// that harness's plugin API: it registers the plugin the way the harness
// does, then reads one JSON event per line from stdin and hands it to the
// plugin's handler for that event, the way the harness would.
//
//   node plugindriver.mjs pi|omp|amp|opencode <plugin file>
//
// It prints DRIVER-READY once the plugin is loaded and has registered its
// handlers, and the test waits for that line. Each event line is
// {"type": <event name>, "event": <payload>, "idle": <bool>}; for opencode the
// type is "event" and the payload is the bus event. The API shapes are the
// ones the plugins are written against: Pi's pi.on(name, (event, ctx)) with
// ctx.mode, ctx.isIdle() and ctx.sessionManager
// (packages/coding-agent/src/core/extensions/types.ts); OMP additionally
// exposes ctx.agent.kind, and the driver accepts agentKind and mode per event;
// Amp's amp.on(name, handler) and amp.configuration.get()
// (https://ampcode.com/manual/plugin-api), and opencode's plugin function
// returning its hooks (https://opencode.ai/docs/plugins/).

import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import assert from "node:assert/strict";

const [harness, file] = process.argv.slice(2);
const mod = await import(pathToFileURL(file).href);
const handlers = new Map();
let idle = true;

let dispatch;
if (harness === "pi" || harness === "omp") {
  const ctx = {
    mode: "tui",
    isIdle: () => idle,
    sessionManager: { getSessionId: () => harness + "-e2e", getSessionFile: () => "" },
  };
  if (harness === "omp") ctx.agent = { kind: "main" };
  mod.default({ on: (name, fn) => handlers.set(name, fn) });
  dispatch = async (line) => {
    if (harness === "omp") {
      ctx.agent.kind = line.agentKind ?? "main";
      ctx.mode = line.mode ?? "tui";
    }
    return handlers.get(line.type)?.(line.event ?? {}, ctx);
  };
} else if (harness === "amp") {
  await mod.default({
    on: (name, fn) => handlers.set(name, fn),
    configuration: { get: async () => ({}) },
  });
  dispatch = async (line) => handlers.get(line.type)?.(line.event ?? {}, {});
} else if (harness === "opencode") {
  const hooks = await mod.TuiosAgentState({ client: {} });
  dispatch = async (line) => hooks.event?.({ event: line.event });
} else if (harness === "opencode-v2") {
  assert.equal(typeof mod.default?.id, "string", "OpenCode V2 requires a default plugin definition");
  assert.equal(typeof mod.default.setup, "function");
  // Server setup must never report using the shared service's pane identity.
  await mod.default.setup({});
  const tui = await import(pathToFileURL(join(dirname(file), "tuios-agent-state", "tui.js")));
  const stop = await tui.default.setup({
    ui: { router: { current: () => ({ type: "session", sessionID: "ses_e2e" }) } },
    data: {
      listen: (handler) => { handlers.set("event", handler); return () => handlers.delete("event"); },
      session: {
        get: (id) => ({ id, ...(id === "ses_child" ? { parentID: "ses_e2e" } : {}) }),
        sync: async () => {},
        status: () => "idle",
        permission: { list: () => [], sync: async () => {} },
        form: { list: () => [], sync: async () => {} },
        message: { get: () => undefined },
      },
    },
    client: {},
  });
  dispatch = async (line) => handlers.get("event")?.({ details: line.event });
  process.on("exit", () => stop?.());
} else {
  throw new Error("no driver for " + harness);
}

console.log("DRIVER-READY");
for await (const text of createInterface({ input: process.stdin })) {
  if (!text.trim()) continue;
  const line = JSON.parse(text);
  if (typeof line.idle === "boolean") idle = line.idle;
  await dispatch(line);
}
