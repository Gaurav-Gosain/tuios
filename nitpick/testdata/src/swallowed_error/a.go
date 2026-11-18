package swallowed_error

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

func work() error        { return errors.New("x") }
func pair() (int, error) { return 0, nil }
func noErr() int         { return 1 }

func cases() error {
	_ = work()                        // want "the error from work is dropped"
	_, _ = pair()                     // want "the error from pair is dropped"
	_ = os.WriteFile("x", nil, 0o644) // want "the error from os.WriteFile is dropped"

	// The file may be there already.
	_ = os.Mkdir("y", 0o755)
	_ = os.Mkdir("z", 0o755) // best effort

	f, _ := os.Open("x")
	_ = f.Close()
	_ = os.Remove("x")
	_, _ = fmt.Fprintln(os.Stderr, "x")

	_ = noErr()
	var b bytes.Buffer
	_, _ = b.WriteString("x")
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "x")
	_ = error(nil)

	n, _ := pair()
	_ = n

	err := work()
	if err != nil { // want "the block for err != nil is empty"
	}
	if err != nil {
		// Nothing to do: the caller retries.
	}
	if err != nil {
		fmt.Println(err)
	}
	if err == nil {
	}
	_ = err
	return nil
}

// Not reported: a function with no error result has no place for the error.
func noPlace() {
	_ = work()
}
