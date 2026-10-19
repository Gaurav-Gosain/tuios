package transcriptview

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// procNetTCP is the kernel's table of IPv4 sockets. A test points it at a
// table of its own.
var procNetTCP = "/proc/net/tcp"

// portOwnedByMe reports whether a socket that listens on 127.0.0.1:port
// belongs to this user, from the uid column of /proc/net/tcp. The table
// writes the address as the hex of the address word in host order, so
// 127.0.0.1 is 0100007F on a little-endian machine and 7F000001 on a
// big-endian one, and the state 0A is LISTEN.
func portOwnedByMe(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	f, err := os.Open(procNetTCP)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	want := strings.ToUpper(strconv.FormatInt(int64(port), 16))
	for len(want) < 4 {
		want = "0" + want
	}
	uid := strconv.Itoa(os.Getuid())
	sc := bufio.NewScanner(f)
	owned := false
	for sc.Scan() {
		// sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid ...
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 || fields[3] != "0A" {
			continue
		}
		if fields[1] != "0100007F:"+want && fields[1] != "7F000001:"+want {
			continue
		}
		if fields[7] != uid {
			// Another user listens there too: refuse.
			return false
		}
		owned = true
	}
	return owned
}
