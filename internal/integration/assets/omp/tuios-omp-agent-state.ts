// installed by tuios
// managed by tuios; `tuios integration install omp` overwrites this file and
// `tuios integration uninstall omp` removes it. Put your own extensions beside
// it instead of editing it.
// TUIOS_INTEGRATION_ID=omp
// TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__
// @ts-nocheck
//
// OMP loads extensions from its agent directory, not Pi's. Its agent_end
// event marks a settled turn unless an automatic continuation is scheduled;
// tool approval events bracket the native permission dialog. Extension UI
// prompts have no corresponding OMP events and remain in the pane.

import { spawn } from "node:child_process";

const TUIOS = __TUIOS_COMMAND__;

function report(event, ctx, extra = {}) {
  let sessionID = "";
  let sessionFile = "";
  try {
    const id = ctx?.sessionManager?.getSessionId?.();
    if (typeof id === "string") sessionID = id;
  } catch {}
  try {
    const file = ctx?.sessionManager?.getSessionFile?.();
    if (typeof file === "string") sessionFile = file;
  } catch {}
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: sessionID,
    transcript_path: sessionFile,
    ...extra,
  });
  try {
    const child = spawn(TUIOS, ["agent-hook", "omp", "--integration", "__TUIOS_VERSION__"], {
      stdio: ["pipe", "ignore", "ignore"],
      windowsHide: true,
    });
    child.on("error", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(payload);
  } catch {
    // Reporting must not interrupt OMP.
  }
}

export default function (pi) {
  if (process.env.TUIOS_ENV !== "1" && !process.env.TUIOS_AGENT) return;
  const mainTUI = (ctx) => ctx?.mode === "tui" && ctx?.agent?.kind === "main";
  pi.on("session_start", (_event, ctx) => {
    if (mainTUI(ctx)) report("session_start", ctx, { busy: ctx.isIdle() === false });
  });
  pi.on("agent_start", (_event, ctx) => {
    if (mainTUI(ctx)) report("agent_start", ctx);
  });
  pi.on("agent_end", (event, ctx) => {
    if (mainTUI(ctx) && !event.willContinue) report("agent_end", ctx);
  });
  pi.on("tool_approval_requested", (event, ctx) => {
    if (mainTUI(ctx)) report("tool_approval_requested", ctx, {
      tool_name: event.toolName,
      reason: event.reason,
    });
  });
  pi.on("tool_approval_resolved", (_event, ctx) => {
    if (mainTUI(ctx)) report("tool_approval_resolved", ctx);
  });
}
