package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"
)

// The speed test of a link: what a copy to a host can get.
//
// host-speed-test runs here and measures one host: the round trip of a small
// call, then speedTestDefault bytes up and the same down, each on a bulk
// stream, the kind a copy uses, so the window and the priority behind typing
// are the copy's too. speed-test is the far end: it pings, takes bytes and
// drops them, or sends bytes. The bytes are random, so a link that compresses
// gets no help, and they are no file's.

const (
	speedTestDefault = 64 << 20
	speedTestMax     = 1 << 30
	speedTestPings   = 5
)

// verbSpeedTest is the far end of a speed test.
func (d *Daemon) verbSpeedTest(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Mode  string `json:"mode"`
		Bytes int64  `json:"bytes"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Bytes < 0 || p.Bytes > speedTestMax {
		return nil, invalidParam("bytes", "bytes is 0 to 1073741824")
	}
	switch p.Mode {
	case "ping":
		return map[string]any{"mode": "ping"}, nil
	case "sink":
		cs.takeover = func(br *bufio.Reader) {
			_ = cs.conn.SetDeadline(time.Time{})
			start := time.Now()
			n, err := io.CopyN(io.Discard, br, p.Bytes)
			out := map[string]any{"bytes": n, "ms": time.Since(start).Milliseconds()}
			if err != nil {
				out["error"] = err.Error()
			}
			line, _ := json.Marshal(out)
			_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, _ = cs.conn.Write(append(line, '\n'))
			_ = cs.conn.Close()
		}
	case "source":
		cs.takeover = func(_ *bufio.Reader) {
			_ = cs.conn.SetDeadline(time.Time{})
			block := make([]byte, 1<<20)
			_, _ = rand.Read(block)
			left := p.Bytes
			for left > 0 {
				k := min(left, int64(len(block)))
				if _, err := cs.conn.Write(block[:k]); err != nil {
					break
				}
				left -= k
			}
			_ = cs.conn.Close()
		}
	default:
		return nil, invalidParam("mode", "mode is ping, sink or source", "ping", "sink", "source")
	}
	return map[string]any{"mode": p.Mode, "bytes": p.Bytes}, nil
}

// verbHostSpeedTest measures the link to one host.
func (d *Daemon) verbHostSpeedTest(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host  string `json:"host"`
		Bytes int64  `json:"bytes"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Host == "" {
		return nil, invalidParam("host", "name the host to test")
	}
	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}
	if p.Bytes == 0 {
		p.Bytes = speedTestDefault
	}
	if p.Bytes < 0 || p.Bytes > speedTestMax {
		return nil, invalidParam("bytes", "bytes is 1 to 1073741824")
	}
	ctx, cancel := context.WithTimeout(d.ctx, 10*time.Minute)
	defer cancel()
	fail := func(err error) *verbError {
		var ve *VerbCallError
		if errors.As(err, &ve) {
			return newVerbError(ve.Code, ve.Message)
		}
		return newVerbError(ErrVerbHostUnreachable, p.Host+": "+err.Error())
	}

	c, err := d.dialHostFiles(ctx, p.Host, false)
	if err != nil {
		return nil, fail(err)
	}
	var rtts []float64
	for range speedTestPings {
		start := time.Now()
		if _, err := c.call(ctx, "speed-test", map[string]any{"mode": "ping"}, 30*time.Second); err != nil {
			_ = c.Close()
			return nil, fail(err)
		}
		rtts = append(rtts, float64(time.Since(start).Microseconds())/1000)
	}
	_ = c.Close()
	slices.Sort(rtts)

	up, err := d.speedUp(ctx, p.Host, p.Bytes)
	if err != nil {
		return nil, fail(err)
	}
	down, err := d.speedDown(ctx, p.Host, p.Bytes)
	if err != nil {
		return nil, fail(err)
	}
	return map[string]any{
		"host":    p.Host,
		"bytes":   p.Bytes,
		"rtt_ms":  rtts[len(rtts)/2],
		"up":      float64(p.Bytes) / up.Seconds(),
		"down":    float64(p.Bytes) / down.Seconds(),
		"up_ms":   up.Milliseconds(),
		"down_ms": down.Milliseconds(),
	}, nil
}

// speedUp sends n bytes to host and returns how long they took to arrive.
func (d *Daemon) speedUp(ctx context.Context, host string, n int64) (time.Duration, error) {
	c, err := d.dialHostFiles(ctx, host, true)
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.call(ctx, "speed-test", map[string]any{"mode": "sink", "bytes": n}, 30*time.Second); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	block := make([]byte, 1<<20)
	_, _ = rand.Read(block)
	start := time.Now()
	for left := n; left > 0; {
		k := min(left, int64(len(block)))
		if _, err := c.Write(block[:k]); err != nil {
			return 0, err
		}
		left -= k
	}
	line, err := readBoundedLine(c.br, 4<<10)
	if err != nil {
		return 0, err
	}
	var r struct {
		Bytes int64  `json:"bytes"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &r); err != nil {
		return 0, err
	}
	if r.Error != "" || r.Bytes != n {
		return 0, errors.New("the far side took " + r.Error)
	}
	return time.Since(start), nil
}

// speedDown asks host for n bytes and returns how long they took.
func (d *Daemon) speedDown(ctx context.Context, host string, n int64) (time.Duration, error) {
	c, err := d.dialHostFiles(ctx, host, true)
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Close() }()
	start := time.Now()
	if _, err := c.call(ctx, "speed-test", map[string]any{"mode": "source", "bytes": n}, 30*time.Second); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	got, err := io.CopyN(io.Discard, c.br, n)
	if err != nil {
		return 0, err
	}
	if got != n {
		return 0, io.ErrUnexpectedEOF
	}
	return time.Since(start), nil
}

func speedVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"speed-test": {
			description: "The far end of a link speed test. ping answers at once. sink takes bytes after the reply and drops them, then answers one line {bytes, ms}. source sends bytes of random data after the reply, then closes.",
			params: []verbParam{
				{Name: "mode", Type: "string", Required: true, Description: "ping, sink or source.", Accepted: []string{"ping", "sink", "source"}},
				{Name: "bytes", Type: "int", Description: "How many bytes sink takes or source sends, at most 1 GiB."},
			},
			returns:  []verbParam{{Name: "mode", Type: "string", Description: "The mode."}},
			examples: []string{`{"id":1,"verb":"speed-test","params":{"mode":"ping"}}`},
			handler:  (*Daemon).verbSpeedTest,
		},
		"host-speed-test": {
			description: "Measure the link to a host the way a copy uses it: the round trip of a small call, then bytes up and down on bulk streams. The bytes are random, so compression does not help them.",
			params: []verbParam{
				{Name: "host", Type: "string", Required: true, Description: "A name from [hosts]."},
				{Name: "bytes", Type: "int", Description: "How many bytes each way.", Default: "67108864"},
			},
			returns: []verbParam{
				{Name: "rtt_ms", Type: "float", Description: "The middle of five round trips, ms."},
				{Name: "up", Type: "float", Description: "Bytes a second from this machine to the host."},
				{Name: "down", Type: "float", Description: "Bytes a second from the host to this machine."},
				{Name: "up_ms", Type: "int", Description: "How long the bytes up took."},
				{Name: "down_ms", Type: "int", Description: "How long the bytes down took."},
			},
			examples: []string{`{"id":1,"verb":"host-speed-test","params":{"host":"build","bytes":16777216}}`},
			handler:  (*Daemon).verbHostSpeedTest,
		},
	}
}
