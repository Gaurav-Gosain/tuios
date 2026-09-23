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
//
// A permission request is also offered to the tuios Inbox. The hook prints a
// reply only when [agents.approvals] in the tuios config names this harness and
// the person answered it there; this plugin then sends that reply to opencode.
// When the hook prints nothing, which is every other case, nothing is sent and
// opencode's own prompt stands.

import { spawn } from "node:child_process";

const TUIOS = __TUIOS_COMMAND__;
const ARGS = ["agent-hook", "__TUIOS_HARNESS__", "--integration", "__TUIOS_VERSION__"];
// The daemon ends a hold within 300 seconds; this only guards against a hook
// that never exits.
const HOLD_LIMIT_MS = 310000;
const REPLIES = ["once", "always", "reject"];
const children = new Set();

function payloadFor(event, sessionID, extra) {
  return JSON.stringify({
    hook_event_name: event,
    session_id: sessionID || "",
    ...extra,
  });
}

function report(event, sessionID, extra) {
  try {
    const child = spawn(TUIOS, ARGS, {
      stdio: ["pipe", "ignore", "ignore"],
      windowsHide: true,
    });
    child.on("error", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(payloadFor(event, sessionID, extra));
  } catch {
    // A report that cannot be sent must never break opencode.
  }
}

// ask runs the hook for a permission request and resolves to the reply it
// printed, or null for anything else: no output, output that is not a reply,
// an error, or a hook that outlived HOLD_LIMIT_MS.
function ask(event, sessionID, extra) {
  return new Promise((resolve) => {
    let out = "";
    let settled = false;
    let timer = null;
    const done = (value) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      resolve(value);
    };
    let child;
    try {
      child = spawn(TUIOS, ARGS, {
        stdio: ["pipe", "pipe", "ignore"],
        windowsHide: true,
      });
    } catch {
      done(null);
      return;
    }
    timer = setTimeout(() => {
      try {
        child.kill();
      } catch {}
      done(null);
    }, HOLD_LIMIT_MS);
    child.on("error", () => done(null));
    child.stdin.on("error", () => {});
    child.stdout.on("error", () => {});
    child.stdout.on("data", (chunk) => {
      if (out.length < 65536) out += chunk;
    });
    child.on("close", () => {
      try {
        const value = JSON.parse(out.trim());
        if (value && REPLIES.includes(value.reply)) {
          done(value);
          return;
        }
      } catch {}
      done(null);
    });
    child.stdin.end(payloadFor(event, sessionID, extra));
  });
}

// reply sends the person's answer to opencode: the permission reply route of
// opencode 1.x, then the SDK method, then the older per-session route.
async function reply(client, sessionID, requestID, answer) {
  const body = answer.message ? { reply: answer.reply, message: answer.message } : { reply: answer.reply };
  const raw = client?._client || client?.client;
  if (raw && typeof raw.post === "function") {
    try {
      await raw.post({
        url: "/permission/{requestID}/reply",
        path: { requestID },
        body,
        throwOnError: true,
        headers: { "Content-Type": "application/json" },
      });
      return;
    } catch {}
  }
  if (typeof client?.permission?.reply === "function") {
    try {
      await client.permission.reply({ requestID, ...body });
      return;
    } catch {}
  }
  if (sessionID && typeof client?.postSessionIdPermissionsPermissionId === "function") {
    try {
      await client.postSessionIdPermissionsPermissionId({
        path: { id: sessionID, permissionID: requestID },
        body: { response: answer.reply },
      });
    } catch {}
  }
}

function text(value) {
  return typeof value === "string" ? value : "";
}

export const TuiosAgentState = async (ctx) => {
  if (process.env.TUIOS_ENV !== "1" && !process.env.TUIOS_AGENT) {
    return {};
  }
  const client = ctx?.client;
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
        case "permission.updated": {
          const title = text(props.title) || text(props.permission) || text(props.type);
          const requestID = text(props.id);
          if (type !== "permission.asked" || !requestID) {
            report(type, sessionID, { title });
            break;
          }
          // Not awaited: opencode's event loop must not wait on the person.
          ask(type, sessionID, { title, permission_id: requestID }).then((answer) => {
            if (answer) return reply(client, sessionID, requestID, answer);
          }).catch(() => {});
          break;
        }
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
