//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
)

const portsSupported = true

func paneTrees(roots map[int]paneProc) map[int]paneProc {
	return walkProcTree(roots, readChildren)
}

func listeningSockets(owner map[int]paneProc) []listenHit {
	byInode := map[uint64]procNetListen{}
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		for _, l := range parseProcNetTCP(string(data)) {
			byInode[l.inode] = l
		}
	}
	if len(byInode) == 0 {
		return nil
	}
	var hits []listenHit
	for pid := range owner {
		dir := "/proc/" + strconv.Itoa(pid) + "/fd"
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		comm := ""
		for _, fd := range fds {
			link, err := os.Readlink(dir + "/" + fd.Name())
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			inode, err := strconv.ParseUint(strings.TrimSuffix(link[len("socket:["):], "]"), 10, 64)
			if err != nil {
				continue
			}
			l, ok := byInode[inode]
			if !ok {
				continue
			}
			if comm == "" {
				b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
				comm = strings.TrimSpace(string(b))
			}
			hits = append(hits, listenHit{pid: pid, port: l.port, addr: l.addr, comm: comm})
		}
	}
	return hits
}
