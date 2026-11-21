package session

import (
	"encoding/binary"
	"syscall"
)

func readMachineLoad() (load1, memPct float64, ok bool) {
	s, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return 0, 0, false
	}
	b := make([]byte, 24)
	copy(b, s)
	scale := float64(binary.LittleEndian.Uint64(b[16:24]))
	if scale == 0 {
		return 0, 0, false
	}
	return float64(binary.LittleEndian.Uint32(b[0:4])) / scale, 0, true
}
