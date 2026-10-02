// Command shmstream stands in for a guest that streams frames over kitty
// shared memory the way the spec asks: a new object for every frame, never
// deleted by the guest, because deleting it is the terminal's job once it has
// read it.
//
// Usage: shmstream LOG COUNT FPS DELAYMS same|vary|big
//
// Each object's name is appended to LOG as it is advertised, and DONE when the
// last one is. The log is a file rather than the screen because the pane may
// not be on screen. "same" paints every frame alike, "vary" paints each one
// differently. "big" is "vary" with each object one byte longer than the frame
// the command describes, the way an object that is not the frame looks.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

const side = 64

func main() {
	if len(os.Args) < 6 {
		fmt.Println("usage: shmstream LOG COUNT FPS DELAYMS same|vary")
		os.Exit(2)
	}
	logPath := os.Args[1]
	count, _ := strconv.Atoi(os.Args[2])
	fps, _ := strconv.Atoi(os.Args[3])
	delay, _ := strconv.Atoi(os.Args[4])
	vary := os.Args[5] == "vary" || os.Args[5] == "big"
	extra := 0
	if os.Args[5] == "big" {
		extra = 1
	}
	if fps <= 0 {
		fps = 20
	}
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Printf("SHMSTREAM-ERR %v\n", err)
		return
	}
	defer func() { _ = logf.Close() }()

	// The harness names the objects with a prefix of its own, so it can
	// remove them after a test even when this process dies by a signal.
	prefix := os.Getenv("TUIOS_E2E_SHM_PREFIX")
	if prefix == "" {
		prefix = "tuios-shmstream-"
	}
	time.Sleep(time.Duration(delay) * time.Millisecond)
	pix := make([]byte, side*side*4+extra)
	for i := range pix {
		pix[i] = byte(i * 7)
	}
	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()
	for n := range count {
		<-tick.C
		if vary {
			pix[n%len(pix)]++
		}
		name := fmt.Sprintf("%s%d-%d", prefix, os.Getpid(), n)
		if err := os.WriteFile("/dev/shm/"+name, pix, 0o600); err != nil {
			fmt.Printf("SHMSTREAM-ERR %v\n", err)
			return
		}
		_, _ = fmt.Fprintln(logf, name)
		fmt.Printf("\x1b[H\x1b_Ga=T,t=s,f=32,s=%d,v=%d,i=1,q=2,C=1;%s\x1b\\",
			side, side, base64.StdEncoding.EncodeToString([]byte(name)))
	}
	_, _ = fmt.Fprintln(logf, "DONE")
}
