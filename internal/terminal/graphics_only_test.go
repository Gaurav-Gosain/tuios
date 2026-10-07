package terminal

import "testing"

// TestGraphicsOnlyRefusesAChunkOfAnEarlierCommand covers a chunked a=T without
// C=1 whose chunks arrive in separate writes. Its last chunk carries no action,
// only m=0, and it is that chunk that places the image and moves the cursor. A
// write that is only that chunk must take the usual path.
func TestGraphicsOnlyRefusesAChunkOfAnEarlierCommand(t *testing.T) {
	first := []byte("\x1b_Ga=T,f=32,s=1,v=1,i=7,q=2,m=1;/wAA\x1b\\")
	last := []byte("\x1b_Gm=0;/w==\x1b\\")
	if graphicsOnly(true, last) {
		t.Errorf("graphicsOnly accepted the last chunk of a command it did not see begin: %q", last)
	}
	// The same command whole, in one write, places the image and moves the
	// cursor, so it is refused as well.
	if graphicsOnly(true, append(append([]byte(nil), first...), last...)) {
		t.Error("graphicsOnly accepted a chunked a=T without C=1")
	}

	// The positive half: with C=1, the whole command in one write is image
	// data alone.
	keep := []byte("\x1b_Ga=T,f=32,s=1,v=1,i=7,q=2,C=1,m=1;/wAA\x1b\\\x1b_Gm=0;/w==\x1b\\")
	if !graphicsOnly(true, keep) {
		t.Errorf("graphicsOnly refused a chunked a=T with C=1 in one write: %q", keep)
	}

}
