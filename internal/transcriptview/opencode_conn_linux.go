package transcriptview

import "os"

// procNetTCP is the kernel's table of IPv4 sockets. A test points it at a
// table of its own.
var procNetTCP = "/proc/net/tcp"

// serverOwner reads the owner of the server side of c from the uid column of
// /proc/net/tcp.
func serverOwner(c serverSide, _ int) owner {
	f, err := os.Open(procNetTCP)
	if err != nil {
		return ownerOther
	}
	defer func() { _ = f.Close() }()
	return procNetOwner(f, c, myUID())
}
