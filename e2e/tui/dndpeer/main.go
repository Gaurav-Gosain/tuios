// Command dndpeer stands in for a program that speaks the kitty drag and
// drop protocol (OSC 72) in a pane: a source that offers files, or a target
// that takes them. It logs every message it receives, and what it did, one
// line per event, so a test can read the whole exchange back.
//
// Usage:
//
//	dndpeer source LOG FILE...   offer FILE... as text/uri-list and text/plain
//	dndpeer target LOG           take text/uri-list and text/plain drops
//
// DNDPEER_IGNORE_GESTURES=N makes a source leave the first N gestures (t=o)
// unanswered, as a program does when the press was on nothing it drags.
// DNDPEER_OFFER=text makes a source offer text/plain alone, two lines of
// text sent ahead, and no files.
//
// Both read a message as yazi reads it: a move, a drop or a gesture without
// x and y (and a move or drop without o), and an event without the key its
// kind rests on, is dropped. The target reads the MIME list from every move,
// as yazi does, and refuses a move whose list has no type it takes. The specification lets a terminal
// send the list with the first move only; kitty sends it with every move,
// and yazi relies on that.
//
// The terminal must already be in raw mode (stty raw -echo). It prints
// DNDPEER-READY once registered, turns on mouse reporting as yazi does
// (1002 and 1006), and exits on q or after two minutes.
//
// It has its own small parser rather than importing tuios.dev/vt, so the
// test fixture stays independent of the code under test.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type cmd struct {
	t       byte
	more    bool
	o, x, y int
	has     map[byte]bool
	payload []byte
}

func parse(body []byte) (cmd, bool) {
	c := cmd{has: map[byte]bool{}}
	body = bytes.TrimPrefix(body, []byte("72;"))
	meta, payload, _ := bytes.Cut(body, []byte{';'})
	for part := range bytes.SplitSeq(meta, []byte{':'}) {
		k, v, ok := bytes.Cut(part, []byte{'='})
		if !ok || len(k) != 1 {
			return c, false
		}
		n, _ := strconv.Atoi(string(v))
		c.has[k[0]] = true
		switch k[0] {
		case 't':
			if len(v) == 1 {
				c.t = v[0]
			}
		case 'm':
			c.more = n == 1
		case 'o':
			c.o = n
		case 'x':
			c.x = n
		case 'y':
			c.y = n
		}
	}
	c.payload = payload
	return c, true
}

func send(meta string, payload []byte) {
	s := "\x1b]72;" + meta
	if len(payload) > 0 {
		s += ";" + string(payload)
	}
	_, _ = os.Stdout.WriteString(s + "\x1b\\")
}

// sendData writes a data stream the way kitty and the kitten write one:
// base64 in chunks, m=0 on the last with data, then an empty message.
func sendData(meta string, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 0 {
		n := min(len(enc), 4096)
		m := "1"
		if n == len(enc) {
			m = "0"
		}
		send(meta+":m="+m, []byte(enc[:n]))
		enc = enc[n:]
	}
	send(meta, nil)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: dndpeer source|target LOG [FILE...]")
		os.Exit(2)
	}
	mode := os.Args[1]
	logf, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer logf.Close()
	logln := func(format string, args ...any) {
		_, _ = fmt.Fprintf(logf, format+"\n", args...)
	}

	ignore, _ := strconv.Atoi(os.Getenv("DNDPEER_IGNORE_GESTURES"))
	textOnly := os.Getenv("DNDPEER_OFFER") == "text"
	var uriList, plain string
	if mode == "source" {
		var uris, names []string
		for _, f := range os.Args[3:] {
			abs, _ := filepath.Abs(f)
			uris = append(uris, (&url.URL{Scheme: "file", Path: abs}).String())
			names = append(names, filepath.Base(abs))
		}
		uriList = strings.Join(uris, "\r\n")
		plain = strings.Join(names, " ")
		if textOnly {
			plain = "line one\nline two"
		}
		send("t=o:x=1", nil)
	} else {
		send("t=a", []byte("text/uri-list text/plain"))
	}
	// Ask whether the terminal speaks the protocol, as yazi and the kitten
	// do. The answer is logged; a test counts it.
	send("t=q", nil)
	_, _ = os.Stdout.WriteString("\x1b[?1002h\x1b[?1006h")
	// Split so the command line that starts the peer, which the pane echoes,
	// does not show the marker before the peer is up.
	_, _ = os.Stdout.WriteString("DNDPEER-" + "READY\r\n")

	// Streams of dropped data by MIME index, joined until the empty close.
	streams := map[int][]byte{}
	var offered []string
	in := make(chan []byte)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				in <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(in)
				return
			}
		}
	}()
	done := time.After(120 * time.Second)
	var pending []byte
	for {
		var chunk []byte
		select {
		case b, ok := <-in:
			if !ok {
				return
			}
			chunk = b
		case <-done:
			return
		}
		pending = append(pending, chunk...)
		for {
			start := bytes.Index(pending, []byte("\x1b]72;"))
			if start < 0 {
				if bytes.IndexByte(pending, 'q') >= 0 && !bytes.Contains(pending, []byte("\x1b")) {
					send("t=A", nil)
					send("t=o:x=2", nil)
					_, _ = os.Stdout.WriteString("\x1b[?1002l\x1b[?1006l")
					return
				}
				if !bytes.Contains(pending, []byte("\x1b]")) {
					if n := bytes.Count(pending, []byte("\x1b[<")); n > 0 {
						logln("mouse %d", n)
					}
					pending = nil
				}
				break
			}
			end := bytes.Index(pending[start:], []byte("\x1b\\"))
			if end < 0 {
				break
			}
			body := pending[start+2 : start+end]
			pending = pending[start+end+2:]
			c, ok := parse(body)
			if !ok {
				logln("unparsable %q", body)
				continue
			}
			logln("recv t=%c o=%d x=%d y=%d m=%v payload=%q", c.t, c.o, c.x, c.y, c.more, c.payload)
			if (c.t == 'm' || c.t == 'M') && !(c.has['x'] && c.has['y'] && c.has['o']) ||
				c.t == 'e' && (c.x == 4 || c.x == 1 || c.x == 5) && !c.has['y'] ||
				c.t == 'e' && c.x == 2 && !c.has['o'] ||
				c.t == 'o' && !(c.has['x'] && c.has['y']) {
				logln("dropped: a key its meaning rests on is missing, as yazi drops it")
				continue
			}
			switch mode {
			case "source":
				switch c.t {
				case 'o':
					if ignore > 0 {
						ignore--
						logln("ignored gesture")
						continue
					}
					// The gesture. Offer, pre-send the first type's data,
					// present a text icon, start.
					if textOnly {
						send("t=o:o=3", []byte("text/plain"))
						sendData("t=p:x=0", []byte(plain))
					} else {
						send("t=o:o=3", []byte("text/uri-list text/plain"))
						sendData("t=p:x=0", []byte(uriList))
					}
					sendData("t=p:x=-1:X=6:Y=1", []byte("dnd-icon"))
					send("t=P:x=-1", nil)
					logln("sent offer+start")
				case 'e':
					if c.x == 5 && c.y == 1 && !textOnly {
						sendData("t=e:y=1", []byte(plain))
						logln("sent text/plain")
					}
					if c.x == 4 {
						logln("finished canceled=%d", c.y)
					}
				}
			case "target":
				switch c.t {
				case 'm':
					if c.x == -1 && c.y == -1 {
						logln("left")
						continue
					}
					offered = strings.Fields(string(c.payload))
					if !slices.Contains(offered, "text/uri-list") {
						send("t=m:o=0", nil)
						continue
					}
					send("t=m:o=1", []byte("text/uri-list"))
				case 'M':
					offered = strings.Fields(string(c.payload))
					idx := 0
					for i, m := range offered {
						if m == "text/uri-list" {
							idx = i + 1
						}
					}
					if idx == 0 {
						send("t=r:o=0", nil)
						continue
					}
					send(fmt.Sprintf("t=r:x=%d", idx), nil)
				case 'r':
					if !c.more && len(c.payload) == 0 {
						dec, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(string(streams[c.x]), "="))
						logln("data x=%d err=%v %q", c.x, err, dec)
						delete(streams, c.x)
						send("t=r:o=1", nil)
						continue
					}
					streams[c.x] = append(streams[c.x], c.payload...)
				case 'R':
					logln("error %q", c.payload)
				}
			}
		}
	}
}
