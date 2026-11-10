//go:build darwin

package session

import (
	"context"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// usageSupported says pane-usage can read processes here.
const usageSupported = true

// procUsage reads every process's parent, CPU share and resident memory from
// one ps listing.
func procUsage(roots map[int]paneProc) (map[int]paneProc, map[int]*procStat) {
	ctx, cancel := context.WithTimeout(context.Background(), portsCommandTimeout)
	defer cancel()
	out, err := childproc.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,pcpu=,rss=").Output()
	if err != nil {
		return roots, nil
	}
	kids := map[int][]int{}
	stats := map[int]*procStat{}
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		pct, _ := strconv.ParseFloat(f[2], 64)
		rss, _ := strconv.ParseInt(f[3], 10, 64)
		kids[ppid] = append(kids[ppid], pid)
		stats[pid] = &procStat{pct: pct, rss: rss * 1024}
	}
	return walkProcTree(roots, func(pid int) []int { return kids[pid] }), stats
}
