// Command imagenumbers stands in for a guest that names its kitty images by
// number (I=) and not by id (i=), the way cbirds --render kitty does.
//
// Usage: imagenumbers [MODE]
//
// MODE is one of:
//
//   - frames (the default): two PNGs go out by number, I=7 at 40x40 pixels
//     and I=9 at 30x60, and one by id, i=5 at 20x20. Two frames follow, each a
//     synchronized update that deletes all placements (d=a) and places each
//     image again. Then the guest frees I=9 by number (d=N).
//   - reupload: three uploads under I=1, at 40x40, 30x60 and 20x20. The first
//     two are placed as they go (a=T) at rows 1 and 11. The third is not
//     placed. Then d=a takes every placement down.
//   - delplace: one upload under I=2, placed twice (p=1 and p=2). Then d=N
//     with p=1 deletes one placement, and a=p places the image again (p=3).
//   - reply: uploads and placements that ask for an answer, and some that do
//     not. The guest reads its input for a while and prints each answer as a
//     REPLY line, with spaces as underscores.
//   - late: an upload under I=1, then a wait for a key, then an upload under
//     I=2 that the guest places by the id the answer gave.
//   - cbirdsrun [exit]: five uploads by number and two frames of placements,
//     the way cbirds draws. With exit, the d=A cbirds sends as it quits.
//     Without, a d=c, which frees nothing.
//
// Every command in the first three modes carries q=2, as cbirds sends it.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

func pngOf(w, h int, shade byte) string {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: shade, G: byte(x * 5), B: byte(y * 3), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func main() {
	mode := "frames"
	if len(os.Args) > 1 && os.Args[1] != "" {
		mode = os.Args[1]
	}
	switch mode {
	case "reupload":
		reupload()
	case "delplace":
		delplace()
	case "reply":
		reply()
	case "late":
		late()
	case "cbirdsrun":
		// cbirds quits when its frames are done, and so does this.
		cbirdsrun(len(os.Args) > 2 && os.Args[2] == "exit")
		return
	default:
		frames()
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case <-stop:
	case <-time.After(60 * time.Second):
	}
}

func frames() {
	out := os.Stdout
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,I=7,m=0;%s\x1b\\", pngOf(40, 40, 200))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,I=9,m=0;%s\x1b\\", pngOf(30, 60, 90))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,i=5,m=0;%s\x1b\\", pngOf(20, 20, 40))

	for range 2 {
		_, _ = out.WriteString("\x1b[?2026h\x1b_Ga=d,d=a\x1b\\")
		_, _ = out.WriteString("\x1b[3;3H\x1b_Ga=p,I=7,q=2,X=1,Y=2,C=1\x1b\\")
		_, _ = out.WriteString("\x1b[3;20H\x1b_Ga=p,I=9,q=2,X=0,Y=0,C=1\x1b\\")
		_, _ = out.WriteString("\x1b[3;40H\x1b_Ga=p,i=5,q=2,C=1\x1b\\")
		_, _ = out.WriteString("\x1b[?2026l")
		time.Sleep(200 * time.Millisecond)
	}
	_, _ = out.WriteString("\x1b_Ga=d,d=N,I=9,q=2\x1b\\")
	_, _ = out.WriteString("\x1b[10;1HIMAGENUMBERS-DONE\r\n")
}

func reupload() {
	out := os.Stdout
	_, _ = fmt.Fprintf(out, "\x1b[1;1H\x1b_Ga=T,q=2,f=100,I=1,C=1,m=0;%s\x1b\\", pngOf(40, 40, 200))
	_, _ = fmt.Fprintf(out, "\x1b[11;1H\x1b_Ga=T,q=2,f=100,I=1,C=1,m=0;%s\x1b\\", pngOf(30, 60, 90))
	time.Sleep(time.Second)
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,I=1,m=0;%s\x1b\\", pngOf(20, 20, 40))
	time.Sleep(500 * time.Millisecond)
	_, _ = out.WriteString("\x1b_Ga=d,d=a,q=2\x1b\\")
	_, _ = out.WriteString("\x1b[20;1HIMAGENUMBERS-DONE\r\n")
}

func delplace() {
	out := os.Stdout
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,I=2,m=0;%s\x1b\\", pngOf(40, 40, 200))
	_, _ = out.WriteString("\x1b[2;2H\x1b_Ga=p,I=2,p=1,q=2,C=1\x1b\\")
	_, _ = out.WriteString("\x1b[2;20H\x1b_Ga=p,I=2,p=2,q=2,C=1\x1b\\")
	time.Sleep(300 * time.Millisecond)
	_, _ = out.WriteString("\x1b_Ga=d,d=N,I=2,p=1,q=2\x1b\\")
	_, _ = out.WriteString("\x1b[2;40H\x1b_Ga=p,I=2,p=3,q=2,C=1\x1b\\")
	_, _ = out.WriteString("\x1b[20;1HIMAGENUMBERS-DONE\r\n")
}

// reply sends uploads and placements that kitty answers, and some it does
// not, then prints each answer the terminal gave. See TestKittyUploadReply
// for the list.
func reply() {
	restore, ok := rawInput()
	if !ok {
		return
	}
	defer restore()

	out := os.Stdout
	send := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }
	send("\x1b_Ga=t,f=100,I=3;%s\x1b\\", pngOf(8, 8, 1))
	send("\x1b_Ga=t,f=100,i=4;%s\x1b\\", pngOf(8, 8, 2))
	send("\x1b_Ga=t,f=100,I=5,q=1;%s\x1b\\", pngOf(8, 8, 3))
	send("\x1b_Ga=t,f=100,i=8,q=2;%s\x1b\\", pngOf(8, 8, 4))
	chunked := pngOf(8, 8, 5)
	cut := len(chunked) / 8 * 4
	send("\x1b_Ga=t,f=100,I=6,m=1;%s\x1b\\", chunked[:cut])
	send("\x1b_Gm=0;%s\x1b\\", chunked[cut:])

	// Files: one that exists, one that does not, and a temporary file in a
	// temporary directory with the name kitty asks for.
	dir, err := os.MkdirTemp("", "imagenumbers")
	if err != nil {
		fmt.Printf("IMAGENUMBERS-ERR %v\r\n", err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	tmp, err := os.MkdirTemp("/tmp", "imagenumbers")
	if err != nil {
		fmt.Printf("IMAGENUMBERS-ERR %v\r\n", err)
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	png8, _ := base64.StdEncoding.DecodeString(pngOf(8, 8, 6))
	file := filepath.Join(dir, "frame.png")
	temp := filepath.Join(tmp, "tty-graphics-protocol-frame.png")
	for _, name := range []string{file, temp} {
		if err := os.WriteFile(name, png8, 0o600); err != nil {
			fmt.Printf("IMAGENUMBERS-ERR %v\r\n", err)
			return
		}
	}
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	send("\x1b_Ga=t,t=f,f=100,i=10;%s\x1b\\", enc(file))
	send("\x1b_Ga=t,t=f,f=100,i=11;%s\x1b\\", enc(filepath.Join(dir, "missing.png")))
	send("\x1b_Ga=t,t=t,f=100,i=12;%s\x1b\\", enc(temp))

	// Both i= and I=: an error.
	send("\x1b_Ga=t,f=100,i=14,I=13;%s\x1b\\", pngOf(8, 8, 7))
	// Placements: an image the pane has, one it does not, a number it does
	// not.
	send("\x1b_Ga=t,f=100,i=19,q=2;%s\x1b\\", pngOf(8, 8, 8))
	send("\x1b_Ga=p,i=19,C=1\x1b\\")
	send("\x1b_Ga=p,i=99,C=1\x1b\\")
	send("\x1b_Ga=p,I=77,C=1\x1b\\")
	// q=2 on the last chunk only: no answer.
	send("\x1b_Ga=t,f=100,I=15,m=1;%s\x1b\\", chunked[:cut])
	send("\x1b_Gm=0,q=2;%s\x1b\\", chunked[cut:])
	// A last chunk that names its number again keeps the first chunk's id,
	// so the next number gets the id after it.
	send("\x1b_Ga=t,f=100,I=16,m=1;%s\x1b\\", chunked[:cut])
	send("\x1b_GI=16,m=0;%s\x1b\\", chunked[cut:])
	send("\x1b_Ga=t,f=100,I=17;%s\x1b\\", pngOf(8, 8, 9))
	// Not a PNG.
	send("\x1b_Ga=t,f=100,i=18;%s\x1b\\", enc("this is not a PNG image at all"))

	for _, r := range readReplies(3 * time.Second) {
		send("\r\nREPLY %s", strings.ReplaceAll(r, " ", "_"))
	}
	_, _ = out.WriteString("\r\nIMAGENUMBERS-DONE\r\n")
}

// late uploads I=1, waits for a key, then uploads I=2, learns its id from
// the answer and places the image by that id.
func late() {
	restore, ok := rawInput()
	if !ok {
		return
	}
	defer restore()
	out := os.Stdout
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,f=100,I=1,q=2;%s\x1b\\", pngOf(40, 40, 200))
	_, _ = out.WriteString("\r\nLATE-WAIT\r\n")
	key := make([]byte, 1)
	if _, err := os.Stdin.Read(key); err != nil {
		return
	}
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,f=100,I=2;%s\x1b\\", pngOf(30, 60, 90))
	id := ""
	for _, r := range readReplies(2 * time.Second) {
		if m := regexp.MustCompile(`^i=([0-9]+),I=2;OK$`).FindStringSubmatch(r); m != nil {
			id = m[1]
		}
	}
	if id == "" {
		_, _ = out.WriteString("\r\nLATE-NOID\r\n")
		return
	}
	_, _ = fmt.Fprintf(out, "\x1b[3;3H\x1b_Ga=p,i=%s,q=2,C=1\x1b\\", id)
	_, _ = fmt.Fprintf(out, "\x1b[20;1HIMAGENUMBERS-DONE id=%s\r\n", id)
}

// cbirdsrun does what one cbirds run does to the terminal: it uploads its
// sprites by number, draws two frames, and with "exit" frees every image
// with d=A as it quits.
func cbirdsrun(exit bool) {
	out := os.Stdout
	for n := 1; n <= 5; n++ {
		_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,I=%d,m=0;%s\x1b\\", n, pngOf(10+n, 10+n, byte(n*40)))
	}
	for range 2 {
		_, _ = out.WriteString("\x1b[?2026h\x1b_Ga=d,d=a,q=2\x1b\\")
		for n := 1; n <= 5; n++ {
			_, _ = fmt.Fprintf(out, "\x1b[3;%dH\x1b_Ga=p,I=%d,q=2,C=1\x1b\\", 4*n, n)
		}
		_, _ = out.WriteString("\x1b[?2026l")
		time.Sleep(300 * time.Millisecond)
	}
	if exit {
		_, _ = out.WriteString("\x1b_Ga=d,d=A,q=2\x1b\\")
	} else {
		// A delete at the cursor takes placements down and frees nothing.
		_, _ = out.WriteString("\x1b_Ga=d,d=c,q=2\x1b\\")
	}
	_, _ = out.WriteString("\x1b[20;1HIMAGENUMBERS-DONE\r\n")
}

// rawInput puts the terminal in raw mode so an answer arrives byte for byte,
// and returns what puts it back.
func rawInput() (func(), bool) {
	raw := exec.Command("stty", "raw", "-echo")
	raw.Stdin = os.Stdin
	if err := raw.Run(); err != nil {
		fmt.Printf("IMAGENUMBERS-ERR stty: %v\r\n", err)
		return nil, false
	}
	return func() {
		sane := exec.Command("stty", "sane")
		sane.Stdin = os.Stdin
		_ = sane.Run()
	}, true
}

// readReplies reads input for d and returns the graphics answers in it.
func readReplies(d time.Duration) []string {
	got := make(chan []byte, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				got <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	var in []byte
	deadline := time.After(d)
read:
	for {
		select {
		case b := <-got:
			in = append(in, b...)
		case <-deadline:
			break read
		}
	}
	var out []string
	for _, m := range regexp.MustCompile(`\x1b_G([^\x1b]*)\x1b\\`).FindAllSubmatch(in, -1) {
		out = append(out, string(m[1]))
	}
	return out
}
