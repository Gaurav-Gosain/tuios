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
resolves to the most recently active session. The exceptions say so in their
own `session` parameter description in `list-verbs`:

- `subscribe` reads an omitted `session` as every session. Its `session` is a
  filter, not a target.
- `wait-for` with `session-exists` requires `session`, because it names the
  session to wait for. `wait-for` with `agent-state` and `any_session` takes no
  `session` and watches every session.
- `kill-session` and `remove-worktree` require `session` and never guess.
- `list-hooks`, `list-options`, `list-themes` and `list-glyphs` fall back to the
  most recently active session, and to the daemon's defaults when there is no
  session at all, rather than failing.

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

### Changes to existing verbs

Changes that alter what an existing verb does, for a caller that relied on the
old behaviour. None of them bumps the protocol integer: every field keeps its
name and type, and a caller that sends nothing new keeps working. What changes
is an answer, and each entry says which.

**An agent on `needs_input` or `unknown` is not ready to be asked.** The
states that count as ready are now `idle`, `done`, `errored` and `none`. They
used to include `needs_input` and `unknown` as well. `fan` types its first
prompt only on `idle` or `done`, and used to accept `unknown` too.

An agent on `needs_input` is most often sitting on a permission menu, and text
typed there is read as the answer: the question approved or denied whatever
the agent had asked for. `unknown` is what the silence timer writes when a pane
went quiet and nothing on its screen said the agent is at its prompt, and an
agent in the middle of a long tool call looks the same.

- `list-agents` returns `ready: false` for a pane on `needs_input` or
  `unknown`. It used to return `true`. `ready` still means what it always
  meant, that `ask-agent` would type at the pane without waiting, and it is
  that answer that changed. A script that polls `ready` before asking now waits
  for the prompt to be answered, or for the agent to reach its prompt.
- `ask-agent` waits on a pane on `unknown` like one that is `working`, and
  fails with `not_ready` when `ready_timeout` runs out, with a message that
  says the state was unknown. `force` sends anyway.
- `fan` does not type into an agent on `unknown` when its harness can show
  that it is at its prompt, which is a harness whose manifest has an idle rule
  on the screen or the title (see below for the list); such an agent reaches
  `idle` from its prompt box or title. For another agent `unknown` counts as
  ready, since nothing could ever show more.
- `ask-agent` against a pane on `needs_input` fails with the new code
  `agent_blocked` and writes nothing. It used to type the question. It also
  stops waiting with `agent_blocked` when a pane it is waiting on reaches
  `needs_input`, instead of running out `ready_timeout`.
- `force` on `ask-agent` still skips the wait for a working agent. It no longer
  types at a pane on `needs_input`; the new param `allow_blocked` does, for a
  caller that has read the prompt and knows it takes free text. `force` and
  `allow_blocked` together are what `force` alone used to be.
- `list-agents` and `get-agent-state` gain `blocked_by`: `approval` or
  `question` for a pane on `needs_input`, empty when the source did not say and
  for every other state. A screen, title or notify rule supplies it from its
  `kind`, a report supplies it with the `kind` param of `set-agent-state`, and
  a report without one is guessed from its message the way a rule without a
  kind is. `get-agent-state` also gains `ready`, with the same
  meaning as in `list-agents`.

**`agent_session_id` can change without a state report.** The new
`set-agent-session` verb writes the window's `agent_session_id`, which
`get-agent-state` and `list-agents` return, without touching the state, its
source or `harness_id`. Before it, the field changed only with a
`set-agent-state` report. A consumer that read a new id as a sign of a new
state report should read the state fields instead. The ten session-only
integrations (see [Agent state](AGENT_STATE.md#harness-integrations)) send it,
so a pane running one of them now carries an id while its state comes from
screen rules. Separately, the daemon forgets the harness pid a window's id was
reported with when the detector sees the agent leave the pane. `set-agent-state`
reads that pid only while a report holds the pane mid-turn, which the agent
leaving has already ended, so its answers are unchanged.

**A prompt is pasted and submitted with a carriage return.** `ask-agent` and
`fan` used to write the text followed by a line feed. Claude Code and Codex
submit on a carriage return, which is what the Enter key sends, and several
agent TUIs bind a line feed to "insert a newline", so a prompt could sit in the
input box unsent, and a prompt of several lines was a sequence of Enters. Both
verbs now write the text with its trailing line breaks dropped, wrapped in
`ESC[200~` and `ESC[201~` when the pane has bracketed paste (DECSET 2004) on,
then wait about 300 ms, or less once the pane has drawn the paste and gone
quiet, then write one carriage return. Line endings inside the text become line
feeds, and the paste delimiters are removed from it so the text cannot end the
paste early. A pane without bracketed paste still reads each line feed in the
text as the application decides, which for a shell is one command per line.

**The submit key and paste come from the harness's manifest.** `ask-agent` and
`fan` read the `[input]` block of the target pane's harness (see
[AGENT_STATE.md](AGENT_STATE.md#typing-a-prompt)). Every bundled harness keeps
the carriage return and the bracketed paste described above, so for them
nothing changes on the wire, with one exception: for GitHub Copilot, a pane
with focus reporting (DECSET 1004) on is sent a focus-in report (`ESC[I`)
before the paste, because Copilot ignores a synthetic Enter after it lost focus.
A user manifest can set `submit = "lf"` or `bracketed_paste = false` for its
harness.

**More harnesses read their screen and title for `working` and `idle`.** The
bundled manifests now carry herdr's working rules for every harness that has
them, and idle rules for Cline, Devin, Grok, Kiro, Maki and Qwen Code on the
screen and Amp, Grok and Hermes in the title, beside the four that had them.
What a caller sees:

- `list-agents` and `get-agent-state` report `working` and `idle` from
  `source: screen` or `source: osc` for these harnesses where they used to
  report nothing from those tiers.
- `ready` is `false`, and `ask-agent` and `fan` wait, for a pane of Amp,
  Cline, Devin, Grok, Hermes, Kiro, Maki or Qwen Code on `unknown`. It used to
  be `true`, because these harnesses had no rule that could show they were at
  their prompt. `force` on `ask-agent` still types at once, and a user manifest
  without the idle rules gives the old answer back.
- Codex reports `needs_input` from its screen for an approval form, the
  directory trust prompt and the update offer. It used to report it only from
  its title and notifications.
- Claude Code reports `needs_input` for a live form under the last rule, a
  dynamic workflow question and an MCP elicitation dialog, and `working` for
  the `/btw` overlay and for background agents and MCP tasks still running.
  Its screen rules now match substrings case-folded.
- Gemini CLI's title rules key on the glyph Gemini CLI writes. A title whose
  text starts with "Action required" after the working glyph `✦` reads as
  `working`, where it used to read as `needs_input`.
- Grok's footer-hint rules read the last two lines only, as herdr's do.

**`explain-agent-screen` says more.** The result gains `manifest_source`
(`bundled` or the path of the user file in force), `replaces_bundled`, and
`progress`, the pane's last OSC 9;4 report. Each entry of `rules` and
`title_rules` may carry `groups`, naming each nested group that refused, and
`text`, what a rule reading a region narrower than the tail read there. The
title rules are explained against the progress report too.

**An OSC 9;4 report can be read by the harness's own rules.** A report from a
pane whose manifest has a title rule with `region = "osc_progress"` that
matches the report is read by those rules instead of mapped by the sequence's
published meaning. No bundled manifest has such a rule, so nothing changes for
a bundled harness.

**A submitted prompt has to be taken.** After Enter, `ask-agent` and `fan` give
the pane five seconds to show that it took the prompt, the check herdr calls
`agent_prompt_stalled`. The pane shows it by turning `working`, by turning
`needs_input` when it was not on `needs_input` before, by turning `done` or
finishing a turn (`completion_seq` goes up), or by printing something after
Enter. Output counts only for a pane whose harness has no screen or title rule
that reports `working`, which today leaves out Claude Code, Codex, Gemini CLI,
opencode and every other harness with such a rule: a TUI that read Enter as a
newline redraws its input box, so output from it proves nothing.

- `ask-agent` fails with the new code `prompt_stalled` when the pane showed none
  of that. It used to wait out `settle` and return an empty reply with
  `settled_by: idle`. The question was typed either way, and the ask is still
  recorded in the ring. The new param `stall_timeout` (milliseconds, default
  5000) sets the window.
- The `settle` clock of `ask-agent` starts when the pane took the question,
  not when Enter was sent, so a pane that is quiet before it reacts is not
  counted as having answered.
- `fan` records the new `prompt_status` value `stalled`, with a `prompt_note`,
  where it used to record `sent`. A prompt stays `pending` for the few seconds
  the check takes. A client that does not know `stalled` should treat it like
  `not_sent`, which is what `tuios fan --wait` from an older build prints.

**A message from `human` says whether it is verified.** Any caller can send
`send-agent-message` with `from: "human"`, and such a message used to be stored
exactly like the person's reply from the mail overlay. Now:

- The attach reply (`AttachedPayload`) carries `human_nonce`, a fresh random
  secret per attach. It is an additive field, empty from an older daemon.
- `send-agent-message` takes a new param `human_nonce`. The tuios client sends
  it with a reply from the mail overlay, and only when it has one, since an
  older daemon refuses the param.
- A message from `human` is stored with `verified_human: true` when
  `human_nonce` matches a client attached to the same session now, over the
  same kind of connection: a nonce issued to an attach on the link socket
  verifies only a send on the link socket, and a local one only a local send.
  Otherwise it is stored with `claimed_human: true`. The send result and
  `read-agent-messages` both report the two fields, and the mail overlay and
  `tuios read-agent-messages` show a claimed one as unverified.
- Over a link, the hub relays the stream without reading it, so no flag in the
  request can stand for a check the hub made. A client attached through the
  link got its nonce from this daemon, and its reply verifies against that
  attach; every other `from: "human"` over the link is claimed.

The nonce alone is not an identity, since an agent can attach too. The next
entry closes that.

**A process inside a pane cannot act as the person.** The daemon reads the
pid of every caller from its socket (`SO_PEERCRED` on Linux, `LOCAL_PEERPID`
on macOS) and counts the caller as inside a pane when the daemon is one of its
ancestors, when its controlling terminal is one of the daemon's pane
terminals, or when its environment names one of the daemon's windows in
`TUIOS_PANE_ID` or `TUIOS_WINDOW_ID`, or the daemon's socket in
`TUIOS_SOCKET`. The last two also place the client's hook commands and dock
components, which are automation and not the person. A process whose
record cannot be read counts as inside a pane. docs/AGENT_STATE.md, "Who can
act as the person", has the threat model.

- `send-agent-message` and `ask-agent` with `from: "human"` from such a caller
  fail with the new code `forbidden` and store nothing. `send-agent-message`
  used to store the message as `claimed_human`, and `ask-agent` used to record
  the ask as from human. A caller outside every pane is served as before.
- The attach reply to such a caller carries no `human_nonce`, so nothing it
  sends verifies.
- `verified_human` also needs the sender to be allowed to act as the person,
  and, where the kernel gave both pids, to be the process that holds the
  attach. The tuios client sends its reply from the process that attached, so
  its replies still verify. A nonce copied to another process does not.
- `read-agent-messages` with `to: "human"` from such a caller is served as a
  peek: it marks nothing read, and the result carries the new field
  `peek_forced: true`. It used to mark the person's mail read.
- A state push from an attached client inside a pane no longer marks the
  focused window's finished turn seen, so `finished_unread` stays true until
  the person looks.
- Over a link, only a stream the hub vouched for can verify. The hub puts
  `{"human":true}` in the stream's open frame when the process that called
  `open-host-connection` may act as the person on the hub; the open frame's
  payload used to be empty, and an empty payload still means not vouched. The
  proxy on the far machine dials the new link-human socket
  (`<socket>.link-human`) for a vouched stream and the plain link socket for
  any other. An attach through the plain link socket gets no nonce, and a
  `from: "human"` send over it is `claimed_human`. A far daemon from before
  this change has no link-human socket, so the proxy falls back to the plain
  one; a hub from before it vouches for nothing, so no attach through it
  verifies on a far daemon that has the change.
- Windows and the BSDs do not give this build the peer's pid. There every
  caller is treated as before, and the nonce is the only proof.

**A reply the person did not type is not signed as theirs.** The tuios client
sends a reply from its mail overlay without the attach nonce when any key that
`send-keys`, `run-command` or a tape script routed to the client opened,
edited or sent the reply line, so the daemon stores it as `claimed_human`. The
reply line reads `automated reply:` while that is so. Such a reply used to be
signed like one typed at the keyboard.

**The Inbox.** The daemon keeps one attention queue over every session, read
with the new verbs `list-attention` and `dismiss-attention` and followed with
the new event type `attention` (see [list-attention](#list-attention)). What
changes for an existing caller:

- `subscribe` with no `types` filter now also delivers `attention` events. A
  consumer that switches on `type` and ignores the ones it does not know is
  unaffected; one that treats every unknown type as an error should filter.
- The replay ring holds `attention` events like any other retained event, so a
  resume with `after_seq` replays them.
- `EventTypeNames`, and so the accepted set of `subscribe`'s `types` param in
  `list-verbs`, gains `attention`.
- The error catalog gains `not_human`, raised only by `dismiss-attention`. Its
  nonce is checked the way a reply from `human` is, so a caller inside a pane
  is refused even with a live nonce.
- Dismissing a `finished` item marks the pane's turns seen, so its
  `finished_unread` in `list-agents` goes false, the same as focusing the pane
  in a client. Dismissing a `mail` item marks the person's mail in that thread
  read, the same as reading it in the mail overlay, and the attached clients get
  the usual read receipt.
- The daemon writes the queue to `attention/items.json` under the session state
  directory, mode 0600, and reads it back on start. Only `finished` and
  `errored` items are kept across a restart; see
  [list-attention](#list-attention).
- A pane that stays on `needs_input` or `errored` and reports a new kind,
  message or name updates its Inbox item. No `agent-state` event is sent for
  it and no hook fires, the same as before: the state did not change.

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
`name`, `type` (`string`, `int`, `bool`, `[]string`, `[]int`, or `object` for
a JSON object such as `set-agent-meta`'s `tokens`), `description`, and
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
| `session_exists` | new-session was given a name the daemon already holds. |
| `window_not_found` | The window target did not resolve to a window. |
| `no_windows` | The session has no windows to act on. |
| `pty_not_found` | The target window has no live PTY. |
| `needs_client` | The verb needs a live renderer that is not attached. |
| `option_not_found` | No option by that path exists. The hint carries the closest match. |
| `command_failed` | A verb routed to the attached client came back failed or timed out. |
| `timeout` | A wait-for condition did not match before its timeout elapsed. |
| `not_ready` | The target agent was mid-turn, so the call declined to type at it. |
| `agent_blocked` | ask-agent declined to type at an agent on `needs_input`, because the text would answer its prompt. Nothing was typed. The hint names `capture-pane`. |
| `prompt_stalled` | ask-agent typed the question and sent Enter, and within `stall_timeout` the pane did not show that it took it. The question was typed; look at the pane before sending it again. The hint names `capture-pane`. |
| `loop_refused` | The call would loop: a pane addressing itself, or an ask that closes a cycle with one in flight. |
| `rate_limited` | The sender is over the cross-agent message rate cap. |
| `not_human` | Only the person at an attached client may make this call, and it carried no nonce from a live attach. `dismiss-attention` raises it. |
| `no_keyboard` | The target is the person's inbox, `human`, which has no pane to type into. |
| `forbidden` | The caller may not do what it asked. A process inside a pane of this daemon cannot send or ask as `human`. Nothing was done. |
| `protocol_mismatch` | The caller's protocol version is outside the range this daemon serves. Only `hello` produces it. |
| `unknown_host` | No host by that name is configured. Host names are matched exactly. |
| `host_unreachable` | The host is configured and is not answering. Nothing was queued. |
| `host_refused` | The host's link is up and cannot take another connection. |
| `unknown_pane` | This daemon is not running a pane with that id. |
| `internal` | An unexpected server side failure. |
| `not_worktree` | The session is not in a git worktree, so there is nothing to remove or diff. |
| `worktree_dirty` | remove-worktree refused: the worktree holds uncommitted changes and neither `stash` nor `force` was passed. Nothing was removed. |
| `git_failed` | A git command failed. The message is git's own. The repository is as it was. |

Codes are stable and additive: existing codes never change meaning, and a new
code is only ever introduced for a condition that previously had none. A client
should treat an unrecognized code as a generic failure and fall back to
`message`. The live catalog with descriptions is in the `list-verbs` result.

## How verbs interact with an attached client

Read verbs (`list-sessions`, `session-info`, `list-windows`, `get-option`) and
input verbs (`send-text`, `capture-pane`, `resize`) always answer from daemon
owned state and the daemon owned PTYs, so they work with or without an attached
TUI.

`new-window`, `close-window` and the `RenameWindow` command always act on daemon
owned state, attached or not. Adding a window to the window set with a PTY under
it, removing one and killing its PTY, and naming a window are the daemon's to do;
an attached client is told what happened and re-renders. There is no second
implementation for these and no round trip to a client that can time out. This is
also the path a keystroke takes: pressing the create or close chord in an
attached TUI sends the same command the CLI would.

The one thing the daemon cannot decide about a window it creates is where the
window goes, because it has no viewport and attached clients may have different
ones. Rather than guess, it sets `unplaced` on the window it hands out. A client
that receives an unplaced window puts it where it would have put a window of its
own and clears the flag by pushing the geometry it chose. A window state without
the field is placed, so state written before this existed is read exactly as
before.

The verbs a live renderer still has to own to stay in sync (`send-keys` and the
live apply half of `set-option`) route to the attached TUI when one is present
and act on daemon owned state otherwise. The routing is transparent to the
caller: it is still one request and one response.

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

Layout is split the same way, along the line between intent and pixels. The
daemon carries the layout intent: `layout_mode` (`bsp`, `master-stack` or
`scrolling`), the BSP tree per workspace, the split ratios, the master and stack ratios, the
tiling scheme, and `num_workspaces`. It does not carry the pixel rectangles for
tiled windows, because those depend on the viewport of whichever client is
rendering, and two clients attached at different sizes must derive different
rectangles from the same topology. So intent persists across a detach and each
client re-tiles from it.

`layout_mode` and `num_workspaces` are both additive and both mean "unstated"
when absent: a client that receives a state without `layout_mode` leaves its own
layout alone rather than resetting to a default, and the daemon falls back to
nine workspaces when no client has told it otherwise. Before `layout_mode`
existed the BSP tree survived a reattach but the mode selecting between layouts
did not, so a scrolling session came back as a BSP one.

A `base_version` of `0` means a client that predates state versioning. It cannot
say what it saw, so its pushes are applied as sent, exactly as before. Input mode
is not part of session state at all: it is per viewer, so one client switching to
terminal mode no longer switches every other client with it.

`kill-session` destroys the session for every client, not just the caller. Each
attached client is told the session ended and exits with a non-zero status, so a
script that kills a session does not leave a user staring at a dead UI. The
`session-closed` event fires on the event stream at the same time.

## Verbs

This catalog is deliberately partial: it documents the verbs whose semantics
need prose. `tuios list-verbs` is the authoritative, always-current list of
every verb the daemon registers, generated from the same tables the request
validator uses.

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
  "num_workspaces": 9,
  "window_count": 3,
  "tiling_mode": "tiling",
  "layout_mode": "bsp",
  "width": 120,
  "height": 40,
  "tui_attached": true
}}
```

`tiling_mode` says only whether tiling is on (`tiling` or `floating`) and keeps
doing so, because callers already dispatch on those two values. `layout_mode`
says which tiling layout is in use (`bsp`, `master-stack`, `scrolling`, or
`unknown` when no client has reported one yet).

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

Params, all optional: `session`, `name` (window name; omit to use the shell's
title), `workspace` (workspace number; omit for the current one), `cwd`
(directory to start in; omit to inherit the daemon's), `focus` (default true;
pass false to leave the focus where it is), `command` (argv to exec instead of a
shell, not parsed by any shell; the window closes when it exits), and `host`
(run the window's process on a machine from the `[hosts]` table; omit or pass
`local` for this machine).

Request:

```json
{"verb": "new-window", "params": {"session": "work", "name": "build"}}
```

Response:

```json
{"result": {"type": "window_created", "window_id": "9a3c...", "name": "build"}}
```

### popup

Open a popup: a floating pane that runs one command and closes when the command
exits. Needs an attached client, and fails with `needs_client` when there is
none.

Params: `command` (required argv), `session` (optional), `width` and `height`
(optional, cells such as `"60"` or a share of the pane region such as `"60%"`,
default `"80%"` and `"60%"`), `name`, `cwd` and `workspace` (all optional).

The size the caller asks for is session state and the rectangle it resolves to
is not. Each attached client centres the popup in its own pane region, the way
each client computes its own zoom box.

Request:

```json
{"verb": "popup", "params": {"session": "work", "command": ["fzf"], "width": "60%"}}
```

Response:

```json
{"result": {"type": "popup_opened", "window_id": "9a3c...", "name": "fzf",
            "workspace": 1, "pty_id": "4bff...", "width": "60%", "height": "60%"}}
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
- `resolved` (optional bool): rewrite ANSI index colours to 24-bit RGB so the
  capture matches what a themed client paints. Index colours are the SGR forms
  `30-37`/`90-97` (foreground), `40-47`/`100-107` (background) and
  `38;5;n`/`48;5;n`; each maps through the palette when it names one of the
  theme's sixteen, and otherwise through the fixed formulas of the standard
  256-colour cube (indices 16-231) and grey ramp (232-255), which every
  consumer draws identically. True colour (`38;2;r;g;b`) passes through
  untouched, as does any parameter that is not a plain integer: a colon-coded
  sub-parameter such as `4:3` (curly underline) travels verbatim instead of
  being flattened into another attribute. Asking for `resolved` implies
  styling, so the reply reports `styled: true`. Default is `false`.
- `palette` (optional []string): the 16 hex colours (`#rrggbb`) a client's
  theme paints indices 0-15 with, used by a `resolved` capture. When present it
  must have exactly 16 entries; anything else is rejected with
  `invalid_params`. Absent, `resolved` falls back to the xterm defaults.
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
{"result": {"type": "pane_content", "source": "recent", "styled": false, "resolved": false, "content": "..."}}
```

The reply echoes `resolved` so a consumer can tell whether the capture it
received was rewritten. Without `resolved`, a `styled` capture emits the
colours exactly as the guest sent them: a program that draws with SGR `31`
comes back as `\x1b[31m`, and the consumer resolves that index against its
own palette. That is the intended contract: appearance is client-owned and
the daemon has no theme. `resolved` exists for consumers that render the
capture verbatim and therefore need the colours already resolved; it takes
the palette explicitly so the daemon still knows nothing about themes.

```json
{"verb": "capture-pane", "params": {"session": "work", "source": "visible", "styled": true, "resolved": true, "palette": ["#45475a", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#bac2de", "#585b70", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#a6adc8"]}}
```

Content is physical rows, not logical lines: a line longer than the pane width
was wrapped by the emulator and comes back as several rows, so `lines`, `start`
and `end` count wrapped rows. There is no unwrapped capture. Earlier builds
documented a reserved `recent-unwrapped` source that was accepted but behaved
exactly like `recent`; it is now rejected rather than silently ignored, because
the emulator does not record which rows are continuations and unwrapping them
would mean guessing. A caller that needs logical lines should widen the pane
with `resize` before capturing.

### screenshot

Render a window to a styled image file. The picture is drawn from the pane's
own cells, so colors, styles and OSC 8 links are exact, and the window chrome
in it is drawn by the renderer rather than scraped off a client's border. It
runs daemon side for the reason `capture-pane` does, so it answers on a
detached session with nobody attached.

Params:

- `session` (optional), `window` (optional).
- `format` (optional): `png` (the default), `svg`, `ansi`, `html` or `txt`.
  Any other value is rejected with `invalid_params`.
- `frame` (optional): `window` (the default), `plain` or `none`. `ansi` and
  `txt` are frameless whatever this says.
- `theme` (optional): render in this theme instead of the session's. Indexed
  and basic cells re map cleanly; truecolor cells, which is most modern TUI
  output, are unchanged by it.
- `scrollback` (optional bool): put the pane's history above the screen.
- `lines` (optional int): bound the history to the last N rows. Needs
  `scrollback`.
- `cursor` (optional bool): draw the cursor cell.
- `out` (optional): write here instead of generating a name under
  `screenshot.directory`.

Everything else about the picture comes from the `screenshot.*` options.

Request:

```json
{"verb": "screenshot", "params": {"session": "work", "window": "build", "format": "svg"}}
```

Response:

```json
{"result": {"type": "screenshot", "path": "/home/u/Pictures/tuios/tuios-build-2026-08-25-204003.svg",
            "host": "daemon", "format": "svg", "cols": 78, "rows": 22, "bytes": 2407, "warnings": []}}
```

The verb always writes a file and returns its path. There is no bytes in the
envelope route: the protocol is line delimited JSON, a scrollback PNG is
megabytes, and a second delivery path is a second set of bugs. `host` names the
machine the path is on, so a script never has to assume; a CLI reaching the
daemon over its unix socket is on that machine by construction.

`warnings` is always present and empty when there is nothing to say. The one
that matters is the no theme case: tuios can never read the host terminal's
palette, so a session with no theme set renders basic and indexed colors in the
xterm reference defaults and says so. Truecolor cells are exact regardless, and
`theme` re renders in any installed palette.

A screenshot cannot contain kitty images a pane is displaying. Those are host
side placements, not cells; the capture shows whatever cells sit under them.

Region and full screen captures are not verbs. They need a viewport, a layout
and composed chrome, which only an attached client has, and they are reached
from capture mode in the TUI.

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

### new-worktree

Create a git worktree of a repository and a session in it. The worktree goes
under `$XDG_DATA_HOME/tuios/worktrees/<repo>/<branch>`, and the session is
named `<repo>-<branch>` with every slash in the branch turned into a hyphen. A
branch that does not exist is created from `base`, or from HEAD.

Params: `repo` (required, a directory inside the repository), `branch`
(required), `base`, `name`, `command` (argv for the first window instead of a
shell).

Request:

```json
{"verb": "new-worktree", "params": {"repo": "/src/api", "branch": "feat/retry", "base": "main", "command": ["claude"]}}
```

Response:

```json
{"result": {"type": "worktree_created", "session": "api-feat-retry", "repo": "api", "repo_root": "/src/api",
 "branch": "feat/retry", "created_branch": true, "path": "/home/u/.local/share/tuios/worktrees/api/feat-retry",
 "window_id": "...", "pty_id": "..."}}
```

A session whose first window starts inside a worktree made by hand is recorded
the same way, by reading the directory. `list-sessions` carries the record as
`worktree` on the session, and it is what the rail groups by.

### list-worktrees

List the sessions whose directory is a git worktree.

Params: `repo` (filter by repository name), `group` (filter by fan-out stem),
`changes` (run git status in each and report `changes` and `ahead`; off by
default).

Request:

```json
{"verb": "list-worktrees", "params": {"changes": true}}
```

Response:

```json
{"result": {"type": "worktree_list", "total": 1, "worktrees": [
  {"session": "api-feat-retry", "repo": "api", "repo_root": "/src/api", "branch": "feat/retry",
   "path": "/home/u/.local/share/tuios/worktrees/api/feat-retry", "base": "main", "group": "",
   "managed": true, "gone": false, "state": "working", "harness": "claude-code", "windows": 1,
   "attached": false, "prompt_status": "", "prompt_note": "", "changes": 3, "ahead": 1}]}}
```

`gone` is true when the worktree directory no longer exists. The session is
kept, so what its agent printed can still be read. `state` is the agent state
rolled up over the session's windows.

### remove-worktree

Remove a worktree session's worktree with `git worktree remove`, and kill the
session. Uncommitted changes are refused with `worktree_dirty` unless `stash`
moves them into the repository's stash as `tuios: <branch>` or `force` discards
them. `force` is the only option that discards work. The branch is never
deleted, and the daemon never runs `git worktree prune`.

Params: `session` (required), `stash`, `force`, `keep_session`.

Request:

```json
{"verb": "remove-worktree", "params": {"session": "api-feat-retry", "stash": true}}
```

Response:

```json
{"result": {"type": "worktree_removed", "session": "api-feat-retry", "branch": "feat/retry",
 "path": "/home/u/.local/share/tuios/worktrees/api/feat-retry", "repo": "api", "changes": 3,
 "stashed": true, "stash_message": "tuios: feat/retry", "discarded": false, "session_killed": true, "branch_kept": true}}
```

### fan

Fan one prompt out across several agents. Creates `count` worktrees and
sessions, starts the agent in each, and types the prompt into each agent once
it is ready to read: `idle` or `done`. An agent in `needs_input` is left for
the person to answer, and the prompt is typed after. `unknown` is not ready for
a harness whose manifest has an idle rule, which reaches `idle` from its prompt
box or title (see Changes to existing verbs); if it never does, the prompt is
left `not_sent` when the wait ends, and `prompt_note` says to send it with
`send-text`. The prompt goes in as one paste and is submitted with the
harness's submit key, a carriage return for every bundled harness (see Changes
to existing verbs). The agent then has five seconds to
show it took the prompt, the same check `ask-agent` makes. The verb returns as
soon as the sessions exist. `list-worktrees` reports `prompt_status` per
session: `pending`, `sent`, `not_sent` with a `prompt_note`, or `stalled` with
a `prompt_note` when the prompt was typed and the agent showed no sign of taking
it. A stalled prompt may still be in the agent's input box.

Params: `count` (required, 1 to 16), `agent` (required, a harness id or the
program name: `claude`, `codex`, `gemini`), `prompt` (required), `repo`
(required), `base`, `name` (branch stem), `ready_timeout` (milliseconds,
default 600000).

Request:

```json
{"verb": "fan", "params": {"count": 3, "agent": "claude", "prompt": "Add a retry to the client.", "repo": "/src/api"}}
```

Response:

```json
{"result": {"type": "fan_started", "group": "fan/add-retry-client", "repo": "api", "agent": "claude-code",
 "command": "claude", "prompt": "Add a retry to the client.", "total": 3, "sessions": [
  {"session": "api-fan-add-retry-client", "branch": "fan/add-retry-client", "path": "...", "window_id": "..."},
  {"session": "api-fan-add-retry-client-2", "branch": "fan/add-retry-client-2", "path": "...", "window_id": "..."},
  {"session": "api-fan-add-retry-client-3", "branch": "fan/add-retry-client-3", "path": "...", "window_id": "..."}]}}
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

### set-agent-state

Record the agent state a pane reports. Params: `session`, `window`, `state`
(required), `message`, `source` (default `report`), `harness`, and five fields
a hook reporter adds, each optional:

- `kind`: `approval` or `question`, what a `needs_input` state waits for. Only
  valid with `needs_input`. Stored as the window's `agent_kind` and reported by
  `get-agent-state` and `list-agents` as `blocked_by`. Without it the kind is
  guessed from `message`, as described under Changes to existing verbs.
- `agent_session_id`: the harness's own conversation id. Stored as the window's
  `agent_session_id`, persisted, and kept when the agent exits. It also turns
  on the nested-session guard below.
- `transcript_path`: the transcript file the harness writes. For a harness whose
  manifest has a transcript reader, the window is joined to exactly that file.
  It is held in daemon memory and never synced.
- `if_state`: comma-separated states. The report applies only while the window
  is in one of them.
- `harness_pid`: the pid of the harness process that ran the hook. Read only
  with `agent_session_id`, and held in daemon memory. It lets a new session
  from the same harness process pass the nested-session guard below.

Request:

```json
{"verb": "set-agent-state", "params": {"session": "work", "window": "build", "state": "working", "if_state": "needs_input", "agent_session_id": "5f1c"}}
```

Response:

```json
{"result": {"type": "agent_state_set", "state": "done", "message": "", "source": "report", "applied": false, "reason": "if_state"}}
```

`state` is what the window shows after the call. `reason` is present only when
`applied` is false: `outranked` (a higher-ranked source owns the window),
`if_state` (the condition did not hold), `foreign_session` or
`foreign_harness`. The last two refuse a report carrying `agent_session_id`
while the window's own harness is `working` or `needs_input` by its own report
and the report names a different session, or comes from a different harness:
a nested run, such as a `claude -p` a tool call started inside the pane. At rest
a different session is accepted and replaces the stored id. Mid-turn, a
different session is also accepted when its `harness_pid` is the one the
window's current session was reported with: that is a new conversation in the
same process, such as `/clear` after an interrupted turn that never reported
`Stop`.

`reason` and the five hook fields are additive. A client that sends none of
the fields is handled exactly as before. A daemon older than them does not
reject them: params are decoded leniently, so it ignores the fields and applies
the report without them. A client that depends on one, `if_state` above all,
has to ask `list-verbs` for `set-agent-state` first and check the field is
listed. `tuios agent-hook` and `tuios set-agent-state --if-state` do.

### set-agent-session

Store the conversation id a harness reports for a pane, without changing the
pane's agent state, the source that holds it, or its harness attribution. It is
what `tuios agent-hook` sends for a harness whose hooks can name the
conversation but cannot be trusted with its state, so the pane's screen rules
keep deciding the state. Params: `session`, `window`, `harness` (required),
`agent_session_id` (required, at most 256 bytes), `harness_pid`.

```json
{"verb": "set-agent-session", "params": {"session": "work", "window": "build", "harness": "qwen", "agent_session_id": "5f1c", "harness_pid": 4100}}
```

```json
{"result": {"type": "agent_session_set", "agent_session_id": "5f1c", "applied": true}}
```

The id is stored as the window's `agent_session_id`, the field
`set-agent-state` writes, persisted and read back by `get-agent-state` and
`list-agents`. `agent_session_id` in the result is the id the window holds after
the call. A report naming the id the window already holds is applied and changes
nothing. Two refusals keep a nested run off the pane, with `applied: false`:

- `foreign_harness`: the window is attributed to a different harness.
- `foreign_session`: the window is `working` or `needs_input`, holds a different
  id, and that id was reported by a known harness process other than the
  report's `harness_pid`. The same process may replace its own id, and a report
  with no pid, or onto a window at rest, is applied. The daemon forgets the pid
  when the detector sees the agent leave the pane, so a harness restarted in
  the pane is not refused.

A report never attributes the window: a pane nothing has named stores the id
and stays a non-agent pane until the detector or a `set-agent-state` names its
harness.

Security: it grants a subset of what `set-agent-state` with `agent_session_id`
already grants any socket caller, the one field and no state.

Wire compatibility: a new verb. An older daemon answers `unknown_verb`, and
`tuios agent-hook` asks `list-verbs` first and sends nothing to a daemon
without it.

### resolve-pane

Name the pane a process runs in, for a hook reporter whose environment lost
`TUIOS_PANE_ID`. Params: `sid` (the caller's session id) and `pids` (its
ancestors, nearest first). A pane's shell leads the session of the pane's
terminal, so `sid` is matched first; then the first ancestor that is a pane's
shell. Only panes on the daemon's own machine are matched.

```json
{"verb": "resolve-pane", "params": {"sid": 4242, "pids": [4250, 4242]}}
```

```json
{"result": {"type": "pane_resolved", "session": "work", "window_id": "3c1f6e4e", "by": "tty", "pid": 4242}}
```

No match is `window_not_found`.

### set-agent-meta

Record display metadata about the agent in a pane: its model, how full its
context is, what the turn cost, a one-line summary. The rail draws it on the
second line of the agent's row. It is display only. Nothing reads it to decide
an agent state, a `wait-for`, an alert or a message.

Params: `session` (optional), `window` (optional, default the focused pane),
`tokens` (an object of key to string, or to `null` to remove the key; required
unless `clear` is true), `source` (optional, recorded on each key), `ttl_ms`
(optional, 0 to 86400000, default 0 for no expiry), `clear` (optional bool).

Limits: 16 keys per call and 32 per pane. A key is 1 to 24 lower-case letters,
digits, `_` or `-`, starting with a letter. A value has control characters
replaced with spaces and is cut to 80 characters. A bad key, too many keys, or
a TTL out of range is `invalid_params`; a cut value is not an error, and its
key is listed in `truncated`.

Order: a key keeps the position it first arrived in, and a new key goes at the
end in the order the `tokens` object lists it, so a feed that rewrites every
key on each tick does not reorder the row.

Lifetime: `clear` removes every key the same `source` wrote, or every key when
`source` is empty, before `tokens` is applied. Keys set with `ttl_ms` are dropped
by the daemon when it runs out, and the change reaches clients through the
ordinary state sync. All metadata clears when the agent leaves the pane, which is
the pane's agent state going to `none`.

Request:

```json
{"verb": "set-agent-meta", "params": {"session": "work", "window": "build", "source": "statusline", "tokens": {"model": "opus", "context": "42%"}, "ttl_ms": 60000}}
```

Response:

```json
{"result": {"type": "agent_meta_set", "window_id": "3f2a9c1e", "meta": {"model": "opus", "context": "42%"}, "truncated": []}}
```

`get-agent-state` and each `list-agents` entry carry the same `meta` object.

Wire compatibility: the metadata rides the window state as an additive field
(`agent_meta`). An older client drops it and draws nothing, and an older daemon
answers the verb with `unknown_verb`.

### list-attention

List the Inbox: everything waiting for the person, in every session on this
daemon. Each item is one of five kinds, and the list is grouped in this order,
oldest first inside each group:

| Kind | Opens when | Closes when |
| --- | --- | --- |
| `approval` | A pane goes to `needs_input` with `blocked_by` `approval`. | The pane leaves `needs_input`. |
| `question` | A pane goes to `needs_input` with any other `blocked_by`, or none. | The pane leaves `needs_input`. |
| `mail` | A message to `human` lands in a thread. One item per thread; `count` is the unread messages. | The person's mail in the thread is read. |
| `errored` | A pane goes to `errored`. | The pane leaves `errored`. |
| `finished` | A pane's `completion_seq` goes up as it comes to rest. | An attached client focuses the pane, the agent starts another turn (`working`), blocks, or errors. |

Every item also closes when its pane closes (mail excepted: the message is
still unread), when its session ends, and on `dismiss-attention`. A pane has at
most one blocking item, one errored item and one finished item, so a harness
repeating itself or a fan of agents moving together updates rows rather than
adding them, and an update that changes nothing publishes nothing.

A blocking or errored item follows the pane's latest report, not only its state
changes. A pane that stays on `needs_input` and reports a new `blocked_by` or
message (a harness hook that says `approval` after the screen tier already set
`needs_input`, say) updates its item: it moves between `question` and
`approval`, and its summary is the new message. The item keeps its id and its
`since`. A change of the pane's name, harness or workspace updates it the same
way. None of this is an `agent-state` event, since the state did not change,
and no hook fires for it; only the `attention` event with action `updated` is
sent.

Params: `session` (optional; unlike most verbs, omitted means every session),
`kinds` (optional list, from `approval`, `question`, `mail`, `errored`,
`finished`).

Response:

```json
{"result": {"type": "attention_list", "items": [{"id": "17", "kind": "approval", "session": "fan-3", "window": "3f2a9c1e", "workspace": 1, "harness": "claude-code", "name": "claude", "summary": "approve Bash: go test ./...", "since": 1790142942055373000, "seq": 41}], "counts": {"approval": 1, "question": 0, "mail": 0, "errored": 0, "finished": 0}, "total": 1, "seq": 1180, "boot_id": "9f2c41d07a3e8b65"}}
```

Item fields: `id` (stable, never reused on this machine), `kind`, `host` (empty
for this machine; a hub will fill it for items from linked hosts), `session`,
`window`, `workspace`, `harness`, `name`, `summary`, `options` (the answers a
prompt offers, when a source reported them; nothing fills it yet), `since`
(unix nanoseconds, when the item started waiting; an update keeps it), `seq`
(the Inbox revision of the item's last change), `thread` and `count` (mail),
`completion_seq` (finished).

`summary` is text an agent wrote. The daemon keeps it to one line, removes
control characters, masks what looks like a credential (`TOKEN=...`,
`password: ...`, `Bearer ...`) and cuts it to 160 bytes before it is stored,
sent to a subscriber or written to disk. The masking is a net for the common
shapes, not a guarantee.

`seq` and `boot_id` are the stream position the answer is current to. To follow
the Inbox without missing anything, list it and then subscribe with
`types: ["attention"]`, `after_seq` and `boot_id` from the listing. Every change
after the listing is replayed; a `gap` means list again.

Persistence: part of the queue survives a daemon restart. On start the daemon
keeps the `finished` and `errored` items whose session and pane came back. It
drops `approval` and `question` items, because the prompt died with the process
that painted it, and `mail` items, because the message ring they point into
does not survive a restart and thread ids start again from 1, so a saved item
could only be merged into an unrelated new thread. An item opened while the
saved queue is still loading keeps its id and wins over a saved item for the
same pane and kind. A saved item whose id such an item already holds is kept
under a fresh id, so no two items ever share one.

Wire compatibility: new verb and new event type. An older daemon answers
`unknown_verb`, and the tuios client then shows the Inbox as unavailable.

### dismiss-attention

Close one Inbox item for the person.

Params: `id` (required), `human_nonce` (required: the nonce from the attach
reply of a TUI client attached right now, over the same kind of connection as
this call).

Only the person may clear what is waiting for the person. An agent in a pane
has no attach and so no nonce, and gets `not_human`. The nonce is checked the
way a reply from `human` is (see "A process inside a pane cannot act as the
person" under [Changes to existing verbs](#changes-to-existing-verbs)): a
caller inside a pane of this daemon, or on a link stream the hub did not vouch
for, gets `not_human` even with a live nonce, and where the kernel gives both
pids the caller must be the process that holds the attach. A client attached to any
session may dismiss items in any session, since the Inbox spans them. A second
dismiss of the same item, or an id that is not open, is `invalid_params`.

Dismissing a `finished` item marks the pane's turns seen. Dismissing a `mail`
item marks the person's unread mail in the thread read. The other kinds only
leave the list; the pane's agent state is not touched, and a later transition
opens a new item.

Response:

```json
{"result": {"type": "attention_dismissed", "id": "17", "kind": "approval", "session": "fan-3", "dismissed": true}}
```

## Event stream

The daemon can push events instead of a caller polling. A connection that issues
the `subscribe` verb is turned into a long-lived event stream; every other
connection never receives events. Each event is one JSON line carrying a
daemon-global monotonic `seq`, the daemon's `boot_id`, a `type`, and the fields
relevant to that type. Two subscribers always see the same `seq` for the same
event.

`boot_id` is a random id the daemon picks each time it starts. A restarted
daemon numbers events from 1 again, so a `seq` only identifies an event
together with the `boot_id` it came with. Keep both if you plan to resume.

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
| `agent-state` | A window's agent state changed, whichever tier changed it. A pane ceasing to be an agent reports `none`. | `session`, `window`, `pty_id`, `state` |
| `agent-message` | A message was left in the session's ring. `window` is the recipient, or empty for a session-wide notice. Read the message itself with `read-agent-messages`. | `session`, `window` |
| `output` | A window produced output (activity signal only; the raw bytes still flow over the binary stream). | `session`, `window`, `pty_id`, `bytes` |
| `bell` | A window rang the terminal bell. | `session`, `window`, `pty_id` |
| `notification` | A window sent a desktop notification with OSC 9, OSC 777 or OSC 99. Each text field is capped at 512 bytes. For a pane attributed to a harness the notification may also move its agent state; see [AGENT_STATE.md](AGENT_STATE.md#notification-rules). | `session`, `window`, `pty_id`, `title`, `body` |
| `mode-changed` | A terminal mode toggled (for example alt-screen). | `session`, `window`, `mode`, `enabled` |
| `session-created` | A session was created. | `session` |
| `session-closed` | A session was terminated. | `session` |
| `gap` | Some events were not delivered to this connection. `reason` says why (see below). A gap has no `seq`. | `reason`, `dropped`, `boot_id` |
| `attention` | An Inbox item opened, changed or closed. `action` is `open`, `update` or `close`, and `attention` is the item as `list-attention` returns it. On `close` the item carries `closed`: `resolved`, `seen`, `read`, `dismissed`, `window_closed`, `session_closed` or `evicted`. `session` and `window` are the item's, so the usual filters apply. | `session`, `window`, `action`, `attention` |

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
- The PTY-driven events (`output`, `bell`, `notification`, `mode-changed`, `window-exit`) hang
  off the PTY rather than off window state, so they have always fired on both
  routes and are unaffected.

Ordering within a single mutation is stable: closes, then creates, then
per-window changes (`window-retitled`, `window-moved`,
`window-minimized`/`window-restored`), then `workspace-switched`, then
`window-focused`. Focus comes last because it is usually a consequence of an
earlier event in the same batch, so a consumer building a model from the stream
already knows about the window being focused by the time it is told to focus it.

Two cases are worth stating plainly because they are easy to guess wrong:

- `window-retitled` fires for an **explicit rename** (the `set-window` verb or
  the TUI's rename) and, separately, when the **shell changes its own title** via
  an OSC escape sequence. The shell-driven case is reported by the PTY, not by
  the state diff, so a shell retitling itself raises the event once, not twice.
- A change that is not a lifecycle change raises nothing. Window geometry,
  z-order, and alt-screen flags move constantly as a TUI renders and re-tiles;
  none of them produce events, so an attached client does not flood the stream.

Restoring a session (daemon cold start, or `tuios resurrect`) raises
`session-created` followed by a `window-created` for each restored window, since
from a subscriber's point of view those windows come into existence at that
moment. A plain subscribe carries what happens from the subscription onward,
and the ack's `seq` is the baseline. Use `list-windows` to establish initial
state, then follow the stream. A subscriber that reconnects can ask for what it
missed with `after_seq` (see "Resuming a stream" below).

### subscribe

Open the event stream on this connection.

Params:

- `session` (optional): only events from this session. Omit it for events from
  every session. This is unlike most verbs, where an omitted session means the
  most recently active one.
- `window` (optional): only events about this window id. The filter compares
  window ids, so a window name matches nothing.
- `types` (optional): event types to include; empty means all.
- `queue` (optional): per-connection queue size; defaults to 256.
- `after_seq` (optional): resume. Replay the retained events with a higher
  `seq` before streaming live. `0` replays everything the daemon still holds.
- `boot_id` (optional, needs `after_seq`): the boot id `after_seq` came with.

Request:

```json
{"id": 1, "verb": "subscribe", "params": {"session": "work", "types": ["output", "bell"]}}
```

Ack response (the stream begins after this line):

```json
{"id": 1, "result": {"type": "subscribed", "seq": 42, "boot_id": "9f2c41d07a3e8b65"}}
```

Subsequent lines are events, for example:

```json
{"seq": 43, "type": "output", "session": "work", "window": "1f3c...", "pty_id": "9ab2...", "bytes": 64, "boot_id": "9f2c41d07a3e8b65", "time": 1737200000000000000}
```

A second `subscribe` on the same connection is rejected with `invalid_request`.

### Resuming a stream

The daemon keeps the last 4096 events in a replay ring, apart from `output`
events, which fire on every PTY read and would push everything else out within
seconds. A subscriber that kept the `seq` of the last event it read, and the
`boot_id` that came with it, can reconnect and pass both:

```json
{"id": 1, "verb": "subscribe", "params": {"types": ["agent-state"], "after_seq": 118, "boot_id": "9f2c41d07a3e8b65"}}
```

The ack then carries `replayed`, the number of events that follow before the
live stream:

```json
{"id": 1, "result": {"type": "subscribed", "seq": 131, "boot_id": "9f2c41d07a3e8b65", "replayed": 3}}
```

Every replayed event has a `seq` above `after_seq` and at or below the ack's
`seq`, and every live event has a higher one, so no event is delivered twice.
The replay honours the same `session`, `window` and `types` filter as the live
stream.

When the replay cannot be everything the caller missed, a gap marker comes
first. Read current state again after a gap (`list-agents`, `list-windows`)
rather than assuming you saw every change:

| `reason` | Meaning | What follows |
| --- | --- | --- |
| `evicted` | Events after `after_seq` have already left the ring. | The events the ring still holds, then live. |
| `boot_changed` | `boot_id` names another daemon start, or no `boot_id` was passed and `after_seq` is above anything this daemon has assigned. The numbers are not comparable. | Live events only. |
| `not_retained` | The filter admits `output` events, and at least one was published after `after_seq`. Output is never replayed. | The retained events, then live. Leave `output` out of `types` for an exact replay. |

Passing this daemon's own `boot_id` with an `after_seq` it has not reached yet
is refused with `invalid_params`, and so is `boot_id` without `after_seq`.

### Slow subscriber policy

Each subscribed connection has a bounded queue. When it is full the daemon drops
the event and counts the drop rather than blocking; the next event delivered to
that connection is preceded by a gap marker:

```json
{"type": "gap", "dropped": 12, "reason": "overflow", "boot_id": "9f2c41d07a3e8b65"}
```

so the connection learns it fell behind (the `seq` values also jump). One slow
reader never stalls the daemon or any other subscriber. A client that reconnects
after an overflow can resume from the last `seq` it read and get the dropped
events back from the ring, as long as they were not `output` events and have
not been evicted.

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
default 500), `until` (agent state names, comma-separated, for `agent-state`),
`thread` (any message id in a thread, to narrow `agent-message` to that
thread), `any_session` (bool, for `agent-state` only: watch every session and
take no `session` or `window`), `timeout` (milliseconds; default 30000).

Conditions:

- `window-output` matches `pattern` against the target window's captured
  content. Checked once immediately, then re-checked as the window produces
  output.
- `window-exit` resolves when the target window's shell process exits.
- `window-idle` resolves after the target window produces no output for `idle`
  milliseconds.
- `session-exists` resolves when a session named `session` exists.
- `agent-state` resolves when a window's agent state becomes one of the states
  named in `until` (checked once immediately, so a pane already in the state
  resolves at once). With `window` it watches that pane and fails with
  `window_not_found` if the pane closes mid-wait; without `window` any window
  in the session matches, which is the "tell me when any agent here needs
  input" shape. With `any_session` any window in any session matches,
  including sessions created during the wait; passing `session` or `window`
  with it, or using it with another condition, is `invalid_params`. The result
  names the `session`, the `window` and the `state` that matched.
- `agent-message` resolves when a message arrives. With `window` it watches
  that inbox, matches the first unread message already there, and fails with
  `window_not_found` if the inbox's window closes mid-wait. Without `window` it
  matches any message left in the session after the wait began. `thread`
  narrows either form to one thread. The result names the message (`message_id`,
  `kind`, `from`, `subject`) and never carries its body: read it with
  `read-agent-messages`.

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

`tuios subscribe` does the last one without socat, and resumes with
`--after-seq` and `--boot-id`.

The tuios CLI speaks this protocol directly. `tuios ls`, `tuios kill-session`,
`tuios send-keys`, `tuios capture-pane`, `tuios list-windows`,
`tuios session-info`, `tuios set-config`, and `tuios get-config` are all verb
protocol clients.
