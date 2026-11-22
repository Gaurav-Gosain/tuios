package main

import (
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
)

// forwardPeerIsUs reports whether the process at the other end of c runs as
// this user. Loopback TCP is open to every user of the machine, so a forward
// closes a connection from anyone else. The peer's socket is found in
// /proc/net/tcp or tcp6 by its address, which is c's remote address.
func forwardPeerIsUs(c net.Conn) bool {
	ra, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	uid := strconv.Itoa(os.Getuid())
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if owner, found := tcpSocketOwner(table, ra); found {
			return owner == uid
		}
	}
	return false
}

// tcpSocketOwner finds the socket whose local address is addr in a
// /proc/net/tcp table and returns its uid.
func tcpSocketOwner(table string, addr *net.TCPAddr) (string, bool) {
	data, err := os.ReadFile(table)
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] { // after the header
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		ip, port, ok := parseProcAddr(fields[1])
		if ok && port == addr.Port && ip.Equal(addr.IP) {
			return fields[7], true
		}
	}
	return "", false
}

// parseProcAddr reads an address of /proc/net/tcp: the IP in hex as 32-bit
// words in host order (little endian here), a colon, the port in hex.
func parseProcAddr(s string) (net.IP, int, bool) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, false
	}
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return nil, 0, false
	}
	for i := 0; i+4 <= len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	port, err := strconv.ParseInt(p, 16, 32)
	if err != nil {
		return nil, 0, false
	}
	return net.IP(b), int(port), true
}
