package session

import (
	"os"
	"strconv"
	"strings"
)

// readMachineLoad reads the one-minute load and the memory in use from /proc.
func readMachineLoad() (load1, memPct float64, ok bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, false
	}
	f, _, _ := strings.Cut(string(b), " ")
	load1, err = strconv.ParseFloat(f, 64)
	if err != nil {
		return 0, 0, false
	}
	if m, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, avail float64
		for line := range strings.SplitSeq(string(m), "\n") {
			key, rest, _ := strings.Cut(line, ":")
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			v, _ := strconv.ParseFloat(fields[0], 64)
			switch key {
			case "MemTotal":
				total = v
			case "MemAvailable":
				avail = v
			}
		}
		if total > 0 {
			memPct = 100 * (total - avail) / total
		}
	}
	return load1, memPct, true
}
