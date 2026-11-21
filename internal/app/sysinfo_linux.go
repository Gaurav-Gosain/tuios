package app

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

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
	_ = scanner.Err()
	return cpuTicks{}, false
}
