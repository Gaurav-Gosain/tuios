# Streaming a pane, and the person's presence

`stream-pane` sends one pane as bytes to a client that is not tuios, such as
a phone app. `attach-presence` gives a connection the person's `human_nonce`
without an attach. Neither verb has a command wrapper. Write JSON to
`$TUIOS_SOCKET` (`tuios --skill events`). A client on another machine uses
`tuios stdio-proxy` over ssh.

```sh
tuios list-verbs stream-pane
tuios list-verbs attach-presence
tuios stdio-proxy --as phone
```

docs/protocol.md has the full contract. This topic is the part an agent needs.

## stream-pane

```json
{"id":1,"verb":"stream-pane","params":{"session":"work","window":"build"}}
```

The daemon sends one JSON reply line. After that line, the connection carries
binary frames in both directions: one type byte, a 4-byte big-endian length,
then the payload.

- From the daemon: `S` snapshot, `O` output, `R` resize, `E` error, `X` end.
- From the client: `I` input, `L` size lease.
- Skip a frame type that you do not know. Read its length, then read and drop
  the payload. A later daemon can add frame types.
- Keep the `seq` of the last frame you wrote into your emulator. Send it back
  as `from_seq` with `boot_id` to resume after a dropped connection. The reply
  `mode` is `resume` or `snapshot`.
- The snapshot is charged to the daemon's memory budget. When the budget is
  full, `stream-pane` fails with `busy`. Try again after a few seconds.

## Input and the size lease

- An `I` frame passes the checks of `send-text`. A pane needs the write grant.
- At most 4 `I` frames wait for a pane that does not read its input. The next
  one gets an `E` frame with code `busy` and is not typed.
- An `L` frame (u16 cols, u16 rows) holds the pane at most at that size. `L`
  with `0 0` releases it. An `L` frame passes the checks of `resize`.
- A lease lasts 30 seconds. Send the same `L` frame again every 10 seconds to
  keep it. When a lease ends, the daemon sends an `E` frame with code
  `lease_expired`, and the pane goes back to the size its clients asked for.
- A lease never changes the session size or another pane.

## attach-presence

```json
{"id":1,"verb":"attach-presence","params":{"session":"work"}}
```

The reply carries `human_nonce`. `respond`, `reply-approval`, `answer-ask`,
`dismiss-attention`, `mark-attention`, `send-agent-message` from `human`, and
every other verb that takes `human_nonce` accept it.

- A session is a boundary. A presence made with `session` acts only in that
  session. A presence made with no session acts in every session.
- The caller must be outside every pane of this daemon. A process in a pane
  gets `forbidden`. An agent cannot hold a presence, and cannot act as the
  person.
- Send the nonce from the same process, over the same kind of connection.
- The presence ends when its connection closes, or when the connection calls
  `restrict-connection`.
