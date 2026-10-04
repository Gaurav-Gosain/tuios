package learnssh

import (
	"net"
	"testing"
)

func TestBoardName(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"Alice":                    "alice",
		"bob.smith+tag":            "bobsmithtag",
		"a_very_long_name_indeed!": "a_very_long_name",
		"!!!":                      "anonymous",
		"":                         "anonymous",
		"root":                     "anonymous",
		"sh1thead":                 "anonymous",
		"x\x1b[31mred":             "x31mred",
		"日本":                       "anonymous",
	} {
		if got := s.boardName(in); got != want {
			t.Errorf("boardName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBoardRanksAndHidesNames(t *testing.T) {
	s, _ := openStore(t.TempDir())
	if _, rank := s.record("k1", true, "ann", "four-tiles", 5000); rank != 1 {
		t.Fatalf("first time rank %d", rank)
	}
	if _, rank := s.record("k2", true, "ben", "four-tiles", 3000); rank != 1 {
		t.Fatalf("faster time rank %d", rank)
	}
	// A slower time from ann keeps her better one.
	best, rank := s.record("k1", true, "ann", "four-tiles", 9000)
	if best != 5000 || rank != 2 {
		t.Fatalf("best %d rank %d", best, rank)
	}
	// Under a second is a script.
	if _, rank := s.record("k3", true, "bot", "four-tiles", 200); rank != 0 {
		t.Fatalf("bot rank %d", rank)
	}
	b := s.publicBoard()["four-tiles"]
	if len(b) != 2 || b[0].Name != "anonymous" || b[1].Name != "anonymous" {
		t.Fatalf("names shown before opting in: %+v", b)
	}
	s.publish("k1", "four-tiles")
	b = s.publicBoard()["four-tiles"]
	if b[1].Name != "ann" || b[0].Name != "anonymous" {
		t.Fatalf("after publish: %+v", b)
	}
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	// It all comes back from disk, with the same salt.
	s2, _ := openStore(s.dir)
	if s2.owner("fp") != s.owner("fp") {
		t.Fatal("salt changed")
	}
	if got := s2.bestsFor("k1")["four-tiles"]; got != 5000 {
		t.Fatalf("best after reload %d", got)
	}
}

func TestLogIPTruncates(t *testing.T) {
	for addr, want := range map[string]string{
		"203.0.113.77:5555":          "203.0.113.0/24",
		"[2001:db8:1:2:3::9]:22":     "2001:db8:1::/48",
		"[::ffff:198.51.100.9]:1234": "198.51.100.0/24",
	} {
		a, _ := net.ResolveTCPAddr("tcp", addr)
		if got := logIP(a); got != want {
			t.Errorf("logIP(%s) = %s, want %s", addr, got, want)
		}
	}
	a, _ := net.ResolveTCPAddr("tcp", "[2001:db8:1:2:3::9]:22")
	if got := ipKey(a); got != "2001:db8:1:2::/64" {
		t.Errorf("ipKey = %s", got)
	}
}
