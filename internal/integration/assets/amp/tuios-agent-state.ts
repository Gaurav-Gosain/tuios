// installed by tuios
// managed by tuios; `tuios integration install amp` overwrites this file and
// `tuios integration uninstall amp` removes it. Put your own plugins beside it
// instead of editing it.
// TUIOS_INTEGRATION_ID=amp
// TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__
//
// Reports Amp's turns to the tuios pane it runs in, through
// `tuios agent-hook amp`: a prompt starts work, the end of a turn says whether
// it finished, failed or was cancelled, and a new thread names itself. A
// question Amp asks the person with its ask_user_choice tool blocks the pane
// until the tool's result comes back. See https://ampcode.com/manual/plugin-api.
//
// Seeing a question start takes a tool.call handler, and every tool.call
// handler must answer allow or reject. The plugin API does not say how the
// answers of several plugins combine, so this plugin only listens there when
// its allow cannot change what Amp permits: when no Amp permission setting is
// in force (without one Amp allows every tool) and no other plugin is
// installed beside it or in the project. Otherwise it leaves tool.call alone,
// and the question is left to the pane's screen rules. tool.result, which it
// always listens to, may answer nothing and so changes nothing.

import { spawn } from "node:child_process";
import { readdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export const description = "Report Amp's turns to the tuios pane it runs in";

const TUIOS = __TUIOS_COMMAND__;
const SELF = "tuios-agent-state.ts";
const QUESTION_TOOL = "ask_user_choice";

function report(event: string, threadID: unknown, extra: Record<string, unknown>) {
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: typeof threadID === "string" ? threadID : "",
    ...extra,
  });
  try {
    const child = spawn(TUIOS, ["agent-hook", "amp", "--integration", "__TUIOS_VERSION__"], {
      stdio: ["pipe", "ignore", "ignore"],
      windowsHide: true,
    });
    child.on("error", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(payload);
  } catch {
    // A report that cannot be sent must never break Amp.
  }
}

// setting reads a setting by its dotted name, whether the configuration
// holds it flat ("amp.permissions") or nested.
function setting(cfg: any, key: string): unknown {
  if (key in cfg) return cfg[key];
  let cur = cfg;
  for (const part of key.split(".")) {
    if (!cur || typeof cur !== "object") return undefined;
    cur = cur[part];
  }
  return cur;
}

// permissionsInForce reports whether a setting that turns Amp's permission
// checks on is set. A configuration it cannot read counts as one.
function permissionsInForce(cfg: unknown): boolean {
  if (!cfg || typeof cfg !== "object") return true;
  return (
    setting(cfg, "amp.permissions") !== undefined ||
    setting(cfg, "amp.guardedFiles.allowlist") !== undefined ||
    setting(cfg, "amp.mcpPermissions") !== undefined ||
    setting(cfg, "amp.dangerouslyAllowAll") === false
  );
}

// otherPlugins reports whether any plugin but this one is installed, in the
// user's plugin directory or the project's. A directory it cannot read has
// none.
function otherPlugins(): boolean {
  const xdg = (process.env.XDG_CONFIG_HOME || "").trim();
  const dirs: Array<[string, boolean]> = [
    [join(xdg || join(homedir(), ".config"), "amp", "plugins"), true],
    [join(process.cwd(), ".amp", "plugins"), false],
  ];
  for (const [dir, mine] of dirs) {
    let names: string[] = [];
    try {
      names = readdirSync(dir);
    } catch {
      continue;
    }
    for (const name of names) {
      if (name.startsWith(".") || (mine && name === SELF)) continue;
      return true;
    }
  }
  return false;
}

function questionOf(input: unknown): string {
  const q = (input as any)?.question;
  return typeof q === "string" ? q : "";
}

export default async function (amp: any) {
  if (process.env.TUIOS_ENV !== "1" && !process.env.TUIOS_AGENT) {
    return;
  }
  amp.on("session.start", (event: any) => {
    report("session.start", event?.thread?.id, {});
  });
  amp.on("agent.start", (event: any) => {
    report("agent.start", event?.thread?.id, {});
    return undefined;
  });
  amp.on("agent.end", (event: any) => {
    report("agent.end", event?.thread?.id, { status: typeof event?.status === "string" ? event.status : "" });
    return undefined;
  });
  amp.on("tool.result", (event: any) => {
    if (event?.tool === QUESTION_TOOL) report("tool.result", event?.thread?.id, { tool: QUESTION_TOOL });
    return undefined;
  });
  let cfg: unknown;
  try {
    cfg = await amp.configuration.get();
  } catch {
    cfg = undefined;
  }
  if (permissionsInForce(cfg) || otherPlugins()) return;
  amp.on("tool.call", (event: any) => {
    if (event?.tool === QUESTION_TOOL) {
      report("tool.call", event?.thread?.id, { tool: QUESTION_TOOL, question: questionOf(event?.input) });
    }
    return { action: "allow" };
  });
}
