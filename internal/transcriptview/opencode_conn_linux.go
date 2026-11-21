package transcriptview

import "os"

var procNetTCP = "/proc/net/tcp"

func serverOwner(c serverSide, _ int) owner {
	f, err := os.Open(procNetTCP)
	if err != nil {
		return ownerOther
	}
	defer func() { _ = f.Close() }()
	return procNetOwner(f, c, myUID())
}
