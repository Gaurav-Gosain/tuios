package transcriptview

import (
	"bufio"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
)

type serverSide struct{ port, peer int }

type owner int

const (
	ownerNone owner = iota
	ownerMine
	ownerOther
)

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

const (
	xinpgenLen = 24

	xsoSocket = 0x001
	xsoInpcb  = 0x010

	xiFport  = 16
	xiLport  = 18
	xiVflag  = 44
	xiFaddr4 = 60
	xiLaddr4 = 76
	xiMinLen = 80

	xsoSo       = 8
	xsoProtocol = 36
	xsoFamily   = 40
	xsoUID      = 64
	xsoMinLen   = 68

	inpIPv4   = 0x1
	afInet    = 2
	protoTCP  = 6
	recordMax = 1 << 16
)

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
	inGroup, match, sawSocket := false, false, false
	finish := func() bool {
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

const (
	tcpRowLen      = 24
	tcpStateSynRcv = 4
	tcpStateEstab  = 5
)

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
