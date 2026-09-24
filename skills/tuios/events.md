# Events, verbs and the socket

Every command in this skill is a wrapper over the daemon's verb protocol. This
topic is for the parts with no wrapper, and for watching many things at once.

## The whole contract

```sh
tuios list-verbs
tuios list-verbs capture-pane
tuios list-verbs --json
```

`list-verbs` is every verb, every parameter with its type and accepted values,
the shape of what comes back, the stable error codes and the request envelope.
If you are unsure what something takes or returns, ask it. A verb with no
wrapper is reached by writing newline-delimited JSON to `$TUIOS_SOCKET` and
reading one JSON line back per request:

```json
{"id":1,"verb":"list-agents","params":{"session":"work"}}
```

A parameter the verb does not take is refused, not ignored, and the failure
lists what it does take. `invalid_params` naming a parameter you believed in
means the daemon is older than you think.

Everything works the same whether the session is attached locally, over SSH, in
tuios-web, or attached to nobody: one daemon, one socket, and no verb routes
through a client except the few that say `needs_client`.

## The event stream

```sh
tuios subscribe --types window-created,window-exit
tuios subscribe --types agent-state,attention
tuios subscribe --hosts --types agent-state
```

```
{"boot_id":"9f2c41d07a3e8b65","seq":133,"type":"subscribed"}
{"seq":134,"type":"window-created","session":"work","window":"86e5e19f-...","boot_id":"9f2c41d07a3e8b65","time":1786611217427984525}
```

Without `-s` it covers every session. Events start from the moment you
subscribe, so subscribe before you start the thing you want to watch. Useful
types: `agent-state`, `attention` (the Inbox changed), `notification` (a pane
sent OSC 9, 777 or 99), `command-started`, `command-finished` (with `exit_code`,
`duration_ms`, `command_seq`), `prompt`, `window-created`, `window-exit`.
`tuios list-verbs subscribe` lists them all. `agent-activity` (one entry of
a pane's activity ring, as `entry`: a prompt, a tool call, its result or a
finished turn) is opt-in: it arrives only when `--types` names it, and a
resumed stream does not replay it, so read `tuios agent-log` after a gap.

## Resuming a dropped stream

Keep the `seq` of the last event you read and its `boot_id`, and resume:

```sh
tuios subscribe --types agent-state --after-seq 134 --boot-id 9f2c41d07a3e8b65
```

The daemon replays what it still holds after that seq and carries on live. A
line with `"type":"gap"` means events are gone for good (the daemon restarted,
or they aged out): read current state again with `tuios list-agents` or
`tuios list-attention` rather than trusting the stream to be complete. Output
events are never replayed.

To follow a list without missing a change, list first, then subscribe from the
listing's `seq` and `boot_id` (`list-attention --json` carries both).

## When to use which

Mail is a stored ring rather than an event, because an agent making one-shot
calls is never subscribed when someone writes to it. `wait-for` is the same
machinery with the bookkeeping done for you. Reach for `subscribe` only when you
need to watch several things at once, as a conductor does
(`tuios --skill recipes`).
