// installed by tuios
// managed by tuios; `tuios integration install pi` overwrites this file and
// `tuios integration uninstall pi` removes it. Put your own extensions beside
// it instead of editing it.
// TUIOS_INTEGRATION_ID=pi
// TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__
// @ts-nocheck
//
// Reports Pi's turns to the tuios pane it runs in: session_start names the
// session, agent_start starts work and agent_settled ends it. ui_prompt_start
// says Pi is waiting on the person in a blocking prompt (a confirm, a choice,
// a line of input), and ui_prompt_end that the prompt was answered; both say
// whether a turn is running, so an answered prompt goes back to working or to
// rest. Pi's subagents report as children of the pane: subagent_start names
// one and what it was asked, subagent_update what it runs now, subagent_stop
// how it ended — an empty stop text is a stop, a non-empty one a finish. Only
// the TUI mode reports, since the print, JSON and RPC modes run with no
// terminal a pane could show.

import { execFileSync, spawn } from "node:child_process";
import { readFileSync, watch } from "node:fs";
import { basename, dirname } from "node:path";
import { homedir } from "node:os";

const TUIOS = __TUIOS_COMMAND__;

// Delivery is best-effort, bounded and FIFO for retained state reports. Keep
// only the newest adjacent metadata update; a slow daemon must not accumulate
// an unbounded promise chain or make /reload wait for every obsolete leaf.
const MAX_PENDING = 32;
const MAX_REPORT_BYTES = 64 * 1024;
const DRAIN_MS = 1000;
const queue = [];
let sending = false;
let activeChild;
let idle;
let lastSelection = "";
let spawnedByNoted = false;
__TUIOS_HOOK_SOCKET__

function send(payload) {
  // The socket path needs no process per report; the process fallback stays
  // for a daemon without the verb and for environments without a socket.
  return hookViaSocket("__TUIOS_HARNESS__", payload, 2000).then(out => {
    if (out === null) return sendByProcess(payload);
  });
}

function sendByProcess(payload) {
  return new Promise(resolve => {
    try {
      const child = spawn(TUIOS, ["agent-hook", "pi", "--integration", "__TUIOS_VERSION__"], {
        stdio: ["pipe", "ignore", "ignore"],
        windowsHide: true,
        timeout: 2000,
      });
      activeChild = child;
      child.on("error", resolve);
      child.on("close", () => {
        if (activeChild === child) activeChild = undefined;
        resolve();
      });
      child.stdin.on("error", () => {});
      child.stdin.end(payload);
    } catch {
      resolve();
    }
  });
}

async function pump() {
  if (sending) return;
  sending = true;
  try {
    while (queue.length) await send(queue.shift().payload);
  } finally {
    sending = false;
    idle?.();
    idle = undefined;
  }
}

function enqueue(payload, key) {
  if (Buffer.byteLength(payload) > MAX_REPORT_BYTES) return;
  const last = queue.at(-1);
  if (key && last?.key === key) {
    last.payload = payload;
    return;
  }
  if (queue.length >= MAX_PENDING) {
    // Under saturation the oldest keyed report is evicted, so a pending keyed
    // report — a stop, say — can be the one dropped. Best-effort by contract.
    const obsolete = queue.findIndex(report => report.key);
    queue.splice(obsolete < 0 ? 0 : obsolete, 1);
  }
  queue.push({ payload, key });
  void pump();
}

async function drain() {
  if (!sending && !queue.length) return;
  let timer;
  await Promise.race([
    new Promise(resolve => { idle = resolve; }),
    new Promise(resolve => { timer = setTimeout(resolve, DRAIN_MS); }),
  ]);
  clearTimeout(timer);
  idle = undefined;
  queue.length = 0;
  activeChild?.kill(); // only our reporting child, never the daemon or Pi
}

function report(event, ctx, extra, key) {
  let sessionID = "";
  let sessionFile = "";
  let leaf;
  try {
    const id = ctx?.sessionManager?.getSessionId?.();
    if (typeof id === "string") sessionID = id;
  } catch {}
  try {
    const id = ctx?.sessionManager?.getLeafId?.();
    if (typeof id === "string" || id === null) leaf = id ?? "";
  } catch {}
  try {
    const file = ctx?.sessionManager?.getSessionFile?.();
    if (typeof file === "string") sessionFile = file;
  } catch {}
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: sessionID,
    transcript_path: sessionFile,
    ...(sessionFile && leaf !== undefined ? { transcript_leaf: leaf } : {}),
    ...(process.env.AGENT_THREAD_TOKEN_ID
      ? { thread_token: process.env.AGENT_THREAD_TOKEN_ID }
      : {}),
    ...extra,
  });
  // The daemon is not told again what it already has: the end of a turn, or a
  // second file event for one append, names the entry the last report named.
  // Every report that carries a selection counts, not only the metadata ones.
  const selection = sessionFile && leaf !== undefined ? sessionID + "\0" + sessionFile + "\0" + leaf : "";
  if (event === "transcript_update" && selection && selection === lastSelection) return;
  // Subagent reports' transcript fields are dropped by the daemon's
  // translatePi, so they must not count as a selection: a subagent report
  // firing between a transcript leaf advance and the watcher callback would
  // otherwise suppress the parent's next transcript_update.
  const dropsTranscript = event === "subagent_start" || event === "subagent_update" || event === "subagent_stop";
  if (selection && !dropsTranscript) lastSelection = selection;
  // A report that cannot be sent must never break Pi.
  enqueue(payload, key || (event === "transcript_update" ? sessionID + "\0" + sessionFile : ""));
}

export default function (pi) {
  if (process.env.TUIOS_ENV !== "1" && !process.env.TUIOS_AGENT) {
    return;
  }
  let tui = false;
  let epoch = 0;
  let watcher;
  const stopWatching = () => {
    watcher?.close();
    watcher = undefined;
  };
  const startWatching = ctx => {
    const file = ctx?.sessionManager?.getSessionFile?.();
    if (typeof file !== "string" || !file) return;
    const current = epoch;
    try {
      // Pi's _appendEntry sets leafId then persists synchronously. A file
      // notification observes that actual boundary even when a later async
      // message_end extension delays persistence, or a custom message at rest
      // emits no extension event. Watch the parent so first creation/rotation
      // works too. No transcript content is ever read by this extension.
      watcher = watch(dirname(file), { persistent: false }, (_kind, name) => {
        if (tui && epoch === current && (!name || String(name) === basename(file))) {
          report("transcript_update", ctx, {});
        }
      });
      watcher.on("error", () => { if (epoch === current) stopWatching(); });
    } catch {
      // Filesystem watching is optional; lifecycle reports still work.
    }
  };
  let selfWindow = "";
  let childrenTimer;
  let childrenCtx;

  // tuiosCall runs one daemon verb and parses its JSON reply. The extension
  // lives inside pi, so its process tree names its pane through resolve-pane
  // even when the harness scrubbed TUIOS_PANE_ID.
  function tuiosCall(verb, params) {
    return new Promise((resolve) => {
      try {
        const child = spawn(TUIOS, ["call", verb, JSON.stringify(params)], {
          stdio: ["ignore", "pipe", "ignore"],
          windowsHide: true,
          timeout: 8000,
        });
        let out = "";
        child.stdout.on("data", (chunk) => { out += chunk; });
        child.on("error", () => resolve(undefined));
        child.on("close", () => {
          try {
            const parsed = JSON.parse(out);
            resolve(parsed.error ? undefined : parsed);
          } catch {
            resolve(undefined);
          }
        });
      } catch {
        resolve(undefined);
      }
    });
  }

  // resolveSelf names the pane this pi runs in, via the daemon's resolve-pane
  // verb: the terminal's session id matches the pane shell's pid first, the
  // ancestor pids second. Empty means unresolved; a run without it shows no
  // children footer but works otherwise.
  function resolveSelf() {
    if (selfWindow || !tui) return Promise.resolve(selfWindow);
    return new Promise((resolve) => {
      try {
        // macOS spells the terminal session id sess; Linux sid. One of the
        // two matches the pane shell's pid, which is what resolve-pane reads.
        let sid = NaN;
        for (const keyword of ["sess", "sid"]) {
          try {
            sid = Number(execFileSync("ps", ["-o", keyword + "=", "-p", String(process.pid)], { encoding: "utf8" }).trim());
            if (Number.isFinite(sid) && sid > 1) break;
          } catch { /* try the next spelling */ }
        }
        const pids = [];
        let pid = process.ppid;
        for (let hops = 0; pid > 1 && hops < 8; hops++) {
          pids.push(pid);
          pid = Number(execFileSync("ps", ["-o", "ppid=", "-p", String(pid)], { encoding: "utf8" }).trim());
        }
        void tuiosCall("resolve-pane", { sid, pids }).then((answer) => {
          if (answer?.window_id) selfWindow = answer.window_id;
          resolve(selfWindow);
        });
      } catch {
        resolve(selfWindow);
      }
    });
  }

  // pollChildren draws the agents this pane started: one footer segment whose
  // names are tuios://window links, so the parent sees who works for it and
  // jumps to a child without opening any listing. Children come from the
  // daemon's agent link, the same record list-agents reads.
  async function pollChildren() {
    if (!tui || !selfWindow || !childrenCtx) return;
    const answer = await tuiosCall("list-agents", { all_sessions: true });
    if (!tui || !childrenCtx) return;
    const rows = answer?.agents || [];
    const children = rows.filter((row) => row.parent?.window === selfWindow);
    if (!children.length) {
      childrenCtx.ui?.setStatus("tuios-children", "");
      return;
    }
    const working = children.filter((child) => child.state === "working").length;
    const parts = children.map((child) => {
      const label = `${child.name || String(child.window_id || "").slice(0, 8)} (${child.state})`;
      return `\x1b]8;;tuios://window/${child.window_id}\x07${label}\x1b]8;;\x07`;
    });
    const head = `${children.length} agent${children.length === 1 ? "" : "s"}${working ? ` \u00b7 ${working} working` : ""}: `;
    childrenCtx.ui?.setStatus("tuios-children", head + parts.join(" \u00b7 "));
  }

  function stopChildren() {
    if (childrenTimer) {
      clearInterval(childrenTimer);
      childrenTimer = undefined;
    }
    childrenCtx = undefined;
  }

  pi.on("session_start", (_event, ctx) => {
    stopWatching();
    stopSubagents();
    stopChildren();
    epoch++;
    tui = ctx?.mode === "tui";
    if (!tui) return;
    // A pane tuios started on behalf of another pane names its parent: the
    // footer carries it for as long as the session runs, and one notification
    // marks the arrival. Reloads re-report without re-notifying.
    const by = process.env.TUIOS_SPAWNED_BY;
    const parentWindow = process.env.TUIOS_SPAWNED_BY_WINDOW;
    if (by && !spawnedByNoted) {
      spawnedByNoted = true;
      // The label links to the parent pane: tuios focuses it on click.
      const label = "spawned from " + by;
      const linked = parentWindow
        ? `\x1b]8;;tuios://window/${parentWindow}\x07${label}\x1b]8;;\x07`
        : label;
      ctx?.ui?.setStatus("tuios-spawned-from", linked);
      ctx?.ui?.notify("Spawned from " + by);
    }
    startWatching(ctx);
    subCtx = ctx;
    startSubagents();
    // The parent side of the same lineage: once the pane knows itself it
    // polls the agents linked to it, so its footer names them with links.
    stopChildren();
    childrenCtx = ctx;
    void resolveSelf().then(() => {
      if (!selfWindow || !childrenCtx) return;
      void pollChildren();
      childrenTimer = setInterval(() => void pollChildren(), 30000);
    });
    // A reload replaces the extension mid-turn without a new agent_start.
    report("session_start", ctx, { busy: ctx?.isIdle?.() === false });
  });
  pi.on("session_tree", (_event, ctx) => {
    if (tui) report("transcript_update", ctx, {});
  });
  pi.on("session_compact", (_event, ctx) => {
    if (tui) report("transcript_update", ctx, {});
  });
  pi.on("turn_end", (_event, ctx) => {
    if (tui) report("transcript_update", ctx, {});
  });
  pi.on("session_shutdown", () => {
    tui = false;
    epoch++;
    stopWatching();
    stopSubagents();
    stopChildren();
    return drain();
  });
  pi.on("agent_start", (_event, ctx) => {
    if (tui) report("agent_start", ctx, {});
  });
  pi.on("agent_settled", (_event, ctx) => {
    if (tui && ctx?.isIdle?.() !== false) report("agent_settled", ctx, {});
  });
  pi.on("ui_prompt_start", (event, ctx) => {
    if (!tui) return;
    report("ui_prompt_start", ctx, {
      kind: typeof event?.kind === "string" ? event.kind : "",
      title: typeof event?.title === "string" ? event.title : "",
      busy: ctx?.isIdle?.() === false,
    });
  });
  pi.on("ui_prompt_end", (event, ctx) => {
    if (!tui) return;
    report("ui_prompt_end", ctx, {
      kind: typeof event?.kind === "string" ? event.kind : "",
      busy: ctx?.isIdle?.() === false,
    });
  });

  // Pi-subagents announces its runs on the in-process event bus. Each run
  // becomes a child row under the pane: subagent_start names one and what it
  // was asked, subagent_update what it runs now, subagent_stop how it ended
  // (empty text a stop, non-empty a finish). A foreground run is the parent's
  // own tool call on the subagent tool, so its id is that call's, behind fg:
  // to keep it off the run ids; a background or workflow run reports through
  // the async events and, for workflow children, through child-status. A
  // workflow child never reports its own natural completion: it ends with
  // its parent's async-complete, which carries a result per child. None of
  // them say anything about the pane's own state.
  let subCtx;
  let subUnsub;
  const subTaskByCall = new Map(); // toolCallId -> task text of a background launch
  const subSeen = new Map(); // subagent id -> the last update reported for it
  const subRuns = new Map(); // run id -> asyncDir, for the live tool line
  const subPolled = new Map(); // run id -> when status was last read
  const subChildParent = new Map(); // child id -> { parent, index, agent } of a workflow child

  const subFirstLine = value => {
    if (typeof value !== "string") return "";
    const line = value.trim().split(/\r?\n/, 1)[0].trim();
    return line.length > 200 ? line.slice(0, 200) : line;
  };

  // The daemon's ValidSubagentID accepts only [A-Za-z0-9_.:@-], and OpenAI
  // Responses/Codex toolCallIds contain '|', so the foreground id built from
  // one must be sanitised. The cap is 128 including the fg: prefix.
  const subId = call => "fg:" + call.replace(/[^A-Za-z0-9_.:@-]/g, "_").slice(0, 125);

  const subResultText = content => {
    if (!Array.isArray(content)) return "";
    for (const block of content) {
      if (block?.type === "text" && typeof block.text === "string" && block.text.trim()) {
        return subFirstLine(block.text);
      }
    }
    return "";
  };

  const subReport = (event, id, extra) => {
    if (!id) return;
    const prior = subSeen.get(id);
    const tool = typeof extra.tool === "string" ? extra.tool : "";
    const target = typeof extra.target === "string" ? extra.target : "";
    const line = tool ? tool + (target ? ": " + target : "") : "";
    const text = typeof extra.text === "string" ? extra.text : prior?.text || "";
    // An update that carries only an outcome never repeats the last report:
    // its line and text may be the ones already given.
    if (event === "subagent_update" && prior && extra.outcome === undefined &&
        line === (prior.line || "") && text === (prior.text || "")) {
      return; // the daemon is not told again what it already has
    }
    subSeen.set(id, { line, text });
    report(event, subCtx, { agent_id: id, ...extra, tool, target }, event + "\0" + id);
  };

  // pi-subagents emits child-status only on user-stop paths: a workflow
  // child that runs to its end is never named again. Track each child under
  // the parent run that child-status names, with the step index and agent
  // that map it to one of the parent's results.
  const subTrackChild = (id, p) => {
    const parent = typeof p.runId === "string" ? p.runId : "";
    if (!parent || parent === id) return;
    subChildParent.set(id, {
      parent,
      index: typeof p.stepIndex === "number" ? p.stepIndex : undefined,
      agent: typeof p.agent === "string" ? p.agent : "",
    });
  };

  const subDropChild = id => {
    subChildParent.delete(id);
    subRuns.delete(id);
    subPolled.delete(id);
  };

  // The parent's async-complete is the only end a workflow child gets on
  // natural completion: end every child it owns, with the child's own result
  // where one maps to it — by step index, else by agent — else a plain done.
  const subEndChildren = (parent, p) => {
    const results = Array.isArray(p.results) ? p.results : [];
    const consumed = new Set();
    for (const [id, tracked] of [...subChildParent]) {
      if (tracked.parent !== parent) continue;
      subDropChild(id);
      let result;
      if (tracked.index !== undefined) {
        result = results.find(r => r && typeof r === "object" && !consumed.has(r) && r.index === tracked.index);
      }
      if (!result) {
        result = results.find(r => r && typeof r === "object" && !consumed.has(r) &&
          typeof r.agent === "string" && r.agent && r.agent === tracked.agent);
      }
      if (result) consumed.add(result);
      if (result?.status === "failed") {
        subReport("subagent_update", id, { outcome: "failed", text: subFirstLine(result.summary) || "failed" });
      } else if (result?.status === "stopped") {
        subReport("subagent_stop", id, { text: "" });
      } else {
        subReport("subagent_stop", id, { text: subFirstLine(result?.summary) || "done" });
      }
    }
  };

  const pollStatuses = async () => {
    const now = Date.now();
    for (const [id, dir] of subRuns) {
      if (!dir || now - (subPolled.get(id) || 0) < 2000) continue;
      subPolled.set(id, now);
      let status;
      try {
        const { readFile } = await import("node:fs/promises");
        status = JSON.parse(await readFile(dir + "/status.json", "utf8"));
      } catch {
        continue; // the file may not exist yet, or not parse mid-write
      }
      try {
        const tool = subFirstLine(status.currentTool);
        subReport("subagent_update", id, { tool, target: subFirstLine(status.currentPath) });
      } catch {}
    }
  };

  const stopSubagents = () => {
    for (const off of subUnsub || []) {
      try { off?.(); } catch {}
    }
    subUnsub = undefined;
    subTaskByCall.clear();
    subSeen.clear();
    subRuns.clear();
    subPolled.clear();
    subChildParent.clear();
  };

  // pi-subagents runs a call in the background unless the model said
  // async: false — or unless asyncByDefault: false says the quiet default
  // the other way. The tool_call event carries the model's validated and
  // coerced args (pi core coerces before the event fires), so an explicit
  // async flag is a boolean here. The two defaults are read from
  // pi-subagents' own config, once per extension load: pi-subagents loads
  // its config once per process, so a hand-edited file mid-session changes
  // neither side until a reload. An agent that is an external CLI or has a
  // launcher is forced background with async omitted even when the default
  // is foreground; that combination is not detected here and draws a
  // phantom foreground row.
  const subConfig = () => {
    try {
      let dir = process.env.PI_CODING_AGENT_DIR || homedir() + "/.pi/agent";
      if (dir.startsWith("~/")) dir = homedir() + dir.slice(1);
      const config = JSON.parse(readFileSync(dir + "/extensions/subagent/config.json", "utf8"));
      return {
        asyncByDefault: config.asyncByDefault !== false,
        forceTopLevelAsync: config.forceTopLevelAsync === true,
      };
    } catch {
      // No config, or one that cannot be read: background is the default.
      return { asyncByDefault: true, forceTopLevelAsync: false };
    }
  };

  // Latched at extension load, the way pi-subagents latches its own.
  const subLadder = subConfig();

  const startSubagents = () => {
    if (subUnsub || !pi?.events?.on) return;
    try {
      subUnsub = [
        pi.events.on("subagent:async-started", p => {
          try {
            if (!tui || !p || typeof p !== "object") return;
            const id = typeof p.id === "string" && p.id ? p.id : "";
            if (!id) return;
            if (typeof p.asyncDir === "string") subRuns.set(id, p.asyncDir);
            // A single background run was launched by a tool call whose
            // input holds the task unredacted; the event redacts it. The
            // stash marks the call as launched: present means async-started
            // has not named this call yet.
            let text = "";
            if (subTaskByCall.has(p.toolCallId)) {
              text = subTaskByCall.get(p.toolCallId);
              subTaskByCall.delete(p.toolCallId);
            } else {
              text = subFirstLine(p.label);
            }
            subReport("subagent_start", id, { agent_type: subFirstLine(p.agent), text });
          } catch {}
        }),
        pi.events.on("subagent:child-status", p => {
          try {
            if (!tui || !p || typeof p !== "object" || p.type !== "subagent.child-status") return;
            const id = (typeof p.childRunId === "string" && p.childRunId) ||
              (typeof p.childId === "string" && p.childId) || "";
            if (!id) return;
            if (typeof p.asyncDir === "string") subRuns.set(id, p.asyncDir);
            if (p.status === "started") {
              subTrackChild(id, p);
              subReport("subagent_start", id, { agent_type: subFirstLine(p.agent), text: subFirstLine(p.label) });
            } else if (p.status === "stopping") {
              subTrackChild(id, p);
              subReport("subagent_update", id, { outcome: "stopping" });
            } else if (p.status === "stopped") {
              subReport("subagent_stop", id, { text: "" });
              subDropChild(id);
            }
          } catch {}
        }),
        pi.events.on("subagent:async-complete", p => {
          try {
            if (!tui || !p || typeof p !== "object") return;
            const id = (typeof p.runId === "string" && p.runId) ||
              (typeof p.id === "string" && p.id) || "";
            subRuns.delete(id);
            if (!id) return;
            if (p.state === "stopped" || p.stopped === true) {
              // A user stop is published with success false and stopped set
              // both; it is a stop, not a failure.
              subReport("subagent_stop", id, { text: "" });
            } else if (p.success === false || p.state === "failed") {
              subReport("subagent_update", id, { outcome: "failed", text: subFirstLine(p.error) || "failed" });
            } else {
              // A stop the daemon reads as done needs a text: a run that
              // finished but left no summary still says so.
              subReport("subagent_stop", id, { text: subFirstLine(p.results?.[0]?.summary) || "done" });
            }
            subEndChildren(id, p);
          } catch {}
        }),
        pi.events.on("subagent:jobs-changed", () => {
          try {
            if (!tui) return;
            void pollStatuses().catch(() => {});
          } catch {}
        }),
      ];
    } catch {
      subUnsub = undefined; // pi-subagents not loaded, or a bus that never fires
    }
  };

  pi.on("tool_call", (event, ctx) => {
    try {
      if (!tui || event?.toolName !== "subagent") return;
      subCtx = ctx;
      const input = event.input;
      const call = typeof event.toolCallId === "string" ? event.toolCallId : "";
      if (!call) return;
      // pi-subagents 0.84.0: management calls ('status', 'guide', 'list',
      // ...) carry an action; executions carry none. Only executions are
      // runs the pane can show, so a management call reports nothing at all.
      if (typeof input?.action === "string" && input.action) return;
      // The ladder, in pi-subagents' own order: an explicit flag wins;
      // forceTopLevelAsync rewrites async: false at depth 0; else the quiet
      // default. Nested calls (parentToolCallId set) are depth > 0, which
      // the rewrite does not touch.
      const forced = subLadder.forceTopLevelAsync && !event.parentToolCallId;
      const background = input?.async === true ||
        (input?.async === false ? forced : subLadder.asyncByDefault || forced);
      if (background) {
        // A background run reports through the async events; keep what they
        // redact stashed against its tool call. A workflow call's start
        // event names no tool call, so its stash is never consumed there —
        // a failed launch still reports through the tool_result instead.
        const task = subFirstLine(input?.task) || subFirstLine(input?.goal);
        subTaskByCall.set(call, task);
        return;
      }
      subReport("subagent_start", subId(call), {
        agent_type: subFirstLine(input?.agent),
        text: subFirstLine(input?.task) || subFirstLine(input?.goal),
      });
    } catch {}
  });

  pi.on("tool_result", (event, ctx) => {
    try {
      if (!tui || event?.toolName !== "subagent") return;
      subCtx = ctx;
      const call = typeof event.toolCallId === "string" ? event.toolCallId : "";
      if (!call) return;
      const id = subId(call);
      if (subTaskByCall.has(call)) {
        // A failed launch was never started, so no row exists yet and the
        // stash is the only witness. Name it, then end it failed, or the
        // rejection is invisible on the pane. A successful launch also
        // leaves the stash unconsumed (a workflow's async-started names no
        // tool call), but its rows came from the async and child-status
        // events: reporting here would draw a phantom start and failure.
        const task = subTaskByCall.get(call) ?? "";
        subTaskByCall.delete(call);
        if (event.isError) {
          subReport("subagent_start", id, { text: task });
          subReport("subagent_update", id, { outcome: "failed", text: subResultText(event.content) || "failed" });
        }
      } else if (event.isError) {
        subReport("subagent_update", id, { outcome: "failed", text: subResultText(event.content) || "failed" });
      } else {
        // A stop the daemon reads as done needs a text, so a success with
        // no text block still says done.
        subReport("subagent_stop", id, { text: subResultText(event.content) || "done" });
      }
    } catch {}
  });
}
