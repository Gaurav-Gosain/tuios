package terminal

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// FuzzGraphicsOnlyChangesNoCell holds graphicsOnly to its promise: a write it
// accepts changes no cell of the screen. A write it wrongly accepts would leave
// the pane showing what it showed before, because no frame is composed for it.
func FuzzGraphicsOnlyChangesNoCell(f *testing.F) {
	for _, seed := range []string{
		"\x1b[H\x1b_Ga=T,f=32,t=s,s=8,v=8,i=1,q=2,C=1;bmFtZQ==\x1b\\",
		"\x1b_Ga=f,r=1,i=1,x=0,y=0,s=64,v=64,f=32,q=2,t=s;bmFtZQ==\x1b\\",
		"\x1b7\x1b[3;4H\x1b_Ga=p,i=1,p=1,C=1,q=2\x1b\\\x1b8",
		"\x1b_Ga=T,f=32,s=1,v=1,i=7,q=2;/wAA/w==\x1b\\",
		"\x1b_Gm=1;AAAA\x1b\\",
		"\x1b[5;5f\x1b_Ga=d,d=I,i=1,q=2\x1b\\",
		"\x1b_Ga=T,U=1,f=32,s=1,v=1,i=7,q=2;/wAA/w==\x1b\\",
		"\x1b[2J\x1b_Ga=d\x1b\\",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if !graphicsOnly(true, b) {
			return
		}
		e := vt.NewEmulator(20, 6)
		_, _ = e.Write([]byte("abc\r\ndef\r\n\x1b[1;31mghi\x1b[m"))
		before := e.Render()
		_, _ = e.Write(b)
		if after := e.Render(); after != before {
			t.Fatalf("graphicsOnly accepted %q, and it changed the screen\nbefore:\n%s\nafter:\n%s", b, before, after)
		}
	})
}

// FuzzGraphicsOnlyAfterAWrite is FuzzGraphicsOnlyChangesNoCell with another
// write before the one under test, which is how the chunks of one kitty
// command arrive when they are read apart.
func FuzzGraphicsOnlyAfterAWrite(f *testing.F) {
	f.Add([]byte("\x1b_Ga=T,f=32,s=1,v=1,i=7,q=2,m=1;/wAA\x1b\\"), []byte("\x1b_Gm=0;/w==\x1b\\"))
	f.Add([]byte("\x1b_Ga=t,f=32,s=1,v=1,i=7,q=2,m=1;/wAA\x1b\\"), []byte("\x1b_Gm=0;/w==\x1b\\"))
	f.Add([]byte("\x1b_Ga=T,f=32,s=1,v=1,i=7,q=2,m=1;/w"), []byte("AA\x1b\\"))
	f.Fuzz(func(t *testing.T, before, b []byte) {
		e := vt.NewEmulator(20, 6)
		_, _ = e.Write([]byte("abc\r\ndef\r\n\x1b[1;31mghi\x1b[m"))
		_, _ = e.Write(before)
		if !graphicsOnly(e.AtGround(), b) {
			return
		}
		screen := e.Render()
		_, _ = e.Write(b)
		if after := e.Render(); after != screen {
			t.Fatalf("graphicsOnly accepted %q after %q, and it changed the screen\nbefore:\n%s\nafter:\n%s", b, before, screen, after)
		}
	})
}
