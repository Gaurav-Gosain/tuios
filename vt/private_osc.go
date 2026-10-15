package vt

import (
	"bytes"
	"strconv"

	"tuios.dev/progstatus"
)

// interpretedOSC is every OSC number the emulator interprets, on both
// backends. Callbacks.PrivateOSC gets every other number. The pure emulator
// registers a handler for each of these (registerDefaultOscHandlers), and the
// ghostty backend handles them itself or leaves them to libghostty-vt. A test
// holds the list to the handlers, so the two backends call the hook for the
// same numbers.
var interpretedOSC = [...]int{
	0, 1, 2, // title and icon name
	4, 104, // palette
	7,          // working directory
	8,          // hyperlink
	9, 99, 777, // notifications, and ConEmu's OSC 9;9 directory
	10, 11, 12, 110, 111, 112, // default colours
	52,                 // clipboard
	66,                 // kitty text sizing
	133,                // semantic prompt
	progstatus.Command, // Program Status Protocol
}

// maxOSCNumber is the largest OSC number either backend reads. The ghostty
// backend's scanner (oscNumber) stops at it, and the pure emulator passes no
// larger number to Callbacks.PrivateOSC.
const maxOSCNumber = 1 << 20

// interpretsOSC reports whether the emulator interprets OSC number n.
func interpretsOSC(n int) bool {
	for _, m := range interpretedOSC {
		if m == n {
			return true
		}
	}
	return false
}

// privateOSCNumber reports whether an OSC the emulator does not interpret
// goes to Callbacks.PrivateOSC: its number is valid and the body starts with
// that number. A body with no number, such as "foo;bar", or a number too
// long for an int, reaches the parser as a number the body does not hold,
// and such a sequence names nothing a host could own. A number above
// maxOSCNumber is dropped too, as the ghostty backend's scanner drops it, so
// both backends pass on the same numbers.
func privateOSCNumber(cmd int, data []byte) bool {
	if cmd < 0 || cmd > maxOSCNumber || interpretsOSC(cmd) {
		return false
	}
	var buf [20]byte
	digits := strconv.AppendInt(buf[:0], int64(cmd), 10)
	if !bytes.HasPrefix(data, digits) {
		return false
	}
	return len(data) == len(digits) || data[len(digits)] == ';'
}
