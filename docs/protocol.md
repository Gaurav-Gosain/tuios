# TUIOS JSON verb protocol

The TUIOS daemon speaks a typed, line-delimited JSON control protocol over the
same unix socket the interactive client uses. It is layered additively on top of
the existing binary framing: a connection is classified as JSON or binary from
its very first byte, so older clients (older tuios binaries, the SSH server, the
web build) keep working unchanged while new tooling can drive the daemon with
one JSON object per line.

This is the stable, language agnostic surface for scripting, CI, and test
harnesses. You can talk to it from a shell with a here-string and read replies
with jq, no client library required.

## Transport and framing

- One request is one line of JSON terminated by a newline.
- One response is one line of JSON terminated by a newline.
- The daemon detects the protocol from the first byte of the connection. A JSON
  client's first byte is `{` (or leading whitespace); a binary client's first
  byte is the high byte of a length prefix, which is always `0x00` or `0x01` for
  frames under the 16MB cap, so the two never collide.
- A single request line is capped at 16MB. Requests are processed in order on a
  connection; issue one request, read one response, then send the next. Run
  independent requests concurrently by opening more than one connection.
- The socket is protected by filesystem permissions (mode 0700). There is no
  application level auth token; use SSH for remote access.

## Request envelope

```json
{"id": 1, "verb": "list-windows", "params": {"session": "work"}}
```

- `id` is opaque and optional. It may be a number or a string. The daemon echoes
  it back verbatim on the matching response. Omit it and the response omits it.
- `verb` is the verb name (required).
- `params` is a verb specific object. It may be omitted when a verb takes no
  parameters.

## Response envelope

A success response carries a `result` object:

```json
{"id": 1, "result": {"type": "window_list", "total": 2, "windows": [ ... ]}}
```

An error response carries an `error` object with a stable string `code`, a human
readable `message`, and usually a structured `hint` naming what resolves the
failure:

```json
{"id": 1, "error": {
  "code": "session_not_found",
  "message": "session wrok not found",
  "hint": {
    "param": "session",
    "command": "tuios ls",
    "did_you_mean": "work",
    "available": ["notes", "work"],
    "detail": "the name matches no live session. ..."
  }
}}
```

### The hint object

`hint` exists so a caller never has to guess what to do next, and never has to
make a second call just to learn what values are legal. Every field is optional
and omitted when empty, so a consumer that reads only `code` and `message` is
unaffected.

| Field | Meaning |
| --- | --- |
| `verb` | The verb that resolves or explains the failure, e.g. `list-verbs` for an unknown verb. |
| `command` | The exact CLI command that resolves it, written to be run as-is. Placeholders are in `<angle brackets>`. |
| `param` | The offending parameter, for `invalid_params` and anything that failed on one input. |
| `accepted` | The values `param` will take, when that set is closed. |
| `did_you_mean` | The closest match to what the caller asked for, when one is close enough to suggest. |
| `available` | What does exist: session names, addressable windows, verb names, option keys. |
| `detail` | One sentence of context that does not fit the fields above. |

A hint is advisory. Acting on `command` or `did_you_mean` is always optional, and
the absence of a hint never changes what `code` means.

Every `result` carries a `type` discriminator string so a generic client can
dispatch on the result shape without tracking which verb it sent.

Most verbs that name a session accept an empty or omitted `session`, which
resolves to the most recently active session.

## Versioning and introspection

The protocol carries a version integer. Bump it only on an incompatible change
to the envelope or to an existing verb; adding a new verb is backward compatible
and does not bump it.

### The hello handshake

`hello` is the first call a client should make. It reports the protocol range the
daemon serves and identifies the daemon, so a version mismatch is reported as a
`protocol_mismatch` error rather than surfacing later as a decode failure or a
dropped connection.

Request:

```json
{"id": 1, "verb": "hello", "params": {"client": "tuios", "version": "1.4.0", "protocol": 1}}
```

Result:

```json
{"id": 1, "result": {
  "type": "hello",
  "protocol": 1,
  "min_protocol": 1,
  "daemon_version": "1.4.0",
  "pid": 4242,
  "sessions": 2
}}
```

The handshake is optional, not a gate: a daemon serves every other verb whether
or not `hello` was called, and a daemon older than the handshake answers
`unknown_verb`, which a client should treat as "older but usable" rather than as
a failure.

There is one case the handshake cannot answer on this protocol, because the
daemon predates the protocol entirely. Such a daemon reads the leading `{` of a
request line as the high byte of a binary length prefix, fails its frame check,
and closes the connection. A client that sees the connection die with no response
line should read that as a version mismatch, not as a transport fault; the
`tuios` CLI confirms it by asking over the older binary handshake, which every
daemon has always answered, and reports both versions along with the
`tuios kill-server` command that resolves it.

### list-verbs

`list-verbs` is the discovery entry point. It returns every verb with its full
parameter schema and runnable examples, the protocol range, the error-code
catalog, and the envelope shapes, which together are enough to drive the control
plane without reading this document.

Request:

```json
{"id": 1, "verb": "list-verbs"}
```

Response (abridged):

```json
{"id": 1, "result": {
  "type": "verb_list",
  "version": 1,
  "min_version": 1,
  "daemon_version": "1.4.0",
  "verbs": [
    {
      "verb": "capture-pane",
      "description": "Capture a pane's content.",
      "params": [
        {"name": "session", "type": "string", "description": "Session name. Omit to target the most recently active session."},
        {"name": "source", "type": "string", "description": "Which buffer to capture.",
         "accepted": ["visible", "recent"], "default": "visible"}
      ],
      "examples": ["{\"id\":1,\"verb\":\"capture-pane\",\"params\":{\"session\":\"work\",\"source\":\"recent\"}}"]
    }
  ],
  "error_codes": [{"code": "session_not_found", "description": "The named session does not exist. ..."}],
  "envelope": {"request": "{\"id\":<any>,\"verb\":\"<name>\",\"params\":{...}}", "...": "..."}
}}
```

Pass a `verb` param to describe only that verb. Each parameter carries its
`name`, `type` (`string`, `int`, `bool`, or `[]string`), `description`, and
optionally `required`, `accepted`, and `default`. The `accepted` lists are the
same lists the handlers enforce, so they cannot drift from the implementation.

From the shell, `tuios list-verbs` and `tuios list-verbs --json` render the same
catalog.

## Error codes

| Code | Meaning |
| --- | --- |
| `invalid_request` | The line was not a valid request envelope (bad JSON, or missing verb). |
| `unknown_verb` | No verb by that name. |
| `invalid_params` | The params failed to decode, or a required field was missing. |
| `session_not_found` | The named session does not exist (or no sessions exist). |
| `window_not_found` | The window target did not resolve to a window. |
| `no_windows` | The session has no windows to act on. |
| `pty_not_found` | The target window has no live PTY. |
| `needs_client` | The verb needs a live renderer that is not attached. |
| `option_not_found` | A get-option key was never set. |
| `command_failed` | A verb routed to the attached client came back failed or timed out. |
| `timeout` | A wait-for condition did not match before its timeout elapsed. |
| `protocol_mismatch` | The caller's protocol version is outside the range this daemon serves. Only `hello` produces it. |
| `internal` | An unexpected server side failure. |

Codes are stable and additive: existing codes never change meaning, and a new
code is only ever introduced for a condition that previously had none. A client
should treat an unrecognized code as a generic failure and fall back to
`message`. The live catalog with descriptions is in the `list-verbs` result.

## How verbs interact with an attached client

Read verbs (`list-sessions`, `session-info`, `list-windows`, `get-option`) and
input verbs (`send-text`, `capture-pane`, `resize`) always answer from daemon
owned state and the daemon owned PTYs, so they work with or without an attached
TUI.

`close-window` and the `RenameWindow` command always act on daemon owned state,
attached or not. Removing a window from the window set, killing its PTY, and
naming a window are the daemon's to do; an attached client is told what happened
and re-renders. There is no second implementation for these and no round trip to
a client that can time out.

The verbs a live renderer still has to own to stay in sync (`new-window`,
`send-keys`, and the live apply half of `set-option`) route to the attached TUI
when one is present and act on daemon owned state otherwise. The routing is
transparent to the caller: it is still one request and one response.

A verb that genuinely cannot run without a renderer (tiling geometry, animation,
theming) fails with `needs_client`, whose hint names the `tuios attach` command
for that session. Everything else works headless.

### Who owns session state

The daemon owns session state. An attached client keeps its own copy and pushes
it back as it renders, but that push does not replace what the daemon holds.

Every state the daemon hands out carries a `version`, which counts the mutations
the daemon has made itself. A client echoes the version it last saw back as
`base_version` on the state it pushes. When the two match, the client has seen
everything the daemon did and its snapshot is applied as sent. When
`base_version` is behind, the client built its snapshot before a daemon side
mutation it has never seen, and the fields the daemon owns are restored on top of
it: which windows exist, their names, workspaces and minimized flags, the focused
window, and the current workspace. The client keeps the fields it owns, which are
the ones derived from its own viewport: pixel geometry, z order, the shell
reported title, pre restore geometry, and alt screen state. The daemon then sends
the merged state back to that client so it converges rather than pushing the same
stale view again.

The daemon does not wait to be asked. Every mutation it makes itself is pushed to
the attached clients as a state sync the moment it lands, so a change made by a
headless verb, a script, or another client shows up in a live TUI rather than
waiting for that client's next push to reveal the disagreement. Pushes are
ordered by `version`, and one overtaken by a newer state is dropped, so a client
is never handed a state older than one it has already applied.

A `base_version` of `0` means a client that predates state versioning. It cannot
say what it saw, so its pushes are applied as sent, exactly as before. Input mode
is not part of session state at all: it is per viewer, so one client switching to
terminal mode no longer switches every other client with it.

`kill-session` destroys the session for every client, not just the caller. Each
attached client is told the session ended and exits with a non-zero status, so a
script that kills a session does not leave a user staring at a dead UI. The
`session-closed` event fires on the event stream at the same time.

## Verbs

### hello

Handshake: report the protocol range this daemon serves. Params: `client`,
`version`, `protocol`. Result type: `hello`. See the versioning section above.

### list-verbs

List every verb with its parameter schema and examples, plus the protocol range,
the error-code catalog, and the envelope shapes. Params: `verb` (optional, to
describe just one).

Request:

```json
{"verb": "list-verbs"}
```

Result type: `verb_list`. See the introspection section above.

### list-sessions

List all sessions the daemon holds. No params.

Request:

```json
{"verb": "list-sessions"}
```

Response:

```json
{"result": {"type": "session_list", "sessions": [
  {"name": "work", "id": "5f...", "window_count": 3, "attached": true, "width": 120, "height": 40}
]}}
```

### session-info

Report details about one session.

Params: `session` (optional).

Request:

```json
{"verb": "session-info", "params": {"session": "work"}}
```

Response:

```json
{"result": {
  "type": "session_info",
  "session_name": "work",
  "session_id": "5f...",
  "current_workspace": 1,
  "window_count": 3,
  "tiling_mode": "tiling",
  "width": 120,
  "height": 40,
  "tui_attached": true
}}
```

### list-windows

List the windows in a session.

Params: `session` (optional).

Request:

```json
{"verb": "list-windows", "params": {"session": "work"}}
```

Response:

```json
{"result": {
  "type": "window_list",
  "total": 2,
  "focused_index": 0,
  "focused_window_id": "7e02...",
  "current_workspace": 1,
  "workspace_windows": [2, 0, 0, 0, 0, 0, 0, 0, 0],
  "windows": [
    {"window_id": "7e02...", "index": 0, "title": "zsh", "display_name": "editor",
     "workspace": 1, "focused": true, "minimized": false, "x": 0, "y": 0,
     "width": 80, "height": 24, "pty_id": "4bff..."}
  ]
}}
```

### new-window

Create a new window in a session.

Params: `session` (optional), `name` (optional window name).

Request:

```json
{"verb": "new-window", "params": {"session": "work", "name": "build"}}
```

Response:

```json
{"result": {"type": "window_created", "window_id": "9a3c...", "name": "build"}}
```

### close-window

Close a window.

Params: `session` (optional), `window` (optional target; defaults to the focused
window). A window target matches, in order, an exact window ID, a unique ID
prefix, an exact custom name, then an exact title.

Request:

```json
{"verb": "close-window", "params": {"session": "work", "window": "build"}}
```

Response:

```json
{"result": {"type": "ok"}}
```

### send-keys

Send parsed key tokens to a window. Tokens are split on spaces and commas and
each is mapped to its terminal byte sequence (named keys such as `enter` and
`tab`, `ctrl+x`, `alt+x`, function keys, or a literal character). With a TUI
attached the keys route to it so window manager keys such as the prefix are
honored; otherwise the parsed bytes go straight to the target PTY.

Params: `session` (optional), `window` (optional), `keys` (required), `literal`
(optional bool, send the text through unchanged), `raw` (optional bool, treat
each character as its own key).

Request:

```json
{"verb": "send-keys", "params": {"session": "work", "keys": "ctrl+c"}}
```

Response:

```json
{"result": {"type": "ok"}}
```

### send-text

Send literal text to a window's PTY. Unlike send-keys the text is written to the
PTY verbatim with no key parsing, so it is always safe and always goes straight
to the daemon owned PTY. Include a trailing newline to submit a line.

Params: `session` (optional), `window` (optional), `text` (required).

Request:

```json
{"verb": "send-text", "params": {"session": "work", "text": "echo hello\n"}}
```

Response:

```json
{"result": {"type": "ok"}}
```

### capture-pane

Capture a pane's content, rendered from the daemon side terminal emulator.

Params:

- `session` (optional), `window` (optional).
- `source` (optional): `visible` (the viewport, the default) or `recent`
  (viewport plus scrollback). Any other value is rejected with
  `invalid_params`; the hint names the accepted set.
- `styled` (optional bool): include ANSI styling escape sequences. Default is
  plain text.
- `scrollback` (optional bool): alias for `source: "recent"`.
- `ansi` (optional bool): alias for `styled`.
- `lines` (optional int): when greater than zero and no region is given, keep
  only the last N lines.
- `start`, `end` (optional ints): a 1 based inclusive line region. When set, the
  region wins over `lines`.

Request:

```json
{"verb": "capture-pane", "params": {"session": "work", "source": "recent", "lines": 20}}
```

Response:

```json
{"result": {"type": "pane_content", "source": "recent", "styled": false, "content": "..."}}
```

Content is physical rows, not logical lines: a line longer than the pane width
was wrapped by the emulator and comes back as several rows, so `lines`, `start`
and `end` count wrapped rows. There is no unwrapped capture. Earlier builds
documented a reserved `recent-unwrapped` source that was accepted but behaved
exactly like `recent`; it is now rejected rather than silently ignored, because
the emulator does not record which rows are continuations and unwrapping them
would mean guessing. A caller that needs logical lines should widen the pane
with `resize` before capturing.

### resize

Resize a window's PTY.

Params: `session` (optional), `window` (optional), `width` (required, positive),
`height` (required, positive).

Request:

```json
{"verb": "resize", "params": {"session": "work", "width": 100, "height": 40}}
```

Response:

```json
{"result": {"type": "resized", "width": 100, "height": 40}}
```

### kill-session

Terminate a session and everything in it.

Params: `session` (required).

Request:

```json
{"verb": "kill-session", "params": {"session": "work"}}
```

Response:

```json
{"result": {"type": "ok"}}
```

### set-option

Set a session option. The value is recorded in daemon owned session state so a
later get-option reads it back, and works with no client attached. When a TUI is
attached the change is also routed to it so options it understands apply to the
live renderer; `applied` reports whether that live apply succeeded.

Params: `session` (optional), `key` (required), `value` (optional).

Request:

```json
{"verb": "set-option", "params": {"session": "work", "key": "border_style", "value": "rounded"}}
```

Response:

```json
{"result": {"type": "option_set", "key": "border_style", "value": "rounded", "applied": true}}
```

### get-option

Read a session option previously set with set-option.

Params: `session` (optional), `key` (required).

Request:

```json
{"verb": "get-option", "params": {"session": "work", "key": "border_style"}}
```

Response:

```json
{"result": {"type": "option", "key": "border_style", "value": "rounded"}}
```

A key that was never set returns an `option_not_found` error.

## Event stream

The daemon can push events instead of a caller polling. A connection that issues
the `subscribe` verb is turned into a long-lived event stream; every other
connection never receives events. Each event is one JSON line carrying a
daemon-global monotonic `seq`, a `type`, and the fields relevant to that type.
Two subscribers always see the same `seq` for the same event.

Event types:

| Type | Meaning | Notable fields |
| --- | --- | --- |
| `window-created` | A window was created. | `session`, `window`, `pty_id`, `title` |
| `window-closed` | A window was removed. | `session`, `window`, `pty_id` |
| `window-exit` | A window's shell process exited. | `session`, `window`, `pty_id` |
| `window-retitled` | A window's title or name changed. | `session`, `window`, `title` |
| `window-focused` | A window became the focused window. | `session`, `window`, `pty_id` |
| `window-moved` | A window moved to another workspace. | `session`, `window`, `pty_id`, `workspace` |
| `window-minimized` | A window was minimized. | `session`, `window`, `pty_id` |
| `window-restored` | A minimized window was restored. | `session`, `window`, `pty_id` |
| `workspace-switched` | The session's current workspace changed. | `session`, `workspace` |
| `output` | A window produced output (activity signal only; the raw bytes still flow over the binary stream). | `session`, `window`, `pty_id`, `bytes` |
| `bell` | A window rang the terminal bell. | `session`, `window`, `pty_id` |
| `mode-changed` | A terminal mode toggled (for example alt-screen). | `session`, `window`, `mode`, `enabled` |
| `session-created` | A session was created. | `session` |
| `session-closed` | A session was terminated. | `session` |
| `gap` | Slow-subscriber marker: `dropped` events were dropped for this connection. | `dropped` |

### What fires when

A mutation reaches the daemon's canonical state by one of two routes. Either the
daemon mutates its own state (every headless mutation, and the ones it owns even
with a client attached), or an attached TUI performs the mutation and syncs the
result back. Both routes converge on the same state, and the window lifecycle
events are derived from that convergence by diffing the state before and after
it, so:

- Every window lifecycle event fires **exactly once** per mutation, with the same
  payload fields and the same relative ordering, whether or not a client is
  attached. There is no separate "headless only" set of events.
- Lifecycle events fire for mutations a **human drives from the TUI**, not just
  for ones a control-plane verb requested. Creating a window with the keyboard
  raises `window-created` exactly as `new-window` does.
- The PTY-driven events (`output`, `bell`, `mode-changed`, `window-exit`) hang
  off the PTY rather than off window state, so they have always fired on both
  routes and are unaffected.

Ordering within a single mutation is stable: closes, then creates, then
per-window changes (`window-retitled`, `window-moved`,
`window-minimized`/`window-restored`), then `workspace-switched`, then
`window-focused`. Focus comes last because it is usually a consequence of an
earlier event in the same batch, so a consumer building a model from the stream
already knows about the window being focused by the time it is told to focus it.

Two cases are worth stating plainly because they are easy to guess wrong:

- `window-retitled` fires for an **explicit rename** (the `rename-window` verb or
  the TUI's rename) and, separately, when the **shell changes its own title** via
  an OSC escape sequence. The shell-driven case is reported by the PTY, not by
  the state diff, so a shell retitling itself raises the event once, not twice.
- A change that is not a lifecycle change raises nothing. Window geometry,
  z-order, and alt-screen flags move constantly as a TUI renders and re-tiles;
  none of them produce events, so an attached client does not flood the stream.

Restoring a session (daemon cold start, or the `resurrect` verb) raises
`session-created` followed by a `window-created` for each restored window, since
from a subscriber's point of view those windows come into existence at that
moment. A subscriber that connects afterwards sees no backfill: the stream
carries what happens from the subscription onward, and the ack's `seq` is the
baseline. Use `list-windows` to establish initial state, then follow the stream.

### subscribe

Open the event stream on this connection.

Params: `session` (optional filter), `window` (optional filter), `types`
(optional list of event types to include; empty means all), `queue` (optional
per-connection queue size; defaults to 256).

Request:

```json
{"id": 1, "verb": "subscribe", "params": {"session": "work", "types": ["output", "bell"]}}
```

Ack response (the stream begins after this line):

```json
{"id": 1, "result": {"type": "subscribed", "seq": 42}}
```

Subsequent lines are events, for example:

```json
{"seq": 43, "type": "output", "session": "work", "window": "1f3c...", "pty_id": "9ab2...", "bytes": 64, "time": 1737200000000000000}
```

A second `subscribe` on the same connection is rejected with `invalid_request`.

### Slow subscriber policy

Each subscribed connection has a bounded queue. When it is full the daemon drops
the event and counts the drop rather than blocking; the next event delivered to
that connection is preceded by a gap marker:

```json
{"type": "gap", "dropped": 12}
```

so the connection learns it fell behind (the `seq` values also jump). One slow
reader never stalls the daemon or any other subscriber.

### unsubscribe

Close this connection's event stream. Params: none.

```json
{"verb": "unsubscribe"}
```

Response: `{"result": {"type": "unsubscribed"}}`. Closing the connection also
tears the stream down.

### wait-for

Block until a condition matches, then return a `wait_result`; return a `timeout`
error if the condition does not match in time. This is sugar over a short-lived
subscription and replaces a caller's capture-pane poll loop.

Params: `condition` (required), `session`, `window`, `pattern` (regex, for
`window-output`), `source` (`visible` or the default recent/scrollback content,
for `window-output`), `idle` (quiet-period milliseconds, for `window-idle`;
default 500), `timeout` (milliseconds; default 30000).

Conditions:

- `window-output` matches `pattern` against the target window's captured
  content. Checked once immediately, then re-checked as the window produces
  output.
- `window-exit` resolves when the target window's shell process exits.
- `window-idle` resolves after the target window produces no output for `idle`
  milliseconds.
- `session-exists` resolves when a session named `session` exists.

Request:

```json
{"id": 1, "verb": "wait-for", "params": {"condition": "window-output", "session": "work", "pattern": "build succeeded", "timeout": 60000}}
```

Response on match:

```json
{"id": 1, "result": {"type": "wait_result", "condition": "window-output", "matched": true, "window": "", "pattern": "build succeeded"}}
```

Response on timeout:

```json
{"id": 1, "error": {"code": "timeout", "message": "timed out waiting for output matching build succeeded"}}
```

## Examples from a shell

Create a detached session, drive it, and read it back:

```sh
SOCK="${XDG_RUNTIME_DIR:-/tmp/tuios-$(id -u)}/tuios/tuios.sock"

# List windows in the most recently active session.
printf '{"id":1,"verb":"list-windows"}\n' | socat - "UNIX-CONNECT:$SOCK" | jq .

# Run a command in a pane and read the output.
printf '{"verb":"send-text","params":{"text":"date\n"}}\n' | socat - "UNIX-CONNECT:$SOCK"
printf '{"verb":"capture-pane","params":{"source":"recent","lines":5}}\n' \
  | socat - "UNIX-CONNECT:$SOCK" | jq -r .result.content

# Block until a build finishes instead of polling capture-pane.
printf '{"verb":"wait-for","params":{"condition":"window-output","pattern":"build succeeded","timeout":120000}}\n' \
  | socat - "UNIX-CONNECT:$SOCK" | jq .

# Watch every window's activity as newline-delimited events.
printf '{"verb":"subscribe","params":{"types":["output","bell","window-exit"]}}\n' \
  | socat - "UNIX-CONNECT:$SOCK" | jq -c .
```

The tuios CLI speaks this protocol directly. `tuios ls`, `tuios kill-session`,
`tuios send-keys`, `tuios capture-pane`, `tuios list-windows`,
`tuios session-info`, `tuios set-config`, and `tuios get-config` are all verb
protocol clients.
