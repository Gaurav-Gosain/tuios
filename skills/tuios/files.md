# Files and copies between machines

Copy files with `tuios cp`, and follow or control copies with
`tuios transfers`. The daemon runs each copy. It goes on after you exit,
after a dropped link and after a daemon restart, and it checks every file
with sha256 before it puts it in place.

## Copy with tuios cp

From an agent, always pass `--json` and an explicit `--conflict`. Never let
the command ask: `--conflict ask` needs a person at a terminal.

```sh
tuios cp --json --conflict skip ./dist build:~/www/
tuios cp --json --conflict replace build:~/out/app.tar ./
tuios cp --json --conflict keep-both ./report.pdf build:
tuios cp --json --conflict skip --contents ./site/ build:~/www/
tuios cp --json --conflict skip -m ./old.log build:~/logs/
```

Paths:

- `HOST:PATH` is a path on a host from `tuios hosts`. A plain path is on this
  machine. `local:PATH` is this machine too, for a path with a colon in it.
- A relative path on a host is under its home folder: `build:logs` is
  `~/logs` on build.
- `HOST:` alone is the host's receive folder: `~/Downloads/tuios`, or the
  first folder of `files_roots` in the config on that host.
- A host lets this machine write only in its `files_roots`. A copy to
  `build:~/` works only when the person set `files_roots = ["~"]` there.

Folders follow the rules of `cp`. A folder copies with all it holds, and `-r`
changes nothing. A DST that is a folder, or ends in `/`, gets each SRC inside
it. `--contents` copies what is inside a SRC folder. A folder into a folder
that is there merges into it, file by file.

A file that is there with the same bytes is never copied again. A file that
is there and differs follows `--conflict`:

| Policy | Short flag | What it does |
| --- | --- | --- |
| `skip` | `-n` | Leave the file there as it is |
| `replace` | `-f` | Replace it. Ask the person first unless they told you to |
| `keep-both` | `-b` | Keep it, and name the copy `name (from HOST).ext` |

Each file keeps its permission bits and modification time. `--no-perms` and
`--no-times` turn that off. `--bwlimit 5M` caps the rate. `--no-compress`
sends the bytes as they are. `--detach` prints the copy's id and returns.
A detached copy skips a file that differs unless you pass `--conflict`.

## The JSON events

`--json` prints one object per line on stdout. Read `event`:

- `started`: `id`, `src`, `dst`, `files`, `bytes`.
- `progress`: `done`, `size`, `rate`, `eta_ms`, `files_done`, at most four a
  second.
- `file`: one file ended. `state` is `done`, `same`, `skipped`, `conflict` or
  `failed`.
- `waiting`: a host is not reachable. The copy goes on by itself when it is
  back. Do not start it again.
- `done`: `dst`, `files_done`, `same`, `conflicts_skipped`, `skipped`,
  `verified`, `exit`.
- `failed`, `cancelled`: `code`, `message`, `exit`.
- `timeout`: a host was not reachable for `--timeout`.

`--timeout 10m` stops waiting for a host after ten minutes, with exit 5. The
copy goes on waiting in the daemon. Add `--cancel-on-timeout` to cancel it.

## Exit codes

| Code | What it means | What to do |
| --- | --- | --- |
| `0` | Every file was copied, or was there with the same bytes | Nothing |
| `1` | The copy failed | Read the message. It names the cause and the fix |
| `2` | The command line is wrong, or the host name is not known | Fix the command. `tuios hosts` lists the names |
| `3` | Done, but some items were not copied: links, pipes, devices, files made at the destination during the copy, or files that differ with no `--conflict` given (always so for `transfers wait`) | Tell the person what stayed behind |
| `4` | Refused by your pane grants or by the host's link policy | Stop. Tell the person |
| `5` | A host was not reachable for `--timeout` | The copy still waits. Check `tuios hosts` |
| `6` | A file did not match its original twice, or changed during the copy | Copy it again when nothing writes it |
| `130` | Cancelled | Nothing more was written |

A refused copy (exit 4) is the person's decision. Do not try another path,
another host or the raw verbs to get the same file across. Ask the person.

## Follow and control copies

```sh
tuios transfers --json
tuios transfers wait 3f9a1c2b7d00 --json
tuios transfers pause 3f9a1c2b7d00
tuios transfers resume 3f9a1c2b7d00
tuios transfers cancel 3f9a1c2b7d00
tuios transfers clear
```

`wait` prints the same events as `tuios cp --json` and exits with the same
codes, except `2` and `5`. It has no `--timeout`, so it waits for a host as
long as the copy does. `resume` also tries a waiting copy now, and a failed copy again from
where it stopped. A copy that ended stays in the list for 30 minutes, or until
`clear`.

## Who may copy

A pane needs `admin` or `files` (`tuios --skill grants`). Under the default
`mode = "open"` every pane holds `admin`. With `files` and not `admin`, a pane
copies only inside its own folder on this machine, and only under the home
folder or to the receive folder on a host. It sees and controls only the
copies it started. Every copy carries the name of the pane that started it.

On another machine, a copy needs `files` in that machine's `allow` for this
one. A write that arrives over the link lands only under that machine's
`files_roots`, plus the drop folder. It never reaches `~/.ssh`, `~/.gnupg`,
credentials, shell start files, login items or tuios's own config, whatever
`files_roots` says, and a read never returns keys or credentials. A read of a
file with more than one name (a hard link) is refused too. A refusal is
`forbidden`: the write refusal names where writes may land and which setting to
change, and the read refusal names the path. To copy into a folder the far
machine does not allow, tell the person to add it to `files_roots` for this
machine in the config there. A machine can also hold reads to
`files_read_roots`. That refusal names the setting in the same way.

A write over a link must leave `files_min_free` (1 GiB unless set) free on
the far disk, or it fails with `disk_full`. A machine may have 8 write
streams and 64 unfinished copies open on another at once. Past that a call
is `busy`. A copy waits and tries again by itself.

`tuios hosts test build --speed` measures how fast a copy to a host can be.

## The verbs under the commands

`tuios cp` and `tuios transfers` call the transfer verbs. Send one verb
yourself with `tuios call VERB '{...}'`. A client that is not the CLI writes
JSON to `$TUIOS_SOCKET` (`tuios --skill events`). `list-verbs` lists every
parameter. docs/protocol.md, "Files and transfers", has the full
contract.

```sh
tuios list-verbs transfer-start
tuios subscribe --types transfer,transfer-progress,transfer-file
```

```json
{"id":1,"verb":"transfer-start","params":{"src":{"host":"build","path":"~/out/app.tar"},"dst":{"path":"~/Downloads/"},"place":"auto","conflict":"merge","each":"skip"}}
```

- `src` and `dst` are `{host, path}`. `host` empty is this machine. A `dst`
  with no `path` is the receive folder of its machine.
- `place`: `auto` reads `dst` as `cp` does. `into` needs a folder that is
  there. `contents` copies what a folder holds. Omit `place` and `dst` is the
  full path of the copy.
- `conflict` says what to do when `dst` is there: `replace`, `keep-both`,
  `merge` (a folder into a folder), `skip`, `ask` or `fail` (the default,
  which answers `file_exists`). `each` says what to do with each file that is
  there and differs inside a merge: `replace`, `keep-both`, `skip` or `ask`.
- `perms`, `times` and `compress` default to true. `rate_limit` is bytes a
  second. `keep_from` false names a keep-both copy `name 2.ext`.
- `move` removes the original after every file is checked. A folder move
  removes only what it copied.
- A copy to a path another copy writes now answers `busy`.

A copy with `ask` stops in state `conflict`, and its row lists the files in
`conflicts`. Answer with `transfer-answer`: `choice` is `replace`,
`keep-both` or `skip`, for one `rel`, several `rels`, or `all`. Do not answer
for the person. Use `skip` or ask them.

A `transfer` event has `action` `created`, `state`, `ended` or `removed`,
and `transfer`, the row. `removed` says a copy that ended left the list,
by `transfer-clear` or 30 minutes after it ended. `transfer-progress` (the row while bytes move) and
`transfer-file` (one file ended) reach only a subscriber that names them.

`transfer-list`, `transfer-pause`, `transfer-resume`, `transfer-cancel` and
`transfer-clear` are under `tuios transfers`.

## The file verbs

On this machine, or on a host through `open-host-connection`:

- `file-list` lists a folder, folders first, in natural order. `file-stat`
  describes one path.
- `file-read` reads up to 4 MiB, base64. `file-hash` is the sha256 of a file
  or a range of it. `file-preview` makes a small preview of a file: a scaled
  image, a PDF page, a film frame, or the start of a text file.
- `file-mkdir`, `file-rename` and `file-remove` change files. `file-mkdir`
  with `receive` makes the receive folder and answers its path.
  `file-remove` of a folder that holds files needs `recursive`, and never
  removes the home folder or `/`. Ask the person before you remove what you
  did not make.

The error codes are `no_file`, `file_exists`, `no_permission`,
`hash_mismatch`, `source_changed`, `cross_device`, `disk_full` and
`no_transfer` (`tuios --skill errors`).
