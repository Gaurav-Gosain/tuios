// Command frameloop stands in for a graphics app that fills its pane, in the
// way that matters to the passthrough: it asks the kernel how big its terminal
// is in pixels, renders a bitmap of exactly that size, and streams it through
// shared memory at a steady rate, reusing one image id.
//
// Guessing the pixel size instead is what makes a harness test something other
// than the reported scenario. A real app renders to the size it was given, so
// the image's pixels-per-cell agrees with the host's by construction, and any
// disagreement seen downstream is tuios's own.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	"unsafe"
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
	// The geometry is reported through a file rather than the screen: the app
	// takes the alt screen immediately afterwards, so anything printed is gone
	// before a poll of the grid can see it.
	var geomFile string
	if len(os.Args) > 1 {
		geomFile = os.Args[1]
	}
	fps := 20
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil && n > 0 {
			fps = n
		}
	}
	// repaint is how long the app takes to re-render after being resized. A
	// browser is not instant: it keeps painting the frame it already has, at
	// the old pixel size, while it relays out. That interval is the one where
	// the terminal has been told one size and the bitmap is still the other.
	var repaint time.Duration
	if len(os.Args) > 3 {
		if ms, err := strconv.Atoi(os.Args[3]); err == nil && ms > 0 {
			repaint = time.Duration(ms) * time.Millisecond
		}
	}
	// transport is "shm" (t=s, the default) or "b64" (t=d, the payload inline
	// in chunked escapes). The bitmap and the geometry are identical either
	// way, so anything that differs downstream is the transport's own.
	transport := "shm"
	if len(os.Args) > 4 && os.Args[4] != "" {
		transport = os.Args[4]
	}

	// One shared memory object per size, never rewritten once advertised. A
	// terminal maps the object it is handed, and truncating it under the map
	// is a bus error in the reader rather than a torn frame.
	var made []string
	defer func() {
		for _, n := range made {
			_ = os.Remove("/dev/shm/" + n)
		}
	}()
	gen := 0
	var encoded string
	var pix []byte
	var pixW, pixH int
	allocate := func(w, h int) error {
		gen++
		if transport == "b64" {
			// A distinguishable bitmap, so a frame that is not the current one
			// is visible as such rather than as identical grey.
			pix = make([]byte, w*h*4)
			for i := range pix {
				pix[i] = byte((i + gen*37) * 7)
			}
			pixW, pixH = w, h
			return nil
		}
		name := fmt.Sprintf("tuios-frameloop-%d-%d", os.Getpid(), gen)
		if err := os.WriteFile("/dev/shm/"+name, make([]byte, w*h*4), 0o600); err != nil {
			return err
		}
		made = append(made, name)
		encoded = base64.StdEncoding.EncodeToString([]byte(name))
		return nil
	}

	// Report the geometry before taking the screen, so the harness can read it
	// off the grid and know what the guest was actually told.
	cols, rows, xpx, ypx, err := size()
	if err != nil {
		fmt.Printf("FRAMELOOP-ERR %v\n", err)
		return
	}
	// Appended, not overwritten: the harness wants every size this app was ever
	// given, because the question it asks of tuios is whether the host was ever
	// told a rectangle the guest was not.
	report := func(c, r, x, y int) {
		if geomFile == "" {
			return
		}
		f, err := os.OpenFile(geomFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		_, _ = fmt.Fprintf(f, "%d %d %d %d\n", c, r, x, y)
	}
	if xpx == 0 || ypx == 0 {
		fmt.Printf("FRAMELOOP-ERR no pixel size\n")
		return
	}
	report(cols, rows, xpx, ypx)

	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)

	if err := allocate(xpx, ypx); err != nil {
		fmt.Printf("FRAMELOOP-ERR %v\n", err)
		return
	}

	_, _ = os.Stdout.WriteString("\x1b[?1049h")
	defer func() { _, _ = os.Stdout.WriteString("\x1b[?1049l") }()

	seq := 0
	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()
	// relayout fires when the app has finished re-rendering at a new size. It
	// is armed by a resize and never blocks the frame loop, so the old frame
	// keeps going out in the meantime, which is the whole point.
	relayout := time.NewTimer(time.Hour)
	defer relayout.Stop()
	if !relayout.Stop() {
		<-relayout.C
	}
	for {
		select {
		case <-resized:
			if repaint == 0 {
				relayout.Reset(0)
				continue
			}
			relayout.Reset(repaint)
		case <-relayout.C:
			if c, r, x, y, err := size(); err == nil && x > 0 && y > 0 {
				cols, rows, xpx, ypx = c, r, x, y
				if err := allocate(xpx, ypx); err != nil {
					return
				}
				report(cols, rows, xpx, ypx)
			}
		case <-tick.C:
			if transport == "b64" {
				// Every frame differs, the way an animating guest's does, so
				// the transmission is not suppressed as a repeat.
				seq++
				for i := 0; i < len(pix); i += 4099 {
					pix[i] = byte(seq)
				}
				_, _ = os.Stdout.Write(buildB64Frame(pix, pixW, pixH))
				continue
			}
			fmt.Printf("\x1b[H\x1b_Ga=T,f=32,t=s,s=%d,v=%d,i=1,q=2,C=1;%s\x1b\\",
				xpx, ypx, encoded)
		}
	}
}

// buildB64Frame is the same frame the shared-memory path advertises, sent as a
// direct transmission instead: one a=T carrying the control keys and the first
// 4096 bytes of base64, then continuation chunks that carry only m=. This is
// the chunking real direct-transport clients use (kitten icat, chafa, wlterm).
func buildB64Frame(pix []byte, w, h int) []byte {
	enc := base64.StdEncoding.EncodeToString(pix)
	b := []byte("\x1b[H")
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
			b = append(b, fmt.Sprintf(
				"\x1b_Ga=T,f=32,t=d,s=%d,v=%d,i=1,q=2,C=1,m=%d;", w, h, m)...)
			first = false
		} else {
			b = append(b, fmt.Sprintf("\x1b_Gm=%d;", m)...)
		}
		b = append(b, chunk...)
		b = append(b, "\x1b\\"...)
	}
	return b
}
