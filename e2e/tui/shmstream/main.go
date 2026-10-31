// Command shmstream stands in for a guest that streams frames over kitty
// shared memory the way the spec asks: a new object for every frame, never
// deleted by the guest, because deleting it is the terminal's job once it has
// read it.
//
// Usage: shmstream LOG COUNT FPS DELAYMS same|vary|big|sync|syncjoined|syncreuse|patches
//
// Each object's name is appended to LOG as it is advertised, and DONE when the
// last one is. The log is a file rather than the screen because the pane may
// not be on screen. "same" paints every frame alike, "vary" paints each one
// differently. "big" is "vary" with each object one byte longer than the frame
// the command describes, the way an object that is not the frame looks. "sync"
// is "vary" with each frame in its own synchronized update, the way
// terminal-browser sends one. "syncjoined" closes one update and opens the next
// in the same write, so an update is open whenever the terminal looks.
// "syncreuse" is "sync" with one object for every frame. "patches" sends two
// images of a size that fills no whole cell, each frame in one update, the way
// the browser sends tiles: a frame that changes in place, and a frame with the
// same pixels at a new offset and layer each time.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

const side = 64

// patchW and patchH fill no whole cell in any cell size the harness uses.
const patchW, patchH = 107, 63

func main() {
	if len(os.Args) < 6 {
		fmt.Println("usage: shmstream LOG COUNT FPS DELAYMS same|vary|big|sync|patches")
		os.Exit(2)
	}
	logPath := os.Args[1]
	count, _ := strconv.Atoi(os.Args[2])
	fps, _ := strconv.Atoi(os.Args[3])
	delay, _ := strconv.Atoi(os.Args[4])
	mode := os.Args[5]
	vary := mode != "same"
	open, closeUpdate := "", ""
	switch mode {
	case "sync", "syncreuse":
		open, closeUpdate = "\x1b[?2026h", "\x1b[?2026l"
	case "syncjoined":
		open = "\x1b[?2026l\x1b[?2026h"
	}
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
	if os.Args[5] == "patches" {
		put := func(name string, pixels []byte, id, x, y, z int) {
			if err := os.WriteFile("/dev/shm/"+name, pixels, 0o600); err != nil {
				fmt.Printf("SHMSTREAM-ERR %v\n", err)
				return
			}
			_, _ = fmt.Fprintln(logf, name)
			fmt.Printf("\x1b[3;3H\x1b_Ga=T,t=s,f=32,s=%d,v=%d,i=%d,p=1,C=1,X=%d,Y=%d,z=%d,q=2;%s\x1b\\",
				patchW, patchH, id, x, y, z, base64.StdEncoding.EncodeToString([]byte(name)))
		}
		same := make([]byte, patchW*patchH*4)
		moving := make([]byte, patchW*patchH*4)
		for n := range count {
			<-tick.C
			moving[n%len(moving)]++
			fmt.Print("\x1b[?2026h")
			put(fmt.Sprintf("%s%d-a%d", prefix, os.Getpid(), n), moving, 1, 3, 5, 2)
			put(fmt.Sprintf("%s%d-b%d", prefix, os.Getpid(), n), same, 2, 1+n%2, 2+n%2, 3+n)
			fmt.Print("\x1b[?2026l")
		}
		_, _ = fmt.Fprintln(logf, "DONE")
		return
	}
	for n := range count {
		<-tick.C
		if vary {
			pix[n%len(pix)]++
		}
		name := fmt.Sprintf("%s%d-%d", prefix, os.Getpid(), n)
		if mode == "syncreuse" {
			name = fmt.Sprintf("%s%d-one", prefix, os.Getpid())
		}
		if err := os.WriteFile("/dev/shm/"+name, pix, 0o600); err != nil {
			fmt.Printf("SHMSTREAM-ERR %v\n", err)
			return
		}
		if mode != "syncreuse" || n == 0 {
			_, _ = fmt.Fprintln(logf, name)
		}
		fmt.Printf("%s\x1b[H\x1b_Ga=T,t=s,f=32,s=%d,v=%d,i=1,q=2,C=1;%s\x1b\\%s",
			open, side, side, base64.StdEncoding.EncodeToString([]byte(name)), closeUpdate)
	}
	if mode == "syncjoined" {
		fmt.Print("\x1b[?2026l")
	}
	_, _ = fmt.Fprintln(logf, "DONE")
}
