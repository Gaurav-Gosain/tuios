# tuios-learn

`tuios-learn` is the server behind `ssh learn.tuios.dev`. Anyone can connect.
No account and no key are necessary. The server teaches tuios with the
lessons of [tuios.dev/learn](https://tuios.dev/learn).

Each session runs the real tuios in its own process. The panes run a pretend
shell. A session cannot reach a real shell, a file or the network.

## Run it on your machine

```sh
CGO_ENABLED=0 go build -o tuios-learn ./cmd/tuios-learn
./tuios-learn --listen 127.0.0.1:2222 --state-dir ./learn-state --health 127.0.0.1:9022
ssh -p 2222 localhost
```

Build it with `CGO_ENABLED=0`. With cgo, the session sandbox cannot cover
every thread, and the server does not start.

`tuios-learn health 127.0.0.1:2222` exits 0 when the port answers with an SSH
banner. `curl 127.0.0.1:9022/healthz` shows the counters.

## Limits

| Flag | Default | What it limits |
|---|---|---|
| `--max-sessions` | 50 | sessions at once |
| `--max-per-ip` | 3 | sessions at once from one address (IPv6: one /64) |
| `--conn-per-minute`, `--conn-burst` | 10, 5 | new connections from one address |
| `--max-conns` | 120 | TCP connections at once |
| `--idle` | 10m | time with no input before a session ends |
| `--max-session` | 30m | the length of a session |
| `--session-mem` | 160 | the memory of one session, in MiB |
| `--input-rate` | 16384 | the bytes per second that a client can send |

## What a session can do

- The server accepts only a shell request with a terminal. It refuses exec,
  subsystems (sftp, scp), port forwards and X11. It does not use agent
  forwarding. It ignores environment variables from the client.
- A session is a child process with a fixed environment. The child uses
  Landlock to block every file, exec and TCP connect, and sets resource
  limits. Then it reads the first byte from the client.
- The logs show a /24 (IPv4) or a /48 (IPv6), never a full address. The
  counters in `stats.json` have no address and no name.
- A challenge time is stored under a salted hash of the SSH key. The
  leaderboard shows "anonymous" until a person chooses to show their name.

## Deploy

`deploy/` has a hardened systemd unit, a Dockerfile and an nftables example.
`.github/workflows/learn-release.yml` builds the Linux binaries when a
`learn-v*` tag is pushed.
