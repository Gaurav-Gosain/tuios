package session

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Kitty images named by a path, from a pane on another machine: the near
// half. The far half, and the design, are in kitty_link_far.go.
//
// The far machine puts a marker in the pane's output where a graphics command
// named a file. This side passes the marker on (an APC no terminal acts on),
// fetches the file's bytes on a bulk stream with pane-kitty-medium, and writes
// them after the marker as a direct transmission, in 4096-character chunks,
// before any later output of the pane. The emulator, the clients and their
// terminals see an ordinary t=d upload.

// kittyLinkChunkRaw is the bytes of one t=d chunk: 4096 base64 characters,
// the most the kitty protocol allows in one.
const kittyLinkChunkRaw = 3072

// kittyLinkFetchBudget bounds the fetch up to its reply.
const kittyLinkFetchBudget = 15 * time.Second

// kittyLinkMarkerMax bounds a marker's body.
const kittyLinkMarkerMax = 1024

// kittyLinkNear finds the markers in a remote pane's output and writes the
// images in their place.
type kittyLinkNear struct {
	// fetch opens the bytes of the image a marker names.
	fetch func(id string) (io.ReadCloser, int64, error)

	// match is how much of kittyLinkMarkerPrefix ended the last read.
	match int
	// inBody is set between the prefix and the marker's ESC \.
	inBody bool
	sawEsc bool
	body   []byte

	// rest is output read past a marker, served after the image.
	rest []byte
	// due is a marker whose image is next.
	due []byte
	exp *kittyLinkExpansion
}

// read is the pane's Read: raw reads the far machine's bytes.
func (x *kittyLinkNear) read(b []byte, raw func([]byte) (int, error)) (int, error) {
	for {
		if x.due != nil {
			x.exp = x.start(x.due)
			x.due = nil
		}
		if x.exp != nil {
			n := x.exp.read(b)
			if x.exp.done() {
				x.exp.close()
				x.exp = nil
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		var data []byte
		if len(x.rest) > 0 {
			n := copy(b, x.rest)
			x.rest = x.rest[n:]
			if len(x.rest) == 0 {
				x.rest = nil
			}
			data = b[:n]
		} else {
			n, err := raw(b)
			if n == 0 {
				return 0, err
			}
			data = b[:n]
		}
		cut, ready := x.scan(data)
		if cut < len(data) {
			x.rest = append(append([]byte(nil), data[cut:]...), x.rest...)
		}
		if ready {
			x.due = x.body
			x.body = nil
		}
		if cut > 0 {
			return cut, nil
		}
	}
}

// scan follows the markers in data. data[:cut] goes to the caller; ready says
// a marker ended at cut.
func (x *kittyLinkNear) scan(data []byte) (cut int, ready bool) {
	p := kittyLinkMarkerPrefix
	i := 0
	for i < len(data) {
		if x.inBody {
			if x.sawEsc {
				x.inBody, x.sawEsc = false, false
				if data[i] == '\\' {
					return i + 1, true
				}
				x.body = nil
				continue
			}
			e := bytes.IndexByte(data[i:], 0x1b)
			if e < 0 {
				x.body = append(x.body, data[i:]...)
				if len(x.body) > kittyLinkMarkerMax {
					x.inBody, x.body = false, nil
				}
				return len(data), false
			}
			x.body = append(x.body, data[i:i+e]...)
			i += e + 1
			x.sawEsc = true
			continue
		}
		if x.match > 0 {
			k := min(len(p)-x.match, len(data)-i)
			if string(data[i:i+k]) == p[x.match:x.match+k] {
				x.match += k
				i += k
				if x.match == len(p) {
					x.match, x.inBody, x.body = 0, true, nil
				}
				continue
			}
			x.match = 0
		}
		j := bytes.Index(data[i:], []byte(p))
		if j >= 0 {
			i += j + len(p)
			x.inBody, x.body = true, nil
			continue
		}
		// A prefix of the marker at the very end continues in the next
		// read.
		tail := data[max(i, len(data)-len(p)+1):]
		for t := bytes.IndexByte(tail, 0x1b); t >= 0; {
			if bytes.HasPrefix([]byte(p), tail[t:]) {
				x.match = len(tail) - t
				break
			}
			n := bytes.IndexByte(tail[t+1:], 0x1b)
			if n < 0 {
				break
			}
			t += n + 1
		}
		return len(data), false
	}
	return len(data), false
}

// start fetches the image a marker body ("id;keys") names. A marker whose
// image cannot be fetched writes nothing: the far machine answers the guest.
func (x *kittyLinkNear) start(body []byte) *kittyLinkExpansion {
	id, keys, ok := bytes.Cut(body, []byte{';'})
	if !ok || len(id) == 0 || len(keys) == 0 || x.fetch == nil {
		return nil
	}
	r, size, err := x.fetch(string(id))
	if err != nil {
		debugLog("[DEBUG] kitty file over link: fetch: %v", err)
		return nil
	}
	return newKittyLinkExpansion(r, size, string(keys))
}

// kittyLinkExpansion writes one image as t=d chunks.
type kittyLinkExpansion struct {
	r         io.ReadCloser
	remaining int64
	keys      string
	quiet     string
	first     bool
	finished  bool
	raw       []byte
	out       []byte
	off       int
}

func newKittyLinkExpansion(r io.ReadCloser, size int64, keys string) *kittyLinkExpansion {
	quiet := ""
	for pair := range bytes.SplitSeq([]byte(keys), []byte{','}) {
		if bytes.HasPrefix(pair, []byte("q=")) {
			quiet = "," + string(pair)
		}
	}
	return &kittyLinkExpansion{
		r: r, remaining: size, keys: keys, quiet: quiet, first: true,
		raw: make([]byte, 16*kittyLinkChunkRaw),
	}
}

func (e *kittyLinkExpansion) done() bool { return e.finished && e.off >= len(e.out) }

func (e *kittyLinkExpansion) close() { _ = e.r.Close() }

// read copies the next chunks into b, reading more of the image as needed.
func (e *kittyLinkExpansion) read(b []byte) int {
	if e.off >= len(e.out) && !e.finished {
		e.fill()
	}
	n := copy(b, e.out[e.off:])
	e.off += n
	return n
}

// fill encodes up to sixteen chunks.
func (e *kittyLinkExpansion) fill() {
	e.out, e.off = e.out[:0], 0
	if e.remaining <= 0 {
		e.finished = true
		return
	}
	n := int(min(e.remaining, int64(len(e.raw))))
	got, err := io.ReadFull(e.r, e.raw[:n])
	if err != nil || got < n {
		// The link dropped in the middle. End the upload so the next
		// output is not read as image data; q=2 leaves the answer to the
		// far machine, which saw the send fail.
		if !e.first {
			e.out = append(e.out, "\x1b_Gm=0,q=2;\x1b\\"...)
		}
		e.finished = true
		return
	}
	e.remaining -= int64(n)
	for off := 0; off < n; off += kittyLinkChunkRaw {
		end := min(off+kittyLinkChunkRaw, n)
		last := e.remaining == 0 && end == n
		e.out = append(e.out, "\x1b_G"...)
		switch {
		case e.first && last:
			e.out = append(e.out, e.keys...)
		case e.first:
			e.out = append(e.out, e.keys...)
			e.out = append(e.out, ",m=1"...)
		case last:
			e.out = append(e.out, "m=0"...)
			e.out = append(e.out, e.quiet...)
		default:
			e.out = append(e.out, "m=1"...)
			e.out = append(e.out, e.quiet...)
		}
		e.first = false
		e.out = append(e.out, ';')
		e.out = base64.StdEncoding.AppendEncode(e.out, e.raw[off:end])
		e.out = append(e.out, "\x1b\\"...)
	}
	if e.remaining == 0 {
		e.finished = true
	}
}

// bulkOpener is the part of federation.Manager that opens a bulk stream.
type bulkOpener interface {
	OpenConnectionAs(ctx context.Context, host string, info federation.StreamOpen) (io.ReadWriteCloser, error)
}

// fetchKittyMedium opens the bytes of image id of the remote pane p, on a
// bulk stream when the federation can open one.
func (p *remotePane) fetchKittyMedium(token, id string) (io.ReadCloser, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kittyLinkFetchBudget)
	defer cancel()
	var stream io.ReadWriteCloser
	var err error
	if bo, ok := p.fed.(bulkOpener); ok {
		stream, err = bo.OpenConnectionAs(ctx, p.host, federation.StreamOpen{Bulk: true})
	} else {
		stream, err = p.fed.OpenConnection(ctx, p.host)
	}
	if err != nil {
		return nil, 0, err
	}
	params, _ := json.Marshal(map[string]string{"pane": p.id, "token": token, "id": id})
	req, _ := json.Marshal(verbRequest{ID: json.RawMessage(`1`), Verb: "pane-kitty-medium", Params: params})
	type reply struct {
		Result *struct {
			Size       int64 `json:"size"`
			Compressed bool  `json:"compressed"`
		} `json:"result"`
		Error *verbError `json:"error"`
	}
	br := bufio.NewReaderSize(stream, 64<<10)
	got := make(chan error, 1)
	var resp reply
	go func() {
		if _, err := stream.Write(append(req, '\n')); err != nil {
			got <- err
			return
		}
		line, err := readLimitedLine(br, maxRemotePaneReply)
		if err == nil {
			err = json.Unmarshal(line, &resp)
		}
		got <- err
	}()
	select {
	case err = <-got:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err == nil && resp.Error != nil {
		err = resp.Error
	}
	if err == nil && (resp.Result == nil || resp.Result.Size <= 0 || resp.Result.Size > kittyLinkMaxBytes) {
		err = errors.New("the reply named no image")
	}
	if err != nil {
		_ = stream.Close()
		return nil, 0, fmt.Errorf("pane-kitty-medium: %w", err)
	}
	var r io.Reader = br
	if resp.Result.Compressed {
		r = flate.NewReader(br)
	}
	return readCloser{Reader: io.LimitReader(r, resp.Result.Size), Closer: stream}, resp.Result.Size, nil
}

type readCloser struct {
	io.Reader
	io.Closer
}
