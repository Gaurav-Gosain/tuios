package vt_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func readReply(t *testing.T, term vt.Terminal) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, _ := term.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(300 * time.Millisecond):
		return ""
	}
}

// TestSixelAdvertisedFollowsHost checks DA1 lists sixel, and XTSMGRAPHICS
// answers, only while the pane is told its images will be shown.
func TestSixelAdvertisedFollowsHost(t *testing.T) {
	for _, on := range []bool{false, true} {
		term := vt.New(80, 24)
		term.SetCellSize(10, 20)
		term.SetSixelAdvertised(func() bool { return on })
		if _, err := term.Write([]byte("\x1b[c")); err != nil {
			t.Fatal(err)
		}
		da1 := readReply(t, term)
		if has := strings.Contains(da1, ";4;"); has != on {
			t.Errorf("advertised=%v: DA1 %q lists sixel=%v", on, da1, has)
		}
		if _, err := term.Write([]byte("\x1b[?1;1;0S")); err != nil {
			t.Fatal(err)
		}
		colors := readReply(t, term)
		if _, err := term.Write([]byte("\x1b[?2;1;0S")); err != nil {
			t.Fatal(err)
		}
		geo := readReply(t, term)
		wantColors, wantGeo := "\x1b[?1;3;0S", "\x1b[?2;3;0S"
		if on {
			wantColors, wantGeo = "\x1b[?1;0;256S", "\x1b[?2;0;800;480S"
		}
		if colors != wantColors || geo != wantGeo {
			t.Errorf("advertised=%v: XTSMGRAPHICS answered %q and %q, want %q and %q", on, colors, geo, wantColors, wantGeo)
		}
		_ = term.Close()
	}
}
