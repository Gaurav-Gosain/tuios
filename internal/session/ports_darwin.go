//go:build darwin

package session

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

const portsSupported = true

const portsCommandTimeout = time.Second

func paneTrees(roots map[int]paneProc) map[int]paneProc {
	ctx, cancel := context.WithTimeout(context.Background(), portsCommandTimeout)
	defer cancel()
	out, err := childproc.CommandContext(ctx, "ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return roots
	}
	kids := map[int][]int{}
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			kids[ppid] = append(kids[ppid], pid)
		}
	}
	return walkProcTree(roots, func(pid int) []int { return kids[pid] })
}

func listeningSockets(owner map[int]paneProc) []listenHit {
	pids := make([]string, 0, len(owner))
	for pid := range owner {
		pids = append(pids, strconv.Itoa(pid))
	}
	if len(pids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), portsCommandTimeout)
	defer cancel()
	out, _ := childproc.CommandContext(ctx, "lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-Fpcn", "-p", strings.Join(pids, ",")).Output()
	return parseLsofListen(string(out))
}

func parseLsofListen(out string) []listenHit {
	var hits []listenHit
	pid, comm := 0, ""
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\n")
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
			comm = ""
		case 'c':
			comm = line[1:]
		case 'n':
			host, port, err := net.SplitHostPort(line[1:])
			if err != nil || pid == 0 {
				continue
			}
			n, err := strconv.Atoi(port)
			if err != nil {
				continue
			}
			if host == "*" {
				host = "0.0.0.0"
			}
			hits = append(hits, listenHit{pid: pid, port: n, addr: host, comm: comm})
		}
	}
	return hits
}
