// plugindriver loads a plugin tuios installed for a harness and stands in for
// that harness's plugin API: it registers the plugin the way the harness
// does, then reads one JSON event per line from stdin and hands it to the
// plugin's handler for that event, the way the harness would.
//
//   node plugindriver.mjs pi|amp|opencode <plugin file>
//
// It prints DRIVER-READY once the plugin is loaded and has registered its
// handlers, and the test waits for that line. Each event line is
// {"type": <event name>, "event": <payload>, "idle": <bool>}; for opencode the
// type is "event" and the payload is the bus event. The API shapes are the
// ones the plugins are written against: Pi's pi.on(name, (event, ctx)) with
// ctx.mode, ctx.isIdle() and ctx.sessionManager
// (packages/coding-agent/src/core/extensions/types.ts), Amp's amp.on(name,
// handler) and amp.configuration.get() (https://ampcode.com/manual/plugin-api),
// and opencode's plugin function returning its hooks
// (https://opencode.ai/docs/plugins/).

import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";

const [harness, file] = process.argv.slice(2);
const mod = await import(pathToFileURL(file).href);
const handlers = new Map();
let idle = true;

let dispatch;
if (harness === "pi") {
  const ctx = {
    mode: "tui",
    isIdle: () => idle,
    sessionManager: { getSessionId: () => "pi-e2e", getSessionFile: () => "" },
  };
  mod.default({ on: (name, fn) => handlers.set(name, fn) });
  dispatch = async (line) => handlers.get(line.type)?.(line.event ?? {}, ctx);
} else if (harness === "amp") {
  await mod.default({
    on: (name, fn) => handlers.set(name, fn),
    configuration: { get: async () => ({}) },
  });
  dispatch = async (line) => handlers.get(line.type)?.(line.event ?? {}, {});
} else if (harness === "opencode") {
  const hooks = await mod.TuiosAgentState({ client: {} });
  dispatch = async (line) => hooks.event?.({ event: line.event });
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
