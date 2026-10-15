# vt

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/vt.svg)](https://pkg.go.dev/tuios.dev/vt)

`tuios.dev/vt` is a terminal emulator in Go. It reads the bytes a program
writes to a terminal and keeps the screen they draw: the cells, their colours
and attributes, the cursor, the scrollback and the modes. It is the emulator
of [tuios](https://github.com/Gaurav-Gosain/tuios), where it runs every pane.

Use it to:

- run a program in a test and check what it drew,
- show a terminal inside a Bubble Tea program or another TUI,
- keep a pane's screen in a server and send it to a client that attaches
  later.

What it covers:

- VT100 to xterm sequences, with a conformance corpus in its tests.
- Scrollback with reflow on resize.
- The alternate screen, scroll regions, left and right margins, tab stops,
  character sets and selective erase.
- Mouse modes and key encoding, with the kitty keyboard protocol and
  modifyOtherKeys.
- Kitty graphics and sixel, OSC 8 links, OSC 133 shell marks, OSC 52
  clipboard, desktop notifications and OSC 9;4 progress.
- [OSC 7501](https://pkg.go.dev/tuios.dev/progstatus), the Program Status
  Protocol.

The default build is pure Go and needs no cgo. With the `ghostty` build tag,
`New` returns an emulator backed by
[libghostty-vt](https://github.com/ghostty-org/ghostty) instead, through
cgo.

## Install

```sh
go get tuios.dev/vt
```

It needs Go 1.26 or later. The go command reads the import path from
tuios.dev.

## Example

```go
package main

import (
	"fmt"

	"tuios.dev/vt"
)

func main() {
	term := vt.New(40, 5)
	defer term.Close()

	// What a program prints goes in.
	term.Write([]byte("hello \x1b[1mworld\x1b[0m\r\n$ "))

	// The screen comes out, as text or cell by cell.
	fmt.Println(term.String())
	fmt.Println(term.CellAt(6, 0).Content) // "w", drawn bold
	fmt.Println(term.CursorPosition())     // column 2, row 1
}
```

Some sequences ask the terminal a question, such as the cursor position.
`Read` returns the replies, and the host writes them back to the program.
With a pseudo-terminal, copy them in a goroutine:

```go
go io.Copy(ptmx, term)
```

A host that reads the program's output on one goroutine can call
`TakeResponses` after each `Write` instead. It returns the queued replies and
never blocks.

## The API

- `Screen` is the small interface most users need: `Write`, `Read`,
  `Resize`, `Width`, `Height`, `CellAt`, `CursorPosition`, `Render`,
  `String` and `Close`.
- `Terminal` is the full interface that tuios uses. It adds the scrollback,
  the modes, key and mouse encoding, graphics, and the state a snapshot
  carries.
- `New` and `NewWithScrollback` return the backend the build selected.
  `NewEmulator` returns the pure Go `*Emulator`.
- `Callbacks` reports what a program asks of the host: a new title, a bell,
  a working directory, a clipboard write, a notification, and private OSC
  sequences.

Sub-packages:

- [`tuios.dev/vt/snapshot`](https://pkg.go.dev/tuios.dev/vt/snapshot)
  captures an emulator's state, packs it for a wire or a file, and brings
  another emulator to the same state. `Replay` turns a state into the bytes
  that repaint it on any xterm-compatible terminal.
- [`tuios.dev/vt/vtgen`](https://pkg.go.dev/tuios.dev/vt/vtgen) generates
  terminal input from a grammar, for fuzzing an emulator. A failure reduces to
  a script of named sequences a person can read.

## Stability

The module is at v0. The API can change between minor versions. A change is
listed in the release notes of the version that makes it.

## Licence

MIT. The package started as a copy of the vt package of
[charmbracelet/x](https://github.com/charmbracelet/x). See LICENSE and NOTICE.

Report a problem in the
[tuios issue tracker](https://github.com/Gaurav-Gosain/tuios/issues).
