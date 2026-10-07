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

## Switching a client to another session

`tuios switch-session` moves an attached client to another session in place,
as the session switcher does. From a pane it moves the client that shows your
session. From outside, name the client's session with `-s` or the client with
`--client`.

```sh
tuios switch-session api
tuios switch-session --create --cwd /src/api api
tuios switch-session build:api
tuios switch-session -s work api
```

`--create` makes the session when it is missing, and `--cwd` sets where its
windows start. `HOST:` names a machine from `[hosts]`. It moves what the
person sees, so it needs the `admin` grant. Do not switch the person's client
unless they asked for it.

## Detaching clients

`tuios detach-client` takes clients off their sessions, as tmux
`detach-client` does. The session continues to run. Each detached client
exits with a message.

```sh
tuios detach-client --client client-1790941960197517900
tuios detach-client -s work
tuios detach-client -s work --all-other
```

`--client` names one client from `list-clients`. `-s` names a session, and
every client of it detaches. `--all-other` keeps the client used last and
detaches the rest. `tuios attach -d NAME` attaches and detaches the other
clients of NAME. `[daemon] single_client = true` does that on every attach.
Detaching takes the screen away from the person, so it needs the `admin`
grant. Do not detach the person's client unless they asked for it.

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
