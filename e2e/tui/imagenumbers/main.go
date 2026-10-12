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
//   - reply: uploads that ask for an answer, and some that do not, sent as
//     bytes and as files. The guest reads its input for a while and prints
//     each answer as a REPLY line, with spaces as underscores.
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

// reply sends uploads with every quiet level, by number and by id, one in
// two chunks, and three as files: a file that exists (i=10), one that does
// not (i=11) and a temporary file (i=12). Then it prints each answer the
// terminal gave. Kitty answers I=3, i=4, I=6, i=10, i=11 with an error and
// i=12, and no other.
func reply() {
	raw := exec.Command("stty", "raw", "-echo")
	raw.Stdin = os.Stdin
	if err := raw.Run(); err != nil {
		fmt.Printf("IMAGENUMBERS-ERR stty: %v\r\n", err)
		return
	}
	defer func() {
		sane := exec.Command("stty", "sane")
		sane.Stdin = os.Stdin
		_ = sane.Run()
	}()

	out := os.Stdout
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,f=100,I=3;%s\x1b\\", pngOf(8, 8, 1))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,f=100,i=4;%s\x1b\\", pngOf(8, 8, 2))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,f=100,I=5,q=1;%s\x1b\\", pngOf(8, 8, 3))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,f=100,i=8,q=2;%s\x1b\\", pngOf(8, 8, 4))
	chunked := pngOf(8, 8, 5)
	cut := len(chunked) / 8 * 4
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,f=100,I=6,m=1;%s\x1b\\", chunked[:cut])
	_, _ = fmt.Fprintf(out, "\x1b_Gm=0;%s\x1b\\", chunked[cut:])

	dir, err := os.MkdirTemp("", "imagenumbers")
	if err != nil {
		fmt.Printf("IMAGENUMBERS-ERR %v\r\n", err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	png8, _ := base64.StdEncoding.DecodeString(pngOf(8, 8, 6))
	file := filepath.Join(dir, "frame.png")
	temp := filepath.Join(dir, "tty-graphics-protocol-frame.png")
	for _, name := range []string{file, temp} {
		if err := os.WriteFile(name, png8, 0o600); err != nil {
			fmt.Printf("IMAGENUMBERS-ERR %v\r\n", err)
			return
		}
	}
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,t=f,f=100,i=10;%s\x1b\\", enc(file))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,t=f,f=100,i=11;%s\x1b\\", enc(filepath.Join(dir, "missing.png")))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,t=t,f=100,i=12;%s\x1b\\", enc(temp))

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
	deadline := time.After(2 * time.Second)
read:
	for {
		select {
		case b := <-got:
			in = append(in, b...)
		case <-deadline:
			break read
		}
	}
	for _, m := range regexp.MustCompile(`\x1b_G([^\x1b]*)\x1b\\`).FindAllSubmatch(in, -1) {
		_, _ = fmt.Fprintf(out, "\r\nREPLY %s", strings.ReplaceAll(string(m[1]), " ", "_"))
	}
	_, _ = out.WriteString("\r\nIMAGENUMBERS-DONE\r\n")
}
