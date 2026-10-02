// Command altonce stands in for a graphics app that takes the alternate screen,
// draws one frame and then waits, the way a compositor does when nothing on
// its screen changes: it sends a frame on damage, not on a timer.
//
// Usage: altonce [DELAY]
//
// The frame is a 64x64 a=T under image id 3, sent directly (t=d) at row 2,
// column 3 of the pane.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	const side = 64
	pix := make([]byte, side*side*4)
	for i := range pix {
		pix[i] = byte(i * 11)
	}
	// A delay first, so the pane exists and has settled before the app takes
	// the alternate screen, as it does for an app that starts slowly.
	if len(os.Args) > 1 {
		if d, err := time.ParseDuration(os.Args[1]); err == nil {
			time.Sleep(d)
		}
	}
	_, _ = os.Stdout.WriteString("\x1b[?1049h\x1b[2;3H")
	_, _ = fmt.Fprintf(os.Stdout, "\x1b_Ga=T,t=d,f=32,s=%d,v=%d,i=3,q=2,C=1;%s\x1b\\",
		side, side, base64.StdEncoding.EncodeToString(pix))
	_, _ = os.Stdout.WriteString("\x1b[10;1HALTONCE-DRAWN")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case <-stop:
	case <-time.After(120 * time.Second):
	}
	_, _ = os.Stdout.WriteString("\x1b[?1049l")
}
