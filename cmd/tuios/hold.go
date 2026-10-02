package main

import (
	"bufio"
	"fmt"
	"os"
)

// holdAfter keeps the terminal open after a failure until enter is pressed,
// when asked to. A pane the rail opened for this command closes the moment it
// exits, and a message nobody had time to read is the same as no message.
func holdAfter(err error, hold bool) error {
	if !hold || err == nil {
		return err
	}
	fmt.Fprintln(os.Stderr, err.Error())
	fmt.Fprint(os.Stderr, "Press enter to close.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	return heldError{}
}

// heldError is what runOnHost returns after it has already printed and held.
// Its text is empty so main prints nothing more, and its status is the plain
// failure code.
type heldError struct{}

func (heldError) Error() string { return "" }

func (heldError) ExitStatus() int { return 1 }
