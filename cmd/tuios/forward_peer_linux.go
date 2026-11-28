package main

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
)

// forwardPeerIsUs reports whether the process at the other end of c runs as
// this user. Loopback TCP is open to every user of the machine, so a forward
// closes a connection from anyone else.
//
// The peer's socket is the row of /proc/net/tcp or tcp6 whose local address
// is c's remote address and whose remote address is c's local address, in
// a state of a live connection. Only the whole 4-tuple names one socket: two
// sockets can share a local port when their destinations differ, and a
// TIME_WAIT row holds the port with uid 0. A missing or ambiguous row
// refuses.
func forwardPeerIsUs(c net.Conn) bool {
	peer, ok1 := c.RemoteAddr().(*net.TCPAddr)
	us, ok2 := c.LocalAddr().(*net.TCPAddr)
	if !ok1 || !ok2 {
		return false
	}
	var owners []string
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		owners = append(owners, socketOwners(string(data), peer, us)...)
	}
	return len(owners) == 1 && owners[0] == strconv.Itoa(os.Getuid())
}

// liveTCPStates are the state columns of a socket that is still one end of
// a connection: ESTABLISHED, SYN_SENT, SYN_RECV, FIN_WAIT1, FIN_WAIT2,
// CLOSE_WAIT, LAST_ACK and CLOSING. A client that sent its request and shut
// its write side before the forward accepted is in FIN_WAIT. TIME_WAIT (06),
// CLOSE (07) and LISTEN (0A) are not.
var liveTCPStates = map[string]bool{"01": true, "02": true, "03": true, "04": true, "05": true, "08": true, "09": true, "0B": true}

// socketOwners returns the uid of every live row of a /proc/net/tcp table
// whose local address is local and whose remote address is remote.
func socketOwners(table string, local, remote *net.TCPAddr) []string {
	var out []string
	lines := strings.Split(table, "\n")
	for _, line := range lines[1:] { // after the header
		f := strings.Fields(line)
		if len(f) < 8 || !liveTCPStates[f[3]] {
			continue
		}
		if procAddrIs(f[1], local) && procAddrIs(f[2], remote) {
			out = append(out, f[7])
		}
	}
	return out
}

func procAddrIs(s string, a *net.TCPAddr) bool {
	ip, port, ok := parseProcAddr(s)
	return ok && port == a.Port && ip.Equal(a.IP)
}

// parseProcAddr reads an address of /proc/net/tcp: the IP in hex as 32-bit
// words in the kernel's byte order, a colon, the port in hex.
func parseProcAddr(s string) (net.IP, int, bool) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, false
	}
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return nil, 0, false
	}
	// The kernel prints each word of the address, as it lies in memory, as a
	// host-order uint32 in hex. Read the hex as that number and lay it back in
	// memory in host order to get the address bytes. NativeEndian keeps this
	// right on big endian machines too.
	ip := make(net.IP, len(b))
	for i := 0; i+4 <= len(b); i += 4 {
		w := binary.BigEndian.Uint32(b[i:])
		var host [4]byte
		binary.NativeEndian.PutUint32(host[:], w)
		copy(ip[i:], host[:])
	}
	port, err := strconv.ParseInt(p, 16, 32)
	if err != nil {
		return nil, 0, false
	}
	return ip, int(port), true
}
