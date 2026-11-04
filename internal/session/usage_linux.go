//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// usageSupported says pane-usage can read processes here.
const usageSupported = true

// procUsage reads each pane's process tree and each process's
// /proc/PID/stat: utime and stime in clock ticks (100 a second on every
// Linux tuios runs on), and rss in pages.
func procUsage(roots map[int]paneProc) (map[int]paneProc, map[int]*procStat) {
	owner := paneTrees(roots)
	page := int64(os.Getpagesize())
	stats := make(map[int]*procStat, len(owner))
	for pid := range owner {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			continue
		}
		// The name in parentheses may hold spaces: fields count from
		// after its closing parenthesis, where field 3 (state) is first.
		s := string(b)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) < 22 {
			continue
		}
		ut, _ := strconv.ParseInt(f[11], 10, 64)
		st, _ := strconv.ParseInt(f[12], 10, 64)
		rss, _ := strconv.ParseInt(f[21], 10, 64)
		stats[pid] = &procStat{cpu: time.Duration(ut+st) * 10 * time.Millisecond, rss: rss * page}
	}
	return owner, stats
}
