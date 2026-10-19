package transcriptview

// The socket table parsers decide whether the opencode password goes out on
// a connection, so they are a security boundary. Ways they can fail, which
// the cases below cover:
//   - call a connection this user's when another user's socket is at its far
//     end (the uid, SID or pid differs: must refuse);
//   - take the client side of the connection, which is always this user's,
//     for the server side;
//   - read past the end of a short or lying buffer, or loop on a record of
//     length 0;
//   - call a table it does not understand this user's instead of refusing.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The connection in every fixture: the service listens on 127.0.0.1:49374,
// and the client's side is 127.0.0.1:51234. pcblist_n/gen.c writes the same.
var fixtureConn = serverSide{port: 49374, peer: 51234}

const fixtureUID = 501

func TestPcblistOwner(t *testing.T) {
	read := func(t *testing.T, name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", "pcblist_n", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	cases := []struct {
		file string
		conn serverSide
		uid  uint32
		want owner
	}{
		{"mine.bin", fixtureConn, fixtureUID, ownerMine},
		{"old.bin", fixtureConn, fixtureUID, ownerMine},
		// Negative controls: the same tables, read as another user.
		{"mine.bin", fixtureConn, fixtureUID + 1, ownerOther},
		{"old.bin", fixtureConn, fixtureUID + 1, ownerOther},
		{"other.bin", fixtureConn, fixtureUID, ownerOther},
		// The client side matches the reversed connection, and must not be
		// taken for the server side.
		{"absent.bin", fixtureConn, fixtureUID, ownerNone},
		{"timewait.bin", fixtureConn, fixtureUID, ownerNone},
		{"mine.bin", serverSide{port: 49374, peer: 51235}, fixtureUID, ownerNone},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s %d %d", c.file, c.conn.peer, c.uid), func(t *testing.T) {
			if got := pcblistOwner(read(t, c.file), c.conn, c.uid); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}

	t.Run("a broken table refuses", func(t *testing.T) {
		mine := read(t, "mine.bin")
		le := binary.LittleEndian
		edit := func(f func(b []byte) []byte) []byte { return f(bytes.Clone(mine)) }
		// The first group starts after the 24-byte xinpgen; its socket
		// record after the 104-byte xinpcb_n.
		const group, sock = 24, 24 + 104
		broken := map[string][]byte{
			"empty":              nil,
			"no trailing header": mine[:len(mine)-24],
			"cut in a record":    append(bytes.Clone(mine[:len(mine)-24-40]), mine[len(mine)-24:]...),
			"a record of length 0": edit(func(b []byte) []byte {
				le.PutUint32(b[group:], 0)
				return b
			}),
			"a record longer than the table": edit(func(b []byte) []byte {
				le.PutUint32(b[group:], 1<<20)
				return b
			}),
			"a short xinpcb_n": edit(func(b []byte) []byte {
				le.PutUint32(b[group:], 40)
				return b
			}),
			"a short xsocket_n": edit(func(b []byte) []byte {
				le.PutUint32(b[sock:], 40)
				return b
			}),
			"a socket before its pcb": edit(func(b []byte) []byte {
				le.PutUint32(b[group+4:], xsoSocket)
				return b
			}),
		}
		for name, buf := range broken {
			for _, uid := range []uint32{fixtureUID, fixtureUID + 1} {
				if got := pcblistOwner(buf, fixtureConn, uid); got != ownerOther {
					t.Errorf("%s, uid %d: got %v, want ownerOther", name, uid, got)
				}
			}
		}
	})
}

// procNetRow is one row of /proc/net/tcp, as the kernel prints it.
func procNetRow(local, remote string, state string, uid int) string {
	return fmt.Sprintf("   0: %s %s %s 00000000:00000000 00:00000000 00000000 %5d        0 1 1 0000000000000000 100 0 0 10 0\n", local, remote, state, uid)
}

const procNetHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func TestProcNetOwner(t *testing.T) {
	// 49374 is C0DE and 51234 is C822.
	listener := procNetRow("0100007F:C0DE", "00000000:0000", "0A", 1000)
	client := procNetRow("0100007F:C822", "0100007F:C0DE", "01", 1000)
	server := procNetRow("0100007F:C0DE", "0100007F:C822", "01", 1000)
	theirs := procNetRow("0100007F:C0DE", "0100007F:C822", "01", 1001)
	synRecv := procNetRow("0100007F:C0DE", "0100007F:C822", "03", 1000)
	bigEndian := procNetRow("7F000001:C0DE", "7F000001:C822", "01", 1000)
	cases := []struct {
		name  string
		table string
		uid   int
		want  owner
	}{
		{"this user's", listener + client + server, 1000, ownerMine},
		{"not finished", listener + client + synRecv, 1000, ownerMine},
		{"big-endian", bigEndian, 1000, ownerMine},
		{"another user's", listener + client + theirs, 1000, ownerOther},
		{"read as another user", listener + client + server, 1001, ownerOther},
		{"two rows, one another user's", server + theirs, 1000, ownerOther},
		{"only the client side", listener + client, 1000, ownerNone},
		{"empty", "", 1000, ownerNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := procNetOwner(bytes.NewReader([]byte(procNetHeader+c.table)), fixtureConn, c.uid); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// tcpTable is a MIB_TCPTABLE_OWNER_PID, as GetExtendedTcpTable writes it:
// a DWORD count, then six DWORDs a row. An address is in network order; a
// port is in network order in the low two bytes of its DWORD.
func tcpTable(rows ...[6]uint32) []byte {
	buf := binary.LittleEndian.AppendUint32(nil, uint32(len(rows)))
	for _, r := range rows {
		for i, v := range r {
			switch i {
			case 1, 3: // address
				buf = binary.BigEndian.AppendUint32(buf, v)
			case 2, 4: // port, with junk in the bytes the docs leave undefined
				buf = binary.BigEndian.AppendUint16(buf, uint16(v))
				buf = append(buf, 0xAB, 0xCD)
			default:
				buf = binary.LittleEndian.AppendUint32(buf, v)
			}
		}
	}
	return buf
}

func TestTCPTableOwner(t *testing.T) {
	const lo, listen, synRcvd, estab = 0x7f000001, 2, 4, 5
	listener := [6]uint32{listen, lo, 49374, 0, 0, 4242}
	client := [6]uint32{estab, lo, 51234, lo, 49374, 7}
	server := [6]uint32{estab, lo, 49374, lo, 51234, 4242}
	cases := []struct {
		name    string
		table   []byte
		wantPID uint32
		want    owner
	}{
		{"the server side", tcpTable(listener, client, server), 4242, ownerMine},
		{"not accepted yet", tcpTable(listener, client, [6]uint32{synRcvd, lo, 49374, lo, 51234, 4242}), 4242, ownerMine},
		{"two owners", tcpTable(server, [6]uint32{estab, lo, 49374, lo, 51234, 99}), 0, ownerOther},
		{"only the client side", tcpTable(listener, client), 0, ownerNone},
		{"empty", tcpTable(), 0, ownerNone},
		{"a count past the end", append(binary.LittleEndian.AppendUint32(nil, 3), tcpTable(server)[4:]...), 0, ownerOther},
		{"short", []byte{1, 0}, 0, ownerOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pid, got := tcpTableOwner(c.table, fixtureConn)
			if got != c.want || (got == ownerMine && pid != c.wantPID) {
				t.Fatalf("got pid %d, %v; want pid %d, %v", pid, got, c.wantPID, c.want)
			}
		})
	}
}

func FuzzPcblistOwner(f *testing.F) {
	for _, name := range []string{"mine.bin", "other.bin", "absent.bin", "timewait.bin", "old.bin"} {
		data, err := os.ReadFile(filepath.Join("testdata", "pcblist_n", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data, uint16(fixtureConn.peer))
	}
	f.Fuzz(func(t *testing.T, buf []byte, peer uint16) {
		c := serverSide{port: fixtureConn.port, peer: int(peer)}
		got := pcblistOwner(buf, c, fixtureUID)
		if got == ownerMine && pcblistOwner(buf, c, fixtureUID+1) != ownerOther {
			t.Fatal("a table is this user's under two uids")
		}
	})
}

func FuzzTCPTableOwner(f *testing.F) {
	f.Add(tcpTable([6]uint32{5, 0x7f000001, 49374, 0x7f000001, 51234, 4242}), uint16(51234))
	f.Add(tcpTable(), uint16(1))
	f.Fuzz(func(t *testing.T, buf []byte, peer uint16) {
		_, _ = tcpTableOwner(buf, serverSide{port: fixtureConn.port, peer: int(peer)})
	})
}

func FuzzProcNetOwner(f *testing.F) {
	f.Add([]byte(procNetHeader+procNetRow("0100007F:C0DE", "0100007F:C822", "01", 1000)), 1000)
	f.Fuzz(func(t *testing.T, table []byte, uid int) {
		got := procNetOwner(bytes.NewReader(table), fixtureConn, uid)
		if got == ownerMine && procNetOwner(bytes.NewReader(table), fixtureConn, uid+1) != ownerOther {
			t.Fatal("a table is this user's under two uids")
		}
	})
}
