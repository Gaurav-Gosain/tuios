package vt_test

import (
	"fmt"

	"tuios.dev/vt"
)

// A program's output goes in, and the screen comes out.
func Example() {
	term := vt.New(40, 5)
	defer term.Close()

	_, _ = term.Write([]byte("hello \x1b[1mworld\x1b[0m\r\n$ "))

	fmt.Println(term.String())
	fmt.Println(term.CellAt(6, 0).Content) // "w", drawn bold
	fmt.Println(term.CursorPosition())     // column 2, row 1
}

// The emulator tells the host what the program asks of it through Callbacks.
func ExampleEmulator_SetCallbacks() {
	term := vt.NewEmulator(80, 24)
	defer term.Close()

	term.SetCallbacks(vt.Callbacks{
		Title: func(title string) { fmt.Println("title:", title) },
		Bell:  func() { fmt.Println("bell") },
	})
	_, _ = term.Write([]byte("\x1b]2;build\x07\a"))
}

// A sequence that asks a question gets its answer through Read. A host
// copies the answers back to the program, usually in a goroutine.
func ExampleEmulator_Read() {
	term := vt.NewEmulator(80, 24)
	defer term.Close()

	_, _ = term.Write([]byte("\x1b[3;5H\x1b[6n")) // move, then ask where the cursor is
	reply := make([]byte, 32)
	n, _ := term.Read(reply)
	fmt.Printf("%q\n", reply[:n]) // "\x1b[3;5R"
}

// A host that reads the program's output on one goroutine can drain the
// answers right after each Write instead. TakeResponses never blocks.
func ExampleEmulator_TakeResponses() {
	term := vt.NewEmulator(80, 24)
	defer term.Close()

	_, _ = term.Write([]byte("\x1b[3;5H\x1b[6n"))
	fmt.Printf("%q\n", term.TakeResponses()) // "\x1b[3;5R"
	fmt.Printf("%q\n", term.TakeResponses()) // "" (nothing is left)
}
