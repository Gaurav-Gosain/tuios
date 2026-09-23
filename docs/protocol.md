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
  "link_policy": true
}}
```

`link_policy` says the daemon holds calls from other machines to a link
policy (see [What a linked machine may do here](#what-a-linked-machine-may-do-here)).
`tuios stdio-proxy` reads it before it would reach the daemon on its own socket
for a link, and refuses when it is set. It is absent from an older daemon.

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
| `agent_blocked` | ask-agent declined to type at an agent on `needs_input`, because the text would answer its prompt. Nothing was typed. The hint names `capture-pane`. |
| `prompt_stalled` | ask-agent typed the question and sent Enter, and within `stall_timeout` the pane did not show that it took it. The question was typed; look at the pane before sending it again. The hint names `capture-pane`. |
| `loop_refused` | The call would loop: a pane addressing itself, or an ask that closes a cycle with one in flight. |
| `rate_limited` | The sender is over the cross-agent message rate cap. |
| `not_human` | Only the person at an attached client may make this call, and it carried no nonce from a live attach. `dismiss-attention`, `respond` and `reply-approval` raise it. |
| `prompt_changed` | `respond` pressed nothing: the pane is not on `needs_input`, no rule reads its prompt now, the prompt is not the one `prompt_id` names, or another client already answered it. Read it again with `peek-prompt`. |
| `no_keyboard` | The target is the person's inbox, `human`, which has no pane to type into. |
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
| read one session | `session-info`, `list-windows`, `list-workspaces`, `capture-pane`, `get-agent-state`, `list-agents`, `wait-for`, `subscribe`, `peek-prompt`, `read-agent-messages`, `explain-agent-screen`, `list-options`, `get-option`, `stash-list`, `stash-get` | session in reach | allowed |
| own pane's record | `set-agent-state`, `set-agent-meta`, `set-agent-session` | own pane only | allowed |
| mail and stash | `send-agent-message`, `stash-put` | session in reach, sent as the own pane | allowed |
| type into a pane | `send-text`, `send-keys`, `ask-agent`, `respond` | session in reach | `forbidden` |
| start sessions | `fan` | needs a pane | `forbidden` |
| everything else | | `forbidden` | `forbidden` |

Under `own`:

- A verb that takes `session` and names none gets the caller's own session.
  `subscribe` with no session streams every session in reach, and each event
  is checked as it is written, so a session that joins the reach later is
  streamed from then on. Events that name no session, and events relayed
  from linked hosts, are not written.
- `all_sessions` on `list-agents`, `any_session` on `wait-for` and `hosts` on
  `subscribe` are refused.
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
the one a harness can offer without a shell tool at all.

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
daemon and on every linked host it follows (see [Following linked
hosts](#following-linked-hosts)). Each item is one of seven kinds, and the list
is grouped in this order, oldest first inside each group:

| Kind | Opens when | Closes when |
| --- | --- | --- |
| `approval` | A pane goes to `needs_input` with `blocked_by` `approval`. | The pane leaves `needs_input`. |
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
from `approval`, `question`, `mail`, `errored`, `resume`, `finished`, `outbox`), `host`
(optional: `local` for this machine or a linked host's name; omitted means
every machine; an unknown name is `unknown_host`).

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
`completion_seq` (finished), `stale` and `seen_at` (an item of a host whose
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
under a fresh id, so no two items ever share one. It drops `resume` items
too: the restore that runs on start opens them again from each window's
`agent_session_id`, after the saved items are loaded. `outbox` items are
opened again from the outbox, which is saved on its own and survives a
restart whole.

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

An item of a linked host is hidden on this daemon and nothing else: nothing on
the host is marked, and the item comes back when the host changes it. The
result then carries `host`.

Response:

```json
{"result": {"type": "attention_dismissed", "id": "17", "kind": "approval", "session": "fan-3", "dismissed": true}}
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
harness.

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
| none | `hello`, `list-verbs`, `link-peer`, `restrict-connection` |
| `list` | `list-*`, `session-info`, `capture-pane`, `screenshot`, `get-option`, `get-agent-state`, `resolve-pane`, `explain-agent-*`, `wait-for`, `subscribe`, `unsubscribe`, `peek-prompt`, `read-dir` |
| `mail` | `send-agent-message`, `read-agent-messages`, `stash-put`, `stash-list`, `stash-get` |
| `open` | `new-session`, `new-window`, `split-window`, `popup`, `new-worktree`, `fan`, `open-pane`, `resize-pane`, `close-pane`, `pane-cwd`, `pane-agent`, `pane-calls` |
| `write` | `send-keys`, `send-text`, `ask-agent`, `run-command`, `close-window`, `kill-session`, `focus-window`, `move-window`, `set-window`, `select-workspace`, `set-layout`, `resize`, `set-option`, `set-session-*`, `set-workspace-*`, `set-agent-*`, `resume-agent`, `request-approval`, `refresh-dock`, `remove-worktree` |
| `respond` | `respond`, `reply-approval`, `dismiss-attention`, `release-agent-message` |
| every one | `open-host-connection` |

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
| `attention` | An Inbox item opened, changed or closed. `action` is `open`, `update` or `close`, and `attention` is the item as `list-attention` returns it. On `close` the item carries `closed`: `resolved`, `seen`, `read`, `dismissed`, `answered`, `window_closed`, `session_closed`, `evicted` or `host_removed`. An `answered` item also carries `answer` and `answered_by`; the close of a linked host's item never reads `answered` here. `session` and `window` are the item's, so the usual filters apply. An item of a linked host also sets `host`, and a subscriber that filters on a session, window or pane does not get it unless it subscribed with `hosts`. | `session`, `window`, `host`, `action`, `attention` |
| `host-changed` | A linked host's link changed state, or what it holds changed: its sessions, windows or agents. List the hosts again to see what. See [Following linked hosts](#following-linked-hosts). | `host`, `status` |

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
