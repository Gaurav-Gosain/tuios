package transcriptview

import "golang.org/x/sys/unix"

// serverOwner reads the owner of the server side of c from the so_uid of its
// socket in the sysctl net.inet.tcp.pcblist_n, the table netstat reads. It
// needs no privilege and no cgo.
func serverOwner(c serverSide, _ int) owner {
	uid := myUID()
	if uid < 0 {
		return ownerOther
	}
	// The table can grow between the call that sizes the buffer and the one
	// that fills it, which fails with ENOMEM. Try again a few times.
	for range 3 {
		buf, err := unix.SysctlRaw("net.inet.tcp.pcblist_n")
		if err == nil {
			return pcblistOwner(buf, c, uint32(uid))
		}
	}
	return ownerOther
}
