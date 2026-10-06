// installed by tuios
// managed by tuios; `tuios integration install opencode` overwrites this file
// TUIOS_INTEGRATION_ID=opencode
// TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__
// OpenCode V2 CLI plugin: https://opencode.ai/v2/docs/build/plugins/cli

import { TuiosAgentState } from "../tuios-agent-state.js";
import { createEffect, createRoot } from "solid-js";

export default {
  id: "tuios-agent-state",
  async setup(ctx) {
    if (process.env.TUIOS_ENV !== "1" && !process.env.TUIOS_AGENT) return;
    let controller = new AbortController();
    let disposed = false;
    const requests = new Map();
    // Only this narrow adapter reaches the V2 permission API. V1's raw HTTP
    // fallback must not run against a V2 server.
    const hooks = await TuiosAgentState({
      get signal() { return controller.signal; },
      client: { permission: { reply: async ({ requestID, ...answer }) => {
        const sessionID = requests.get(requestID);
        if (!sessionID || disposed) return;
        requests.delete(requestID);
        await ctx.client.permission.reply({ sessionID, requestID, ...answer });
      } } },
    });
    const totals = new Map();
    const active = (sessionID) => {
      const route = ctx.ui.router.current();
      return !disposed && route.type === "session" && route.sessionID === sessionID;
    };
    const forward = (type, properties) => active(properties.sessionID || properties.info?.sessionID)
      ? hooks.event({ event: { type, properties } })
      : Promise.resolve();
    // OpenCode dispatches a batch synchronously. Awaiting usage/tool forwarding
    // inside independent listeners can otherwise move idle after the next busy,
    // or permission.asked after its reply. Preserve the server's event order.
    let queue = Promise.resolve();
    const enqueue = (fn) => {
      queue = queue.then(fn).catch(() => {});
      return queue;
    };
    const stop = ctx.data.listen(({ details: event }) => {
      // data.listen observes the shared server, not just this terminal. Only
      // the displayed root session owns this pane's state and Inbox requests.
      const data = event.data ?? {};
      const sessionID = data.sessionID || data.form?.sessionID;
      const route = ctx.ui.router.current();
      if (route.type !== "session" || !sessionID || route.sessionID !== sessionID) return;
      const session = ctx.data.session.get(sessionID);
      if (session?.parentID) return;
      return enqueue(() => active(sessionID) && handle(event.type, data, sessionID, session));
    });

    let selected;
    const dispose = createRoot((dispose) => {
      createEffect(() => {
        const route = ctx.ui.router.current();
        const sessionID = route.type === "session" ? route.sessionID : undefined;
        if (sessionID === selected) return;
        const previous = selected;
        selected = sessionID;
        controller.abort();
        controller = new AbortController();
        requests.clear();
        if (!sessionID) {
          if (previous) void enqueue(() => {
            if (!disposed && ctx.ui.router.current().type !== "session") {
              return hooks.event({ event: { type: "session.deleted", properties: { sessionID: previous } } });
            }
          });
          return;
        }
        // Sync outside the reactive computation. Only a route change starts a
        // snapshot; subsequent state changes arrive on the ordered event stream.
        void Promise.resolve().then(async () => {
          await Promise.all([
            ctx.data.session.sync(sessionID),
            ctx.data.session.permission.sync(sessionID),
            ctx.data.session.form.sync(sessionID),
          ]);
          return enqueue(async () => {
            if (!active(sessionID)) return;
            const session = ctx.data.session.get(sessionID);
            if (!session || session.parentID) return;
            totals.set(sessionID, { modelID: session.model?.id, cost: session.cost });
            while (totals.size > 64) totals.delete(totals.keys().next().value);
            await forward("session.created", { sessionID });
            await handle("session.usage.updated", { cost: session.cost }, sessionID, session);
            if (ctx.data.session.status(sessionID) === "running") {
              await forward("session.status", { sessionID, status: { type: "busy" } });
            } else if (session.outcome === "failed") {
              await forward("session.error", { sessionID, error: { data: { message: "Session failed" } } });
            }
            for (const request of ctx.data.session.permission.list(sessionID) ?? []) {
              await handle("permission.asked", request, sessionID, session);
            }
            for (const form of ctx.data.session.form.list(sessionID) ?? []) {
              await handle("form.created", { form }, sessionID, session);
            }
          });
        }).catch(() => {});
      });
      // Active-session hydration is independent of session.sync(). An attach
      // can initially look idle even though execution.started predates this
      // client. Observe the later running value without clearing live prompts.
      createEffect(() => {
        const route = ctx.ui.router.current();
        if (route.type !== "session" || ctx.data.session.status(route.sessionID) !== "running") return;
        const sessionID = route.sessionID;
        void enqueue(() => {
          if (!active(sessionID) || ctx.data.session.get(sessionID)?.parentID) return;
          if (ctx.data.session.status(sessionID) !== "running") return;
          if ([...requests.values()].includes(sessionID)) return;
          if (ctx.data.session.permission.list(sessionID)?.length || ctx.data.session.form.list(sessionID)?.length) return;
          return forward("session.status", { sessionID, status: { type: "busy" } });
        });
      });
      return dispose;
    });

    async function handle(type, data, sessionID, session) {
      const properties = { sessionID };
      if (!totals.has(sessionID)) {
        totals.set(sessionID, { modelID: session?.model?.id, cost: session?.cost });
        while (totals.size > 64) totals.delete(totals.keys().next().value);
      }
      const usage = totals.get(sessionID);
      switch (type) {
        case "session.model.selected":
        case "session.step.started":
          usage.modelID = data.model?.id;
          break;
        case "session.usage.updated":
          usage.cost = data.cost;
          break;
      }
      // V2 publishes cumulative session usage, including resumed history and
      // auxiliary requests. Replace one synthetic cost entry rather than sum
      // each session.usage.updated as if it were a new assistant message.
      if (["session.model.selected", "session.step.started", "session.usage.updated", "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"].includes(type)) {
        await forward("message.updated", { info: { id: "session-total", sessionID, role: "assistant", ...usage } });
      }
      switch (type) {
        case "session.status":
          await forward(type, { ...properties, status: data.status });
          break;
        case "session.execution.started":
          await forward("session.status", { ...properties, status: { type: "busy" } });
          break;
        case "session.execution.succeeded":
        case "session.execution.interrupted":
          await forward("session.status", { ...properties, status: { type: "idle" } });
          break;
        case "session.execution.failed":
          await forward("session.error", { ...properties, error: { data: { message: data.error?.message || data.error?.type } } });
          break;
        case "session.deleted":
          totals.delete(sessionID);
          await forward(type, properties);
          break;
        case "permission.asked": {
          if (requests.has(data.id)) {
            await forward("permission.updated", { ...properties, title: data.message || data.action });
            break;
          }
          requests.set(data.id, sessionID);
          while (requests.size > 256) requests.delete(requests.keys().next().value);
          const message = data.source?.type === "tool"
            ? ctx.data.session.message.get(sessionID, data.source.messageID)
            : undefined;
          const tool = message?.content?.find((part) => part.type === "tool" && part.id === data.source.id);
          if (tool && tool.state?.status !== "streaming") {
            // Keep V2 tool names/inputs intact: the Inbox offers only calls
            // its existing whole-call checks understand. Otherwise the
            // OpenCode permission dialog remains the place to answer.
            await hooks["tool.execute.before"](
              { sessionID, callID: data.source.id, tool: tool.name },
              { args: tool.state?.input },
            );
          }
          await forward(type, {
            ...properties, id: data.id, permission: data.action,
            title: data.message || data.action, always: data.save,
            tool: { callID: data.source?.id },
          });
          break;
        }
        case "permission.replied":
          requests.delete(data.requestID);
          await forward(type, properties);
          break;
        case "form.created":
          await forward("question.asked", { ...properties, title: data.form.title });
          break;
        case "form.replied":
        case "form.cancelled":
          await forward(type === "form.replied" ? "question.replied" : "question.rejected", properties);
          break;
      }
    }

    return () => {
      disposed = true;
      dispose();
      stop();
      controller.abort();
      requests.clear();
    };
  },
};
