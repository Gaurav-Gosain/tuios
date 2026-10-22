package session

import (
	"encoding/binary"
	"syscall"
)

// readMachineLoad reads the one-minute load from vm.loadavg: three 32-bit
// fixed point values, then their 64-bit scale at offset 16. Memory in use is
// not read on macOS.
func readMachineLoad() (load1, memPct float64, ok bool) {
	s, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return 0, 0, false
	}
	// Sysctl drops one trailing zero byte, which the scale ends in.
	b := make([]byte, 24)
	copy(b, s)
	scale := float64(binary.LittleEndian.Uint64(b[16:24]))
	if scale == 0 {
		return 0, 0, false
	}
	return float64(binary.LittleEndian.Uint32(b[0:4])) / scale, 0, true
}
