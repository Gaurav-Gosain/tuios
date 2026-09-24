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
  "sessions": 2,
  "link_policy": true,
  "pane_grants": true
}}
```

`link_policy` says the daemon holds calls from other machines to a link
policy (see [What a linked machine may do here](#what-a-linked-machine-may-do-here)).
`tuios stdio-proxy` reads it before it would reach the daemon on its own socket
for a link, and refuses when it is set. It is absent from an older daemon.

`pane_grants` says the daemon holds calls from panes to their grants (see
[pane-grants](#pane-grants)) and takes a pane's token with `pane-grants`. On a
platform where the daemon cannot read the peer's pid, the `tuios` CLI reads it
and then presents `$TUIOS_PANE_ID` and `$TUIOS_PANE_TOKEN` on every
connection it opens from a pane. It is absent from an older daemon.

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

**A call from another machine is held to a link policy.** Every verb and
every binary message that arrives over a link is checked against what the
`[hosts]` table on the receiving machine lets the calling machine do, before
it runs (see [What a linked machine may do here](#what-a-linked-machine-may-do-here)).
A refused call does nothing and fails with `forbidden`, and a hint naming the
capability and the table that grants it. The default lets a link do what it
could before, with these exceptions:

- `respond`, `reply-approval`, `dismiss-attention` and the new
  `release-agent-message` need the `respond` capability, which the default
  does not grant. Over a link they used to be refused only for want of a
  verified nonce (`not_human`); with the default policy they are now
  `forbidden` first. `tuios respond -w HOST:SESSION:WINDOW` needs
  `allow = [..., "respond"]` on that host for this machine.
- `open-host-connection` over a link, which relays on to the far machine's own
  hosts, needs every capability, so the default refuses it.
- A binary message a policy refuses is answered with `MsgError` code 10
  (`ErrCodeForbidden`), and the connection stays open.
- Calls on the daemon's own socket are unchanged.

**Mail from another machine can be held for the person.** With
`hold_mail = true` for the sending machine, `send-agent-message` over a link
to anyone but `human` stores the message addressed to `human`, with `held`,
`held_for` and `held_for_label` naming the window it was for. The reply
carries `held: true` and `held_for`, and `to` is `human`. The agent does not
see it until the person passes it on with `release-agent-message`. The Inbox
mail item carries `held_id` and `held_for`. Without `hold_mail` nothing
changes.

**A pane on another machine can outlive a dropped link.** `open-pane` takes
`resumable`, and the far daemon then keeps the process for its
`hosted_grace` for the asking machine after the connection drops, returning
`resume_token` and `grace` (seconds). `open-pane` with `resume` reattaches it.
The asking daemon now always sends `resumable`; a far daemon that does not
know it refuses it with `invalid_params`, and the asking daemon sends the
request again without it, as it does for `window`. What changes:

- A window on another machine no longer closes when the link drops, for up to
  the far grace. `list-windows`, `session-info` and the state pushed to clients
  carry `host_link: "reconnecting"` and `host_link_until` (unix seconds) while
  it is being reattached. A caller that waited for `window-exit` on a link drop
  now waits for the grace, or for the process to exit.
- Keystrokes to such a window while it is reconnecting are refused, not queued.
- Closing such a window sends `close-pane` to the far daemon, which ends the
  process at once.
- On the far machine, a pane opened with `resumable` is still read while no
  connection is attached, so its process no longer blocks on a full pty while
  the link is down. A pane opened without it behaves as before.

**Mail for a machine whose link is down can wait for it.** `send-agent-message`
takes `host`, a machine in the `[hosts]` table: the daemon delivers the message
to `session` there over its link and returns that machine's answer with `host`
and `queued: false`. When the link is down it keeps the message instead, on
disk, and answers `{"type": "agent_message_queued", "queued": true,
"queue_id": 3, "waiting": 1, ...}`; the messages go in order when the link is
back. At most 64 wait per machine and 256 in all; past that the send is
`rate_limited`. A send is also queued when the call fails without an answer
from the far machine while the link stays up (a timeout, no room for another
stream), and when earlier mail for that machine still waits or is being
delivered, so it cannot arrive ahead of it. With the link up the queue is
tried again at once, then after 1 second, doubling to 30 seconds. An error
the far machine answers is returned as before and is not queued. What changes
for existing callers:

- `list-attention` has a seventh kind, `outbox`, one item per machine with
  mail waiting or refused. A consumer that switched on the six kinds sees a
  kind it does not know.
- `dismiss-attention` on an `outbox` item discards the mail still waiting for
  that machine, and answers `for_host` and `discarded`.
- `list-hosts` rows carry `queued`.
- `tuios send-agent-message -s HOST:SESSION` falls back to this when the host
  is unreachable, instead of failing with `host_unreachable`.
- A message queued from `human` arrives on the far machine as `claimed_human`:
  no nonce the far daemon would honour survives the wait. `host` is refused
  over a link and dropped from a hosted pane's report channel.

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

**A restore keeps each pane's `agent_session_id`, and the Inbox has a
`resume` kind.** A daemon restart used to bring every window back with its
`agent_session_id` empty, although the state file held it: the restore's state
push took daemon-owned fields from the empty session it had just made. The id
now comes back on every restored window that ran on this machine (a window that
ran on another machine drops it, since the conversation is over there), so
`get-agent-state` and `list-agents` return it after a restart. With it, the
restore offers to resume each conversation whose harness has a `[resume]`
command and whose pane had an agent running when the state was saved (a saved
`agent_state` other than none, or an `agent_harness`; a pane whose agent had
exited keeps its id and gets no offer), as `daemon.resume_agents` says. A
restored window also comes back with no agent state: `agent_state`,
`agent_message`, `agent_kind`, `agent_state_at`, `agent_harness`, `agent_meta`
and `foreground_cmd` are cleared, since its shell is new. They used to come
back as saved, so a restored pane at a fresh prompt reported the old agent as
`working` until it closed. Clearing them is also what keeps an offer from
coming back on every later restart. In the default `ask` mode,
`list-attention` gains one item of the new kind `resume` per such pane, with
the command as its summary, and subscribers see an `attention` event opening
it. `AttentionKindNames`, the order `list-attention` groups by and the
`counts` object gain `resume`, between `errored` and `finished`. A client that
groups by kind and does not know `resume` should treat it like any unknown
kind. Windows also carry a new field, `agent_session_harness`, the harness
the id belongs to; `set-agent-state` with `agent_session_id` and
`set-agent-session` write it with the id, and a `set-agent-session` report that
names the stored id under a different harness is now applied (it updates the
harness) rather than answered as unchanged.

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
- The error catalog gains `not_human`, raised by `dismiss-attention` (and,
  since peek and respond, by `respond`). Its
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

**Peek and respond.** Two new verbs, [peek-prompt](#peek-prompt) and
[respond](#respond), read the prompt an agent is blocked on and answer it
without attaching. What changes for an existing caller:

- The error catalog gains `prompt_changed`, raised only by `respond`.
- `not_human` is now raised by `respond` as well as `dismiss-attention`.
- The harness manifest schema gains an optional `[answers]` block under a
  `needs_input` screen or title rule (see
  [the answers block](AGENT_STATE.md#the-answers-block)). `schema_version`
  stays 1: an older build ignores the block, and a manifest without one loads
  as before. A block this build rejects fails the manifest's load.
- The config file gains `[daemon] respond_from_shell`, false by default. It is
  not an option `set-option` can change.
- The bundled Claude Code and Codex manifests declare answers. Their rules
  match exactly what they matched before; only the block is new.

**Approvals answered from the Inbox.** The new verbs `request-approval` and
`reply-approval` let a harness hook hold a permission prompt until the person
answers it in the Inbox (see [request-approval](#request-approval)). It is off
unless `[agents.approvals]` in the config names the harness. What changes for
an existing caller:

- An `approval` item gains `request_id` and `expires` while a hook holds it,
  and `options` is now filled then: the decisions `reply-approval` takes for
  it, with `always_scope` (the rules always adds) when `options` holds
  `always`. All four are absent when nothing holds the item, which is every
  item when approvals are off.
- While a hook holds an `approval` item, its `summary` is the held call's
  line, the one `request-approval` was given, and a new message the pane
  reports meanwhile does not replace it. When the hold ends the item shows the
  newest message reported during it. With nothing held, a new message updates
  `summary` as before.
- The `attention` event's `closed` gains the reason `answered`. The closing
  item then carries `answer` (`once`, `always` or `deny`) and `answered_by`
  (the id of the client that answered).
- A decision from `reply-approval` moves the pane from `needs_input` to
  `working` straight away, with an ordinary `agent-state` event, instead of
  waiting for the harness's next report.
- When an attached client of the person moves the session's focus to a pane
  whose approval is held, the hold ends with no decision and the harness asks
  in its pane. Nothing else about focusing a pane changed.
- The error catalog's `not_human` is also raised by `reply-approval`.
- The Claude Code integration is now version 2: its `PermissionRequest` hook
  entry gets a 310 second timeout instead of 5, so a hold can run. With
  approvals off the hook still returns within 500 ms. `tuios integration
  status` reports a version 1 install as out of date until it is installed
  again. The opencode and Kilo plugins are version 2 as well: a
  `permission.asked` event runs the hook and waits for what it prints, and
  sends a reply to opencode only when the hook printed one. The plugin names
  the tool call a request is about, from `tool.execute.before`, so the Inbox
  line is `approve bash: <command>` rather than opencode's permission name.
- Only a call the Inbox can show whole on one line is held; every other
  prompt is reported as before and answered in the pane. See
  [request-approval](#request-approval).
- In the Inbox, `space` on a held approval does not open the peek of
  [respond](#respond): the hook keeps the prompt off the pane, so there is
  nothing to read. The TUI says to answer with `1`, `2` or `3` instead.

**The Inbox and the host listings cover every machine.** A daemon with a
`[hosts]` table now keeps one stream per linked host over the link: it lists
the host's Inbox, then subscribes to the host's `attention`, `agent-state`,
`agent-message`, session and window events from the position the listing was
current to, resuming with `after_seq` after a redial and listing again after a
`gap` (see [Following linked hosts](#following-linked-hosts)). What changes for
an existing caller:

- `list-attention` also lists the items of every linked host, with `host` set
  and an id of the form `host:id`, and `counts` counts them. It used to list
  this machine's items only. A caller that wants the old answer passes the new
  param `host: "local"`. `session` without `host` still names a session on this
  machine, so a far session of the same name is not in it.
- Items gain `stale` and `seen_at`: while a host's link is down its items stay,
  with `stale: true` and when the host was last heard from, and the item is
  updated (an `attention` event with action `update`) each time the mark
  changes.
- A host item never carries `request_id`, `expires` or `always_scope`, and
  `options` is display only: a held approval is answered on the machine that
  holds it, so `reply-approval` never sees a host item's id.
- `dismiss-attention` accepts a host item's id. It hides the item on this
  daemon only, until the host changes it, and marks nothing on the host; the
  result carries `host`. A dismiss is the person's fact, and a second hub or a
  client on the host still sees the item.
- `attention` events of a host item carry `host`. A subscriber that names a
  `session`, `window` or `pane` does not get them, because it reads those
  names as this machine's; one that names none does.
- `subscribe` takes the new param `hosts`. With it, the `agent-state`,
  `session-created` and `session-closed` events of linked hosts are delivered
  too, each with `host` set and only its identifying fields, and `session` and
  `window` match events of other machines. Without it nothing relayed from a
  host is delivered, so an existing subscriber sees what it saw.
- The new event type `host-changed` (in `EventTypeNames` and the accepted set
  of `types`) says a host's link changed state or what the host holds changed.
  It carries `host` and `status` and nothing else. `subscribe` with no `types`
  filter delivers it.
- `list-hosts` gains `events_push: true` at the top and, per host, `events`
  (`live`, `polling`, or empty while the link is not up) and `events_note`,
  which says why a host is polled: its tuios is too old to have an Inbox or to
  resume its stream, and the note names the update.
- `list-host-agents` lists every session on each host, where it used to list
  only the host's most recently active one. Each row gains `session`, and the
  rows gain `agent_state_at`, `cwd`, `blocked_by`, `completion_seq` and
  `finished_unread`. The entry's `session` is set only when every row is in the
  same session. Hosts are asked at once rather than one after another. A host
  that predates `all_sessions` on `list-agents` is asked session by session.
- `list-host-sessions` and `list-host-agents` answer for a host that did not
  answer with the rows it last gave, with `stale: true` and `fetched_at` (unix
  seconds), beside the `error` it always had. Both gain `events`.
- `list-agents` takes the new param `all_sessions`, and every row gains
  `session`.
- The `MsgHostsChanged` push to attached clients gains `Changed`, the hosts
  whose link, sessions or agents changed. The daemon now sends it on such a
  change too, not only when the `[hosts]` table changes. An older client reads
  it as it always did, as a reason to list the hosts once.

**A pane on another machine reports to the daemon that holds its window.**
`open-pane` takes the new param `window`, the asking daemon's id for the
window, and then returns `calls_token`; the asking daemon opens the new verb
`pane-calls` with it (see [Reports from a pane on another
machine](#reports-from-a-pane-on-another-machine)). What changes:

- A hosted pane's process gets `TUIOS_PANE_ID`, the owner's window id. It used
  to get none.
- On the machine running the process, `set-agent-state`, `set-agent-meta`,
  `set-agent-session` and `wait-for` (condition `agent-message`) with `window`
  set to a hosted pane's id or window id, `read-agent-messages` with `to` set
  to it, and `send-agent-message` with `from` set to it are no longer answered
  there. They are sent to the owner and its answer is returned. A caller that
  is not in that pane gets `forbidden`, and when the owner is too old to take
  the call, `protocol_mismatch`. Such a call used to fail there with
  `window_not_found` or `session_not_found`. A forwarded
  `send-agent-message` may attach only paths in the owner session's stash,
  like a message from the link, and a forwarded `wait-for` is capped at one
  hour on the owner and ends if the report channel drops.
- The asking daemon may send `open-pane` twice on one connection. A far
  daemon from before this refuses `window` with `invalid_params`, and the
  asking daemon then asks again without it, so a window still opens on that
  machine, with no reports from the pane.
- The `session` param of `open-pane` was documented as exported as
  `TUIOS_SESSION`. It is exported as `TUIOS_SESSION_REMOTE`, as it has been
  since hosted panes stopped exporting `TUIOS_SESSION`; the description now
  says so.

**A connection can restrict itself.** The new verb `restrict-connection` (see
[restrict-connection](#restrict-connection)) narrows what one connection may
do for as long as it is open, and `tuios mcp` restricts every connection it
opens. A connection that never calls it is served exactly as before. What
changes for everyone:

- Every pane is started with `TUIOS_PANE_TOKEN` beside `TUIOS_PANE_ID`. It is
  new, and nothing reads it but `restrict-connection`.
- `fan` records on each session it starts the session of the pane that ran
  it, when a pane of this daemon ran it, and `list-worktrees` rows gain
  `launched_from` for such a session. Rows of any other session are unchanged.
  The field is additive on the saved session record, so an older daemon reads
  a newer record and drops it.
- On a restricted connection, every verb outside the table in
  `internal/session/conn_scope.go` answers `forbidden` with a hint naming
  `restrict-connection`, and under scope `own` a verb that names no session
  gets the caller's own session, not the most recently active one.

**A shell's commands are facts the daemon reports.** The daemon's emulator
always recorded a shell's OSC 133 marks for the scrollback browser; it now
turns them into events and facts (see [run](#run)). A pane whose shell sends
no marks is unaffected by all of this. What changes for everyone else:

- A subscriber with no `types` filter now also receives the new event types
  `prompt`, `command-started` and `command-finished`, from every pane whose
  shell marks its commands. They carry the new fields `cmdline`, `exit_code`,
  `duration_ms` and `command_seq`. A consumer that switches on `type` and
  ignores what it does not know is unaffected; one that treats every type it
  does not know as an error has three more to ignore.
- `list-windows` entries gain `at_prompt`, `command_seq`, `running_cmdline`,
  `last_cmdline`, `last_exit_code` and `last_duration_ms`, only for a window
  whose shell has sent a mark. Other entries keep their shape exactly. Such an
  entry also carries `marks_commands`, true once the shell has sent the C mark
  that starts a command, and `prompt_marks_only: true` once a line `run` typed
  came back to a new prompt with no C: the shell marks its prompts only (bash
  before 4.4 with the bash recipe, which ignores PS0, or a prompt theme that
  sends only A). `at_prompt` is then false, since the daemon cannot tell a
  prompt from a running command there, and `run` refuses the pane with
  `no_shell_integration`. A subscriber sees one more `prompt` event in that
  case: the new prompt that shows it.
- `capture-pane` accepts the new source `last-command-output`, so its
  documented `accepted` set in `list-verbs` has three values. `wait-for`'s
  `source` was documented with the same list and never took the new value; it
  is now documented as `visible` and `recent`, which is what it always used.
- `wait-for` takes the new condition `command-finished` and the new param
  `command_seq`.
- The new hook event `after-command-finished` runs on the daemon with
  `TUIOS_COMMAND`, `TUIOS_EXIT_CODE` and `TUIOS_DURATION_MS`. Every hook now
  gets those three variables, empty for the other events.
- The new error codes `no_shell_integration` and `not_at_prompt` come only from
  the new verb `run` and the new capture source.

**The Inbox has a seventh kind, `ask`.** The new verb `ask-human` puts a
question to the person as an Inbox item, and the new verb `answer-ask` is how
the person's client answers it (see [ask-human](#ask-human)). What changes for
existing callers:

- `list-attention` can return items of kind `ask`, and its `counts` object has
  an `ask` key. The kinds sort approval, ask, question, mail, errored, resume,
  finished; `ask` is new between the first two. `kinds` accepts `ask`.
- `attention` events carry `ask` items, and the close reason `superseded` is
  new: the pane asked a newer question.
- An `ask` item from a pane closes when the pane closes (`window_closed`), like
  a blocking item.
- `popup` takes `wait`, `capture_stdout` and `timeout`. Without them it answers
  exactly as before; with `wait` it answers `popup_result` instead of
  `popup_opened`, when the command exits.
- In the TUI's Inbox, the digit keys 1 to 9 pick an answer on an `ask` item.
  On everything else, 1, 2 and 3 do what they did, and 4 to 9 do nothing, as
  before.

**Selectors address many panes.** A new param `select` takes a selector (see
[Selectors](#selectors)) on `list-agents`, `list-host-agents`,
`list-attention`, `wait-for`, `send-agent-message` and `ask-agent`. A call that
sends none of the new params is answered as before. What changes:

- Every `list-agents` row gains `group`, the fan-out group of the pane's
  session, empty outside one. `list-host-agents` rows carry it from a host that
  sends it.
- `list-agents` with `select` and no `session` lists every session, as
  `all_sessions` does, and answers with `select` and, without `all`, `confirm`.
  With `session` it narrows that session and carries no `confirm`, since a
  write by the same selector reaches every session.
- `send-agent-message` and `ask-agent` with `select` answer with a new result
  shape, `agent_messages_sent` with `results`, or `agent_replies` with
  `replies`. Without `confirm`, or with a `confirm` for a different set of
  panes, they send nothing and fail with the new code `confirm_required`, whose
  hint carries the new field `confirm`. `ask-agent` no longer marks `window` as
  required in `list-verbs`, since `select` replaces it; a call with neither is
  still `invalid_params`.
- `wait-for` takes `select` and `every` for `agent-state`. With `every` the
  result carries `panes` and `total` instead of one `window`.
- A pane on another machine that sends its calls through its owner is refused
  with `forbidden` when a call names `select`.

**fan starts any program, several at once, from the caller's PATH.** `fan`
takes the new params `agents`, `prompts` and `env`, and the new verb
`start-agent` starts one agent beside the caller. What changes for a caller of
`fan`:

- `agent` is an argv string, split the way a shell splits words. `agent`
  `"claude"` means what it meant. A name no manifest recognises used to be
  refused with `invalid_params`; it is now started if it is on the `PATH`, and
  refused as not installed otherwise, with the harness ids in `available`
  instead of `accepted`, since they are no longer the only values.
- `count` and `agent` are no longer marked required in `list-verbs`, since
  `prompts` and `agents` stand in for them. A call with neither is still
  `invalid_params`.
- `prompt_status` has the new value `held`, which a caller that waits for
  `pending` to end should treat the same way. The `tuios fan --wait` of this
  build does. A CLI from before it stops waiting at `held` and reports the
  prompt as not sent.
- A held prompt opens a `question` item in the Inbox for the pane, with the
  summary "waiting at a screen tuios does not recognise: look at the pane and
  answer it". It closes when the prompt is typed or given up on, or when the
  pane's state changes.
- The harness the daemon started stands in for detection while the wait runs,
  so `unknown` is not ready for an agent of a harness that can show idle even
  before the detector has named the pane. It used to be ready until detection
  ran.
- `list-worktrees` rows gain `agent`, the agent as it was named, and
  `prompt_ready_by`. Sessions of `fan_started` gain `agent` and `command`, and
  the top-level `command` is an absolute path when the caller sent a `PATH`.
- The `tuios fan` CLI sends its `PATH` in `env`. Against a daemon from before
  `env` it retries without it, unless `--env` was passed. `tuios fan --host`
  sends no `env`, since a call over a link may not carry any, and refuses an
  explicit `--env`.

**A repository can be named by its origin URL.** `new-worktree`, `fan` and
`start-agent` take the new params `repo_url`, `repos_root` and `clone` (see
[new-worktree](#new-worktree)), so a caller on another machine can name a
repository without a path. What changes:

- `repo` is no longer required when `repo_url` is passed. Passing both is
  `invalid_params`, and so is `repos_root` or `clone` without `repo_url`. A
  call with `repo` alone is answered as before.
- A `repo_url` that matches no checkout is the new code `repo_not_found`.
- `fan`'s result gains `repo_root`, and both results gain `cloned: true` when
  the call cloned the repository.
- With `clone`, `fan` checks its agents before cloning, so a clone is not made
  for a call that fails on an agent. Without `clone` the order is as it was:
  the repository first.
- `start-agent` also takes `repo` and `args`, creates a named session that
  does not exist, and answers with `session_id`, `created_session`,
  `workspace` and `cwd`.

The new verb `bundle-worktree` changes no old one. A daemon from before it,
or from before `start-agent`, answers `unknown_verb`, and one from before
`repo_url` answers `invalid_params` naming it; the CLI turns both into "tuios
on HOST is too old".

**start-agent can run an agent headless over a protocol.** `start-agent`
takes the new param `protocol`, `acp` or `codex` (see
[start-agent](#start-agent)). Without it nothing changes. What changes for an
existing caller:

- `request-approval` holds a permission for a pane `start-agent --protocol`
  opened even when `[agents.approvals]` does not name the harness: choosing
  the protocol is the opt in. Every other rule of the verb applies to it
  unchanged, and every other pane is refused with `disabled` as before.
- `list-agents` rows gain `protocol`: `acp` or `codex` for such a pane, empty
  for every other.
- A pane a protocol names is ready only on a state it reports. `unknown` is
  never ready for it, whatever its harness, since its screen is a transcript
  no manifest rule reads. Without `protocol` the wait is as before.
- A daemon from before `protocol` refuses it with `invalid_params` naming it,
  so an old daemon never starts the agent in its TUI instead.
- The mark is the daemon's own, by window id, and nothing a caller sends sets
  it. It is not saved: after a daemon restart a restored pane's approvals
  follow `[agents.approvals]` like any other pane's.

**Calls from a pane are held to the pane's grants.** Every pane holds grants
(`read`, `write`, `fan`, `respond`, `admin`; see [pane-grants](#pane-grants)),
and the daemon checks every JSON verb and every client protocol message from
a connection placed in a pane against them before the handler runs. With no
`[agents.permissions]` in the config and no pane given grants of its own,
every pane holds `admin`, which is everything a pane could do before, and
every call is answered exactly as before. What changes:

- Every pane is started with `TUIOS_PANE_GRANTS`, the grants it holds as it
  starts, comma separated, or `none`.
- `hello` answers with `pane_grants: true`.
- `new-window`, `start-agent` and `fan` take the new param `grants`. A call
  that sends none is answered as before, except from a pane that does not
  hold `admin`: the new pane then holds the caller's own grants rather than
  the default. A `grants` the calling pane does not hold is `forbidden`.
- `respond` may now be answered for a pane that holds `respond`, without a
  `human_nonce`, on a pane in its reach. Its result then carries the new field
  `by_pane`. A pane without `respond` gets `not_human` as before, whose hint
  now says the grant exists. `admin` does not include `respond`, so no pane
  can answer a prompt unless the person gave it the grant.
- `list-windows` entries gain `grants` for a pane given grants of its own. The
  entries of every other pane keep their shape.
- The session record's windows gain a daemon-owned `grants` field. It is
  additive in JSON and gob, a client sync can neither set nor clear it, and an
  older daemon reads a newer record and drops it, so a pane restored by an
  older daemon holds that daemon's (unchecked) rights.
- For a pane without `admin`: a verb its grants do not cover answers
  `forbidden` with a hint naming `pane-grants`, the pane's grants and the
  config key; a verb that names no session gets the pane's own session, not
  the most recently active one; a subscription carries only the sessions the
  pane may read; and every client protocol message except the hello answers
  an error, so such a pane cannot attach or use `run-command`. The error for
  the command message names `run-command` and points at `get-window`.
- For a pane without `admin`, `send-text`, `send-keys`, `run` and
  `ask-agent` on another pane answer `forbidden` when the target holds a
  grant the caller does not, or when the target is on `needs_input` and the
  caller does not hold `respond` (see [pane-grants](#pane-grants)). A call
  with no window is pinned to the pane focused when it is checked.
  `send-keys` from such a pane is written to the target's terminal even when
  a client is attached, so its keys never reach the window manager. Calls
  from outside every pane, and from a pane holding `admin`, are answered as
  before.
- `tuios get-window` reads with the new verb `get-window` instead of the
  client protocol's `GetWindow` command, so a pane holding `read` may run it
  on its own session. The verb answers as `GetWindow` did, from the attached
  client when there is one, so the `--json` output keeps its shape; with no
  client attached it gains the shell fields `list-windows` gives a window. A
  window that matches nothing is now reported the way `list-windows` reports
  it, with the available windows. Against a daemon from before the verb the
  command falls back to `GetWindow`.
- `restrict-connection` and pane grants share the code that fills in and
  checks a caller's session and window. A restricted connection is answered
  as it was; a refusal of a parameter that reaches every session now reads
  "and the connection is restricted to its own", the words it used before.

The new verbs `pane-grants`, `set-pane-grants` and `get-window` change no
old one.

**send-keys with a window goes to that window.** With a client attached,
`send-keys` used to hand every parsed key to the client, which read them as
the person's keys and ignored `window`: in window-management mode an arrow
moved the selection, in terminal mode it went to the focused window, and the
call answered ok either way. Now:

- With `window`, the keys are written to that window's terminal whether or
  not a client is attached. `PREFIX` with `window` and a client attached is
  `invalid_params`, because the window manager acts on the focused window and
  not the one named. A caller that sent the leader chord with a window leaves
  the window out.
- Without `window`, keys go to the attached client as before, or to the
  focused window's terminal when none is attached. `literal` always writes to
  a terminal: routed to the client it waited out the 10 second timeout and
  answered `command_failed` on a send that had worked.
- Every key is parsed before anything is sent, on both routes. Names are
  case-insensitive and take the spellings of tmux, curses, vim and the DOM:
  `up`, `arrow-up`, `ArrowUp`, `KEY_UP`, `<Up>`, `PgDn`, `BSpace`, `C-c`,
  `M-x`, `^C`. New names: `BTab` (shift+Tab) and `shift+` on named keys.
  A token written as an escape sequence (`\e[A`, `\x1b[A`, `\033[A`, `^[[A`)
  is sent as those bytes. A word that looks like a key name and is not one
  (`Dwon`, `KEY_FOO`, `F13`, `a+b`) used to be typed as its letters; it is now
  `invalid_params` with `param: keys`, the key names in `accepted` and the
  closest name in `did_you_mean`, and nothing is sent. A plain lower-case
  word such as `ls` is still typed.
- Arrows, `Home` and `End` written to a terminal follow the pane's
  application cursor keys mode (DECCKM): `ESC O A` when the program turned it
  on, as `less` and `vim` do, `ESC [ A` otherwise. Modified named keys use
  xterm's form (`ctrl+Up` is `ESC [ 1 ; 5 A`), where `alt+Up` used to be
  `ESC ESC [ A`.
- The new param `repeat` (1 to 1000, default 1) sends the sequence that many
  times.
- The result gains `sent_to` (`window` or `client`), `keys` (how many were
  sent), and for `window` the `window_id` and `window` name it went to.
  `tuios send-keys` prints that as `sent 3 keys to window docs (3a42ab8f)`
  and takes `--repeat` (`-N`) and `--json`.

**A window's name wins over another window's title.** A window target that
is not an id, index or prefix used to match a name or a title alike, so a
program that set its title to another window's name made both ambiguous. A
name given with `new-window` or `set-window --name` is now matched first, and
titles only when no name matches. An ambiguous prefix or name still answers
`window_not_found`; the message now lists the index, short id and name of
each window it matched, and the hint says it matched more than one.

**new-window can print the id alone.** `tuios new-window --print-id` prints
the full window id and nothing else, for `id=$(tuios new-window --print-id)`.

**new-window waits for an attached client to place the window.** With a
client attached, `new-window` now answers once the client has placed the
window and sized its terminal, up to one second, so `unplaced` is `false` in
the usual case. It used to answer at once with the nominal geometry, and a
program started in the pane straight after the call got a resize while it
drew: glow's pager then showed a blank screen and ignored keys. With no client
attached nothing changes. A client that does not place the window within the
second leaves the answer as it was, `unplaced: true`.

**The agent review, triage, queue and approval work is registered ahead of
its handlers.** Twelve verbs are listed by `list-verbs` with their parameters,
scope and link capability, and answer `internal` with a message ending "is not
built yet in this daemon" until their work lands: `review-diff`,
`review-note`, `send-review`, `compare-fan`, `verify-fan`, `keep-fan`,
`mark-attention`, `agent-activity`, `queue-prompt`, `list-queued`,
`cancel-queued` and `get-approval` (see
[Agent review, triage and queue verbs](#agent-review-triage-and-queue-verbs)).
`mark-attention` checks the person's nonce first, so a caller without one gets
`not_human` as it will once built. The older verbs change as follows, and a
caller that sends nothing new is answered as before:

- `request-approval` takes `kind` (`approval` or `plan`), `plan`, `tool`,
  `target` and `deny_message`. A call that sets `kind` to `plan` or any of the
  other four answers `internal`, holds nothing, and the hook gives the prompt
  back to the pane. An unknown `kind` is `invalid_params`.
- `reply-approval` takes `risk_ack` and `plan_sha`, and `respond` takes
  `risk_ack`. A call that sets one answers `internal` and answers nothing;
  `reply-approval` checks the nonce before that.
- `set-agent-state` takes `activity`, one hook event for the pane's activity
  ring. Its `event` must be one of `prompt`, `tool`, `tool_done`,
  `tool_failed` or `turn_end`, or the call is `invalid_params` and nothing is
  applied. A valid one is not recorded yet, and the state part of the report
  applies exactly as without it.
- `list-attention` takes `include_snoozed`. Nothing can be snoozed yet, so it
  lists what the plain call lists. `kinds` accepts the new kind `plan`, which
  sorts right after `approval`.
- Attention items gain `snoozed_until`, `marked_unread`, `risk`,
  `deny_message`, `plan_lines` and `plan_sha`, each omitted when unset, and a
  close may carry the new reason `snoozed`. An item mirrored from a linked
  host keeps that host's `risk` and `plan_lines`, display only.
- `get-agent-state` and every `list-agents` entry gain `queued`, the length of
  the pane's delivery queue, 0 until the queue is built. The synced window
  state gains `agent_queued`, omitted when zero and taken from the daemon's
  own state on every client push. A row of another machine's agents
  (`list-hosts` with agents) keeps that host's `queued`, held to 0 through
  `[agents.queue]`'s ceiling of 64, display only.
- A worktree's record gains `verify`, the last `verify-fan` check in it,
  omitted when none has run.
- The event type `agent-activity` exists and is opt-in: a subscription
  receives it only when its `types` names it, so a subscriber that names no
  types never sees it. Nothing publishes it yet.
- The error codes `not_repo`, `no_notes`, `queue_full` and
  `risk_unacknowledged` are in the catalog.

**The activity ring is built.** `agent-activity` answers (see
[agent-activity](#agent-activity)) instead of `internal`, and these older
verbs change:

- `set-agent-state` records `activity` in the pane's activity ring when the
  report passes the identity guard (the nested-session guard behind
  `foreign_session` and `foreign_harness`), whether the state part applies or
  not: a `PostToolUse` refused by `if_state` still records its tool result. A
  report the guard refuses records nothing, even when `if_state` refused it
  first. The answer gains `activity_recorded` (bool), present only when the
  call sent `activity`. The state part is handled exactly as before.
- `set-agent-state` with `activity` also moves the pane's metadata: a `tool`
  sets `now` to `<tool>: <target>`, a `prompt` sets `prompt` to its first line
  and clears `now`, and a `tool_failed` or `turn_end` clears `now`.
- The daemon clears `now` whenever a pane's agent state moves to one other
  than `working` or `needs_input`, however it moved: a report with or without
  `activity`, a screen rule, an OSC sequence, the foreground detector or the
  silence timer. A pane at rest never keeps a stale `now`. A `model` in the activity sets `model` (source `hook`) when
  the pane does not already show that model from any source. These keys ride
  the window state's `agent_meta` like any other, so `get-agent-state`,
  `list-agents` and older clients see them as ordinary metadata.
- `set-agent-meta` refuses the keys `now` and `prompt`, set or removed, with
  `invalid_params`: they are written by tuios from hook activity. `clear`
  leaves them in place, whatever `source` it names.
- `set-agent-meta` no longer pushes state for a call that changes nothing. A
  key set to the value it holds, by the source that wrote it, keeps its
  expiry unless less than half of the TTL is left, and a call where every key
  is like that leaves the session version alone and sends clients nothing. A
  status line that writes the same values several times a second used to push
  state each time. The answer is the same either way.
- The event type `agent-activity` is published, one event per entry, carrying
  the entry as `entry`. It is not kept in the replay ring, like `output`: a
  resume whose `types` names it gets a `not_retained` gap when one was
  published after `after_seq`. Read the ring with `agent-activity` instead.

**tuios now feeds `set-agent-meta` itself.** No verb changes; what changes is
who calls two of them, and a client that draws metadata now has some to draw:

- A protocol pane (`tuios agent-proto`, what `start-agent --protocol` runs)
  calls `set-agent-meta` for its own pane with the source `protocol` and the
  keys `model`, `context`, `cost` and `plan`, each only when its value
  changes, and all of them again at the start of each turn, so values a
  restarted daemon or a clear to `none` lost come back. It also calls
  `set-agent-state` with `activity` for each prompt, tool call and finished
  turn. `state` is the state the pane last reported (`working` during a
  turn, the turn's end state for `turn_end`) and `if_state` is `none`: the
  daemon records the activity and refuses the state part on every pane that
  has a state, so the call restamps no `agent_state_at`, rewrites no
  message and pushes no state. Only a pane whose state was lost mid-turn
  (`none`, after a restart or a clear) gets its state back from it. These
  calls and the metadata go on a connection of their own, off the pane's
  input loop. It sends activity only to a daemon whose `list-verbs` lists
  `activity` for `set-agent-state`, and nothing extra to an older one.
- An ACP protocol pane whose agent names its model in `session/new`
  `models` now says so in the transcript's first line: `connected to
  opencode 1.2 (Claude Sonnet 4)` where it said `connected to opencode 1.2`.
  A Codex pane said it before, and still does.
- `tuios agent-statusline` (Claude Code's status line, opt in, and the
  opencode and Kilo plugin) calls `set-agent-meta` for its own pane with the
  source `statusline` and the keys `model`, `context` and `cost`, at most once
  every 15 seconds per pane while they change, and never with a TTL. What
  the interval held back is sent at the end of the turn: by Claude Code's
  `Stop` hook (`tuios agent-hook claude-code`, which then calls
  `set-agent-meta` for its own pane as well as `set-agent-state`), and by
  the opencode plugin's `--turn-end`.
- The opencode and Kilo plugin is version 3, so `integration status` reads a
  version 2 install as out of date until it is installed again.

**The delivery queue is built.** `queue-prompt`, `list-queued` and
`cancel-queued` now work (see
[The delivery queue](#the-delivery-queue)), and no longer answer "not built
yet". What changes for a caller that uses none of them: nothing, since a pane
has no queue until something is queued. For one that does:

- `queue-prompt` for a pane that runs no agent tuios knows of is
  `invalid_params`, and for `human` it is `no_keyboard`. A `human_nonce` that
  does not verify is `not_human`, not a quiet fall back to the caller's own
  name.
- `list-queued` with no window lists every pane of the session, not the
  focused one, and its entries gain `session`, `window`, `name` and `from`.
  Its result has `type` `queued_prompts` and `session`.
- `cancel-queued` with an `id` and no window finds the entry in any pane of
  the session. `id` together with `all` is `invalid_params`. An entry being
  typed, or one the caller may not drop, is `forbidden`; an id that is not
  queued is `invalid_params`. Its result has `type` `queue_cancelled`.
- `get-agent-state` and `list-agents` report `queued` as the pane's queue
  length, and the synced window state carries a non-zero `agent_queued` while
  something waits. A client that does not know the field drops it.
- An Inbox question may carry the summary "your queued message was typed but
  NAME did not take it: look at the pane", on the pane's blocking key, for a
  queued message the agent showed no sign of taking. It is an ordinary
  `question` item and closes like one.
- `[agents.queue] max` is read at start and again when the config file
  changes.
- One message per rest holds across an emptied queue: a message queued just
  after the last one was typed waits for a rest the agent reaches after that
  typing, and `delivering` is false until then. The typing time is the moment
  the Enter went out, so a turn that ends while the daemon is still watching
  for the agent to take the message counts as that rest.
- For a pane whose harness cannot show working (the daemon counts its output
  as taking a message), a rest after a typed message is also output followed
  by 5 seconds of silence, since such a pane may never change state.
- `cancel-queued` of a stalled message closes its question, and the messages
  behind it are typed at the pane's next rest, not in the rest the stalled one
  was typed into: its text may still sit in the agent's input box.
- A linked machine that gave no name drops, with `cancel-queued`, only what it
  queued on the same connection. `queue-prompt` and `cancel-queued` from a pane
  on another machine, forwarded through its report channel, are `forbidden`.

**Fan compare, verify and keep are built.** `compare-fan`, `verify-fan` and
`keep-fan` answer instead of `internal` (see [compare-fan](#compare-fan),
[verify-fan](#verify-fan) and [keep-fan](#keep-fan)). What changes for an
existing caller:

- `verify-fan` takes `env`, with the rules of `fan`'s `env`: a call over a
  link that passes it is refused with `forbidden`.
- A worktree's `verify` record gains `note`, omitted when empty, saying why a
  failed check has no exit status: it timed out, or the daemon restarted
  while it ran. A record saved as `running` by a daemon that has since
  restarted is reported by `compare-fan` as `failed` with that note; the
  saved record itself is not rewritten.
- `verify-fan` opens a window named `verify` in each fan session it reaches.
  An attached client shows it like any other new window, without focusing
  it. It closes when the check passes and stays open when it fails, until
  someone presses enter in it.
- The daemon's shell facts gain the time the last command finished, which
  `compare-fan` reports as `last_command.at`. No other verb reports it.
- `tuios fan keep` calls `keep-fan`, and falls back to the loop over
  `remove-worktree` it ran before when the daemon answers `unknown_verb`.
  Its output and `--json` shape are unchanged, except that a sibling left
  dirty is described by the daemon's refusal plus a sentence naming
  `--stash` and `--force`, and siblings are now matched by the repository's
  main checkout rather than its directory name, so two checkouts that share
  a name no longer count as one fan.

**The Inbox's lifecycle is built** ([mark-attention](#mark-attention)). The
person can snooze an item, wake it, mark a finished pane unread, and restore
an item they dismissed or snoozed in the last 10 seconds. What changes for a
caller of the older verbs and for a subscriber:

- `list-attention` with `include_snoozed` lists the snoozed items after the
  open ones, each with `snoozed_until`. They are not in `counts`, which say
  what is waiting. Without the parameter the listing is as before and leaves
  them out.
- A snooze publishes a `close` with the reason `snoozed`, the closing item
  carrying `snoozed_until`. When the item wakes (its time comes, its fact
  changes, the person wakes or restores it) an `open` follows with the same
  `id` and `since`, so a subscriber that knows nothing of snoozing sees the
  item close and open again.
- When the fact behind a snoozed item ends (the pane leaves `needs_input`, a
  client focuses a finished pane, the pane or session closes, the thread is
  read), a `close` is published for it with that reason. A subscriber that
  took the `snoozed` close as final gets a `close` for an id it no longer
  holds, which changes nothing for it.
- `dismiss-attention` accepts the id of a snoozed item. A dismiss of
  anything but an `outbox` item or an `ask` can be undone for 10 seconds with
  `mark-attention` `restore`, which reopens it with its id and `since`.
- A hold that `request-approval` starts on a pane whose approval the person
  snoozed wakes the approval first, so the Inbox can answer it.
- The saved queue holds snoozed `finished` and `errored` items with
  `snoozed_until`, and they come back asleep after a restart; one whose time
  passed while the daemon was down wakes on start. An older daemon reading
  the file lists them as open.
- `not_human` is raised by `mark-attention` as by `dismiss-attention`.

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
| `not_ready` | The target agent was mid-turn, so the call declined to type at it. `resume-agent` raises it for a pane whose shell is not at its prompt. |
| `not_resumable` | `resume-agent` found no conversation it can resume in the pane: none recorded, a harness with no `[resume]` command, an id that is not one plain shell token, or a pane on another machine. Nothing was typed. |
| `no_shell_integration` | The pane's shell has sent no OSC 133 marks, or marks its prompts and not its commands, so `run` cannot tell where a command starts and ends, and `capture-pane` with `last-command-output` has no finished command to read. Nothing was typed, except by a `run` that found out after typing, which says so. |
| `not_at_prompt` | `run` typed nothing because a command is running in the pane. The message names it; the hint names the `wait-for command-finished` call that waits for it. |
| `agent_blocked` | ask-agent declined to type at an agent on `needs_input`, because the text would answer its prompt. Nothing was typed. The hint names `capture-pane`. |
| `prompt_stalled` | ask-agent typed the question and sent Enter, and within `stall_timeout` the pane did not show that it took it. The question was typed; look at the pane before sending it again. The hint names `capture-pane`. |
| `loop_refused` | The call would loop: a pane addressing itself, or an ask that closes a cycle with one in flight. |
| `rate_limited` | The sender is over the cross-agent message rate cap. |
| `not_human` | Only the person at an attached client may make this call, and it carried no nonce from a live attach. `dismiss-attention`, `mark-attention`, `respond` and `reply-approval` raise it. |
| `prompt_changed` | `respond` pressed nothing: the pane is not on `needs_input`, no rule reads its prompt now, the prompt is not the one `prompt_id` names, or another client already answered it. Read it again with `peek-prompt`. |
| `no_keyboard` | The target is the person's inbox, `human`, which has no pane to type into. |
| `confirm_required` | A write by `select` sent nothing: it carried no `confirm` token, or a token for a different set of panes than the selector matches now. The hint lists the panes in `available` and carries their token in `confirm`. |
| `forbidden` | The caller may not do what it asked. A process inside a pane of this daemon cannot send or ask as `human`, and a machine linked to this one cannot call what its link policy does not grant; the hint names the capability and the `[hosts]` table that grants it. Nothing was done. |
| `protocol_mismatch` | The caller's protocol version is outside the range this daemon serves. Only `hello` produces it. |
| `unknown_host` | No host by that name is configured. Host names are matched exactly. |
| `host_unreachable` | The host is configured and is not answering. Nothing was queued; only `send-agent-message` with `host` keeps a message for a host that is down, and it answers `queued` instead of this. A write to a window on another machine whose link is being restored also answers it. |
| `host_refused` | The host's link is up and cannot take another connection. |
| `unknown_pane` | This daemon is not running a pane with that id. |
| `internal` | An unexpected server side failure. |
| `not_worktree` | The session is not in a git worktree, so there is nothing to remove or diff. |
| `worktree_dirty` | remove-worktree refused: the worktree holds uncommitted changes and neither `stash` nor `force` was passed. Nothing was removed. |
| `git_failed` | A git command failed. The message is git's own. The repository is as it was. |
| `repo_not_found` | No checkout on this machine has the origin `repo_url` names, and `clone` was not passed. Pass `clone`, a `repos_root`, or `repo` with the directory. |
| `not_repo` | No git repository is under the pane or session named, so there is nothing to review. Nothing was read. |
| `no_notes` | `send-review` found no unsent review notes for the pane. Nothing was typed. |
| `queue_full` | The pane's delivery queue holds `[agents.queue] max` messages. Nothing was queued. |
| `risk_unacknowledged` | An allow for an approval that matched risk rules came without `risk_ack` naming exactly those rules. Nothing was answered. |

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

The verbs a live renderer still has to own to stay in sync (`send-keys` with
no window, and the live apply half of `set-option`) route to the attached TUI
when one is present and act on daemon owned state otherwise. `send-keys` with
a window writes to that window's terminal either way. The routing is transparent to the
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

### restrict-connection

Give up authority on this connection for as long as it is open. It is how a
caller that drives tuios for an agent, `tuios mcp` above all, makes the daemon
hold every later call on the connection to what the agent was granted, so a
prompt-injected agent cannot reach further through that caller than the grant.

Params:

- `scope`: `own` (the default) or `all`. Under `own` the connection reaches
  only the caller's own session, the sessions in its fan group (sessions a
  single `fan` started together, in the same repository) and the sessions a
  `fan` run from its session started. `all` leaves the sessions alone, for
  `read_only` by itself.
- `read_only`: refuse `send-text`, `send-keys`, `ask-agent`, `respond` and
  `fan`. The caller may still read, report its own pane's state and meta, and
  leave mail.
- `pane_id`, `pane_token`: the caller's `$TUIOS_PANE_ID` and
  `$TUIOS_PANE_TOKEN`, for when the kernel cannot place the caller.

The caller's pane is found first from the kernel's record of the process that
connected (`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS), walked up to a
pane's shell or matched by its controlling terminal, the same way
`request-approval` places its caller. Only when that places the caller in no
pane is `pane_id` checked against `pane_token`, an HMAC of the window id under
a key the daemon picks at start and never writes down. A `pane_id` that
disagrees with the kernel's answer is refused with `forbidden`, and so is a
token that does not match. A caller placed in no pane gets an empty `window`
and no error; under `own` it then reaches no session at all. Over a link the
kernel's answer is the local proxy's, and tokens are never good there, so a
restricted link connection reaches nothing under `own`.

A later call may narrow further (turn on `read_only`, or go from `all` to
`own`) and never widen: lifting `read_only`, going back to `all`, or naming
another pane is refused with `forbidden`, and the connection keeps the
restriction it had.

Result:

```json
{"result": {"type": "connection_restricted", "scope": "own", "read_only": true,
 "window": "7f3c...", "session": "work", "via": "pid", "sessions": ["work", "work-fan-retry"]}}
```

`via` is `pid`, `token`, or empty when no pane was found. `sessions` is
present under `own` and lists what the connection reaches now; sessions a
`fan` starts later join it.

What a restricted connection may call:

| Class | Verbs | Under `own` | Under `read_only` |
|---|---|---|---|
| open | `hello`, `list-verbs`, `unsubscribe`, `restrict-connection` | allowed | allowed |
| across sessions | `list-sessions`, `list-attention`, `list-worktrees`, `list-hosts`, `list-host-sessions`, `list-host-agents`, `list-themes`, `list-glyphs`, `list-hooks` | `forbidden` | allowed |
| read one session | `session-info`, `list-windows`, `get-window`, `list-workspaces`, `capture-pane`, `get-agent-state`, `list-agents`, `wait-for`, `subscribe`, `peek-prompt`, `read-agent-messages`, `explain-agent-screen`, `list-options`, `get-option`, `stash-list`, `stash-get` | session in reach | allowed |
| own pane's record | `set-agent-state`, `set-agent-meta`, `set-agent-session`, `ask-human` | own pane only | allowed |
| mail and stash | `send-agent-message`, `stash-put` | session in reach, sent as the own pane | allowed |
| type into a pane | `send-text`, `send-keys`, `ask-agent`, `respond`, `run` | session in reach | `forbidden` |
| start sessions | `fan`, `start-agent` | needs a pane; `start-agent` opens its pane in a session in reach | `forbidden` |
| everything else | | `forbidden` | `forbidden` |

Under `own`:

- A verb that takes `session` and names none gets the caller's own session.
  `subscribe` with no session streams every session in reach, and each event
  is checked as it is written, so a session that joins the reach later is
  streamed from then on. Events that name no session, and events relayed
  from linked hosts, are not written.
- `all_sessions` on `list-agents`, `any_session` on `wait-for` and `hosts` on
  `subscribe` are refused.
- `select` is refused on every verb but `list-agents`, where it reads only the
  caller's own session: a selector reaches every session.
- A `host` naming another machine, such as `send-agent-message`'s outbox
  delivery, is refused: no session on another machine is in reach.
- `set-agent-state`, `set-agent-meta` and `set-agent-session` with no
  `window` land on the caller's own pane, and naming another pane is refused.
- `send-agent-message` and `ask-agent` in the caller's own session get `from`
  set to the caller's pane, and a different `from` is refused.
  `read-agent-messages` may name only the caller's own inbox in `to`.

This scopes what goes through a restricted connection. A process in a pane can
still open a connection of its own with the tuios CLI and not restrict it; a
harness's shell tool can do that. What the restriction bounds is the MCP
surface, which is what an agent reaches without writing a shell command, and
the one a harness can offer without a shell tool at all. Pane grants, below,
bound that connection too.

### pane-grants

Say what the caller may do through tuios. Every pane holds a set of grants,
and the daemon holds every JSON verb and every client protocol message from a
connection placed in a pane to them, before the handler runs and before
`restrict-connection` is applied. A connection placed in no pane (the
person's own CLI, the attached client, the hooks and dock components the
person configured) is held to nothing new.

| Grant | Allows |
|---|---|
| `read` | Read the pane's own session and the sessions of its fan group (the reach `restrict-connection` calls `own`): listings, captures, agent state, waits, the event stream, mail and stash reads |
| `write` | Type into the panes of its own session (`send-text`, `send-keys`, `ask-agent`, `run`) that hold nothing it does not, and leave mail and stashed files there |
| `fan` | What `write` allows, in the sessions of its fan group and the sessions it launched, and start agents with `fan` and `start-agent` |
| `respond` | Answer an on-screen prompt with `respond`, without the person's `human_nonce`, on a pane in its reach, and type into a pane on `needs_input` |
| `admin` | Everything else, as every pane could before grants: every session, the listings across sessions, windows, layouts, options, `kill-session`, and the client protocol (attach, and `run-command`, which sends a client protocol message). Implies `read`, `write` and `fan`, never `respond` |

Whatever it holds, a pane may call `hello`, `list-verbs`, `unsubscribe`,
`restrict-connection`, `pane-grants` and `resolve-pane`, and report about
itself: `set-agent-state`, `set-agent-meta`, `set-agent-session`,
`ask-human` and `request-approval`, on its own pane only.

A pane holds the grants it was given: `new-window`, `start-agent` and `fan`
take `grants`, and `set-pane-grants` changes them later. A pane given none
holds the default of `[agents.permissions]` in config.toml: `admin` under
`mode = "open"`, which is the default mode, and the `grants` list under
`mode = "strict"` (default `read`, `write`, `fan`). A mode the daemon does not
know is read as strict, and an unknown grant name is dropped.

A pane can never hand out more than it holds. From a pane that does not hold
`admin`, a launch that names no `grants` gives the new pane the caller's own,
and a `grants` the caller does not hold is `forbidden`. `admin` cannot give
`respond`. A link or a pane run for another machine cannot give `respond`.

A pane cannot gain grants by typing either. What is typed into a pane runs
with that pane's grants, so a pane without `admin` that calls `send-text`,
`send-keys`, `run` or `ask-agent` on any pane but its own is also held to the
target pane:

- The target must hold nothing the caller does not, counting what `admin`
  implies. Otherwise the call is `forbidden`, and the message names what the
  target holds.
- A target on `needs_input` is `forbidden` unless the caller holds `respond`,
  since keys typed there answer its prompt. `ask-agent` without
  `allow_blocked` answers `agent_blocked` first, as before.
- A call that names no window is pinned to the pane focused when it is
  checked, and both checks run again right before anything is written.
- `send-keys` from such a pane is written to the target's terminal, never
  routed through an attached client, where the prefix key drives the window
  manager.

`respond` is not held to the first rule: the `respond` grant is the person's
consent to answer prompts on the panes in the pane's reach.

How a connection is placed in a pane, strongest first:

1. The kernel's record of the peer's pid (`SO_PEERCRED`, `LOCAL_PEERPID`),
   walked up to a pane's shell or matched by its controlling terminal, as
   `request-approval` does. A process does not change panes, so the answer is
   kept for the connection once it names one.
2. For a process the kernel places inside the daemon's panes but in no pane
   yet, the `TUIOS_PANE_ID` in its environment, when it names a pane the
   daemon is still creating. Every local pane is entered in the grant table
   before its process starts, so a process never runs before its grants hold.
3. Where the daemon cannot read the peer's pid (Windows, the BSDs), the pane
   id and token the connection presents with `pane-grants`. The `tuios` CLI
   does this on every connection it opens from a pane. The token is the
   `restrict-connection` token: an HMAC of the window id under a key picked at
   daemon start, so it names one pane and cannot be made for another.

A connection over a link is held to that link's policy instead (see
[What a linked machine may do here](#what-a-linked-machine-may-do-here)), and
a report from a pane run for another machine is held on the machine that owns
the pane. A call such a process makes to this machine's daemon directly holds
this machine's default and reaches no session here, so under `strict` it may
call only the verbs every pane may.

Params:

- `pane_id`, `pane_token`: the caller's `$TUIOS_PANE_ID` and
  `$TUIOS_PANE_TOKEN`. Used only where the kernel places the caller in no
  pane; a `pane_id` that disagrees with the kernel is `forbidden`, and so is a
  token that does not match. A connection placed by token stays in that pane
  for as long as it is open, and a second pane's token is `forbidden`.

Result, from a pane:

```json
{"result": {"type": "pane_grants", "pane": true, "window": "7f3c...", "session": "work",
 "via": "pid", "grants": ["read", "write", "fan"], "explicit": false,
 "mode": "strict", "default_grants": ["read", "write", "fan"]}}
```

From outside every pane, `pane` is false and only `mode` and
`default_grants` are set. `via` is `pid`, `env` or `token`. `explicit` is true
for a pane given grants of its own.

A refusal:

```json
{"error": {"code": "forbidden",
 "message": "send-text is refused for this pane: writing into the pane's own session needs the write grant",
 "hint": {"verb": "pane-grants", "command": "tuios pane-grants",
  "detail": "Pane 7f3c1a2b holds read, the grants it was given. Nothing was done. The person can give this pane more with tuios set-pane-grants -w 7f3c1a2b --grants <names>, or every pane started with none with mode and grants under [agents.permissions] in config.toml."}}}
```

The daemon log records every refusal. For a pane without `admin`, a verb that
takes `session` and names none gets the pane's own session, and a subscription
carries only the sessions the pane may read, as they stood at its subscribe.

This scopes accidents and prompt-injected agents that use tuios the ordinary
way, not a determined local attacker: a process that leaves its pane on
purpose (a double fork with a cleaned environment, a service manager) is not
placed in it, and is then treated as the person, as the human checks are. See
[AGENT_STATE.md](AGENT_STATE.md#what-a-pane-may-do).

### set-pane-grants

Give a pane grants, or with `reset` the default of `[agents.permissions]`.

Params: `session` and `window` (omit either from a pane for its own; from
outside every pane `session` defaults to the most recently active one),
`grants` (names, or `["none"]`), `reset`. Pass `grants` or `reset`, not both.

From outside every pane anything may be given. From a pane, the target must be
its own pane unless it holds `admin`, and what it gives, or the default for
`reset`, must be something it holds, so a pane can narrow itself and never
widen itself. Refused over a link and for a window on another machine. The
change applies to the pane's next call; `TUIOS_PANE_GRANTS` in the running
process is not rewritten. The grants are saved with the window and hold again
after a restore.

Result:

```json
{"result": {"type": "pane_grants_set", "session": "work", "window": "7f3c...",
 "grants": ["read"], "explicit": true, "previous": ["read", "write", "fan"], "previous_explicit": false}}
```

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

A window whose process runs on another machine also has `host`. While the link
to it is lost and the pane is being reattached, it has `host_link:
"reconnecting"` and `host_link_until`, the unix time the far machine stops
keeping the process (see [A pane that outlives its link](#a-pane-that-outlives-its-link)).

### get-window

Describe one window, as the client protocol's `GetWindow` command does. With
a client attached the client answers, with its cursor and process fields;
with none, the daemon answers with the window's `list-windows` entry. The
window is resolved by the daemon either way, so a target that matches nothing
is `window_not_found`. A client that does not answer in time is not waited on
twice: the daemon's entry follows.

Params: `session` and `window` (both optional; `window` omitted means the
focused window).

It is a read, like `list-windows`: a pane holding `read` may call it on its
own session and its fan group, and a restricted connection on a session in
reach. `tuios get-window` uses it.

Request:

```json
{"verb": "get-window", "params": {"session": "work", "window": "build"}}
```

Response:

```json
{"result": {"type": "window", "window_id": "7e02...", "index": 1, "title": "zsh",
 "display_name": "build", "workspace": 1, "focused": false, "minimized": false,
 "agent_state": "idle", "width": 80, "height": 24, "pty_id": "4bff..."}}
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
default `"80%"` and `"60%"`), `name`, `cwd` and `workspace` (all optional),
`wait`, `capture_stdout` and `timeout` (optional, below).

With `wait` the call stays open until the command exits, and answers with
`{"type": "popup_result", "window_id", "name", "exit_code"}`. `exit_code` is
-1 when a signal ended the command, which is how closing the popup by hand
ends it. `capture_stdout` (which needs `wait`) gives the command a pipe the
daemon owns as its standard output instead of the popup, and adds `stdout`
(at most 1 MiB) and `stdout_truncated` to the answer. A picker such as fzf
draws on the terminal and prints only the choice, so the choice is what comes
back. It is refused on Windows. `timeout` bounds the wait in milliseconds; 0,
the default, waits as long as the popup is open, and a wait that runs out
fails with `timeout` and leaves the popup open. None of this grants anything:
the caller picked the command, and what comes back is what it printed.

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

Send keys to a window's program: arrows, page keys, Enter, `ctrl+c`. Tokens
are split on spaces and commas, and every token is parsed before anything is
sent. A token is a key name (`Enter` `Tab` `BTab` `Space` `Escape`
`Backspace` `Up` `Down` `Right` `Left` `Home` `End` `PageUp` `PageDown`
`Insert` `Delete` `F1` to `F12`, case-insensitive, also spelled `arrow-up`,
`ArrowUp`, `KEY_UP`, `<Up>`, `PgDn`, `Esc`, `Return`, `BSpace` and the like),
a single character, either of those after `ctrl+`, `alt+` or `shift+` (or
tmux's `C-`, `M-`, `S-`, or `^C`), an escape sequence written `\e[A`,
`\x1b[A`, `\033[A` or `^[[A`, or `PREFIX` for the leader key. A word that
looks like a key name and is not one is `invalid_params` and nothing is
sent; any other word is typed as its letters.

Where the keys go:

- With `window`, to that window's terminal, attached or not. Arrows, `Home`
  and `End` follow the pane's application cursor keys mode. `PREFIX` with a
  window and a client attached is `invalid_params`.
- Without `window` and with a client attached, to the client, which reads
  them as the person's keys: the window manager or the focused window.
- Otherwise, and always for `literal` or a pane without `admin`, to the
  terminal of the window named or the focused one.

Params: `session` (optional), `window` (optional), `keys` (required), `literal`
(optional bool, send the text through unchanged), `raw` (optional bool, treat
each character as its own key), `repeat` (optional int, 1 to 1000, send the
sequence that many times).

Request:

```json
{"verb": "send-keys", "params": {"session": "work", "window": "docs", "keys": "Down", "repeat": 5}}
```

Response:

```json
{"result": {"type": "ok", "sent_to": "window", "window_id": "3a42ab8f-9eca-4738-8991-5f21aeb206f3", "window": "docs", "keys": 5}}
```

`sent_to` is `client` when the attached client took the keys; there is no
window then.

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

### run

Type one command line at a pane's shell prompt, wait for the shell to report
that it finished, and return its exit status and what it printed.

It rests on the shell's OSC 133 marks: A where the prompt starts, B where the
input starts, C when the command runs and `D;<status>` when it finishes. fish
and zsh send them with prompt integration on, bash with a setup, and every
shell a terminal like Ghostty, kitty or WezTerm injects its script into. The
daemon reads them from the pane's own output, so nothing needs installing in
tuios.

Params:

- `session` (optional), `window` (optional; the focused window when omitted).
- `command` (required string): one line, with no control characters. It is
  typed as a bracketed paste when the shell has asked for one, then submitted
  with Enter. Several commands are joined with `;` or `&&`.
- `timeout` (optional int): milliseconds to wait for the command. Default
  30000. The command keeps running after a timeout.
- `lines` (optional int): keep only the last N lines of the output.

What it refuses, with nothing typed:

- `no_shell_integration`: the shell has sent no mark. A pane that has not
  sent one yet gets three seconds to draw its first prompt, so a window
  opened a moment ago is not refused for being slow to start. The same code,
  with a message that says "prompt marks only", refuses a pane whose shell
  has run a command without the C mark (`prompt_marks_only` in
  `list-windows`). A fresh pane cannot show that before a command runs, so the
  first `run` there types; when the shell draws a new prompt with no C first,
  that `run` returns this error at once rather than at its timeout, and says
  the command was typed. bash needs 4.4 or newer for the bash recipe's C mark.
- `not_at_prompt`: a command is running in the pane. The message names it, and
  the hint is the `wait-for command-finished` call with the pane's
  `command_seq`. Another `run` in the same pane that has not ended yet is
  refused the same way, with the message "another run is typing or running",
  so two callers sharing a pane never type into one line: a pane runs one
  `run` at a time.
- `invalid_params`: the command holds a newline or another control
  character. A newline at a prompt is Enter, so a second line would run as a
  second command the result says nothing about.

Response:

```json
{"result": {"type": "command_result", "session": "work", "window": "4f1c...", "cmdline": "go test ./...", "exit_code": 1, "duration_ms": 8123, "command_seq": 5, "output": "--- FAIL: TestX ...", "truncated": false}}
```

`exit_code` is omitted when the shell sent no status, which bash integrations
do for a command ended with ctrl+c. `output` is plain text read out of the
pane between the C and D marks, at most its last 256 KiB; `truncated` says it
was cut or its start had already left the scrollback. `cmdline` is the command
line as the shell showed it, cut to 512 bytes, with likely secrets masked the
way an Inbox summary is.

A timeout returns `timeout` with a hint naming `wait-for command-finished`
with the `command_seq` from before the command, which matches as soon as it
finishes, or at once if it already has.

`run` grants nothing that `send-text` and `capture-pane` do not: a caller that
can type into a pane and read it back can already do all of it. What it adds is
the refusal to type into a running program.

### capture-pane

Capture a pane's content, rendered from the daemon side terminal emulator.

Params:

- `session` (optional), `window` (optional).
- `source` (optional): `visible` (the viewport, the default), `recent`
  (viewport plus scrollback) or `last-command-output` (what the last finished
  command printed, read between its shell's OSC 133 marks; see [run](#run)).
  Any other value is rejected with `invalid_params`; the hint names the
  accepted set. `last-command-output` is plain text, so `styled`, `ansi` and
  `resolved` are refused with it; its result adds `cmdline`, `exit_code`
  (omitted when the shell sent none), `command_seq` and `truncated`, and it
  fails with `no_shell_integration` when no command has finished under the
  marks.
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

Params: `repo` (a directory inside the repository, required unless
`repo_url` is passed), `branch` (required), `base`, `name`, `command` (argv
for the first window instead of a shell), `repo_url`, `repos_root`, `clone`.

`repo_url` names the repository by its origin URL, for a caller on another
machine where a path means nothing. The daemon finds its own checkout whose
origin is the same repository, after folding the spellings git accepts for one
remote (`https://github.com/o/r`, `git@github.com:o/r.git` and
`ssh://git@github.com/o/r` are one repository). It looks under `repos_root`
(absolute, or `~/` for the daemon's home) three levels deep, or when that is
omitted under `~/src`, `~/dev`, `~/code`, `~/projects`, `~/repos`, `~/git`,
`~/work`, `~/go/src`, the home itself one level deep, and
`$XDG_DATA_HOME/tuios/repos`. Two checkouts of one origin are refused with
`invalid_params`, the hint listing both, and none is `repo_not_found`. With
`clone`, none is cloned into `repos_root` or `$XDG_DATA_HOME/tuios/repos`. A
clone fetches only `https`, `ssh` and `git` URLs, or `user@host:path`: a local
path, a `file` URL, a transport helper such as `ext::` or anything starting
with a hyphen is refused before git runs, and git runs with
`GIT_ALLOW_PROTOCOL=https:ssh:git`, no terminal prompt and ssh in batch mode.
The result carries `cloned: true`.

Request:

```json
{"verb": "new-worktree", "params": {"repo": "/src/api", "branch": "feat/retry", "base": "main", "command": ["claude"]}}
{"verb": "new-worktree", "params": {"repo_url": "git@github.com:acme/api.git", "repos_root": "~/src", "branch": "feat/retry"}}
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
session: `pending`, `held`, `sent`, `not_sent` with a `prompt_note`, or
`stalled` with a `prompt_note` when the prompt was typed and the agent showed
no sign of taking it. A stalled prompt may still be in the agent's input box.
`held` is a prompt whose agent has not been ready for 30 seconds and is not on
`needs_input`: the Inbox holds a `question` for its pane saying it waits at a
screen tuios does not recognise, and the prompt is typed as soon as the agent
is ready. The question closes when the prompt is typed or given up on, or when
the pane's state changes. Once the prompt is typed, `prompt_ready_by` says on
what: `idle`, `done`, or `quiet` for `unknown` on a harness that cannot show
idle. The harness the daemon started counts from the start, before detection
has named the pane's harness.

An agent is one string, the way a person types it: a harness id or program
name, or any program, with its arguments after it (`"codex --model o5"`). It
is split into words the way a POSIX shell splits them, single and double
quotes and backslashes included, and exec'd directly; nothing is expanded or
substituted, and no shell runs. A path must be absolute. A program no manifest
recognises is ready only once it reports a state of its own.

Params: `count` (1 to 16; required unless `prompts` sets it), `agent` (one
agent for every session) or `agents` (a list, cycled across the sessions;
one of the two is required), `prompt` (one for every session) or `prompts`
(one per session, in order; `count` must equal its length when given), `repo`
(required unless `repo_url` is passed), `base`, `name` (branch stem),
`ready_timeout` (milliseconds, default 600000), `env` (an object of variable
names to values, on top of the daemon's environment; `PATH` in it is where the
programs are looked up), and `repo_url`, `repos_root` and `clone` as for
`new-worktree`. The result names the checkout used as `repo_root`.

`env` rules: at most 64 variables, a value at most 32 KiB and all of them at
most 256 KiB; a name is `[A-Za-z_][A-Za-z0-9_]*`; `TUIOS_` names, `TMUX` and
`TMUX_PANE` are refused with `invalid_params`, since tuios sets the first for
every pane and strips the others on purpose; a call from another machine (over
a link, or from a hosted pane) that passes `env` is refused with `forbidden`,
because its variables describe that machine. The variables go to the process
only: they are not logged, not saved, and a pane a restore brings back starts
with the daemon's environment. The `TUIOS_` variables and `TERM` are set after
them, so they cannot be overridden.

Each entry of `sessions` gains `agent` (the harness id, empty for a program no
manifest knows) and `command` (the argv as one line). The top-level `agent`,
`command` and `prompt` are the first session's.

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

### start-agent

Start an agent in a new pane of a session, and answer once it is ready for a
prompt, on the same evidence `fan` waits for: `idle` or `done`, from a report,
a hook, the screen or the title, with `unknown` counting only for a harness
that can never show idle. A pane on `needs_input` ends the wait at once with
`ready: false`, `outcome: "blocked"` and `blocked_by`, and is kept: whatever it
asks is the person's to answer, and the Inbox already shows it. A pane that is
not ready for 30 seconds gets the same Inbox question as a held `fan` prompt.
With `prompt`, the first prompt is typed once the agent is ready and checked
the way `fan` checks it.

The session `session` names is created when it does not exist. With no
`session`, the most recently active one is used, or a new one named after the
harness or program when there is none. The pane starts in `cwd`, or in the
main checkout of the repository `repo` or `repo_url` names (see
[new-worktree](#new-worktree) for `repo_url`, `repos_root` and `clone`), or
else in the focused pane's directory. Passing `cwd` and a repository is
`invalid_params`. From another machine it is reached like every session verb,
with a host-qualified session, and names the repository by `repo_url`.

Params: `session`, `agent` (required, written as for `fan`), `args` (more
argv after the agent's own words), `name` (the window's name, which
`list-agents` shows and `-w` and `name:` take), `cwd`, `repo`, `repo_url`,
`repos_root`, `clone`, `workspace`, `focus` (default false), `prompt`,
`ready_timeout` (milliseconds, default 120000), `env` (the rules of `fan`),
`protocol` (`acp` or `codex`; see below).
The agent is checked before a clone, so a missing agent does not cost one. A
call that reaches the daemon
over a host link is refused with `forbidden` when it carries `env`, since the
variables describe the caller's machine. `tuios start-agent -s host:session`
therefore sends no `env` unless `--env` was passed, and the agent is looked up
on the far machine's `PATH`.

Response:

```json
{"result": {"type": "agent_started", "session": "work", "session_id": "...", "created_session": false,
 "window_id": "4be1c09a-...", "pty_id": "...", "workspace": 1, "name": "reviewer", "agent": "claude-code",
 "command": "claude", "cwd": "/src/api", "ready": true, "ready_by": "idle", "state": "idle",
 "outcome": "ready", "prompt_status": "sent"}}
```

`cloned` is true when the call cloned the repository.

`outcome` is `ready`, `blocked`, `timeout`, `window_closed` (the program
exited), `session_closed` or `shutdown`, and `reason` says in words why a pane
is not ready. `prompt_status` is `sent`, `stalled` or `not_sent` with a
`prompt_note`, and absent without `prompt`.

#### Headless agents: protocol

With `protocol`, the agent runs headless over a structured protocol instead of
in its own TUI, and the pane shows the conversation as a transcript:

- `acp` is the [Agent Client Protocol](https://agentclientprotocol.com),
  version 1: `initialize`, `session/new`, `session/prompt`, `session/cancel`,
  `session/update` and `session/request_permission`. `agent` is the agent's
  ACP command, such as `"opencode acp"`.
- `codex` is the Codex app-server protocol, its v2 core: `initialize` and
  `initialized`, `thread/start`, `turn/start`, `turn/interrupt`, the
  `item/*` and `turn/completed` notifications, and
  `item/commandExecution/requestApproval` and
  `item/fileChange/requestApproval`. A file change approval is never held
  for the Inbox, and its `grantRoot`, when set, is shown in the pane.
  `app-server` is added to the agent's
  words when neither they nor `args` name it, between the two, so `args` are
  the app-server's own.

The pane's process is this daemon's own binary, `tuios agent-proto --protocol
P --harness H -- <agent argv>`, and it execs the agent's argv directly, with
pipes for its stdin and stdout, in a session of its own with no controlling
terminal. It advertises no file system and no terminal capability, and answers
every request it does not handle (`fs/*`, `terminal/*`, MCP elicitations,
dynamic tool calls) with method not found, so the agent can do nothing
through tuios that it could not do in its own TUI. Everything it shows is
cleaned of escape sequences and control characters before it reaches the pane.

It reports the pane's state with `set-agent-state` under the harness the
manifest named, or the protocol: `idle` once the conversation is open,
`working` during a turn, `done` with the reply's first line, `errored` with
why, and `needs_input` with kind `approval` and the request's line when the
agent asks permission. The pane is ready on that report alone. A prompt from
`prompt`, `ask-agent` or `send-text` is typed into its prompt line and sent as
one turn.

A permission is shown in the pane with a number key per answer. When its line
is the whole request (see [request-approval](#request-approval): a command
with nothing the line leaves out, not a diff, an edit or input to a running
command), the pane program also holds it with `request-approval`, which a
protocol pane may do without `[agents.approvals]`. The first answer wins: a key
in the pane ends the hold by closing its connection, and a decision from the
Inbox answers the agent and is written into the pane. The Inbox offers `once`
and `deny` only, mapped to ACP's `allow_once` and `reject_once` or Codex's
`accept` and `decline`; the other answers, which allow more than the one call,
are the pane's. A digit answers only once the question has been on screen for
half a second, and a paste never answers. Ctrl+C in the pane cancels the turn
and answers the agent `cancelled` (Codex: `cancel`).

The result gains `protocol`, and `command` is the agent's command, not the pane
program's. `list-agents` shows `protocol` for the pane.

### compare-fan

The attempts of a fan side by side: one row per session of the fan, in the
order fan made them. Name any session of the fan; a worktree session that is
not part of a fan is `invalid_params`, with the fan sessions the caller
reaches in the hint (a pane without `admin` sees none outside its own reach),
and a session that is not a worktree is `not_worktree`.

Params: `session`, `changes` (default true: count each attempt's changes,
which runs a few git calls per sibling: the merge base, a snapshot of the
working state, the diff, the ahead count and `git status`. All of one
sibling's calls together are bounded at 10 seconds, and a sibling that passes
it reports the row without counts, with `note` saying so).

Each row carries `session`, `branch`, `path`, `agent` (as the fan named it),
`harness`, `state` (rolled up over the session's windows), `prompt_status`,
and:

- `files`, `added`, `removed`: what the attempt changed against its base,
  committed and uncommitted work together, untracked files included and
  ignored files left out. The working state is read through a temporary
  index that starts as a copy of the worktree's own, so git's stat cache
  holds and the worktree's own index and files are not touched. The counts
  run from `base_sha`: the merge base of the attempt's `HEAD` and the fan's
  `base`, or for a fan made from `HEAD`, of the attempt's `HEAD` and the main
  checkout's `HEAD`. Counting from the merge base rather than the base's tip
  means a base that moves on (a fetch, or another attempt merged into it)
  does not change a row. When no merge base resolves, the counts run from the
  base's tip and `base_sha` is omitted. `ahead` counts the attempt's commits
  past `base_sha`, and `dirty` says whether it holds uncommitted work. All are omitted with `changes` false, and when git
  could not count, in which case `note` says why.
- `verify`: the last `verify-fan` check, as the worktree record holds it.
- `last_command`: the newest command a shell in the session finished, from
  its OSC 133 marks: `cmdline`, `exit` (omitted when the shell sent none),
  `at` (unix nanoseconds) and `window`. Omitted when no shell has marked one.
  An agent's own tool runs do not show here, which is what `verify-fan` is
  for.
- `gone` when the worktree directory no longer exists.

A pane without `admin`, or a connection restricted to its own session, gets
rows only for the sessions it could name itself: its own session, its fan
group, and the sessions a fan run from it started. Over a link it needs `list`,
because it returns counts and states, not file contents.

```json
{"verb": "compare-fan", "params": {"session": "api-fan-retry"}}
```

```json
{"result": {"type": "fan_compare", "group": "fan/retry", "repo": "api", "repo_root": "/src/api",
 "base": "main", "total": 2, "rows": [
  {"session": "api-fan-retry", "branch": "fan/retry", "agent": "claude", "harness": "claude-code",
   "state": "done", "files": 4, "added": 120, "removed": 31, "ahead": 2, "dirty": true,
   "prompt_status": "sent", "verify": {"command": "go test ./...", "state": "passed", "exit": 0,
   "started_at": 1790000000000000000, "finished_at": 1790000012000000000}},
  {"session": "api-fan-retry-2", "branch": "fan/retry-2", "agent": "codex", "harness": "codex",
   "state": "working", "files": 2, "added": 40, "removed": 3, "ahead": 0, "dirty": true,
   "prompt_status": "sent", "last_command": {"cmdline": "make lint", "exit": 1, "at": 1790000030000000000, "window": "9c2e..."}}]}}
```

### verify-fan

Run one check in every attempt of a fan. Each session of the fan the caller
reaches gets a window named `verify`, in its worktree, running the command
with `sh -c` (`cmd.exe /c` on Windows). The call returns once the windows are
open; each result lands in the session's worktree record as `verify`, which
reaches clients with the ordinary state push and which `compare-fan` reports.

- The command is always the caller's, at most 4096 bytes. tuios never reads a
  check from the repository, so cloning a repository cannot make `fan` run
  its code.
- The window holds the grants `none`, whatever the default is, so the check
  cannot call tuios.
- The check's exit status reaches the daemon on a pipe the check itself does
  not inherit, so nothing it prints can pass for its status. A check that
  passes closes its window. One that fails keeps it open, with the output, and
  says so; enter closes it. On Windows the window closes either way.
- `timeout_ms` fails a check that runs longer and closes its window; the
  record's `note` says it timed out. Without it a check runs until it ends.
- A check still running in a session is stopped, and its window closed,
  before the new one starts. So is the window a failed check left open, so
  running a check again does not pile up windows waiting for enter.
- A session whose worktree directory is gone is skipped, and `skipped` says
  so. When nothing could be started the call is `internal`, with the reasons
  in the hint.
- `env` adds variables for the check, with the rules of `fan`'s `env`. The
  tuios CLI sends its `PATH`.

A pane needs the `fan` grant, and reaches its own fan group; the checks start
only in the sessions it reaches. Over a link it needs `open` and `write`.

Params: `session`, `command` (required), `timeout_ms`, `env`.

```json
{"verb": "verify-fan", "params": {"session": "api-fan-retry", "command": "go test ./...", "timeout_ms": 600000}}
```

```json
{"result": {"type": "fan_verify_started", "group": "fan/retry", "command": "go test ./...",
 "sessions": ["api-fan-retry", "api-fan-retry-2"]}}
```

The record while it runs and once it ends:

```json
{"command": "go test ./...", "state": "running", "started_at": 1790000000000000000}
{"command": "go test ./...", "state": "failed", "exit": 1, "started_at": 1790000000000000000, "finished_at": 1790000042000000000}
```

### keep-fan

Keep one attempt of a fan and remove the others. Each sibling is removed the
way `remove-worktree` removes it, each on its own: a sibling with uncommitted
changes is left in place, with `worktree_dirty`, unless `stash` or `force`
says what to do with them, and the others still go. Branches are never
deleted. A check running in a removed sibling is stopped. A session that is
not part of a fan is `invalid_params`.

Only the person or a caller with `admin` may call it: a pane without `admin`
and any restricted connection are refused. Over a link it needs `write`.

Params: `session` (required), `stash`, `force`.

```json
{"verb": "keep-fan", "params": {"session": "api-fan-retry-2"}}
```

```json
{"result": {"type": "fan_kept", "kept": "api-fan-retry-2", "branch": "fan/retry-2", "group": "fan/retry",
 "repo": "api", "left": 1, "removed": [
  {"session": "api-fan-retry", "removed": true, "branch": "fan/retry", "path": "/home/u/.local/share/tuios/worktrees/api/fan-retry",
   "changes": 0, "stashed": false, "discarded": false, "session_killed": true, "branch_kept": true},
  {"session": "api-fan-retry-3", "removed": false, "code": "worktree_dirty",
   "note": "/home/u/.local/share/tuios/worktrees/api/fan-retry-3 holds 1 uncommitted change. Nothing was removed."}]}}
```

### bundle-worktree

Read a worktree session's work out in chunks, so it can cross a link that caps
a reply line at 16 MB. `tuios worktree pull` is the caller. The transfer is the
branch's commits as a git bundle, then the uncommitted work, untracked files
included, as a binary patch against HEAD. The patch is read through a
temporary index, so the worktree's own index is not touched.

The first call names `session` and makes the transfer. Without `full`, the
bundle holds only the commits past the merge base of HEAD and the worktree's
`base`, and the reader must have `base_commit`. With `full`, or when the base
is not known, it holds the whole branch. A branch with nothing past the base
has no bundle (`bundle_bytes: 0`). The reply carries `token`, `repo`,
`origin_url`, `branch`, `base`, `base_commit`, `head`, `full`,
`bundle_bytes`, `patch_bytes`, `changes` (paths the patch touches), `size`,
`sha256` of the whole transfer, and the first chunk.

`branch` and `head` are what the worktree's HEAD is on when the call runs, not
the branch the session was made on. An agent that made its own branch in the
worktree has that branch carried. A detached HEAD is refused with
`not_worktree`, since there is no branch to carry. HEAD is read again once the
bundle and the patch are written, and if it moved in between, from a commit or
a branch switch, the call answers `git_failed` and nothing is kept. Try again.
The bundled branch always ends at `head`, and the patch is made against it.

Every reply carries a chunk: `content` (base64, at most 4 MB before encoding),
`offset`, `next` and `done`. Later calls pass `token` and `offset` set to the
previous `next`. `release` with `token` ends a transfer early.

A transfer is readable only by the connection that made it. It ends after its
last chunk, on `release`, when that connection closes, or after ten minutes
unread, and its files are removed each time. At most four are open on one
daemon, and past that the call is `rate_limited`. A transfer is capped at
512 MB.

Request:

```json
{"verb": "bundle-worktree", "params": {"session": "api-fan-add-retry-2"}}
{"verb": "bundle-worktree", "params": {"token": "3f...", "offset": 4194304}}
```

Response to the first call:

```json
{"result": {"type": "worktree_bundle", "session": "api-fan-add-retry-2", "token": "3f...", "repo": "api",
 "origin_url": "git@github.com:acme/api.git", "branch": "fan/add-retry-2", "base": "main",
 "base_commit": "9c1e...", "head": "b07a...", "full": false, "bundle_bytes": 2310, "patch_bytes": 812,
 "changes": 2, "size": 3122, "sha256": "...", "content": "...", "offset": 0, "next": 3122, "done": true}}
```

### What a caller on another machine can do with these

`start-agent`, `fan` and `new-worktree` with `repo_url` or `clone`, and
`bundle-worktree`, grant a caller on a link or in a pane nothing it could not
already do with `new-window` and a command: start a program in a directory of
this machine, or read files there. Over a link they are held to the per-host
link policy ([What a linked machine may do here](#what-a-linked-machine-may-do-here)):
`start-agent`, `fan` and `new-worktree` need `open`, and a clone checks `open`
again at the point it would run. `bundle-worktree` needs `write`, since it
reads a worktree's files, and `write` already lets a machine type into a shell
here and read any of them. What they add on their own is bounded: a clone
fetches only network URLs, the search reads only `.git/config` files under the
roots named above, and a transfer is only readable by the connection that made
it.

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
- `activity`: the hook event itself, for the pane's activity ring: `event`
  (`prompt`, `tool`, `tool_done`, `tool_failed` or `turn_end`), `tool`,
  `target`, `text`, `files`, `ok` and `model`, all optional but `event`. It is
  recorded when the report passes the nested-session guard below, whether or
  not the state applies, and read back with
  [agent-activity](#agent-activity). It also sets the reserved metadata keys
  `now` and `prompt` (see [set-agent-meta](#set-agent-meta)). An unknown
  `event` is `invalid_params` and nothing is applied. The answer then carries
  `activity_recorded`.

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

`reason`, `activity_recorded` and the hook fields are additive. A client that
sends none of the fields is handled exactly as before. A daemon older than them does not
reject them: params are decoded leniently, so it ignores the fields and applies
the report without them. A client that depends on one, `if_state` above all,
has to ask `list-verbs` for `set-agent-state` first and check the field is
listed. `tuios agent-hook` and `tuios set-agent-state --if-state` do, and the
hook sends `activity` only to a daemon that lists it.

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

### resume-agent

Resume the agent conversation recorded for a pane: type the harness's resume
command into the pane's shell. A daemon restart ends every program in every
pane, and the restore starts a new shell in each; this is how a pane gets its
conversation back. It brings back the conversation, not the process: the turn
that was running when the daemon stopped did not finish.

Params: `session`, `window` (default: the focused window), `dry_run` (return
the command without typing it).

```json
{"verb": "resume-agent", "params": {"session": "work", "window": "build"}}
```

```json
{"result": {"type": "agent_resumed", "window_id": "3f2a9c1e", "harness": "claude-code", "agent_session_id": "5f1c", "argv": ["claude", "--resume", "5f1c"], "command": "claude --resume 5f1c", "typed": true}}
```

The command is the harness manifest's `[resume] argv` with `{session_id}`
replaced by the window's `agent_session_id`. The harness is the window's
`agent_session_harness`, else its `harness_id` for state written before that
field existed. The command is typed followed by a carriage return, and the
pane's `resume` Inbox item, if one is open, closes with reason `resolved`.

Failures, each with nothing typed:

- `not_resumable`: no `agent_session_id` on the window; a harness with no
  `[resume]` block; an id that is empty, over 256 bytes, starts with `-`, or
  holds anything but letters, digits and `_ . / : -`; or a window whose
  process runs on another machine.
- `not_ready`: the pane's shell does not hold its terminal's foreground, so a
  program is running there. Where the kernel does not report the foreground
  process group (Windows, the BSDs), the daemon falls back to the detector's
  last reading: no foreground program and agent state `none`.

Security: what a caller can make it type is fixed by the manifest and by an id
already stored on the window, and the manifest loader and the id check hold
every token to characters every supported shell (sh, bash, zsh, fish,
PowerShell, cmd) reads as one unquoted argument, so a pane that reported a
hostile id cannot turn it into a second command. It types only into a pane at
its shell prompt. That is strictly less than `send-text`, which every caller of
the socket, a pane or a link included, already has, so it is not gated on the
person. User manifests (under the user harness directory) can set any
program as the first token; that directory is the user's own configuration.

Restore behaviour, from `daemon.resume_agents`:

- `ask` (default, and any unrecognised value): one `resume` Inbox item per
  restored pane with a resumable conversation. The Inbox answers it with `y`,
  which calls this verb.
- `auto`: the daemon waits for each restored shell to draw its prompt (up to
  10 seconds, then 300 ms of quiet), and types the command, 100 ms apart
  between panes. A pane that does not get there, or whose shell is not in the
  foreground, gets the `ask` item instead.
- `off`: nothing. The id stays on the window, so this verb still works.

Wire compatibility: a new verb. An older daemon answers `unknown_verb`.

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

Reserved keys: `now` (what the agent is doing, such as `Bash: go test ./...`)
and `prompt` (the first line of the last prompt) are written by tuios from the
`activity` a hook reports with `set-agent-state`, with source `activity`. This
verb refuses them with `invalid_params`, and its `clear` leaves them. A hook
that names a model also sets `model`, with source `hook`, which a caller may
still write.

Unchanged writes: a key set to the value it holds, by the source that wrote
it, changes nothing, and its TTL is renewed only once less than half of it is
left. A call that changes nothing leaves the session version alone and pushes
nothing to clients, so a feed may write as often as it likes.

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

Keys tuios writes itself, each only when the harness states it: `model`,
`context` (`42%`), `cost` (`$1.20`, or the amount and an ISO 4217 code for
another currency) and `plan` (`3/7`). The source says which feed wrote them:
`statusline` for `tuios agent-statusline` (Claude Code's status line and the
opencode plugin), `protocol` for a protocol pane. Both write only their own
pane, and a caller may overwrite or clear them like any other key; they are
display only (see [Agent metadata](AGENT_STATE.md#what-feeds-it)).

Wire compatibility: the metadata rides the window state as an additive field
(`agent_meta`). An older client drops it and draws nothing, and an older daemon
answers the verb with `unknown_verb`.

### list-attention

List the Inbox: everything waiting for the person, in every session on this
daemon and on every linked host it follows (see [Following linked
hosts](#following-linked-hosts)). Each item is one of seven kinds, and the list
is grouped in this order, oldest first inside each group:

| Kind | Opens when | Closes when |
| --- | --- | --- |
| `approval` | A pane goes to `needs_input` with `blocked_by` `approval`. | The pane leaves `needs_input`. |
| `ask` | `ask-human` puts a question to the person. `summary` is the question, `options` its answers and `request_id` the question's id. | The person answers it (`answered`, with `answer` and `answered_by`) or dismisses it, the asking pane asks another (`superseded`) or closes. |
| `question` | A pane goes to `needs_input` with any other `blocked_by`, or none. | The pane leaves `needs_input`. |
| `mail` | A message to `human` lands in a thread. One item per thread; `count` is the unread messages. | The person's mail in the thread is read. |
| `errored` | A pane goes to `errored`. | The pane leaves `errored`. |
| `resume` | A restore brings back a pane with a conversation its harness can resume, with `daemon.resume_agents` on `ask`. The summary is the command. | `resume-agent` types it, or the pane goes to `working` or `needs_input` (an agent is running there again). |
| `finished` | A pane's `completion_seq` goes up as it comes to rest. | An attached client focuses the pane, the agent starts another turn (`working`), blocks, or errors. |
| `outbox` | `send-agent-message` with `host` is kept here because that machine's link is down, or a kept message was refused there. One item per machine, with `for_host`; `count` is the messages waiting, and the summary says how many and the last refusal. | Everything waiting was delivered and nothing was refused, or `dismiss-attention`, which also discards what waits. |

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

Params: `session` (optional; unlike most verbs, omitted means every session;
without `host` it names a session on this machine), `kinds` (optional list,
from `approval`, `plan`, `ask`, `question`, `mail`, `errored`, `resume`, `finished`, `outbox`),
`include_snoozed` (optional bool: also list the items the person snoozed,
after the open ones and in the same order among themselves, each with
`snoozed_until`; they are not in `counts`), `host`
(optional: `local` for this machine or a linked host's name; omitted means
every machine; an unknown name is `unknown_host`), `select` (optional, a
[selector](#selectors): keeps the items it matches, reading an item's state
from its kind, `needs_input` for an approval or a question, `errored`, `done`
for finished; `group` and `cwd` are known for items of this machine only).

Response:

```json
{"result": {"type": "attention_list", "items": [{"id": "17", "kind": "approval", "session": "fan-3", "window": "3f2a9c1e", "workspace": 1, "harness": "claude-code", "name": "claude", "summary": "approve Bash: go test ./...", "since": 1790142942055373000, "seq": 41}], "counts": {"approval": 1, "question": 0, "mail": 0, "errored": 0, "resume": 0, "finished": 0}, "total": 1, "seq": 1180, "boot_id": "9f2c41d07a3e8b65"}}
```

Item fields: `id` (stable, never reused on this machine; an item of a linked
host is `host:id`, the host's own id after the colon), `kind`, `host` (empty
for this machine, the host's name for an item of a linked host), `session`,
`window`, `workspace`, `harness`, `name`, `summary`, `options` (the decisions
`reply-approval` takes, set only while a hook holds the item), `request_id` and
`expires` (the held request and when its hold ends, in unix nanoseconds; see
[request-approval](#request-approval)), `always_scope` (what answering
`always` adds, one rule per line, set only while held with `always` offered),
`since`
(unix nanoseconds, when the item started waiting; an update keeps it), `seq`
(the Inbox revision of the item's last change), `thread` and `count` (mail),
`completion_seq` (finished), `snoozed_until` (a snoozed item listed with
`include_snoozed`: when it opens again, in unix nanoseconds, or -1 for when
its fact changes), `marked_unread` (a finished item the person reopened with
`mark-attention` `unread`), `stale` and `seen_at` (an item of a host whose
link is down: what the host said last, and when this daemon last heard from
it, in unix nanoseconds), `held_id` and `held_for` (mail from another machine
held for the person by `hold_mail`: the message `release-agent-message` takes
and the window it was for), `for_host` (outbox: the machine the mail waits
for).

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
under a fresh id, so no two items ever share one. A snoozed `finished` or
`errored` item is saved with `snoozed_until` and comes back asleep; one whose
time passed while the daemon was down wakes on start. It drops `resume` items
too: the restore that runs on start opens them again from each window's
`agent_session_id`, after the saved items are loaded. `outbox` items are
opened again from the outbox, which is saved on its own and survives a
restart whole.

Wire compatibility: new verb and new event type. An older daemon answers
`unknown_verb`, and the tuios client then shows the Inbox as unavailable.

### Selectors

A selector addresses every agent pane that fits a description. It is one
string of terms separated by spaces, all of which must match; a term is
`key:value`, and commas in the value are alternatives. The keys are `harness`
(the harness id, or a program name a manifest detects), `state`, `needs`
(only `needs:you`: `needs_input` or `errored`), `session` (glob), `group` (the
fan-out group, glob), `host` (`local` or a host name, glob), `name` (window
name, glob) and `cwd` (the directory or under it; a leading `~` is the
daemon's home). Globs are `path.Match`: `*` does not cross a slash. A term a
pane cannot answer does not match. A selector that does not parse is
`invalid_params` naming `select`, with the keys in `accepted`.

Reads: `list-agents`, `list-host-agents`, `list-attention` and `wait-for`
(`agent-state`) keep what the selector matches. Writes: `send-agent-message`
and `ask-agent` send to every agent pane on this machine the selector matches,
in every session, and take no `session`, no `window` or `to`, and for a message
no `reply_to`. A write reaches at most 32 panes, an ask at most 16; more is
`invalid_params`, and none is `window_not_found`.

A write is confirmed before anything is sent. Without `confirm` the call fails
with `confirm_required`:

```json
{"id": 1, "error": {"code": "confirm_required", "message": "the selector matches 2 panes; nothing was sent",
 "hint": {"param": "confirm", "available": ["api-fan-retry/codex (3f2a9c1e)", "web/codex (91bd07aa)"],
  "confirm": "5c1f0e9ad2b37744", "detail": "..."}}}
```

The token is a hash of the set of panes (each by window id). The call again
with `"confirm": "5c1f0e9ad2b37744"` goes ahead only if the selector still
matches exactly that set; otherwise it fails with `confirm_required` again and
the new set. `list-agents` with the same `select` answers with the same token
in `confirm`. The token is not a secret and grants nothing: it only says the
caller saw the set.

A confirmed message answers `agent_messages_sent`:

```json
{"result": {"type": "agent_messages_sent", "select": "harness:codex", "sent": 2, "failed": 0, "total": 2,
 "results": [{"session": "api-fan-retry", "window": "3f2a9c1e-...", "name": "codex", "ok": true, "message_id": 14, "thread_id": 14},
             {"session": "web", "window": "91bd07aa-...", "name": "codex", "ok": true, "message_id": 3, "thread_id": 3}]}}
```

A confirmed ask answers `agent_replies`, with `replies` holding one row per
pane: `ok`, and either the fields of a single ask's answer (`reply`,
`settled_by`, `state`, `lines`, `truncated`, `waited_for`) or `error` with the
code, message and hint the single ask would have failed with. `answered` and
`failed` count them, and `untrusted` is always true.

Each pane of a write goes through the checks of a single call: the rate cap
per pane, the `human` rules, `agent_blocked` for a pane on `needs_input`
unless `allow_blocked`, the wait for a working pane unless `force`, and the
loop guard. `from` may name the sender's pane in another session by its exact
window id. A call that names `select` from a pane on another machine running
its calls through its owner is refused with `forbidden`, since such a call
acts only as the window it is drawn in.

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
opens a new item. A snoozed item can be dismissed by its id too.

For 10 seconds after, the dismiss can be undone with
[mark-attention](#mark-attention) `restore`, except on an `outbox` item, whose
dismiss discarded the mail, and an `ask`, whose asker was told.

An item of a linked host is hidden on this daemon and nothing else: nothing on
the host is marked, and the item comes back when the host changes it. The
result then carries `host`.

Response:

```json
{"result": {"type": "attention_dismissed", "id": "17", "kind": "approval", "session": "fan-3", "dismissed": true}}
```

### mark-attention

Snooze an Inbox item, wake it, mark a finished pane unread, or restore an item
dismissed or snoozed in the last 10 seconds, for the person.

Params: `action` (required: `snooze`, `wake`, `unread` or `restore`), `id`, or
`session`, `window` and `kind` to name the item by what it is about (`unread`
needs only `session` and `window`), `human_nonce` (required, as for
[dismiss-attention](#dismiss-attention)). With `snooze`, exactly one of
`until` (unix milliseconds, within the next year), `for_ms` (at most a year)
and `until_change` (true).

- `snooze` closes an open item with the reason `snoozed`. It opens again with
  its id and `since` when its time comes, when its fact changes (the pane
  reports something the item does not already say, or a hook starts holding
  the approval), or on `wake` or `restore`. A report that repeats what the
  item says leaves it asleep. `until_change` waits only for the change. A
  snooze of an item already asleep changes when it wakes. Only these kinds
  can be snoozed: `finished`, `errored`, `mail`, `resume`, `question`, and an
  `approval` no hook is holding. A held approval, a `plan` and an `ask` wait
  on an answer, and `outbox` is about a link, so each is `invalid_params`.
- `wake` opens a snoozed item now.
- `unread` reopens a pane's finished turn: the daemon forgets that it was
  seen, so the next client to focus the pane marks it seen again, and a
  `finished` item opens (or an open one is marked) with `marked_unread`. The
  pane must be on this machine, have finished a turn, and be at rest.
- `restore` reopens an item this daemon closed for a dismiss or a snooze less
  than 10 seconds ago, with its id and `since`. A restored `finished` item
  forgets the look again, like `unread`; restored `mail` comes back as a row,
  and its messages stay read. An approval, question, plan or error comes back
  only while the pane is still in the state it was about, and nothing comes
  back over a newer item about the same thing. The check and the reopen are
  one step: a transition that arrives during a restore lands after it and
  closes the restored item as it would any other.

An item of a linked host is snoozed on this daemon only, like a dismiss:
nothing on the host changes, and the item wakes when the host changes it or
its time comes. `unread` does not reach another machine's panes.

A client finds out whether a daemon has this verb by asking `list-verbs` for
it once per attach; tuios's own client hides its snooze, undo and unread keys
from a daemon that does not.

Only the person can: the call is refused to a pane without `admin`
(`forbidden`), needs `respond` over a link, and carries the nonce, checked as
for `dismiss-attention` (`not_human`). An agent cannot hide, reorder or
restore what the person reads.

Errors: `not_human`, `forbidden`, `invalid_params` (an id that names nothing,
an action the item's kind does not take, a length missing or given twice, a
time in the past, a restore after 10 seconds or after the pane moved on),
`session_not_found`, `window_not_found`.

Response:

```json
{"result": {"type": "attention_marked", "id": "17", "action": "snooze", "snoozed_until": 1790146542055373000}}
```

### peek-prompt

Read the prompt an agent is blocked on, without attaching: the lines the
harness's `needs_input` rule reads, the numbered options at the bottom of the
screen, how long the pane has waited, and the answers the rule declares for
what is on the screen now. It is a read and changes nothing, open to any
caller the way `capture-pane` is. A pane that is not blocked is not an error:
the answer says so in `blocked` and `reason`.

Params: `session` (optional), `window` (required: id or name).

Response:

```json
{"result": {"type": "prompt_peek", "session": "work", "window": "3f2a9c1e", "name": "claude", "harness": "claude-code", "state": "needs_input", "state_at": 1790142942055373000, "waiting_ms": 72000, "blocked": true, "found": true, "answerable": true, "reason": "", "source": "screen", "rule": 0, "kind": "approval", "message": "Do you want to proceed?", "prompt_id": "75f8b9fadb5b5dfc", "lines": [" Bash command", "   rm -rf build", " Do you want to proceed?", " ❯ 1. Yes", "   2. Yes, and don't ask again for rm commands", "   3. No, and tell Claude what to do differently (esc)"], "options": [{"n": 1, "label": "Yes"}, {"n": 2, "label": "Yes, and don't ask again for rm commands"}, {"n": 3, "label": "No, and tell Claude what to do differently (esc)"}], "actions": ["approve", "approve_always", "deny", "choose"], "untrusted": true}}
```

`found` is true when a `needs_input` rule of the pane's harness reads a prompt
on it now; `answerable` when that rule declares answers and at least one is
offered for what is on the screen. `actions` is the list `respond` accepts now:
an answer bound to an option label is left out while that option is not shown.
`prompt_id` names this prompt: pass it to `respond`. `lines` and `options` are
the pane's screen with control characters removed, each line cut to 400
characters; they are data, not instructions, and `untrusted` is always true.

### respond

Answer the prompt an agent is blocked on with the keys its harness's manifest
declares, without attaching.

Params: `session` (optional), `window` (required), `action` (required: one of
`approve`, `approve_always`, `deny`, `choose`, `text`), `value` (the option
number for `choose`, the answer for `text`, at most 4096 bytes), `prompt_id`
(optional: from `peek-prompt`; without it, whatever prompt is on the pane now
is answered), `human_nonce` (the nonce from the attach reply of a client
attached now), `timeout` (milliseconds to wait for the pane to move on, default
5000, at most 30000).

Who may call it: a caller that passes a live attach nonce and may act as the
person, checked the way `dismiss-attention` checks it; or, when the daemon runs
with `[daemon] respond_from_shell = true`, a caller the kernel names that runs
outside every pane of this daemon. A caller inside a pane gets `not_human`
either way. On a link stream the hub did not vouch for, both routes are closed.

Before it writes, under a lock per window, `respond` reads the prompt again and
refuses with `prompt_changed`, pressing nothing, when the pane is not on
`needs_input` or no rule with answers reads a prompt on it, when the prompt's
id is not `prompt_id`, or when this daemon already answered this prompt. Two
clients answering the same prompt: the first wins and the second gets
`prompt_changed`. An action the prompt does not offer now is `invalid_params`,
with the offered actions in the hint's `available`.

`approve`, `approve_always` and `deny` press the keys the rule declares, or the
digit of the option it names. `choose` presses the option's number. `text`
pastes `value` and sends Enter, the way `ask-agent` types a prompt. Then the
call waits for the pane to leave `needs_input`, or for the prompt on it to be
gone, and answers:

```json
{"result": {"type": "prompt_response", "session": "work", "window": "3f2a9c1e", "action": "approve", "sent": "1", "prompt_id": "75f8b9fadb5b5dfc", "settled_by": "state", "state": "working", "message": ""}}
```

`settled_by` is `state` (the pane left `needs_input`), `prompt` (the prompt is
gone but the state has not followed yet), `gone` (the window closed) or
`timeout` (the prompt is still on the screen; look at the pane).

Wire compatibility: new verbs. An older daemon answers `unknown_verb`.

### request-approval

Hold a pane's permission prompt until the person answers it in the Inbox.
`tuios agent-hook` calls it; a script has little reason to. It is for a harness
that takes a decision back from its hook: Claude Code's `PermissionRequest`
hook, and opencode or Kilo through the plugin tuios installs. The call does not
answer until the person answers with `reply-approval` or the hold ends, and it
is opt in: nothing is held unless `[agents.approvals]` in the config names the
harness, or the pane is one `start-agent` opened with `protocol` (see
[Headless agents: protocol](#headless-agents-protocol)).

```toml
[agents.approvals]
enabled = ["claude-code", "opencode"]
hold_seconds = 120
```

Params: `session` (optional), `window` (required), `harness` (required, an id
or alias), `options` (optional list of the decisions the harness can take, from
`once`, `always`, `deny`; omitted means `once` and `deny`), `summary`
(required: the line the person answers from, which must be the whole request),
`always_scope` (optional list: what `always` allows from now on, one rule per
line).

The pane must already be on `needs_input` with an `approval` item open, which
the hook's own report sets up just before. The item then carries `request_id`,
`options`, `expires` and `always_scope`, its `summary` becomes the given line
for as long as the hold runs, and the Inbox shows the keys that answer it.

The person answers from that line, so it has to be the whole request. The hook
only asks for a call whose effect one argument decides and whose line shows
that argument in full (see
[Approvals from the Inbox](AGENT_STATE.md#approvals-from-the-inbox)). The
daemon checks again: a `summary` the Inbox could not show as it is, because it
is longer than 160 bytes, has a control or format character (a bidi override,
a zero width space), a newline, tab or doubled space, or text the Inbox would
mask as a secret, holds nothing and answers at once with reason `not_shown`.
`always` is dropped from `options` unless `always_scope` has one to four
lines, each of which passes the same check, so `always` is never offered
without the rules it adds on screen.

Response, when the person answered:

```json
{"result": {"type": "approval_result", "request_id": "9f86d081884c7d65", "decision": "once", "reason": "answered", "answered_by": "client-1790155072046345000"}}
```

`decision` is `once`, `always` or `deny`, or empty when there is none. An empty
decision means the harness should ask in its pane, as it would have without
tuios, and `reason` says why:

| `reason` | Meaning |
| --- | --- |
| `answered` | The person answered. `decision` is set, and `message` too when they gave a reason for a deny. |
| `disabled` | `[agents.approvals]` does not name the harness. Answered at once. |
| `not_blocked` | The pane is not on `needs_input` with an `approval` item. Answered at once. |
| `not_shown` | The Inbox could not show `summary` as it is. Answered at once. |
| `viewed` | A client of the person has the pane focused, or focused it during the hold. The prompt is quickest to answer in the pane. |
| `timeout` | `hold_seconds` passed (120 by default, kept between 10 and 300). |
| `handed_back` | The person pressed enter on the item to go to the pane, or sent `reply-approval` with `ask`. |
| `superseded` | A newer `request-approval` for the same pane replaced this one. |
| `caller_gone` | The caller closed its connection, which is how a harness that gave up on its hook shows. |
| `shutdown` | The daemon is stopping. |
| `resolved`, `dismissed`, `window_closed`, `session_closed`, `evicted` | The Inbox item closed for that reason: the pane moved on, the person dismissed it, and so on. |

Who may call it: it is refused with `forbidden` over a link, and a caller inside
a pane of this daemon may only hold its own pane's prompt (the daemon places the
caller by its process ancestry, then its controlling terminal, then its
`TUIOS_PANE_ID`; a caller it cannot place is refused). What the caller gets back
is the answer to the prompt it asked about, nothing more.

Send nothing else on the connection while the call waits. The daemon reads it
only to notice the caller going away, and a byte that arrives is discarded.

Robustness: a daemon that is gone, restarts during the hold, or predates the
verb (`unknown_verb`) gives the hook an error, and the hook prints nothing. A
hold is not saved across a restart. The hook only ever prints a decision the
daemon returned and the harness was offered.

### reply-approval

Answer a held approval for the person.

Params: `request_id` (the item's; or name the pane with `session` and `window`
instead), `decision` (required: `once`, `always`, `deny`, or `ask` to give the
prompt back to the pane with no decision), `message` (optional, the reason for
a deny, passed to the model; one line, at most 500 bytes), `human_nonce`
(required, as for `dismiss-attention`), `summary` (optional: the item's
`summary` the decision was made from; the TUI always sends it).

When `summary` is given and the hold is now on another line, nothing is
answered: the response has `applied: false`, reason `changed`, an empty
`decision` and the `summary` the hold is on now, and the hold runs on so the
person can read it and answer. This closes the gap between drawing an item and
the key arriving. `ask` is never refused this way.

Only a client attached right now can answer, with the nonce its attach reply
carried, checked exactly as `dismiss-attention` checks it: a caller inside a
pane, or on a link the hub did not vouch for, gets `not_human` even with a live
nonce. No mail, `ask-agent`, `send-keys` or `send-text` reaches a hold, so an
agent cannot approve its own call or another agent's.

A decision the item's `options` do not offer is `invalid_params`, and so is a
request that is not held. The first reply wins: a later reply for the same
request answers with the decision that stands and `applied: false`, and changes
nothing. A decision closes the Inbox item with reason `answered` and moves the
pane to `working`. `ask` ends the hold and leaves the item open.

Response:

```json
{"result": {"type": "approval_replied", "request_id": "9f86d081884c7d65", "decision": "once", "applied": true, "reason": "answered", "answered_by": "client-1790155072046345000", "session": "fan-3", "window": "3f2a9c1e"}}
```

Wire compatibility: both verbs are new, and the item fields are additive. An
older daemon answers `unknown_verb`, which the hook reads as no decision.
`reply-approval` without `summary` answers as before, with no check of the
line.

### ask-human

Put a question with a fixed set of answers to the person, and wait for the
answer. The question is an `ask` item in the Inbox. A client the person holds
that shows the asking pane opens the Inbox on it at once, unless the person
typed into the pane in the last 600 ms or an overlay is open there, and then
drops every key for 400 ms; anywhere else it waits there with the usual
alert, and with nobody attached it waits for the next attach.

Params:

- `session` (optional).
- `window` (optional): the pane asking, where a late answer is mailed. A caller
  inside a pane asks as its own pane when it names none, and naming another is
  `forbidden`. A caller outside every pane may name any pane, or none.
- `question` (required unless `request_id`): one line of printable text, at
  most 160 bytes, that the Inbox shows as written. Anything the Inbox would
  cut, mask or rewrite is `invalid_params`.
- `options` (required unless `request_id`): 1 to 9 answers, each one line of
  printable text of at most 60 bytes, no two the same. The person picks one with
  the digit keys.
- `timeout` (optional int): milliseconds to wait, default 120000, at most one
  hour. The question outlives the wait.
- `wait` (optional bool, default true): false asks and answers `pending` at
  once.
- `request_id` (optional): come back for a question already asked: wait on it
  again, or read how it ended. It takes no question or options, and a caller
  inside a pane may come back only for its own pane's questions.

Response:

```json
{"result": {"type": "human_answer", "request_id": "9f86d081884c7d65", "status": "answered", "answer": "no", "answer_index": 2, "answered_by": "client-1790155072046345000", "verified_human": true}}
```

`status` is `answered`, `pending` (the wait ended first), `dismissed`,
`superseded` (the pane asked another question), `window_closed`,
`session_closed`, `evicted` or `shutdown`. A `pending` question stays in the
Inbox; the answer, when it comes, is mailed to the asking pane from `human`
with `verified_human` set, so `wait-for agent-message` returns on it, and a
call with `request_id` reads it. A question from no pane is only read that way.
When the caller goes away while it waits, the daemon learns so only when it
cannot write the reply. If that reply was the person's answer to a question
from a pane, the answer is mailed to the pane the same way, so an agent whose
tool killed the call still gets it.

Refused over a link (`forbidden`): a question is put to the person at this
machine's clients by something on this machine. At most 16 questions from no
pane are open per session (`rate_limited`); a pane has one, and a new one
supersedes it. Questions do not survive a daemon restart.

### answer-ask

Answer an `ask` item for the person. Only a client attached right now can,
with `human_nonce` from its attach reply and from a process outside every pane,
the proof `reply-approval` takes; anything else is `not_human`.

Params: `request_id` (required), `answer` (required: one of the item's
`options`, else `invalid_params` naming them), `human_nonce` (required),
`question` (optional: the question the answer was picked from; when it is not
the item's, nothing is answered and the reply says `applied: false`,
`reason: changed`).

Response:

```json
{"result": {"type": "ask_answered", "request_id": "9f86d081884c7d65", "answer": "no", "applied": true, "reason": "answered", "answered_by": "client-1790155072046345000"}}
```

The first answer wins. A later one gets `applied: false` with the answer that
stands. The item closes with reason `answered`, carrying `answer` and
`answered_by`, so every other client can say it was answered elsewhere.

Wire compatibility: both verbs are new and the item fields are the ones an
approval already uses. An older client lists an `ask` item under its own
heading, or not at all, and cannot answer it; an older daemon answers
`unknown_verb`.

### Following linked hosts

A daemon with a `[hosts]` table follows the agents and the Inbox of every
linked host. For each host whose link is up it opens one connection over the
link, calls `list-attention` with `host: "local"` there, and subscribes to
`attention`, `agent-state`, `agent-message`, `session-created`,
`session-closed`, `window-created` and `window-closed` from the listing's `seq`
and `boot_id`. From then on:

- The host's own items are mirrored into this Inbox (see
  [list-attention](#list-attention)). Items the host mirrors from its own
  hosts are not passed on: they are that machine's to report.
- A burst of the host's agent and session events refreshes a cache of the
  host's `list-sessions` and `list-agents` answers, then publishes
  `host-changed` and pushes `MsgHostsChanged` to the attached clients.
- With `subscribe` `hosts: true`, a subscriber also receives the host's
  `agent-state`, `session-created` and `session-closed` events, with `host`
  set, `session` and `window` cut to 128 bytes, and nothing else from the
  host's event.

When the link drops, the host's items are marked `stale` with `seen_at`, and
`list-host-sessions` and `list-host-agents` answer from the cache with `stale`
and `fetched_at`. On a redial the stream resumes from the last `seq` it
delivered; a `gap` from the host, or a host that restarted, lists again.

A host whose tuios has no `list-attention` or no resumable stream is followed
by polling, as every host was before: `list-hosts` reports `events: "polling"`
with an `events_note` that names the update, and the tuios client keeps its
poll for it.

Everything a host sends is data from another machine. Lines are bounded to 1
MiB, an item's kind must be one this build knows and its id a short token,
its text is cleaned and cut as a local summary is, a host holds at most 256
items here, apart from this machine's so it cannot evict one of them, and the
cache keeps at most 512 sessions and 2048 agent rows per host. Nothing a host
sends runs a command, types into a pane, fires a hook or marks anything on
this machine.

### Reports from a pane on another machine

A window's process can run on another machine (`new-window` with `host`). Its
reports travel back over a connection the owner opened, since the link is
dialled one way:

1. The owner sends `window`, its window id, with `open-pane`. The far daemon
   exports it to the process as `TUIOS_PANE_ID` and returns `calls_token`.
2. The owner calls `pane-calls` with `pane` and `token` on a new connection.
   After the reply the connection carries requests from the far daemon,
   `{"id":1,"verb":"set-agent-state","params":{...}}`, one per line, and the
   owner answers each with `{"id":1,"result":{...}}` or
   `{"id":1,"error":{...}}`, in any order.
3. On the far machine, a call of `set-agent-state`, `set-agent-meta` or
   `set-agent-session` with `window`, `read-agent-messages` with `to`,
   `send-agent-message` with `from`, or `wait-for` with condition
   `agent-message` and `window`, set to the pane's id or window id, is sent to
   the owner, and the owner's answer is the answer.

How the grant is held to that:

- The far daemon forwards only for a caller that runs in the pane: the pane's
  process, a descendant of it, or a process on the pane's terminal, by the pid
  the kernel gave for the connection. Anyone else naming the pane gets
  `forbidden`. A platform that does not give the pid forwards nothing.
- Only the verbs above cross; `wait-for` for anything but `agent-message` is
  `forbidden`.
- The owner runs each request as the window itself: `session` and the
  addressing param are overwritten with the window's own, `transcript_path`,
  `harness_pid`, `human_nonce`, `from_host` and `any_session` are dropped,
  `send-agent-message` from anyone but the window is `forbidden`, and the call
  runs as a caller inside a pane, so it cannot act as the person. A far daemon
  that writes its own requests gets no more than the pane's process would.
- `send-agent-message` may attach only paths in the session's stash, as on
  the link: its process and the far daemon are on the other machine, so a
  path names a file on the owner they cannot see. Any other path is
  `invalid_params` before it is looked at, so the answer does not say whether
  the file exists on the owner.
- `wait-for` runs on the owner with `timeout` capped at one hour, whatever the
  request said, and ends when the channel it came on ends. The owner does not
  wait for calls in flight before it opens the next channel.
- `pane-calls` needs the token, which only the owner saw. At most 16 calls per
  pane are in flight, and lines are bounded to 1 MiB.

An owner from before this sends no `window`, and the far daemon exports no
`TUIOS_PANE_ID` and answers a call naming the pane id with
`protocol_mismatch`. A far daemon from before this refuses `open-pane` with
`invalid_params`, because it checks every request against its schema and its
`open-pane` has no `window`. Nothing is spawned by that refusal and the
connection still takes requests, so the owner sends `open-pane` again on it
without `window`. The pane opens as before, with no `calls_token`, and the
owner opens no channel.

### A pane that outlives its link

`open-pane` with `"resumable": true` asks the far daemon to keep the pane
through a dropped connection. The far daemon decides for how long, from its
own `hosted_grace` for the asking machine (ten minutes by default, `"0"` for
none, at most a day), and says so in the reply:

```json
{"id": 1, "result": {"type": "pane", "pane": "f2c1...", "calls_token": "...", "resume_token": "9e0a...", "grace": 600}}
```

With no grace given there is no `resume_token`, and the pane ends with its
connection as it always did. With one, the far daemon reads the process's
output whether or not a connection is attached, keeps the last 64 KB, and
counts every byte. When the connection drops it waits `grace` seconds. The
asking daemon reattaches on a new connection:

```json
{"id": 1, "verb": "open-pane", "params": {"resume": {"pane": "f2c1...", "token": "9e0a...", "offset": 18234}}}
```

`offset` is how many bytes of output it has received. The reply is
`{"type":"pane","pane":...,"resumed":true,"gap":false,"grace":600}`, and after
it the connection carries what was missed from `offset` on, then the live
stream, exactly as after an open. `gap` is true when more was missed than the
ring holds; the whole ring is sent then. A wrong token is `forbidden`, and a
pane whose process exited or whose grace ran out is `unknown_pane`: both are
final. A reattach closes a connection still attached to the pane.

### close-pane

```json
{"id": 1, "verb": "close-pane", "params": {"pane": "f2c1..."}}
```

Ends a hosted pane at once: its process is killed and its connection closed.
The asking daemon sends it when a window is closed on purpose. Result: `pane`,
`closed`. An unknown pane is `unknown_pane`. Over a link it needs `open`.

### What a linked machine may do here

A connection that arrives over a link is accepted on a link socket
(`<socket>.link` or `<socket>.link-human`), which only `tuios stdio-proxy` on
this machine dials. The daemon marks it before a byte is read, and holds every
verb and every binary message on it to the policy for the machine it came from.
The policy is read from the `[hosts]` table on this machine: the built-in
default, then `[hosts."*"]`, then `[hosts.NAME]`, each field inheriting from
the one before. The configuration is in
[CONFIGURATION.md](CONFIGURATION.md#what-another-machine-may-do-here).

| Capability | Verbs |
| --- | --- |
| none | `hello`, `list-verbs`, `link-peer`, `restrict-connection`, `pane-grants` (which says no pane grants apply over a link) |
| `list` | `list-*`, `session-info`, `get-window`, `capture-pane`, `screenshot`, `get-option`, `get-agent-state`, `resolve-pane`, `explain-agent-*`, `wait-for`, `subscribe`, `unsubscribe`, `peek-prompt`, `read-dir`, `compare-fan`, `agent-activity`, `get-approval` |
| `mail` | `send-agent-message`, `read-agent-messages`, `stash-put`, `stash-list`, `stash-get` |
| `open` | `new-session`, `new-window`, `split-window`, `popup`, `new-worktree`, `fan`, `start-agent`, `open-pane`, `resize-pane`, `close-pane`, `pane-cwd`, `pane-agent`, `pane-calls` |
| `write` | `send-keys`, `send-text`, `ask-agent`, `run-command`, `close-window`, `kill-session`, `focus-window`, `move-window`, `set-window`, `select-workspace`, `set-layout`, `resize`, `set-option`, `set-session-*`, `set-workspace-*`, `set-agent-*`, `resume-agent`, `request-approval`, `refresh-dock`, `remove-worktree`, `bundle-worktree`, `run`, `ask-human` (whose handler refuses a link caller anyway), `review-diff` (it returns file contents), `review-note`, `send-review`, `queue-prompt`, `cancel-queued`, `keep-fan` |
| `open` and `write` | `verify-fan` |
| `respond` | `respond`, `reply-approval`, `dismiss-attention`, `release-agent-message`, `answer-ask`, `mark-attention` |
| every one | `open-host-connection`, `set-pane-grants` (whose handler refuses a link caller anyway) |

Binary messages: `MsgList`, the PTY subscribe messages, `MsgGetTerminalState`,
`MsgReadDir` and `MsgGetLogs` need `list`; `MsgAttach` needs `list` and
`write`; `MsgInput`, `MsgResize`, `MsgClosePTY`, `MsgUpdateState`,
`MsgExecuteCommand`, `MsgCommandResult` and `MsgKill` need `write`;
`MsgCreatePTY` and `MsgResurrect` need `open`; `MsgNew` needs `open`, `list`
and `write`. A refused message is answered with `MsgError` code 10.

The default grants `list`, `mail`, `open` and `write`.

A verb or message with no entry in the table is refused over a link. A test
holds the table to the verb registry, so a new verb cannot ship without one.

**Naming the machine.** `tuios stdio-proxy` sends `link-peer` as the first
line of every connection it opens:

```json
{"id": 0, "verb": "link-peer", "params": {"peer": "laptop", "pinned": false}}
```

The name is the one the hub gave for itself in the stream's open frame (its
host name up to the first dot, lowered), or the one the proxy was started with
by `--as`, which wins and sets `pinned`. The daemon takes `link-peer` only on a
link socket, once, and before anything else on the connection; after its reply
the connection is read from scratch, so an attach can follow it. A link from a
proxy too old to send it has no name and gets `[hosts."*"]`. A daemon too old
to know `link-peer` answers `unknown_verb`, and the proxy dials again without
it. If the link sockets cannot be reached, the proxy asks `hello` on the main
socket and refuses the stream when the daemon reports `link_policy`, so a
daemon that failed to open its link sockets is not reached on a socket with no
policy.

Only a pinned name is a boundary. A hub whose ssh key may run any command can
run a shell, and can claim any name. Pin it in `authorized_keys` on this
machine:

```
command="tuios stdio-proxy --as laptop",restrict ssh-ed25519 AAAA...
```

**Holding mail.** With `hold_mail`, `send-agent-message` over the link to
anyone but `human` is stored for `human` instead, with `held`, `held_for` and
`held_for_label`, and opens a mail item in the Inbox with `held_id` and
`held_for`. The person passes it on:

### release-agent-message

```json
{"id": 1, "verb": "release-agent-message", "params": {"session": "work", "id": 12, "human_nonce": "<from the attach reply>"}}
```

Delivers the held message as a new message to the window it was for, with its
original sender, `origin` and `origin_host`, and `released_from` set to the
held id. The held copy is marked `released` and read, and its Inbox item closes
when nothing else in the thread is unread. Only a client attached right now can
call it, with its nonce (`not_human` otherwise), and over a link it needs
`respond`. A message is released once; a second call, or an id that is not a
held message the ring still holds, is `invalid_params`. A window that closed
while the message was held is `window_not_found`.

Result: `held_id`, `message_id`, `to`, `to_name`, `thread_id`.

### agent-activity

Read what the agent in a pane has been doing. The daemon keeps a ring of the
newest 256 entries per pane, in memory only, from the `activity` its hooks
report with `set-agent-state`. A pane gets a ring on the first report that
carries activity, so a plain shell pane and an agent whose harness has no
hooks have none. Once it has one, the commands its shell finishes (OSC 133)
and its agent state changes are added too. The ring goes when the window
closes, when the session ends, and with the daemon.

Params: `session`, `window` (default the focused pane), `since` (unix
nanoseconds; entries after it), `since_seq` (entries after it), `limit` (1 to
256, default 64: the newest that many), `recap` (bool).

Each entry has `seq` (per pane, from 1), `at` (unix nanoseconds) and `kind`:

| `kind` | From | Fields |
| --- | --- | --- |
| `prompt` | a prompt submitted | `text`: its first line |
| `tool` | a tool call starting | `tool`, `target` (the command, file, URL or pattern) |
| `tool_done` | a tool call that finished | `tool`, `target`, `files` it wrote, `ok` when the harness said |
| `tool_failed` | a tool call that failed | `tool`, `target`, `ok` false, `text`: the error's first line |
| `turn_end` | the agent finishing a turn | `text`: the first line of what it said last, absent when the harness sent none |
| `command` | a command the pane's shell finished | `target`: the command line, `exit` when the shell sent one |
| `state` | the pane's agent state changing | `text`: the new state |

Every string is the agent's or its shell's: one line, control characters
removed, likely secrets masked, cut to 160 bytes (a file path to 256, a tool
name to 64). A `command` entry's command line is held to the same 160 bytes,
although the shell's own record of it (`last_cmdline`, the
`command-finished` event) keeps up to 512. The answer is marked `untrusted`.

A pane's first report with activity gets its ring before its state applies,
so a first report that finishes a turn (a `Stop` from hooks installed
mid-session, or after a daemon restart mid-turn) records its `state` entry
and counts the turn, like any later one.

With `recap`, the answer also summarises every entry after `since` and
`since_seq`, whatever the `limit`:

- `since`: where the summary starts, the `since` asked for, or the oldest
  entry the ring holds when it was asked for everything or has dropped entries
  after `since`;
- `turns`: how many turns finished, the sum of the pane's `completion_seq`
  steps its state entries recorded;
- `files` (the first 20, first written first) and `files_total`;
- `commands`: shell tool calls (`Bash`, `shell`, `exec_command`,
  `local_shell`, `run_shell_command`) plus the shell's own commands;
- `tests`: the newest command matching `[agents.recap] test_patterns`, with
  `cmdline`, `at` and `ok`: true or false from a finished or failed tool call
  or a shell exit status, and null when nothing said (a call still running, or
  a shell that sent no status). Absent when no command matched;
- `last_said`: the first line of the newest `turn_end`;
- `state`: the pane's agent state now.

Request:

```json
{"id": 1, "verb": "agent-activity", "params": {"session": "work", "window": "api", "since": 1790000000000000000, "recap": true}}
```

Response:

```json
{"id": 1, "result": {"type": "agent_activity", "session": "work", "window": "3f2a9c1e", "last_seq": 42, "untrusted": true,
  "entries": [{"seq": 41, "at": 1790000123000000000, "kind": "tool_done", "tool": "Bash", "target": "go test ./...", "ok": true},
              {"seq": 42, "at": 1790000125000000000, "kind": "turn_end", "text": "Added retry with backoff and tests."}],
  "recap": {"since": 1790000000000000000, "turns": 3, "files": ["api/retry.go", "api/retry_test.go"], "files_total": 2, "commands": 11,
            "tests": {"cmdline": "go test ./...", "ok": true, "at": 1790000123000000000}, "last_said": "Added retry with backoff and tests.", "state": "done"}}}
```

Who may read it: from a pane, `read` reaches its own session and fan group, as
for the other reads; over a link it needs `list`. Only the pane's own reports
write the ring: activity rides `set-agent-state`, which a pane may call only
for itself, and it is recorded only past the identity guard. Nothing reads the
ring to decide a state, a wait or an alert.

The event type `agent-activity` carries each entry as it is recorded, as
`entry`, to a subscription whose `types` names it. It is not replayed on a
resume; read the ring instead.

`tuios agent-log` is this verb on the command line.

### Agent review, triage and queue verbs

These verbs are registered with their parameters, scope and link capability,
and answer `internal` ("not built yet") until their work lands. These have
landed: `agent-activity` (see [agent-activity](#agent-activity)), the
delivery queue's three (see [The delivery queue](#the-delivery-queue)),
`compare-fan`, `verify-fan` and `keep-fan`, which have sections of their own,
and `mark-attention` (see [mark-attention](#mark-attention)). `list-verbs`
describes each one's parameters and result. What is fixed now is who may call
them:

| Verb | What it does | A pane without `admin` | Over a link | The person only |
| --- | --- | --- | --- | --- |
| `review-diff` | The diff of what the agent in a pane changed, against its base or a fan sibling, marked `untrusted` | `read`, own session and fan group | `write` (file contents) | no |
| `review-note` | Add, edit, remove, list or clear review notes on a pane's changes | `write` | `write` | a note is the person's only with a live `human_nonce` |
| `send-review` | Send the unsent notes to the agent through the delivery queue | `write`, and a typing verb: the target holds nothing the caller does not, and is not on `needs_input` unless the caller holds `respond` | `write` | "from the person" only with a live `human_nonce` |
| `compare-fan` | One row per attempt of a fan: branch, agent, state, changes, last check | `read`, own fan group | `list` | no |
| `verify-fan` | Run one check with `sh -c` in a window named `verify` in every attempt; the window holds no grants | `fan`, own fan group | `open` and `write` | no |
| `keep-fan` | Keep one attempt and remove the others, refusing a dirty one without `stash` or `force` | refused | `write` | admin or the person |
| `mark-attention` | Snooze, wake, mark unread or restore an Inbox item | refused | `respond` | yes: `human_nonce` from a live attach, never from inside a pane |
| `agent-activity` | The pane's ring of hook events, and a recap of it, marked `untrusted` | `read`, own session and fan group | `list` | no |
| `queue-prompt` | Queue a message, typed when the agent comes to rest and never over a prompt | `write`, and a typing verb as for `send-review`; checked again against the caller's grants when typed | `write` | "by the person" only with a live `human_nonce` |
| `list-queued` | The messages waiting in a pane's queue | `read` | `list` | no |
| `cancel-queued` | Drop queued messages; a pane drops only what it queued | `write` | `write` | the person's entries need a live `human_nonce` |
| `get-approval` | A held approval or plan whole, with its risk rules, marked `untrusted` | `read`; its `session` is the pane's own unless named | `list` | no |

A connection restricted with `restrict-connection` is held the same way: with
`read_only`, `review-note`, `send-review`, `queue-prompt`, `cancel-queued`
and `verify-fan` are refused, and `keep-fan` and `mark-attention` are refused
on any restricted connection.

### The delivery queue

`queue-prompt`, `list-queued` and `cancel-queued` are built. A queued message
is typed as a prompt once the agent in the pane has been at rest for a
second: `idle` or `done`, or `unknown` for a harness whose rules can never
show idle. One message is typed per rest, never over a pane on
`needs_input`, and never twice. After a message is typed, the next waits for a
rest reached after it, even if the queue emptied in between; a pane whose
harness cannot show working, and so may never change state, also reaches one
by printing something and then going silent for 5 seconds. [AGENT_STATE.md](AGENT_STATE.md#queued-messages)
describes the whole lifecycle.

`queue-prompt` params: `session`, `window` (default the focused window; it
must run an agent, and `human` is `no_keyboard`), `text` (required, at most
16 KiB), `human_nonce`, `from`.

```json
{"id":1,"verb":"queue-prompt","params":{"session":"work","window":"build","text":"make the backoff jitter configurable"}}
{"id":1,"result":{"type":"prompt_queued","id":"q3","position":1,"queued":1,"delivering":false}}
```

`delivering` is true when the entry is next and the agent is at rest now, so
it is typed within about a second. Errors: `queue_full` when the pane already
holds `[agents.queue] max` entries, `no_keyboard`, `not_human` for a nonce
that does not verify, `forbidden`, `invalid_params`.

`list-queued` params: `session`, `window` (omit it to list every pane of the
session). Each entry has `id`, `session`, `window`, `name`, `at` (Unix
nanoseconds), `by`, `from` when given, `preview` (the first line, at most 80
characters, control characters left out) and `state` (`waiting`,
`delivering` or `stalled`), next first within each pane.

`cancel-queued` params: `session`, `window`, and `id` or `all`, and
`human_nonce`. It answers `cancelled` (the ids dropped) and `queued` (what the
pane holds now). Dropping a stalled entry leaves the next to the pane's next
rest.

Who queued an entry, `by`, is the daemon's own reading of the connection:
`human` only with a live `human_nonce`, `link:HOST` over a link (the peer the
link-peer handshake named, `*` for none), the pane's window id for a process
in a pane, and `shell` for anything else. What each is held to:

| Origin | At `queue-prompt` | When it is typed | May drop with `cancel-queued` |
| --- | --- | --- | --- |
| `human` | the nonce | nothing more | every entry |
| a pane | `write`, and the typing rules of `send-text` | its grants as they are then, against the target; a refusal drops the entry | only its own |
| `link:HOST` | `write` in its `[hosts]` policy | the policy as it is then; a refusal drops the entry | only its own; for a machine that gave no name, only what it queued on the same connection |
| `shell` | nothing new | nothing new | every entry but `human`'s |

For every entry the pane is checked for `needs_input` right before it is
typed. An entry being typed cannot be dropped. A pane on another machine,
whose calls arrive through its report channel, may neither queue nor drop:
both verbs answer `forbidden`.

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
| `attention` | An Inbox item opened, changed or closed. `action` is `open`, `update` or `close`, and `attention` is the item as `list-attention` returns it. On `close` the item carries `closed`: `resolved`, `seen`, `read`, `dismissed`, `answered`, `superseded`, `window_closed`, `session_closed`, `evicted`, `host_removed` or `snoozed` (the person snoozed it; it opens again with the same id). An `answered` item also carries `answer` and `answered_by`; the close of a linked host's item never reads `answered` here. `session` and `window` are the item's, so the usual filters apply. An item of a linked host also sets `host`, and a subscriber that filters on a session, window or pane does not get it unless it subscribed with `hosts`. | `session`, `window`, `host`, `action`, `attention` |
| `host-changed` | A linked host's link changed state, or what it holds changed: its sessions, windows or agents. List the hosts again to see what. See [Following linked hosts](#following-linked-hosts). | `host`, `status` |
| `prompt` | A shell that marks its commands with OSC 133 shows its prompt after anything else: at start, or after a command. A prompt drawn again changes nothing and raises nothing. | `session`, `window`, `pty_id` |
| `command-started` | A shell with OSC 133 marks started a command. `cmdline` is cut to 512 bytes, with likely secrets masked. | `session`, `window`, `pty_id`, `cmdline` |
| `command-finished` | That command finished. `exit_code` is absent when the shell sent no status; a prompt with no finish mark ends the command that way. `command_seq` counts the pane's finished commands. | `session`, `window`, `pty_id`, `cmdline`, `exit_code`, `duration_ms`, `command_seq` |
| `agent-activity` | One entry of an agent pane's activity ring, as [agent-activity](#agent-activity) returns it. Opt-in: only a subscription whose `types` names it receives it. Not replayed on a resume. | `session`, `window`, `entry` |

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
- `hosts` (optional): also deliver the `agent-state`, `session-created` and
  `session-closed` events this daemon relays from its linked hosts, each with
  `host` set, and let `session` and `window` match events of other machines.
  Without it, an event about another machine reaches only a subscriber that
  names no session, window or pane, and only an `attention` or `host-changed`
  event. See [Following linked hosts](#following-linked-hosts).

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
seconds, and `agent-activity` events, which fire on every tool call of every
agent. A subscriber that kept the `seq` of the last event it read, and the
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
| `not_retained` | The filter admits `output` events, or names `agent-activity`, and at least one such event was published after `after_seq`. Neither is ever replayed. | The retained events, then live. Leave `output` and `agent-activity` out of `types` for an exact replay. |

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
take no `session` or `window`), `command_seq` (for `command-finished` with a
`window`), `select` (a selector, for `agent-state` only:
watch the agent panes it matches in every session, including panes that open
during the wait; takes no `session`, `window` or `any_session`), `every` (bool,
with `select`: match only when at least one pane matches and all of them are in
an `until` state, and answer with them in `panes`), `timeout` (milliseconds;
default 30000).

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
- `command-finished` resolves when a shell that marks its commands with OSC
  133 finishes one. With `window` it watches that pane: the next command to
  finish after the wait starts, or with `command_seq` N the first once the
  pane has finished more than N, which matches at once when that already
  happened. Read N from `list-windows` before starting the command and the
  wait cannot miss it. It fails with `pty_not_found` if the pane's shell
  exits. Without `window` any pane in the session matches, and `command_seq`
  is `invalid_params`. The result carries `window`, `cmdline`, `exit_code`
  (omitted when the shell sent none), `duration_ms` and `command_seq`.

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

## A client built on these verbs: the tmux shim

`tuios tmux` and the `tmux` link `tuios tmux-shim` installs answer tmux
commands by calling the verbs above on the caller's own session. They add no
verb and change none, and the wire is unchanged: an older daemon serves the
shim as well as a new one. The mapping, for a reader of a daemon log:

| tmux | verbs |
|------|-------|
| any target | `list-windows`, `list-workspaces` (a pane `%N` is a window, N derived from its id; a window `@N` is workspace N) |
| `split-window`, `new-window` | `new-window` with `workspace`, `focus`, `cwd`, and `command` running `tuios tmux-pane`; `new-window -n` adds `set-workspace-name` |
| `send-keys` | `send-text`, with tmux key names turned into bytes by the shim |
| `capture-pane -p` | `capture-pane`, `source` visible, and recent when `-S` reaches into history |
| `kill-pane`, `kill-window` | `close-window` |
| `select-pane`, `select-pane -T` | `focus-window`, `set-window` with `name` |
| `select-window`, `rename-window` | `select-workspace`, `set-workspace-name` |
| `respawn-pane -k` | `pane-grants`, then a request on the pane holder's unix socket. From a pane without `admin`, only the caller's own pane is respawned |

Every call names the caller's session, so nothing the shim does reaches
another. It holds no authority the caller's own tuios CLI does not. See
[TMUX_SHIM.md](TMUX_SHIM.md).

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
