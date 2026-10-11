package session

import (
	"slices"
	"testing"
)

// The /proc/net/tcp reader is the one piece of list-ports a wrong answer
// cannot be told from a right one end to end: a byte-order slip gives a real
// looking address, and a missed state lists a connection as a server. The
// ways it can fail, written down first:
//   - the address words read in the wrong byte order (127.0.0.1 as 1.0.0.127);
//   - the port read little-endian (8080 as 36895);
//   - an established or time-wait socket listed as listening;
//   - the header line, a short line or a zero inode taken as a socket;
//   - a v4-mapped v6 address shown as ::ffff:a.b.c.d.
func TestParseProcNetTCP(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 41234 1 0000000000000000 100 0 0 10 0
   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1111 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F90 0100007F:D2F0 01 00000000:00000000 00:00000000 00000000  1000        0 41299 1 0000000000000000 20 4 30 10 -1
   3: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 0 1 0000000000000000 100 0 0 10 0
   4: 0100007F:1F92 00000000:0000
`
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:1F40 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5555 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6666 1 0000000000000000 100 0 0 10 0
   2: 0000000000000000FFFF00000100007F:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 7777 1 0000000000000000 100 0 0 10 0
`
	got := append(parseProcNetTCP(tcp), parseProcNetTCP(tcp6)...)
	want := []procNetListen{
		{addr: "127.0.0.1", port: 8080, inode: 41234},
		{addr: "0.0.0.0", port: 22, inode: 1111},
		{addr: "::1", port: 8000, inode: 5555},
		{addr: "::", port: 80, inode: 6666},
		{addr: "127.0.0.1", port: 3000, inode: 7777},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parsed\n%+v\nwant\n%+v", got, want)
	}
}

// Smart case is the default of search-panes and command-history. It fails
// when a capital anywhere, not only the first letter, does not make the
// search exact, or when an explicit mode is overridden by the query.
func TestSmartCase(t *testing.T) {
	for _, c := range []struct {
		mode, query string
		insensitive bool
	}{
		{"smart", "error", true},
		{"", "error", true},
		{"smart", "Error", false},
		{"smart", "erRor", false},
		{"smart", "ÉCOLE", false},
		{"smart", "école 42", true},
		{"sensitive", "error", false},
		{"insensitive", "Error", true},
	} {
		if got := smartCaseInsensitive(c.mode, c.query); got != c.insensitive {
			t.Errorf("smartCaseInsensitive(%q, %q) = %v, want %v", c.mode, c.query, got, c.insensitive)
		}
	}
}
