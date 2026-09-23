// installed by tuios
// managed by tuios; `tuios integration install amp` overwrites this file and
// `tuios integration uninstall amp` removes it. Put your own plugins beside it
// instead of editing it.
// TUIOS_INTEGRATION_ID=amp
// TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__
//
// Reports Amp's turns to the tuios pane it runs in, through
// `tuios agent-hook amp`: a prompt starts work, the end of a turn says whether
// it finished, failed or was cancelled, and a new thread names itself. It
// subscribes to nothing that decides anything, tool.call above all, so it
// cannot change what Amp permits. See https://ampcode.com/manual/plugin-api.

import { spawn } from "node:child_process";

export const description = "Report Amp's turns to the tuios pane it runs in";

const TUIOS = __TUIOS_COMMAND__;

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

export default function (amp: any) {
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
}
