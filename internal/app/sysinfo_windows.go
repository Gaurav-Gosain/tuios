package app

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// golang.org/x/sys/windows has no wrapper for GetSystemTimes, so it is called
// through kernel32 directly.
var procGetSystemTimes = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimes")

// readCPUTicks reads GetSystemTimes, in 100 ns units summed over all CPUs.
// Kernel time already includes idle time, so the total is kernel plus user.
func readCPUTicks() (cpuTicks, bool) {
	if procGetSystemTimes.Find() != nil {
		return cpuTicks{}, false
	}
	var idle, kernel, user windows.Filetime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return cpuTicks{}, false
	}
	return cpuTicks{
		idle:  filetimeTicks(idle),
		total: filetimeTicks(kernel) + filetimeTicks(user),
	}, true
}

func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
