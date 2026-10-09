# Pairing a phone

`tuios pair` adds this machine to the tuios app on a phone. It shows a QR
code. The phone sends its ssh key, the person accepts it, and the key can
then open a tuios link under one name and nothing else.

```sh
tuios pair --name phone
tuios pair --name phone --allow list,mail,open,write
tuios pair --name phone --json
```

The person runs it. Do not run it for them, and do not accept a key for them:

- The person must compare the check code and the key fingerprint on the
  phone with the ones in the question, and type `y`. `--yes` skips that
  question. It is for tests only. Never pass it.
- A pairing request from this machine, or from a machine in `[hosts]`, stops
  the pairing. A program that read the code from a pane is not the phone.
  `--accept-local` lets this machine through, for an emulator or a userspace
  tailscaled. Do not pass it to get around the refusal.
- Do not read the code or the link out of the person's pane, and do not copy
  it anywhere. Anyone with the code can pair a key until the phone does.

What it changes, in this order:

1. A new `[hosts.DEVICE]` table in `config.toml`. Its `allow` list is
   `--allow`, or `list` and `mail` when `--allow` is not given. With `list`
   the phone reads listings, pane captures, the Inbox and events. With
   `mail` it sends and reads agent mail. It cannot start programs or type
   into panes unless the person adds `open` or `write`.
2. One line in `~/.ssh/authorized_keys`, which ends in `tuios-pair:DEVICE`.
   Its forced command runs `tuios stdio-proxy --as DEVICE`.

When the table cannot be written, tuios does not add the key. A device name
that a `[hosts]` table has, that another paired key uses, or that is this
machine's name, is refused. Case does not matter.

To see the paired devices, look for lines that end in `tuios-pair:` in
`~/.ssh/authorized_keys`. To remove one, the person deletes that line and the
`[hosts.DEVICE]` table. Do not edit either file for them: both decide what
another machine may do here.

The flags, the addresses in the code and the protocol the phone speaks are in
`docs/CLI_REFERENCE.md` under `tuios pair`. What a linked machine may do is in
`tuios --skill hosts`.
