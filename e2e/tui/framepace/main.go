// Command framepace is the guest of the frame pacing measurements in
// perf_frames_test.go. It draws frames into its pane at a fixed rate and logs
// when it wrote each one, so the harness can line a frame up with the moment
// the host terminal received it.
//
//	framepace MODE FPS LOG [BASE]
//
// MODE is one of:
//
//	text   a full screen of text that changes every frame, the shape of an
//	       animating TUI (btop, a game, cmatrix)
//	scroll lines appended at the bottom, so the pane scrolls every frame
//	shm    a kitty image the size of the pane in shared memory (t=s), one id
//	       reused, the way the tuios-wayland viewer and mpv's shm mode send it
//	b64    the same image inline as chunked base64 (t=d)
//	patch  the shm image once, then a 64x64 edit of it (a=f) each frame, the
//	       way the tuios-wayland viewer sends small damage
//
// Every frame carries its sequence number where the host can read it back:
// text frames start with a tag (see tag), and image frames carry it in their
// first eight pixel bytes. FPS 0 draws as fast as the terminal takes it. BASE
// is added to every sequence number, so guests in several panes of one screen
// can be told apart.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

type winsize struct{ rows, cols, xpixel, ypixel uint16 }

func size() (cols, rows, xpx, ypx int) {
	var ws winsize
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	return int(ws.cols), int(ws.rows), int(ws.xpixel), int(ws.ypixel)
}

// tag encodes seq as ten characters that differ in every position from the
// previous frame's tag, and that hold no two equal neighbours. A renderer that
// diffs cells therefore writes the whole tag as one run, and cannot shorten it
// with a repeat sequence, so the host can find it in the byte stream.
func tag(seq int) []byte {
	digits := fmt.Sprintf("%08d", seq%100000000)
	b := make([]byte, 0, 10)
	if seq%2 == 0 {
		b = append(b, '<')
	} else {
		b = append(b, '>')
	}
	for i := range 8 {
		d := digits[i] - '0'
		if (i+seq)%2 == 0 {
			b = append(b, '0'+d)
		} else {
			b = append(b, 'a'+d)
		}
	}
	return append(b, '|')
}

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: framepace MODE FPS LOG")
		os.Exit(2)
	}
	mode := os.Args[1]
	fps, _ := strconv.Atoi(os.Args[2])
	base := 0
	if len(os.Args) > 4 {
		base, _ = strconv.Atoi(os.Args[4])
	}
	logf, err := os.Create(os.Args[3])
	if err != nil {
		fmt.Println("FRAMEPACE-ERR", err)
		return
	}
	log := bufio.NewWriterSize(logf, 64<<10)
	defer func() { _ = log.Flush(); _ = logf.Close() }()

	cols, rows, xpx, ypx := size()
	out := bufio.NewWriterSize(os.Stdout, 8<<20)

	var shmName string
	var shm *os.File
	var pix []byte
	var patchName string
	var patch *os.File
	var patchPix []byte
	if mode == "patch" {
		patchPix = make([]byte, 64*64*4)
		prefix := os.Getenv("TUIOS_E2E_SHM_PREFIX")
		if prefix == "" {
			prefix = "tuios-framepace-"
		}
		patchName = fmt.Sprintf("%s%d-patch", prefix, os.Getpid())
		if err := os.WriteFile("/dev/shm/"+patchName, patchPix, 0o600); err != nil {
			fmt.Println("FRAMEPACE-ERR", err)
			return
		}
		patch, _ = os.OpenFile("/dev/shm/"+patchName, os.O_WRONLY, 0o600)
		defer func() { _ = os.Remove("/dev/shm/" + patchName) }()
	}
	if mode == "shm" || mode == "b64" || mode == "patch" {
		if xpx == 0 || ypx == 0 {
			fmt.Println("FRAMEPACE-ERR no pixel size")
			return
		}
		pix = make([]byte, xpx*ypx*4)
		for i := range pix {
			pix[i] = byte(i * 7)
		}
		if mode == "shm" || mode == "patch" {
			prefix := os.Getenv("TUIOS_E2E_SHM_PREFIX")
			if prefix == "" {
				prefix = "tuios-framepace-"
			}
			shmName = fmt.Sprintf("%s%d", prefix, os.Getpid())
			if err := os.WriteFile("/dev/shm/"+shmName, pix, 0o600); err != nil {
				fmt.Println("FRAMEPACE-ERR", err)
				return
			}
			shm, _ = os.OpenFile("/dev/shm/"+shmName, os.O_WRONLY, 0o600)
			defer func() { _ = os.Remove("/dev/shm/" + shmName) }()
		}
	}
	if mode != "scroll" {
		_, _ = out.WriteString("\x1b[?1049h\x1b[?25l")
		_ = out.Flush()
	}

	var tick <-chan time.Time
	if fps > 0 {
		tk := time.NewTicker(time.Second / time.Duration(fps))
		defer tk.Stop()
		tick = tk.C
	}
	enc := base64.StdEncoding.EncodeToString([]byte(shmName))
	patchEnc := base64.StdEncoding.EncodeToString([]byte(patchName))
	line := make([]byte, 0, cols+16)
	for seq := base + 1; ; seq++ {
		if tick != nil {
			<-tick
		}
		switch mode {
		case "text":
			_, _ = out.WriteString("\x1b[H")
			_, _ = out.Write(tag(seq))
			for r := range rows - 1 {
				if r > 0 {
					_, _ = fmt.Fprintf(out, "\x1b[%d;1H", r+1)
				}
				_, _ = fmt.Fprintf(out, "\x1b[3%dm", (seq+r)%8)
				start := 0
				if r == 0 {
					start = 10
				}
				line = line[:0]
				for c := start; c < cols; c++ {
					line = append(line, byte('K'+(seq+r*3+c)%16))
				}
				_, _ = out.Write(line)
			}
			_, _ = out.WriteString("\x1b[0m")
		case "scroll":
			_, _ = out.Write(tag(seq))
			line = line[:0]
			for c := 10; c < cols-1; c++ {
				line = append(line, byte('K'+(seq+c)%16))
			}
			_, _ = out.Write(line)
			_, _ = out.WriteString("\r\n")
		case "patch":
			if seq == base+1 {
				binary.LittleEndian.PutUint64(pix, uint64(seq))
				if _, err := shm.WriteAt(pix, 0); err != nil {
					return
				}
				_, _ = fmt.Fprintf(out, "\x1b[H\x1b_Ga=T,f=32,t=s,s=%d,v=%d,i=1,q=2,C=1;%s\x1b\\", xpx, ypx, enc)
				break
			}
			binary.LittleEndian.PutUint64(patchPix, uint64(seq))
			for i := 8; i < len(patchPix); i += 97 {
				patchPix[i] = byte(seq)
			}
			if _, err := patch.WriteAt(patchPix, 0); err != nil {
				return
			}
			_, _ = fmt.Fprintf(out, "\x1b_Ga=f,r=1,i=1,x=0,y=0,s=64,v=64,f=32,q=2,t=s;%s\x1b\\", patchEnc)
		case "shm", "b64":
			binary.LittleEndian.PutUint64(pix, uint64(seq))
			for i := 8; i < len(pix); i += 4093 {
				pix[i] = byte(seq)
			}
			if mode == "shm" {
				if _, err := shm.WriteAt(pix, 0); err != nil {
					return
				}
				_, _ = fmt.Fprintf(out, "\x1b[H\x1b_Ga=T,f=32,t=s,s=%d,v=%d,i=1,q=2,C=1;%s\x1b\\", xpx, ypx, enc)
			} else {
				b64 := base64.StdEncoding.EncodeToString(pix)
				_, _ = out.WriteString("\x1b[H")
				for first := true; len(b64) > 0; first = false {
					chunk := b64[:min(len(b64), 4096)]
					b64 = b64[len(chunk):]
					m := 0
					if len(b64) > 0 {
						m = 1
					}
					if first {
						_, _ = fmt.Fprintf(out, "\x1b_Ga=T,f=32,t=d,s=%d,v=%d,i=1,q=2,C=1,m=%d;", xpx, ypx, m)
					} else {
						_, _ = fmt.Fprintf(out, "\x1b_Gm=%d;", m)
					}
					_, _ = out.WriteString(chunk)
					_, _ = out.WriteString("\x1b\\")
				}
			}
		}
		// The time is taken before the write: it is when the frame was ready,
		// and a write that blocks is the terminal holding the guest back, which
		// is part of the latency being measured.
		ready := time.Now().UnixNano()
		if err := out.Flush(); err != nil {
			return
		}
		_, _ = fmt.Fprintf(log, "%d %d\n", seq, ready)
		if seq%16 == 0 {
			_ = log.Flush()
		}
	}
}
