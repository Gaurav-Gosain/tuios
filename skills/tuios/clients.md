# Clients: who is attached, and where

A client is a connection to the daemon: a terminal running `tuios attach`, an
SSH or web client, or a short-lived CLI call. `tuios list-clients` lists them.

```sh
tuios list-clients
tuios list-clients --json
```

```json
[
  {"client_id":"client-1790941960197517900","pid":4242,"session":"work"}
]
```

- `session` is the session the client shows. It is empty while the
  connection is detached. Your own `list-clients` call is in the list, with
  no session.
- `pid` is the kernel's record of the peer process. It is `0` on Windows and
  the BSDs. For an SSH or web client it is the local server process, not the
  remote person.
- `tuios ls --json` says only whether a session has a client. Use
  `list-clients` to learn which session each client shows.

Geometry verbs (`split-window`, `set-layout`, `popup`, `pip`) need a client
attached to the session, and fail with `needs_client` without one. Check
before you call them:

```sh
tuios list-clients --json | jq -e --arg s "$TUIOS_SESSION" 'any(.[]; .session == $s)'
```

## Following clients as they move

`tuios subscribe` carries `client-session-changed` when a client attaches,
detaches, switches session or disconnects while attached. It has
`client_id`, `pid`, `session` and `attached`. A switch sends two events: one
with `attached: false` for the session left, and one with `attached: true` for
the session entered. A session rename sends `attached: true` with the new name
for each client in it.

```sh
tuios subscribe --types client-session-changed
```

`tuios --skill events` has the rest of the stream. Under the tmux shim,
`tmux list-clients` lists one client per session that a tuios client shows.
