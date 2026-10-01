# tuios-courier

`tuios-courier` carries agent mail between people's machines where tuios links
cannot reach. tuios already sends mail between daemons over ssh
(`tuios --skill hosts`). Where ssh is blocked, the courier carries it instead,
through a relay over HTTPS.

It is a separate binary on purpose, the way `tuios-web` is. It adds no way into
the daemon: it does not link the daemon's packages (a test keeps it that way),
never talks to the daemon's socket, and can only carry text. It cannot type
into a pane, open a window or answer a prompt.

```
agent A ── tuios-courier send ──HTTPS──► relay ◄──HTTPS── tuios-courier (B)
                                  sealed mail only        held until B releases it
```

## Setting up

### The relay

One relay serves a team. It needs a roster: one `NAME IDENTITY` line per
person it serves.

```bash
tuios-courier relay --addr 127.0.0.1:8080 --roster /etc/tuios-courier/roster --data /var/lib/tuios-courier
```

- Without `--data`, mail is kept in memory and is lost when the relay stops.
- The roster is read again when the file changes. Remove a line to cut
  someone off.
- Plain HTTP is refused on any address but loopback. Give `--tls-cert` and
  `--tls-key`, or pass `--insecure` when an ingress terminates TLS in front of
  the relay.
- `--prefix /courier` serves under a path, for an ingress that does not strip
  it.
- A fetch waits at most 50 seconds, under the 60 seconds an nginx ingress
  allows a response by default. Clients ask for 10.

Health check: `GET /v1/healthz`, no authentication.

### Each person

```bash
tuios-courier init --name ghaith --relay https://courier.example.internal/
```

`init` prints the line to give the relay's operator and your teammates. Add a
teammate with the identity they give you, and compare fingerprints with them
(`tuios-courier whoami` prints yours) before you trust their mail:

```bash
tuios-courier peers add gg tc1.AbC...
```

Then connect Claude Code:

```bash
tuios-courier integration claude-code
```

This prints two hooks to merge into `.claude/settings.json`. Released mail
reaches Claude when you send a prompt, and new mail is handled before Claude
stops. Give each agent a label in the shell that starts it, so mail and
replies reach the right one:

```bash
export TUIOS_COURIER_AGENT=frontend
```

## Using it

Agents learn it from `tuios-courier --skill`. In short:

```bash
tuios-courier send gg --agent backend 'Is POST /orders returning totals yet?'
tuios-courier wait --thread 3f9a12c4 --timeout 30m
tuios-courier read
tuios-courier reply 3f9a12c4 'Yes: total_cents.'
```

New mail from a peer is **held** until you release it:

```bash
tuios-courier inbox
tuios-courier show 3f9a12c4
tuios-courier release 3f9a12c4
tuios-courier release --from gg
tuios-courier drop 3f9a12c4
tuios-courier watch          # a line per arrival, for a spare pane
```

- `peers add NAME ID --release auto` lets a peer's mail through without you.
- A reply in a thread you started, from the peer you started it with, is
  released at once. Set `reply_release = "hold"` in `courier.toml` to hold
  those too. Answering a thread someone else started does not make their later
  mail in it skip the hold.
- Mail no agent has read is never cleaned up, however old: drop it yourself.
- Mail from someone not in your peers is kept unopened and delivered when you
  add them.

## Files

| Path | What |
| --- | --- |
| `$XDG_CONFIG_HOME/tuios/courier/courier.toml` | name, relay, peers |
| `$XDG_CONFIG_HOME/tuios/courier/identity.key` | your private keys, mode 600 |
| `$XDG_STATE_HOME/tuios/courier/` | your mail |

`TUIOS_COURIER_HOME` moves all of it to one directory (config at its root,
mail under `state/`), for a second identity on one machine.

```toml
name = "ghaith"
relay = "https://courier.example.internal/"
reply_release = "auto"

[peers.gg]
identity = "tc1...."
release = "hold"
```

## Security

| Threat | What stops it |
| --- | --- |
| A courier bug becomes control of tuios | Separate process. It does not link the daemon, and cannot reach a pane. |
| The relay reads or changes mail | Mail is sealed to the recipient (HPKE: X25519, HKDF-SHA256, ChaCha20-Poly1305) and signed (Ed25519). |
| A recipient passes mail on as if it had been written to someone else | The recipient's identity is inside the signature. |
| Someone on the network sends mail | Only roster identities may send, and every request is signed. |
| A captured request is replayed | A two-minute time window and a nonce cache. |
| A message is replayed | Each message is stored once. A dropped message stays as a tombstone until it is too old to be accepted. |
| A teammate's agent tries to instruct yours | New mail is held for you, printed inside the untrusted fence tuios uses, with control and bidi characters removed. |
| Someone floods a mailbox | Size, count and rate limits, and a TTL. |

What it does not protect:

- The relay sees who writes to whom, when, and how much.
- `release` is a command any process of yours can run. Outside the daemon there
  is no way to tell you apart from your agents, so the skill tells agents never
  to release mail, and a peer set to `release = "auto"` is one you trust
  unreviewed.
- The keys live in a file only you can read. Anyone who can read your files can
  be you.
- A request signature names no relay. Two relays that serve the same roster
  would each accept, within two minutes, a request captured from the other.
  Run one relay per roster.
- The nonce cache is in memory, so a request captured in the two minutes
  before a relay restarts can be replayed once after it. A replayed fetch or
  ack only fetches or deletes the requester's own mail, which the client
  already has; a replayed send is a duplicate the recipient's store drops.

## Protocol

Every request but the health check carries:

```
Authorization: TC1 id=<identity>, ts=<unix seconds>, nonce=<base64url 16 bytes>, sig=<base64url>
```

The signature is Ed25519 over
`"tuios-courier-req-v1\n" METHOD "\n" PATH?QUERY "\n" ts "\n" nonce "\n" hex(sha256(body))`,
with the path relative to the relay's prefix.

| Request | Answer |
| --- | --- |
| `POST /v1/mail/{mailbox}`, body the sealed box | `202 {"id","expires_at"}` |
| `GET /v1/mail?wait=SECONDS` | `{"messages":[{"id","from","received_at","box"}]}`, the caller's own mailbox, oldest first, at most 64 |
| `POST /v1/ack`, `{"ids":[…]}` | `{"acked":N}`, from the caller's own mailbox only |

Errors are `{"error":CODE,"message":…}` with `unauthorized`,
`unknown_recipient`, `too_large`, `mailbox_full`, `rate_limited`,
`bad_request` or `not_found`.

An identity is `tc1.` and the base64url of the Ed25519 public key followed by
the X25519 public key. Its mailbox is the hex of the first 16 bytes of the
SHA-256 of those 64 bytes. The version is in the prefix, so a later key type,
such as a post-quantum hybrid, is a new prefix.

## Limits

| | Default |
| --- | --- |
| Message body | 64 KiB |
| Sealed box | 192 KiB |
| Messages per mailbox | 512, and 32 MiB |
| Relay total | 1 GiB |
| Sends per sender | 60 a minute |
| Time a message waits on the relay | 7 days (`--ttl`, at most 7 days) |
| Oldest message a client accepts | 8 days |

## Networks

The client uses `HTTPS_PROXY` and `NO_PROXY`, and trusts the system roots. A
corporate CA that inspects TLS is added with `SSL_CERT_FILE`. Mail stays
sealed through such a proxy, because the inner encryption does not depend on
TLS.

The transport is HTTP/1.1 or HTTP/2 over TCP 443. HTTP/3 (QUIC) is not used:
UDP 443 is commonly dropped on the networks where ssh is blocked, and the
long-poll needs nothing QUIC adds.
