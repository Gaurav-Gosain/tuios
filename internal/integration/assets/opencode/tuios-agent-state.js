// installed by tuios
// managed by tuios; `tuios integration install __TUIOS_HARNESS__` overwrites this file
// and `tuios integration uninstall __TUIOS_HARNESS__` removes it. Put your own plugins
// beside it instead of editing it.
// TUIOS_INTEGRATION_ID=__TUIOS_HARNESS__
// TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__
//
// Reports __TUIOS_NAME__'s session state to the tuios pane it runs in, through
// `tuios agent-hook __TUIOS_HARNESS__`. The event names are opencode's bus events
// (https://opencode.ai/docs/plugins/), handled the way herdr's plugin handles
// them. Events from child sessions, the subagents opencode starts, are dropped
// so a subagent finishing cannot mark the pane done mid-turn.

import { spawn } from "node:child_process";

const TUIOS = __TUIOS_COMMAND__;
const children = new Set();

function report(event, sessionID, extra) {
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: sessionID || "",
    ...extra,
  });
  try {
    const child = spawn(TUIOS, ["agent-hook", "__TUIOS_HARNESS__", "--integration", "__TUIOS_VERSION__"], {
      stdio: ["pipe", "ignore", "ignore"],
      windowsHide: true,
    });
    child.on("error", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(payload);
  } catch {
    // A report that cannot be sent must never break opencode.
  }
}

function text(value) {
  return typeof value === "string" ? value : "";
}

export const TuiosAgentState = async () => {
  if (process.env.TUIOS_ENV !== "1" && !process.env.TUIOS_AGENT) {
    return {};
  }
  return {
    "chat.message": async ({ sessionID }) => {
      if (sessionID && children.has(sessionID)) return;
      report("chat.message", sessionID, {});
    },
    "tool.execute.before": async (input) => {
      const sessionID = text(input?.sessionID);
      if (sessionID && children.has(sessionID)) return;
      report("tool.execute.before", sessionID, {});
    },
    event: async ({ event }) => {
      const type = event?.type;
      const props = event?.properties ?? {};
      const info = props.info;
      if (info?.id && info.parentID) children.add(info.id);
      const sessionID = text(props.sessionID) || text(info?.id);
      if (sessionID && children.has(sessionID)) return;
      switch (type) {
        case "session.created":
        case "session.idle":
        case "session.deleted":
        case "permission.replied":
        case "question.replied":
        case "question.rejected":
          report(type, sessionID, {});
          break;
        case "session.status": {
          const status = props.status;
          report(type, sessionID, { status: typeof status === "string" ? status : text(status?.type) });
          break;
        }
        case "permission.asked":
        case "permission.updated":
          report(type, sessionID, { title: text(props.title) || text(props.permission) || text(props.type) });
          break;
        case "question.asked":
          report(type, sessionID, { title: text(props.question) || text(props.title) });
          break;
        case "session.error":
          report(type, sessionID, { error: text(props.error?.name) || text(props.error?.data?.message) });
          break;
        default:
          break;
      }
    },
  };
};
