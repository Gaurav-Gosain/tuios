//go:build freebsd || openbsd

package app

import (
	"encoding/binary"
	"strconv"

	"golang.org/x/sys/unix"
)

func readCPUTicks() (cpuTicks, bool) {
	buf, err := unix.SysctlRaw("kern.cp_time")
	if err != nil {
		return cpuTicks{}, false
	}
	const longSize = strconv.IntSize / 8
	if len(buf) < 5*longSize || len(buf)%longSize != 0 {
		return cpuTicks{}, false
	}
	var t cpuTicks
	for off := 0; off < len(buf); off += longSize {
		var v uint64
		if longSize == 8 {
			v = binary.NativeEndian.Uint64(buf[off:])
		} else {
			v = uint64(binary.NativeEndian.Uint32(buf[off:]))
		}
		t.total += v
		t.idle = v
	}
	return t, true
}
