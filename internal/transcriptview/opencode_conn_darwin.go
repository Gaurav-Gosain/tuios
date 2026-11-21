package transcriptview

import "golang.org/x/sys/unix"

func serverOwner(c serverSide, _ int) owner {
	uid := myUID()
	if uid < 0 {
		return ownerOther
	}
	for range 3 {
		buf, err := unix.SysctlRaw("net.inet.tcp.pcblist_n")
		if err == nil {
			return pcblistOwner(buf, c, uint32(uid))
		}
	}
	return ownerOther
}
