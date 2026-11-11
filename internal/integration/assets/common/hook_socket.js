// hookViaSocket sends one hook event to the tuios daemon over its socket, as
// the agent-hook-event verb, so no tuios process starts for it. The daemon
// finds the pane from this process, as `tuios agent-hook` finds it from its
// own. It resolves to what the hook prints for the harness to read (a string,
// often empty, and empty when signal aborts the call), or to null when the
// event has to go through `tuios agent-hook` instead: no socket in the
// environment, a socket that cannot be reached, or a daemon older than the
// verb. The two last are remembered, so
// the events after them go to the process at once. A call that reached the
// daemon is never sent again, so an event is never reported twice.
import { connect as tuiosConnect } from "node:net";

// The variables the hook mapping reads, as tuios integration writes them.
const TUIOS_HOOK_ENV = __TUIOS_HOOK_ENV__;
let tuiosSocketOff = !process.env.TUIOS_SOCKET;

function hookViaSocket(harness, payload, limitMs, signal) {
  if (tuiosSocketOff) return Promise.resolve(null);
  if (signal?.aborted) return Promise.resolve("");
  return new Promise((resolve) => {
    let settled = false;
    let connected = false;
    let buf = "";
    let sock = null;
    let timer = null;
    const done = (value) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
      try {
        sock?.destroy();
      } catch {}
      resolve(value);
    };
    // An abort ends the call with nothing printed; the daemon ends the hold
    // when it sees the connection go.
    const abort = () => done("");
    signal?.addEventListener("abort", abort, { once: true });
    const env = {};
    for (const name of TUIOS_HOOK_ENV) {
      if (process.env[name]) env[name] = process.env[name];
    }
    try {
      sock = tuiosConnect(process.env.TUIOS_SOCKET);
    } catch {
      tuiosSocketOff = true;
      done(null);
      return;
    }
    // Past the limit nothing is printed, as when the hook process gives up.
    timer = setTimeout(() => done(connected ? "" : null), limitMs);
    sock.setEncoding("utf8");
    sock.on("connect", () => {
      connected = true;
      sock.write(JSON.stringify({ id: 1, verb: "agent-hook-event", params: { harness, payload, env } }) + "\n");
    });
    sock.on("data", (chunk) => {
      buf += chunk;
      const nl = buf.indexOf("\n");
      if (nl < 0) {
        if (buf.length > 1 << 20) done("");
        return;
      }
      let msg = null;
      try {
        msg = JSON.parse(buf.slice(0, nl));
      } catch {
        done("");
        return;
      }
      if (msg && msg.error) {
        if (msg.error.code === "unknown_verb") {
          tuiosSocketOff = true;
          done(null);
          return;
        }
        done("");
        return;
      }
      const out = msg && msg.result ? msg.result.stdout : "";
      done(typeof out === "string" ? out : "");
    });
    sock.on("error", () => {
      if (!connected) tuiosSocketOff = true;
      done(connected ? "" : null);
    });
    sock.on("close", () => done(connected ? "" : null));
  });
}
