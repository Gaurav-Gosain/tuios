// Command placeholders stands in for an application that shows an image
// through kitty Unicode placeholders, as ntcharts/picture does: one a=T,U=1
// per frame under one image id, and a grid of U+10EEEE cells printed once
// where the picture goes.
//
// Usage: placeholders TRANSPORT FPS [split] [early]
//
// TRANSPORT is b64 (t=d), file (t=f) or shm (t=s). "split" writes some cells
// as two writes, the base and then its marks, with a pause between. "early"
// prints the grid before the first frame is transmitted.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/x/ansi/kitty"
)

const (
	imageID = 7
	header  = "PLACEHOLDER-HEADER"
	footer  = "PLACEHOLDER-FOOTER"
)

type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

func size() (cols, rows, xpx, ypx int, err error) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0, 0, 0, errno
	}
	return int(ws.cols), int(ws.rows), int(ws.xpixel), int(ws.ypixel), nil
}

func main() {
	transport := "b64"
	if len(os.Args) > 1 && os.Args[1] != "" {
		transport = os.Args[1]
	}
	fps := 10
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil && n > 0 {
			fps = n
		}
	}
	split, early := false, false
	for _, arg := range os.Args[3:] {
		switch arg {
		case "split":
			split = true
		case "early":
			early = true
		}
	}

	cols, rows, xpx, ypx, err := size()
	if err != nil || xpx == 0 || ypx == 0 || rows < 4 {
		fmt.Printf("PLACEHOLDERS-ERR size %dx%d %dx%dpx %v\n", cols, rows, xpx, ypx, err)
		return
	}
	// The picture fills the rows between the header and the footer.
	imgCols, imgRows := cols, rows-2
	pixW, pixH := imgCols*(xpx/cols), imgRows*(ypx/rows)
	pix := make([]byte, pixW*pixH*4)

	var path, encoded string
	switch transport {
	case "file", "shm":
		dir := ""
		if transport == "shm" {
			dir = "/dev/shm"
		}
		// The harness names shared memory objects with a prefix of its own,
		// so it can remove them after a test even when this process dies by a
		// signal and the deferred removal never runs.
		prefix := "tuios-placeholders-"
		if p := os.Getenv("TUIOS_E2E_SHM_PREFIX"); p != "" && transport == "shm" {
			prefix = p
		}
		f, err := os.CreateTemp(dir, fmt.Sprintf("%s%d-*", prefix, os.Getpid()))
		if err != nil {
			fmt.Printf("PLACEHOLDERS-ERR %v\n", err)
			return
		}
		path = f.Name()
		_ = f.Close()
		defer func() { _ = os.Remove(path) }()
		name := path
		if transport == "shm" {
			name = strings.TrimPrefix(path, "/dev/shm/")
		}
		encoded = base64.StdEncoding.EncodeToString([]byte(name))
	}

	_, _ = os.Stdout.WriteString("\x1b[?1049h\x1b[H\x1b[2J")
	defer func() { _, _ = os.Stdout.WriteString("\x1b[?1049l") }()
	fmt.Printf("\x1b[1;1H%s\x1b[%d;1H%s", header, rows, footer)

	grid := func() {
		fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", imageID>>16&0xff, imageID>>8&0xff, imageID&0xff)
		for y := range imgRows {
			var row strings.Builder
			fmt.Fprintf(&row, "\x1b[%d;1H%s", y+2, fg)
			for x := range imgCols {
				cell := string(kitty.Placeholder) + string(kitty.Diacritic(y)) + string(kitty.Diacritic(x))
				if split && x%7 == 3 {
					// The base alone, a pause, then its marks.
					_, _ = os.Stdout.WriteString(row.String())
					row.Reset()
					_, _ = os.Stdout.WriteString(string(kitty.Placeholder))
					time.Sleep(15 * time.Millisecond)
					_, _ = os.Stdout.WriteString(cell[len(string(kitty.Placeholder)):])
					continue
				}
				row.WriteString(cell)
			}
			row.WriteString("\x1b[m")
			_, _ = os.Stdout.WriteString(row.String())
		}
	}
	if early {
		grid()
	}

	seq := 0
	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()
	for range tick.C {
		seq++
		for i := 0; i < len(pix); i += 4099 {
			pix[i] = byte(seq)
		}
		// The cursor sits on the footer, as after printing a last line. A
		// virtual placement is not drawn there.
		fmt.Printf("\x1b[%d;1H", rows)
		switch transport {
		case "b64":
			_, _ = os.Stdout.Write(b64Frame(pix, pixW, pixH, imgCols, imgRows))
		default:
			if err := os.WriteFile(path, pix, 0o600); err != nil {
				return
			}
			medium := "f"
			if transport == "shm" {
				medium = "s"
			}
			fmt.Printf("\x1b_Ga=T,U=1,t=%s,f=32,s=%d,v=%d,i=%d,c=%d,r=%d,q=2;%s\x1b\\",
				medium, pixW, pixH, imageID, imgCols, imgRows, encoded)
		}
		if seq == 1 && !early {
			grid()
		}
	}
}

// b64Frame is the frame as a direct transmission in 4096-byte chunks.
func b64Frame(pix []byte, w, h, cols, rows int) []byte {
	enc := base64.StdEncoding.EncodeToString(pix)
	var b []byte
	first := true
	for len(enc) > 0 {
		chunk := enc
		if len(chunk) > 4096 {
			chunk = chunk[:4096]
		}
		enc = enc[len(chunk):]
		m := 0
		if len(enc) > 0 {
			m = 1
		}
		if first {
			b = append(b, fmt.Sprintf("\x1b_Ga=T,U=1,t=d,f=32,s=%d,v=%d,i=%d,c=%d,r=%d,q=2,m=%d;",
				w, h, imageID, cols, rows, m)...)
			first = false
		} else {
			b = append(b, fmt.Sprintf("\x1b_Gm=%d;", m)...)
		}
		b = append(b, chunk...)
		b = append(b, "\x1b\\"...)
	}
	return b
}
