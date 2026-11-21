package vt

import (
	"bytes"
	"strconv"

	"tuios.dev/progstatus"
)

var interpretedOSC = [...]int{
	0, 1, 2,
	4, 104,
	7,
	8,
	9, 99, 777,
	10, 11, 12, 110, 111, 112,
	52,
	66,
	133,
	progstatus.Command,
}

const maxOSCNumber = 1 << 20

func interpretsOSC(n int) bool {
	for _, m := range interpretedOSC {
		if m == n {
			return true
		}
	}
	return false
}

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
