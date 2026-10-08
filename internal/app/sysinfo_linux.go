package app

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// readCPUTicks reads the aggregate "cpu" line of /proc/stat. Busy time is
// everything but idle: iowait counts as busy, as it always has here. Guest
// time is left out because the kernel already counts it in user.
func readCPUTicks() (cpuTicks, bool) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTicks{}, false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		// user nice system idle iowait irq softirq steal [guest guest_nice]
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			return cpuTicks{}, false
		}
		var t cpuTicks
		for i, f := range fields[:min(len(fields), 8)] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return cpuTicks{}, false
			}
			t.total += v
			if i == 3 {
				t.idle = v
			}
		}
		return t, true
	}
	return cpuTicks{}, false
}
