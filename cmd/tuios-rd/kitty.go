//go:build linux || darwin

package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// How frames reach the terminal.
type outMode int

const (
	modeShm   outMode = iota // whole frames through a reused /dev/shm object (t=s)
	modeEdits                // zlib frame edits of changed tiles over the pty (a=f)
	modeFull                 // zlib whole frames over the pty, for terminals that refuse a=f
)

func (m outMode) String() string {
	return [...]string{"shm", "edits", "frames"}[m]
}

const imgID = 7311

// shmSlot is one file in /dev/shm that lives for the whole run, mapped once.
// Each frame is written into it and sent with t=f, so the terminal reads the
// file and leaves it. Nothing is created or deleted per frame, so nothing is
// left behind in /dev/shm and a frame costs no page faults. The display loop
// reuses a slot only after the terminal answered the frame in it.
type shmSlot struct {
	path string
	fd   int
	size int
	mem  []byte
}

func newSlot(size int, path string) (*shmSlot, error) {
	// O_NOFOLLOW and O_EXCL after an unlink: another user cannot point the
	// path at a file of theirs with a symlink planted first.
	_ = syscall.Unlink(path)
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Ftruncate(fd, int64(size)); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	mem, err := syscall.Mmap(fd, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}
	return &shmSlot{path: path, fd: fd, size: size, mem: mem}, nil
}

func (s *shmSlot) free() {
	_ = syscall.Munmap(s.mem)
	syscall.Close(s.fd)
	_ = syscall.Unlink(s.path)
}

// cleanStale removes the /dev/shm files of tuios-rd processes that are gone,
// such as one that was killed before it could clean up.
func cleanStale() {
	ents, err := os.ReadDir("/dev/shm")
	if err != nil {
		return
	}
	for _, e := range ents {
		rest, ok := strings.CutPrefix(e.Name(), "tuios-rd-")
		if !ok {
			continue
		}
		pidS, _, _ := strings.Cut(rest, "-")
		pid, err := strconv.Atoi(pidS)
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			os.Remove("/dev/shm/" + e.Name())
		}
	}
}

type shmPool struct {
	slots []*shmSlot
	n     int
	pid   int
}

func (p *shmPool) next(size int) (*shmSlot, error) {
	k := p.n % len(p.slots)
	p.n++
	if s := p.slots[k]; s != nil && s.size == size {
		return s, nil
	}
	if p.slots[k] != nil {
		p.slots[k].free()
	}
	s, err := newSlot(size, fmt.Sprintf("/dev/shm/tuios-rd-%d-%d", p.pid, k))
	p.slots[k] = s
	return s, err
}

func (p *shmPool) close() {
	for i, s := range p.slots {
		if s != nil {
			s.free()
			p.slots[i] = nil
		}
	}
}

// placement says where and how big the image shows.
type placement struct {
	w, h       int // image pixels
	cols, rows int // 0: show at the image's own pixel size
}

func (pl placement) keys() string {
	if pl.cols > 0 {
		return fmt.Sprintf(",c=%d,r=%d", pl.cols, pl.rows)
	}
	return ""
}

func zl(p []byte) []byte {
	var b bytes.Buffer
	z, _ := zlib.NewWriterLevel(&b, zlib.BestSpeed)
	_, _ = z.Write(p)
	z.Close()
	return b.Bytes()
}

// writeFull sends rgb as the whole image, zlib and base64 in 4096-byte chunks.
func writeFull(o *bytes.Buffer, rgb []byte, pl placement) {
	enc := base64.StdEncoding.EncodeToString(zl(rgb))
	first := true
	for len(enc) > 0 {
		n := min(4096, len(enc))
		chunk := enc[:n]
		enc = enc[n:]
		more := 0
		if len(enc) > 0 {
			more = 1
		}
		if first {
			fmt.Fprintf(o, "\x1b_Ga=T,i=%d,p=1,f=24,o=z,s=%d,v=%d%s,C=1,q=2,m=%d;%s\x1b\\", imgID, pl.w, pl.h, pl.keys(), more, chunk)
			first = false
		} else {
			fmt.Fprintf(o, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
}

// writePatch edits one rectangle of frame 1 in place. A frame edit must fit
// one escape, so it splits the rectangle until the payload fits.
func writePatch(o *bytes.Buffer, rgb []byte, w, x, y, pw, ph int, scratch *[]byte) {
	sub := (*scratch)[:0]
	for yy := y; yy < y+ph; yy++ {
		sub = append(sub, rgb[(yy*w+x)*3:(yy*w+x+pw)*3]...)
	}
	*scratch = sub
	z := zl(sub)
	if base64.StdEncoding.EncodedLen(len(z)) > 4096 {
		if ph > 1 {
			h1 := ph / 2
			writePatch(o, rgb, w, x, y, pw, h1, scratch)
			writePatch(o, rgb, w, x, y+h1, pw, ph-h1, scratch)
			return
		}
		if pw > 1 {
			w1 := pw / 2
			writePatch(o, rgb, w, x, y, w1, ph, scratch)
			writePatch(o, rgb, w, x+w1, y, pw-w1, ph, scratch)
			return
		}
	}
	fmt.Fprintf(o, "\x1b_Ga=f,i=%d,r=1,X=1,x=%d,y=%d,s=%d,v=%d,f=24,o=z,q=2;%s\x1b\\", imgID, x, y, pw, ph, base64.StdEncoding.EncodeToString(z))
}

type rect struct{ x, y, w, h int }

// damage diffs two RGB frames on 32x8 tiles and merges runs of dirty tiles.
func damage(prev, cur []byte, w, h int) []rect {
	const tw, th = 32, 8
	var rs []rect
	for by := 0; by < h; by += th {
		bh := min(th, h-by)
		run := -1
		for bx := 0; bx <= w; bx += tw {
			dirty := false
			if bx < w {
				bw := min(tw, w-bx)
				for yy := by; yy < by+bh && !dirty; yy++ {
					o := (yy*w + bx) * 3
					dirty = !bytes.Equal(prev[o:o+bw*3], cur[o:o+bw*3])
				}
			}
			if dirty && run < 0 {
				run = bx
			}
			if !dirty && run >= 0 {
				rs = append(rs, rect{run, by, min(bx, w) - run, bh})
				run = -1
			}
		}
	}
	var m []rect
	for _, r := range rs {
		if n := len(m); n > 0 && m[n-1].x == r.x && m[n-1].w == r.w && m[n-1].y+m[n-1].h == r.y {
			m[n-1].h += r.h
			continue
		}
		m = append(m, r)
	}
	return m
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// probeShmFile makes a 1x1 RGB file for the t=f probe.
func probeShmFile() (string, error) {
	name := fmt.Sprintf("/dev/shm/tuios-rd-%d-probe", os.Getpid())
	_ = os.Remove(name)
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return name, err
	}
	_, err = f.Write([]byte{0, 0, 0})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return name, err
}
