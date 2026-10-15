// Package vt is a terminal emulator. It reads the bytes a program writes to a
// terminal and keeps the screen they draw: the cells, their colours and
// attributes, the cursor, the scrollback and the modes.
//
// New returns an emulator. Write feeds it what the program printed, and
// CellAt, String and Render read the screen. Read returns the replies the
// emulator sends the program, such as the answer to a cursor position query.
// Screen is the interface with those methods. Terminal is the full interface
// tuios uses, with the scrollback, the modes, key and mouse encoding,
// graphics and the state a snapshot carries. Callbacks reports what the
// program asks of the host, such as a new title.
//
// The default build is pure Go and returns an *Emulator. With the ghostty
// build tag, New returns an emulator backed by libghostty-vt through cgo.
//
// The sub-package snapshot captures an emulator's state and brings another
// emulator to the same state, and vtgen generates terminal input for fuzzing
// an emulator.
package vt
