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

An error response carries an `error` object with a stable string `code` and a
human readable `message`:

```json
{"id": 1, "error": {"code": "session_not_found", "message": "session work not found"}}
```

Every `result` carries a `type` discriminator string so a generic client can
dispatch on the result shape without tracking which verb it sent.

Most verbs that name a session accept an empty or omitted `session`, which
resolves to the most recently active session.

## Versioning and introspection

The protocol carries a version integer. Bump it only on an incompatible change
to the envelope or to an existing verb; adding a new verb is backward compatible
and does not bump it. Read the version and the full verb list with `list-verbs`:

Request:

```json
{"id": 1, "verb": "list-verbs"}
```

Response:

```json
{"id": 1, "result": {
  "type": "verb_list",
  "version": 1,
  "verbs": [
    {"verb": "capture-pane", "description": "Capture a pane's content ..."},
    {"verb": "close-window", "description": "Close a window ..."}
  ]
}}
```

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
| `internal` | An unexpected server side failure. |

## How verbs interact with an attached client

Read verbs (`list-sessions`, `session-info`, `list-windows`, `get-option`) and
input verbs (`send-text`, `capture-pane`, `resize`) always answer from daemon
owned state and the daemon owned PTYs, so they work with or without an attached
TUI.

Structural verbs that a live renderer must own to stay in sync (`new-window`,
`close-window`, `send-keys`, and the live apply half of `set-option`) route to
the attached TUI when one is present and act on daemon owned state otherwise. The
routing is transparent to the caller: it is still one request and one response.

## Verbs

### list-verbs

List every supported verb and the protocol version. No params.

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
- `source` (optional): `visible` (the viewport, the default), `recent`
  (viewport plus scrollback), or `recent-unwrapped` (reserved; currently
  identical to `recent`).
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
| `window-created` | A daemon owned window was created. | `session`, `window`, `pty_id`, `title` |
| `window-closed` | A daemon owned window was removed. | `session`, `window`, `pty_id` |
| `window-exit` | A window's shell process exited. | `session`, `window`, `pty_id` |
| `window-retitled` | A window's title or name changed. | `session`, `window`, `title` |
| `output` | A window produced output (activity signal only; the raw bytes still flow over the binary stream). | `session`, `window`, `pty_id`, `bytes` |
| `bell` | A window rang the terminal bell. | `session`, `window`, `pty_id` |
| `mode-changed` | A terminal mode toggled (for example alt-screen). | `session`, `window`, `mode`, `enabled` |
| `session-created` | A session was created. | `session` |
| `session-closed` | A session was terminated. | `session` |
| `gap` | Slow-subscriber marker: `dropped` events were dropped for this connection. | `dropped` |

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
