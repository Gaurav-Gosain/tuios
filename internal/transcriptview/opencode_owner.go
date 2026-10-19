package transcriptview

import (
	"bufio"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
)

// The opencode reader sends the service's password only over a connection
// whose server side is shown to belong to this user. The kernel's socket
// table is read after the connect and before the first byte goes out, so
// there is no gap between the check and the use: whatever socket the table
// shows at the far end of this connection is the one the request reaches.
// The server side of a TCP connection takes its owner from the listening
// socket, also while it still waits in the accept queue.
//
// This file holds the parsers of the three tables, which have no build tag
// so that every one of them is tested and fuzzed on every machine. The
// system calls that fill them are in opencode_conn_*.go.

// serverSide names the server side of one loopback connection: local
// 127.0.0.1:port, remote 127.0.0.1:peer.
type serverSide struct{ port, peer int }

// owner is what a socket table says about one connection.
type owner int

const (
	// ownerNone: the table has no row for the connection (yet).
	ownerNone owner = iota
	// ownerMine: every row for it belongs to this user, and there is one.
	ownerMine
	// ownerOther: a row for it belongs to someone else, or cannot be read.
	ownerOther
)

// procNetOwner reads a Linux /proc/net/tcp table. The table writes the
// address as the hex of the address word in host order, so 127.0.0.1 is
// 0100007F on a little-endian machine and 7F000001 on a big-endian one. The
// port is %04X. The state is 01 (ESTABLISHED) for a connection, or 03
// (SYN_RECV) for one the listener has not finished; both carry the
// listener's uid.
func procNetOwner(r io.Reader, c serverSide, uid int) owner {
	if !c.valid() {
		return ownerOther
	}
	local := [2]string{"0100007F:" + hexPort(c.port), "7F000001:" + hexPort(c.port)}
	remote := [2]string{"0100007F:" + hexPort(c.peer), "7F000001:" + hexPort(c.peer)}
	want := strconv.Itoa(uid)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 512), 4096)
	got := ownerNone
	for sc.Scan() {
		// sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid ...
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 || (fields[3] != "01" && fields[3] != "03") {
			continue
		}
		if (fields[1] != local[0] && fields[1] != local[1]) || (fields[2] != remote[0] && fields[2] != remote[1]) {
			continue
		}
		if fields[7] != want {
			return ownerOther
		}
		got = ownerMine
	}
	if sc.Err() != nil {
		return ownerOther
	}
	return got
}

func hexPort(p int) string {
	s := strings.ToUpper(strconv.FormatInt(int64(p), 16))
	return strings.Repeat("0", max(0, 4-len(s))) + s
}

func (c serverSide) valid() bool {
	return c.port > 0 && c.port <= 65535 && c.peer > 0 && c.peer <= 65535
}

// The records of the macOS sysctl net.inet.tcp.pcblist_n, from XNU's
// bsd/netinet/in_pcblist.c (get_pcblist_n) and the structs in
// bsd/netinet/in_pcb.h and bsd/sys/socketvar.h, all under #pragma pack(4).
// The buffer is a struct xinpgen, then for each socket a group of records,
// then a struct xinpgen again. Each record starts with its length and kind,
// and the next one starts at the length rounded up to 8. A group is an
// XSO_INPCB record, then XSO_SOCKET, two XSO_RCVBUF/XSO_SNDBUF, XSO_STATS,
// and for TCP XSO_TCPCB. The offsets below are the same from XNU 6153
// (macOS 10.15) to the current source; xsocket_n has grown at its end only.
const (
	xinpgenLen = 24

	xsoSocket = 0x001
	xsoInpcb  = 0x010

	// struct xinpcb_n
	xiFport  = 16 // u_short, network order
	xiLport  = 18 // u_short, network order
	xiVflag  = 44 // u_char; INP_IPV4 is 0x1
	xiFaddr4 = 60 // inp_dependfaddr.inp46_foreign.ia46_addr4
	xiLaddr4 = 76 // inp_dependladdr.inp46_local.ia46_addr4
	xiMinLen = 80

	// struct xsocket_n
	xsoSo       = 8  // u_int64_t; 0 when the pcb has no socket
	xsoProtocol = 36 // int
	xsoFamily   = 40 // int
	xsoUID      = 64 // uid_t
	xsoMinLen   = 68

	inpIPv4   = 0x1
	afInet    = 2
	protoTCP  = 6
	recordMax = 1 << 16
)

// pcblistOwner reads a net.inet.tcp.pcblist_n buffer. The numbers in it are
// in host order, which is little-endian on every Mac Go builds for. Anything
// in the buffer it does not expect makes the answer ownerOther: a password
// is never sent on a table this reader is unsure of.
func pcblistOwner(buf []byte, c serverSide, uid uint32) owner {
	if !c.valid() || len(buf) < 2*xinpgenLen {
		return ownerOther
	}
	le := binary.LittleEndian
	if le.Uint32(buf) != xinpgenLen || le.Uint32(buf[len(buf)-xinpgenLen:]) != xinpgenLen {
		return ownerOther
	}
	body := buf[xinpgenLen : len(buf)-xinpgenLen]
	got := ownerNone
	// match is the state of the group the walk is in: whether its pcb is
	// the server side of c, and whether its socket record was seen.
	inGroup, match, sawSocket := false, false, false
	finish := func() bool {
		// A pcb of c with no socket record is a table this reader does not
		// understand.
		return !(inGroup && match && !sawSocket)
	}
	for len(body) > 0 {
		if len(body) < 8 {
			return ownerOther
		}
		n, kind := le.Uint32(body), le.Uint32(body[4:])
		if n < 8 || n > recordMax || int(n) > len(body) {
			return ownerOther
		}
		rec := body[:n]
		step := (int(n) + 7) &^ 7
		if step > len(body) {
			// The last record of the walk may end without its padding only
			// if nothing follows, and the trailing xinpgen always follows.
			return ownerOther
		}
		body = body[step:]
		switch kind {
		case xsoInpcb:
			if !finish() {
				return ownerOther
			}
			if n < xiMinLen {
				return ownerOther
			}
			inGroup, sawSocket = true, false
			match = rec[xiVflag]&inpIPv4 != 0 &&
				int(be16(rec[xiLport:])) == c.port && int(be16(rec[xiFport:])) == c.peer &&
				isLoopback4(rec[xiLaddr4:]) && isLoopback4(rec[xiFaddr4:])
		case xsoSocket:
			if !inGroup || sawSocket || n < xsoMinLen {
				return ownerOther
			}
			sawSocket = true
			if !match {
				continue
			}
			if le.Uint64(rec[xsoSo:]) == 0 {
				// A pcb with no socket, such as one in TIME_WAIT, holds no
				// listener's uid and cannot be the far end of a live
				// connection.
				continue
			}
			if le.Uint32(rec[xsoProtocol:]) != protoTCP || le.Uint32(rec[xsoFamily:]) != afInet {
				return ownerOther
			}
			if le.Uint32(rec[xsoUID:]) != uid {
				return ownerOther
			}
			got = ownerMine
		default:
			// Socket buffers, statistics and the TCP control block carry
			// nothing this check needs.
			if !inGroup {
				return ownerOther
			}
		}
	}
	if !finish() {
		return ownerOther
	}
	return got
}

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }

func isLoopback4(b []byte) bool { return b[0] == 127 && b[1] == 0 && b[2] == 0 && b[3] == 1 }

// The Windows MIB_TCPTABLE_OWNER_PID that GetExtendedTcpTable fills for
// TCP_TABLE_OWNER_PID_ALL: a DWORD count, then that many
// MIB_TCPROW_OWNER_PID rows of six DWORDs each: dwState, dwLocalAddr,
// dwLocalPort, dwRemoteAddr, dwRemotePort, dwOwningPid. An address is in
// network order. A port is in network order in the first two bytes of its
// DWORD, and the documentation leaves the other two undefined.
const (
	tcpRowLen      = 24
	tcpStateSynRcv = 4
	tcpStateEstab  = 5
)

// tcpTableOwner reads a MIB_TCPTABLE_OWNER_PID buffer and gives the pid that
// owns the server side of c. Two rows with different pids, or a table it
// cannot read, give false.
func tcpTableOwner(buf []byte, c serverSide) (uint32, owner) {
	if !c.valid() || len(buf) < 4 {
		return 0, ownerOther
	}
	le := binary.LittleEndian
	count := le.Uint32(buf)
	if uint64(count)*tcpRowLen > uint64(len(buf)-4) {
		return 0, ownerOther
	}
	var pid uint32
	got := ownerNone
	for i := range int(count) {
		row := buf[4+i*tcpRowLen : 4+(i+1)*tcpRowLen]
		state := le.Uint32(row)
		if state != tcpStateEstab && state != tcpStateSynRcv {
			continue
		}
		if !isLoopback4(row[4:]) || int(be16(row[8:])) != c.port ||
			!isLoopback4(row[12:]) || int(be16(row[16:])) != c.peer {
			continue
		}
		p := le.Uint32(row[20:])
		if got == ownerMine && p != pid {
			return 0, ownerOther
		}
		pid, got = p, ownerMine
	}
	return pid, got
}
