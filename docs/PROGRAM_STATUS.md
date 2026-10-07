# Program status (OSC 7501)

Any program in a tuios pane can say what it is doing: a build, a deploy
script, a package manager or a coding agent. It writes one escape sequence, and
tuios shows the state in the rail, the Inbox and the pane's title. The program
needs no hook, no socket and no plugin.

The sequence is the Program Status Protocol, OSC 7501, revision 0.2 of
2026-10-06. Read the specification at
[superlogical.com/rex/docs/build/program-status](https://www.superlogical.com/rex/docs/build/program-status).
The Rex terminal reads it too. Support in libghostty, the library under
Ghostty, is proposed in a pull request.

```sh
# Terraform waits for an approval. The message is base64 of
# "Apply 3 to add, 1 to change, 0 to destroy?".
printf '\e]7501;state=blocked:kind=permission:app=terraform:msg=QXBwbHkgMyB0byBhZGQsIDEgdG8gY2hhbmdlLCAwIHRvIGRlc3Ryb3k/\e\\'
```

## Report from a script

Use `tuios status`. It writes the sequence and does the base64 for you. It
needs no daemon, so a script can use it in any terminal.

```sh
tuios status working --app build --msg 'Compiling' --progress 40
tuios status blocked --kind permission --app deploy --msg 'Approve deploy to production?'
tuios status done --app build --msg 'Built 12 crates'
tuios status error --app build --msg 'Linker failed'
tuios status --clear
```

| Flag | What it does |
| --- | --- |
| `STATE` | `idle`, `working`, `done`, `blocked` or `error` |
| `--kind` | What a blocked program waits for: `permission`, `question` or `auth` |
| `--progress N` | Progress from 0 to 100, with `working` or `blocked` |
| `--app NAME` | The program's name, such as `cargo` or `terraform` |
| `--title TEXT` | A short label for the record |
| `--msg TEXT` | One line that says what the program does or waits for |
| `--id ID` | The record to address, such as `build` or `build/test` |
| `--clear` | Remove the record and the records under it. Without `--id`, remove all records |
| `--stdout` | Write the sequence to standard output, not to the terminal |

The sequence goes to the controlling terminal (`/dev/tty`), so it is not lost
when the script's output goes to a file or a pipe. `tuios status` refuses a
report that a terminal must discard, and says why.

Each report replaces its record. A key that a report does not send is gone from
the record. Send `--app` and `--title` in every report that must keep them.

### A shell function

A script that cannot call tuios can use this function:

```sh
status() {
  printf '\e]7501;state=%s:msg=%s\e\\' "$1" "$(printf '%s' "$2" | base64 | tr -d '\n')"
}

status working "Syncing photos"
rsync -a ~/Photos backup:/photos && status done "Photos synced" || status error "rsync failed"
```

### A build with stages

```sh
#!/bin/sh
tuios status working --app ci --msg 'Running the pipeline'
tuios status working --app ci --id build --title Build --msg 'Compiling' --progress 10
cargo build --release || { tuios status error --app ci --msg 'Build failed'; exit 1; }
tuios status done --app ci --id build --title Build --msg 'Built'
tuios status working --app ci --id test --title Tests --msg 'Testing'
cargo test || { tuios status error --app ci --msg 'Tests failed'; exit 1; }
tuios status --clear --id build
tuios status --clear --id test
tuios status done --app ci --msg 'Pipeline passed'
```

A program that exits right after its work reports `done` or `error` first. The
record then stays for you to find.

### Check for support

A program sends `OSC 7501 ; ? ST`. A terminal that reads the protocol answers
with the same sequence. tuios answers from the pane's emulator, with the
terminator the question used. Send a primary device attributes query (`CSI c`)
after the question. If that answer comes first, the terminal does not read the
protocol.

## What tuios shows

A report makes the pane an agent pane, for any program. The agent state comes
from the most urgent record of the pane:

| OSC 7501 state | tuios agent state | `blocked_by` |
| --- | --- | --- |
| `working` | `working` | |
| `blocked`, `kind=permission` | `needs_input` | `approval` |
| `blocked`, `kind=question` | `needs_input` | `question` |
| `blocked`, `kind=auth` | `needs_input` | `auth` |
| `blocked`, no kind | `needs_input` | empty |
| `done` | `done`, finished and unread | |
| `error` | `errored` | |
| `idle` | `idle` | |

- **The rail.** The agent row shows the `app` where a harness name goes, the
  progress beside the name (`build · 40%`), and the title and message on the
  second line. The `progress` token of `[appearance.sidebar.agent_row]` places
  the progress.
- **The Inbox.** A blocked record opens an approval or a question item, an
  `error` record an error item, and a `done` record a finished item. The item
  names the pane by its title and by its id, such as `build [3f2a9c1e]`,
  because the program can set the title and cannot set the id. The detail of
  the item lists every record of the pane. For `kind=auth` the Inbox offers no
  answer: you type a login in the pane that asks for it.
- **The verbs.** `get-agent-state` and `list-agents` report `source:
  "program"` and the records as `program_status`.
- **Hooks and alerts.** An agent state from a record raises the same hooks,
  alerts and push notifications as any other agent state. An alert names the
  pane by its id too. The dock shows every alert, but a notification, a bell, a
  sound or a client hook from one such pane goes out at most once in 30
  seconds.
- **A harness hook in the same pane wins.** The source `program` ranks just
  below a hook's report. When a hook reports for the pane, the records still
  show, and the pane's state is the hook's. When the records of a pane end, a
  state that a weaker source (the screen, the detector) held before the
  program reported comes back.

### More than one record

A program can keep records for its parts. The `id` is a path: `build/test` is
under `build`, and `build` is under the root record, which has no id.

- The pane shows the most urgent record. `blocked` comes first, then `error`,
  `done`, `working` and `idle`. Among records in the same state, the root
  record comes first, then the record that changed last.
- A record without `app` takes the `app` of its nearest parent that has one.
- `state=clear` with an `id` removes that record and every record under it.
  Without an `id`, it removes every record of the pane.
- A pane holds at most 256 records. A new record past that removes the record
  that changed least recently.

### How long a record stays

There is no heartbeat. A program does not have to send its state again.

| Event | `working`, `blocked`, `idle` | `done`, `error` |
| --- | --- | --- |
| The shell starts a prompt (OSC 133 A) | removed | kept |
| The program exits | removed | kept |
| You type in the pane | kept | removed |
| A full reset (RIS) | removed | removed |
| A soft reset (DECSTR), or a switch to the alternate screen | kept | kept |

A shell without OSC 133 marks does not tell tuios that a prompt started. So
when a working, blocked or idle report arrives, tuios reads which process
group holds the pane's terminal. If it is not the shell, a program in the
foreground reported, and when the agent detector next sees the shell in the
foreground, that program has exited and its records go. The detector looks
every 2 seconds. A report from a background job (`job &`), or from a pane
whose own process is the program (`tuios new-window -- cargo watch`), does not
come from a foreground program, so its records stay until the program changes
them or a prompt starts. A program that reports and exits before tuios reads
the report leaves its record until the next prompt or OSC 133 A.

Keys that `tuios send-text` or `tuios send-keys` types do not count as your
typing for `done` and `error`. Keys from an attached client do.

### OSC 9;4

tuios reads OSC 9;4 progress as an agent state on a pane known to run an
agent. When a pane sends an OSC 7501 report, tuios stops reading OSC 9;4 for
the agent state of that pane, until the next full reset. A progress report
would otherwise wipe out the kind and message of the program's own report.

## tuios inside a terminal that reads OSC 7501

tuios is a program too. When it runs in Rex or another terminal that reads
the protocol, it reports the agent states of its panes there. The
terminal can then show that a pane in tuios waits for you while tuios is in
another tab.

- At start, each local or ssh client asks its terminal `OSC 7501 ; ?`. It
  reports only when the terminal answers.
- Each pane of the attached session has one record, with the id
  `<session>/<pane>`. The title is the pane's name. The `app` is the agent's
  harness, or the pane's own `app`. The message is the pane's agent message.
- A pane with no agent state, or one in the `unknown` state, has no record.
  A finished turn that you saw is reported as `idle`.
- tuios sends only the records that changed, at most once in 250 milliseconds.
  It keeps at most 64 records on the terminal.
- tuios never passes a pane's own sequence through. What the terminal gets is
  tuios's own report, built from the pane's agent state.
- When a client exits or detaches, it sends `state=clear` for each record it
  left. A local client then also resets the terminal. An ssh client's
  terminal gets the clears over the connection, when the connection is still
  up.

To turn this off:

```toml
[agents]
host_program_status = "off"   # "auto" is the default
```

A browser client of `tuios-web` does not ask.

## Limits

tuios applies the limits of the specification. A report that breaks one is
discarded whole, and nothing from it is applied.

| Item | Limit |
| --- | --- |
| The whole sequence, OSC through ST | 4096 bytes |
| A key | 16 bytes |
| `msg` | 2732 bytes encoded, 2048 bytes decoded |
| `title` | 256 bytes encoded, 192 bytes decoded |
| `app` | 32 bytes |
| `id` | 128 bytes, 8 levels, 32 bytes for each level |
| Records in one pane | 256 |

The parser follows the grammar of the specification:

- Values use only `A-Z a-z 0-9 _ . , + / = -`. ASCII spaces and tabs around a
  key or a value are removed. Other blank characters are not, so they make the
  pair malformed.
- Inside the sequence, a C0 control other than BEL and ESC is ignored. CAN and
  SUB cancel the sequence. An ESC that does not start ST ends the sequence
  there. Both emulators do the same.
- A pair without `=`, with an empty key, or with a character outside the set is
  skipped. The rest of the report still applies.
- An unknown key is ignored. When a key repeats, the last value wins.
- A report without `state`, or with a state tuios does not know, is ignored.
- A report with an `id` that does not match the grammar is ignored. This
  includes an `id` with a character outside the value set, such as `id=a b`
  or `id=x;y`: the report does not fall back to the root record.
- A `kind` with a state other than `blocked`, or an unknown `kind`, is ignored.
  So is a `progress` outside 0 to 100, or with a state other than `working` or
  `blocked`, and an `app` with a character outside its set.

## Security

Everything in a report comes from a program that already controls the pane.
tuios treats it as untrusted text.

- A report whose `msg` or `title` does not decode, is not UTF-8, or holds a
  control character is discarded whole. Base64 is read strictly: padding is
  optional, but padding that is present must be right, and the unused bits at
  the end must be zero. Control characters are U+0000 to
  U+001F, U+007F and U+0080 to U+009F.
- tuios never reads markup in a report. `<b>` is shown as `<b>`.
- tuios removes bidi overrides, zero width characters and other invisible
  format characters before it shows the text in the rail, the Inbox or a verb.
- tuios writes back only the fixed answer to `OSC 7501 ; ?`. No id, title or
  message is ever sent back, and a program cannot read the records.
- Every place that shows a record names the pane it came from. Alerts and
  Inbox items add the pane's id, which a program cannot set, to the title,
  which it can.
- A pane whose state comes from OSC 7501 raises a notification, a bell, a
  sound or a client hook at most once in 30 seconds. Push notifications have
  their own limits for each pane. tuios reports to a host terminal at most
  once in 250 milliseconds.
- `app` is only a label. tuios does not look it up as a harness, so a program
  cannot get the answers that a harness's approvals allow.

## Decisions where the specification leaves a choice

- **Idle records** end at a shell prompt and when the program exits. The
  specification allows this. A program that returned to the shell is no longer
  at rest in the pane.
- **Done and error records** end when you type in the pane.
- **A blocked record without a kind** gives an empty `blocked_by`. tuios does
  not guess the kind from the message, because the specification forbids
  reading meaning into `msg`.
- **Text that is not UTF-8** discards the report, as a control character does.
- **An `app` longer than 32 bytes** discards the report, because it breaks a
  limit. An `app` with a character outside its set is treated as absent.
- **When a key repeats**, only the last value is checked. An earlier bad value
  that a later one replaces does not discard the report. The exception is
  `id`: any `id` pair with a character outside the value set discards the
  report, so a malformed id never lands on the root record.
- **The process exit** in a pane whose own process is a shell is the exit of
  the program that reported from the foreground (see
  [How long a record stays](#how-long-a-record-stays)).
- **OSC 9;4** is not mapped to the root record. tuios keeps its own reading of
  OSC 9;4 as an agent state, and stops it once a pane sends OSC 7501.
- **The answer to the query** ends with the terminator the query used, as
  xterm does for its own queries.
