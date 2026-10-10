# Files and copies between machines

The daemon reads and writes files with the file verbs, and runs copies between
this machine and its hosts with the transfer verbs. They have no command
wrapper yet. Write JSON to `$TUIOS_SOCKET` (`tuios --skill events`), and ask
`list-verbs` for every parameter:

```sh
tuios list-verbs transfer-start
tuios list-verbs file-list
tuios subscribe --types transfer,transfer-progress
```

docs/protocol.md, "Files and transfers", has the full contract. This topic is
the part an agent needs.

## Who may use them

The file and transfer verbs are the person's, not a pane's. A pane needs the
`admin` grant (`tuios --skill grants`). Under the default `mode = "open"`
every pane holds it; under `strict` a call fails with `forbidden`. That is the
person's decision. Tell them, do not look for another way.

On another machine, the file verbs need `files` in that machine's `allow` for
this one. A write that arrives over the link lands only under that machine's
`files_roots` (one receive folder, `~/Downloads/tuios`, unless the person
added more), plus the drop folder. It never reaches `~/.ssh`, `~/.gnupg`,
credentials, shell start files, login items or tuios's own config, whatever
`files_roots` says, and a read never returns keys or credentials. A refusal is
`forbidden`: the write refusal names where writes may land and which setting to
change, and the read refusal names the path. Do not try another path to the
same file. To copy into a folder the far machine does not yet allow, tell the
person to add it to `files_roots` for this machine in the config there.

## Copy a file or a folder

```json
{"id":1,"verb":"transfer-start","params":{"src":{"host":"build","path":"~/out/app.tar"},"dst":{"path":"~/Downloads/app.tar"}}}
```

`host` empty is this machine. `dst` is the full path of the copy, not the
folder it goes into. The answer is the job's row, with its `id`. The copy runs
in the daemon, so it goes on after you exit, after a dropped link and after a
daemon restart.

- A destination that exists answers `file_exists`. Pass `conflict`:
  `replace`, `keep-both` (the copy gets " 2"), `merge` for a folder into a
  folder, or `fail`. Ask the person before `replace`.
- `move` removes the original after every file is checked. A folder move
  removes only what it copied.
- A copy to a path another copy writes now answers `busy`.
- Every file is checked with sha256 before it is put in place, and keeps the
  original's permission bits and modification time.
- Links, named pipes, sockets and devices in a folder are not copied. The row
  counts them in `skipped` and names them in `skipped_items`. Tell the person
  what stayed behind.

## Follow a copy

Wait on the event stream instead of polling `transfer-list`:

```sh
tuios subscribe --types transfer --count 50
```

A `transfer` event has `action` `created`, `state` or `ended`, and `transfer`,
the row. The copy is finished at `ended`: read `transfer.state` (`done`,
`failed` or `cancelled`), `transfer.verified` and `transfer.error`.
`transfer-progress` adds the row while bytes move, at most four times a
second. It reaches only a subscriber that names it.

`waiting` means a machine went away. The copy goes on by itself when it is
back. Do not start it again.

## Control a copy

```json
{"id":1,"verb":"transfer-list"}
{"id":2,"verb":"transfer-pause","params":{"id":"3f9a1c2b7d00"}}
{"id":3,"verb":"transfer-resume","params":{"id":"3f9a1c2b7d00"}}
{"id":4,"verb":"transfer-cancel","params":{"id":"3f9a1c2b7d00"}}
```

`transfer-resume` also tries a waiting copy now, and a failed copy again from
where it stopped. A finished copy leaves the list 30 minutes after it ends
(`no_transfer`).

## The file verbs

On this machine, or on a host through `open-host-connection`:

- `file-list` lists a folder, folders first, in natural order. `file-stat`
  describes one path.
- `file-read` reads up to 4 MiB, base64. `file-hash` is the sha256 of a file
  or a range of it.
- `file-mkdir`, `file-rename` and `file-remove` change files. `file-remove`
  of a folder that holds files needs `recursive`, and never removes the home
  folder or `/`. Ask the person before you remove what you did not make.

The error codes are `no_file`, `file_exists`, `no_permission`,
`hash_mismatch`, `cross_device`, `disk_full` and `no_transfer`
(`tuios --skill errors`).
