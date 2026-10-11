# progstatus

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/progstatus.svg)](https://pkg.go.dev/tuios.dev/progstatus)

`tuios.dev/progstatus` is a Go package for the Program Status Protocol, OSC
7501. A program writes one escape sequence to say what it does: idle, working,
done, blocked or error. A terminal reads the sequence and shows the state.

The package has the parts that every user of the protocol needs:

- The parser. It applies every discard rule of the specification before it
  returns a report.
- The record store. It has the limits, the eviction, the id hierarchy and the
  lifetime rules.
- The summary rule. It picks the record that a pane shows.
- The encoder. A program uses it to write a report.

The package uses only the standard library. tuios uses it in its terminal
emulator, its daemon, its client and the `tuios status` command.

The specification is at
[superlogical.com/rex/docs/build/program-status](https://www.superlogical.com/rex/docs/build/program-status).
This package follows revision 0.2 (2026-10-06).

## Install

```sh
go get tuios.dev/progstatus
```

The go command reads the import path from tuios.dev. Go 1.25 or later can
fetch it directly. With the default module proxy, any Go version can fetch it.

## Write a report from a program

```go
package main

import (
	"os"

	"tuios.dev/progstatus"
)

func main() {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return // no terminal: nothing to report to
	}
	defer tty.Close()

	tty.WriteString(progstatus.Sequence(progstatus.Report{
		State:    progstatus.Working,
		App:      "build",
		Msg:      "Compiling",
		Progress: 40,
	}))
}
```

Write the sequence to `/dev/tty`. Then the report reaches the terminal when
the output of the program goes to a file or a pipe.

A terminal discards a report that breaks a rule, such as a message that is too
long. Use `FitText` for free text and `FitSegment` for an id or an app name
that comes from outside input:

```go
seq := progstatus.Sequence(progstatus.Report{
	State:    progstatus.Working,
	ID:       progstatus.FitSegment(branch, progstatus.MaxSegment),
	Msg:      progstatus.FitText(line, progstatus.MaxMsgDecoded),
	Progress: progstatus.NoProgress,
})
```

## Read reports in a terminal

Give `Parse` the bytes after `7501;` and the length of the whole sequence.
Store each report that it accepts.

```go
var store progstatus.Store

func onOSC7501(body []byte, seqLen int, reply func(string)) {
	if progstatus.IsQuery(body) {
		reply(progstatus.QueryReply + "\x1b\\") // use the terminator of the query
		return
	}
	report, ok := progstatus.Parse(body, seqLen)
	if !ok {
		return // discarded: apply nothing
	}
	store.Apply(report, time.Now().UnixNano())
}

func paneStatus() (progstatus.Record, bool) {
	return progstatus.Summary(store.Records())
}
```

`SequenceLen` gives the length of a sequence from the length of its payload.
Call `Store.DropTransient` when a shell prompt appears (OSC 133 A) or the
process exits. Call `Store.Reset` on a full reset (RIS).

## Stability

The module is at v0. The API can change between minor versions. The tags have
the folder as a prefix, such as `progstatus/v0.1.0`.

## Issues

The code is in the [tuios repository](https://github.com/Gaurav-Gosain/tuios),
in the `progstatus` folder. Report issues in the tuios issue tracker.

## Licence

MIT. See [LICENSE](LICENSE).
