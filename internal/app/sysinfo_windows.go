package app

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// golang.org/x/sys/windows has no wrapper for GetSystemTimes or
// GlobalMemoryStatusEx, so they are called through kernel32 directly.
var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

// memoryStatusEx is MEMORYSTATUSEX. Only the length and the load are read.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// readRAMPercent reads the share of physical memory in use from
// GlobalMemoryStatusEx, the same figure gopsutil reports on Windows. It calls
// kernel32 itself because gopsutil's mem package links WMI and OLE here, about
// 18 KB the windows binary cannot spare under the size ceiling.
func readRAMPercent() (float64, bool) {
	if procGlobalMemoryStatusEx.Find() != nil {
		return 0, false
	}
	st := memoryStatusEx{}
	st.length = uint32(unsafe.Sizeof(st))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st)))
	if r == 0 {
		return 0, false
	}
	return float64(st.memoryLoad), true
}

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
