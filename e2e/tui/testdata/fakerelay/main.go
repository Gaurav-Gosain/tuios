// fakerelay stands in for the screen-relay engine in the remote desktop e2e
// tests. It takes the engine's flags, checks the token from --token-file,
// answers /capabilities, and on /stream sends one tiles keyframe of
// --size (one fill op), then waits for the client to close. It records
// each start in the file --started-file names, when given.
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

func main() {
	bind := flag.String("bind", "127.0.0.1", "")
	port := flag.String("port", "8080", "")
	tokenFile := flag.String("token-file", "", "")
	flag.String("viewer-dir", "", "")
	flag.String("backend", "", "")
	flag.String("scale", "", "")
	flag.Bool("tiles", false, "")
	size := flag.String("size", "320x200", "")
	flag.Parse()
	token := ""
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakerelay: token file:", err)
			os.Exit(1)
		}
		token = strings.TrimSpace(string(b))
	}
	var w, h int
	fmt.Sscanf(*size, "%dx%d", &w, &h)
	ok := func(r *http.Request) bool {
		if token == "" {
			return true
		}
		if r.Header.Get("Authorization") == "Bearer "+token {
			return true
		}
		for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			if strings.TrimSpace(p) == "bearer."+token {
				return true
			}
		}
		return false
	}
	http.HandleFunc("/capabilities", func(rw http.ResponseWriter, r *http.Request) {
		if !ok(r) {
			http.Error(rw, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"version": 1, "backend": "fake", "width": w, "height": h, "codecs": []string{"tiles"}, "clipboard": false,
			"outputs": []map[string]any{{"name": "FAKE-1", "x": 0, "y": 0, "width": w, "height": h, "scale": 1, "primary": true}}})
	})
	http.HandleFunc("/stream", func(rw http.ResponseWriter, r *http.Request) {
		if !ok(r) {
			http.Error(rw, "unauthorized", 401)
			return
		}
		conn, brw, err := upgrade(rw, r)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = writeBinary(brw, keyframe(w, h))
		// Read and drop the client's frames (ACKs) until it goes.
		for {
			if _, err := readFrame(brw); err != nil {
				return
			}
		}
	})
	ln, err := net.Listen("tcp", net.JoinHostPort(*bind, *port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakerelay:", err)
		os.Exit(1)
	}
	fmt.Printf("Relay listening on %s\n", ln.Addr())
	_ = http.Serve(ln, nil)
}

// keyframe is an NSL1 type 8 message: one fill op that paints the frame.
func keyframe(w, h int) []byte {
	p := make([]byte, 20+12)
	p[0], p[1] = 1, 1 // version, keyframe
	binary.BigEndian.PutUint16(p[2:], uint16(w))
	binary.BigEndian.PutUint16(p[4:], uint16(h))
	binary.BigEndian.PutUint16(p[14:], 1) // one op
	op := p[20:]
	op[0] = 3 // fill
	binary.BigEndian.PutUint16(op[5:], uint16(w))
	binary.BigEndian.PutUint16(op[7:], uint16(h))
	op[9], op[10], op[11] = 0x30, 0x60, 0x90
	m := make([]byte, 20, 20+len(p))
	copy(m, "NSL1")
	m[4], m[5] = 1, 8
	binary.BigEndian.PutUint32(m[8:], 1)
	binary.BigEndian.PutUint32(m[12:], 1)
	binary.BigEndian.PutUint32(m[16:], uint32(len(p)))
	return append(m, p...)
}

func upgrade(rw http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	hj, ok := rw.(http.Hijacker)
	if key == "" || !ok {
		http.Error(rw, "not a websocket", 400)
		return nil, nil, io.EOF
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: nsl1\r\n\r\n",
		base64.StdEncoding.EncodeToString(sum[:]))
	return conn, brw, brw.Flush()
}

func writeBinary(w *bufio.ReadWriter, payload []byte) error {
	hdr := []byte{0x82}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 1<<16:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	if _, err := w.Write(append(hdr, payload...)); err != nil {
		return err
	}
	return w.Flush()
}

// readFrame reads one client frame (masked) and returns its opcode.
func readFrame(r *bufio.ReadWriter) (byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, err
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if h[1]&0x80 != 0 {
		var mask [4]byte
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, err
		}
	}
	if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
		return 0, err
	}
	if h[0]&0x0f == 8 {
		return 8, io.EOF
	}
	return h[0] & 0x0f, nil
}
