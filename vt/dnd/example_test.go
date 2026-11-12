package dnd_test

import (
	"fmt"

	"tuios.dev/vt/dnd"
)

// A program in a pane says what it accepts, in two chunks, and the terminal
// answers a data request. Parse reads each OSC 72 body, and an Assembler
// joins the chunks of the MIME list.
func Example() {
	// The OSC body of a program that accepts drops, in two chunks.
	var asm dnd.Assembler
	for _, body := range []string{
		"72;t=a:m=1;text/uri-list text/",
		"72;m=0;plain",
	} {
		cmd, ok := dnd.Parse([]byte(body))
		if !ok {
			continue
		}
		if whole, done := asm.Add(cmd); done {
			fmt.Printf("type %c accepts %q\n", whole.Type, dnd.MimeList(whole.Payload))
		}
	}

	// The terminal answers a data request with the bytes of a file.
	reply := dnd.EncodeData(dnd.Command{Type: 'r', X: 1}, []byte("hello"))
	fmt.Printf("%q\n", reply)
	// Output:
	// type a accepts ["text/uri-list" "text/plain"]
	// "\x1b]72;t=r:x=1:y=0:m=0;aGVsbG8\x1b\\\x1b]72;t=r:x=1:y=0\x1b\\"
}
