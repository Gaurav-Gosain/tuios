# Recording a pane to an asciicast file

**A recording holds everything the pane prints while it runs, and the history
on its screen when it starts.** A token a command echoed, a secret a script
printed and a file you showed with `cat` are all in the file. Nothing is
recorded unless you ask for it, no setting turns recording on, and the file is
readable by you only (mode 0600). The keys you type into the pane are not
recorded. Read the file before you share it.

`tuios record-pane` writes what a pane does to an
[asciicast v2](https://docs.asciinema.org/manual/asciicast/v2/) file (`.cast`).
These programs open it:

- `asciinema play pane.cast` plays it in a terminal.
- `agg pane.cast pane.gif` makes a GIF.
- `reel export pane.cast -o pane.webp` makes a GIF, WebP or MP4, and
  `reel still --mark NAME` renders one moment.
- The inspector at inspector.getsteep.dev shows each event on a timeline.

## Record, mark, stop

```sh
tuios record-pane start -w build -o build.cast   # returns at once
tuios record-pane mark -w build "tests start"    # a marker at this moment
tuios record-pane stop -w build                  # returns when the file is complete
```

Without `-w`, tuios records the focused pane. A relative `-o` is taken from
the folder you run the command in. An existing file is refused unless you give
`--overwrite`. `tuios record-pane list` shows the recordings that run.

The daemon writes the file, so a recording runs with no client attached, in a
detached session, and after you close the terminal you started it from.

## What the file holds

- The header has the pane's size when the recording started, the start time,
  the pane's `TERM` and the `--title`.
- The first event, at time 0, paints the screen the pane shows when the
  recording starts, with up to 500 rows of history above it. A player opens on
  that screen, not on a blank one.
- Every chunk of output after that is an `o` event at the time the daemon
  passed it on.
- A resize of the pane is an `r` event (`"COLSxROWS"`).
- `tuios record-pane mark` writes an `m` event with the label. Players list
  the markers and jump to them.
- When a recording ends for a reason other than `stop` or the end of the pane,
  the last event is a marker that names the reason.

## When a recording ends

| What happens | The recording |
| --- | --- |
| `tuios record-pane stop` | ends. stop returns when the file is complete. |
| `--for 30s` passes | ends. |
| The file reaches `--max-size` (default 64MiB, at most 1GiB) | ends with a `size limit` marker. |
| The pane's program exits, or the pane is closed | ends after the last output. |
| The daemon stops | ends with a `daemon stopped` marker. |
| A live upgrade (`tuios daemon upgrade`) | ends with a `daemon upgrade` marker before the panes move to the new daemon. Start a new recording after the upgrade. |
| The disk is full | ends. The file holds every event up to the last whole line. |

A pane has one recording at a time, and a daemon runs at most 16. A recording
never slows the pane: if the pane prints faster than the file is written, the
recording catches up from the daemon's buffer, or starts again from a new
snapshot with a `resync` marker.

## Who may record

- Your own commands, on this machine, may write anywhere you may.
- A linked machine needs `files` in its `[hosts]` policy, and its file goes
  only where a linked file copy may go: the write roots, never a folder that
  holds keys, shell start files or the tuios config. See `files` in
  [CONFIGURATION.md](CONFIGURATION.md).
- A restricted connection, such as a pane that runs under strict grants,
  cannot start or stop a recording. It may add a marker to the recording of
  its own pane.

## Verbs

`record-pane`, `stop-recording`, `mark-recording` and `list-recordings`.
`tuios list-verbs record-pane` gives the parameters and the result of each.
