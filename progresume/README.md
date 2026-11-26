# progresume

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/progresume.svg)](https://pkg.go.dev/tuios.dev/progresume)

`tuios.dev/progresume` is a Go package for the Program Resume Protocol, OSC
7577. A program tells its terminal how to start it again after the terminal
restarts. The terminal puts a token in the environment of the first process of
each pane. The program signs each report with that token, so text that only
passes through a pane cannot make a record.

The package has both sides of the protocol:

- The program side. `Report` and `Clear` read the token, take it out of the
  environment, sign the report and write it to the controlling terminal.
- The terminal side. `Parse` checks the mac, the time and the grammar in the
  order of the specification, and says if a report is ignored, rejected or
  accepted.
- `cmd/resume-wrap`, the wrapper of the specification in Go.

The package uses only the standard library. tuios uses it in its daemon and
its client.

The specification is at
[superlogical.com/rex/docs/build/program-resume](https://www.superlogical.com/rex/docs/build/program-resume).
This package follows revision 0.5 (2026-11-25).

## Install

```sh
go get tuios.dev/progresume
```

## Report from a program

```go
package main

import "tuios.dev/progresume"

func main() {
	// Start me again with this session file. ErrNoToken means that the
	// terminal does not support the protocol.
	_ = progresume.Report([]string{"nvim", "-S", "/home/user/.local/state/nvim/sessions/8f2c1d.vim"}, "")
	run()
	// The person quit on purpose: there is nothing to resume.
	_ = progresume.Clear()
}
```

argv[0] is a bare name, which the terminal looks up on PATH, or an absolute
path. The second argument is the folder to start the program in, or "" for
none. A report that breaks a rule of the specification is not sent, and the
error says which rule.

## Wrap any program

```sh
go install tuios.dev/progresume/cmd/resume-wrap@latest
resume-wrap ssh devbox
```

`resume-wrap` reports itself, the command and its folder, and then becomes
the command. The command never gets the token.

## License

MIT. See [LICENSE](LICENSE).
