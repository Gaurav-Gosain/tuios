package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// gh runs the gh CLI for every request, so the plugin never reads or stores
// a token. A list asks with the ETag of the last answer. GitHub does not
// count a "not modified" answer against the hourly limit, so a poll of a
// build that did not move costs nothing.
type gh struct {
	bin  string
	mu   sync.Mutex
	kept map[string]keptAnswer
	// slots holds at most four gh processes at a time.
	slots chan struct{}
}

type keptAnswer struct {
	etag string
	body []byte
}

func newGH() (*gh, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return nil, errors.New("gh is not on PATH. Install the GitHub CLI and run gh auth login")
	}
	return &gh{bin: bin, kept: map[string]keptAnswer{}, slots: make(chan struct{}, 4)}, nil
}

const ghTimeout = 60 * time.Second

func (g *gh) run(args ...string) ([]byte, error) {
	g.slots <- struct{}{}
	defer func() { <-g.slots }()
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New(clean(firstLine(msg)))
	}
	return out, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

var etagRE = regexp.MustCompile(`(?im)^etag:\s*(\S.*?)\s*$`)

// list gets a JSON answer. changed is false when GitHub said "not
// modified" and the answer is the one kept from last time.
func (g *gh) list(path string) (body []byte, changed bool, err error) {
	args := []string{"api", "--include"}
	g.mu.Lock()
	kept, ok := g.kept[path]
	g.mu.Unlock()
	if ok {
		args = append(args, "-H", "If-None-Match: "+kept.etag)
	}
	args = append(args, path)
	out, runErr := g.run(args...)
	head, rest, found := bytes.Cut(out, []byte("\r\n\r\n"))
	if !found {
		head, rest, found = bytes.Cut(out, []byte("\n\n"))
	}
	if !found {
		head, rest = out, nil
	}
	status, _, _ := bytes.Cut(head, []byte("\n"))
	if ok && bytes.HasPrefix(status, []byte("HTTP/")) && bytes.Contains(status, []byte(" 304")) {
		return kept.body, false, nil
	}
	if runErr != nil {
		return nil, false, runErr
	}
	if m := etagRE.FindSubmatch(head); m != nil {
		g.mu.Lock()
		g.kept[path] = keptAnswer{etag: string(m[1]), body: rest}
		g.mu.Unlock()
	}
	return rest, true, nil
}

// act runs a gh command that changes or opens something, such as a rerun.
func (g *gh) act(args ...string) error {
	_, err := g.run(args...)
	return err
}

// forget drops the kept answer of path.
func (g *gh) forget(path string) {
	g.mu.Lock()
	delete(g.kept, path)
	g.mu.Unlock()
}

// maxLogBytes bounds the log kept of a job. A failure is at the end of a
// log, so the tail is kept.
const maxLogBytes = 4 << 20

// tail keeps the last max bytes written to it, with a copy at most every
// max bytes, so memory stays near twice the bound whatever the log's size.
type tail struct {
	buf []byte
	max int
	cut bool
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
		t.cut = true
	}
	return len(p), nil
}

// log gets the tail of the log of a job, and whether the bound cut it. It
// is plain text, and GitHub serves it only once the runner has sent the
// first part.
func (g *gh) log(repo string, job int64) ([]byte, bool, error) {
	g.slots <- struct{}{}
	defer func() { <-g.slots }()
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.bin, "api", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, job))
	out := &tail{max: maxLogBytes}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, false, errors.New(clean(firstLine(msg)))
	}
	b := out.buf
	if len(b) > maxLogBytes {
		b, out.cut = b[len(b)-maxLogBytes:], true
	}
	if out.cut {
		// The bound cut a line in two: drop its end.
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, out.cut, nil
}
