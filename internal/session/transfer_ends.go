package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The two ends of a copy: this machine's disk, read directly, or a host's,
// through its daemon over the link with the file verbs.

// fileEnd is a machine's disk.
type fileEnd struct {
	d    *Daemon
	host string
	// wire, when set, counts the bytes this end's streams carry over the
	// link.
	wire *atomic.Int64
}

type statResult struct {
	exists bool
	isDir  bool
	size   int64
	perm   uint32
	// mtime is the modification time in Unix ms.
	mtime int64
}

func (e fileEnd) local() bool { return e.host == "" }

// name is the machine, for a message.
func (e fileEnd) name() string {
	if e.local() {
		return "this machine"
	}
	return e.host
}

func (e fileEnd) stat(ctx context.Context, path string) (statResult, error) {
	if e.local() {
		p, verr := expandPath(path)
		if verr != nil {
			return statResult{}, &VerbCallError{Code: verr.Code, Message: verr.Message}
		}
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return statResult{}, nil
		}
		if err != nil {
			return statResult{}, err
		}
		return statResult{exists: true, isDir: fi.IsDir(), size: fi.Size(), perm: uint32(fi.Mode().Perm()), mtime: fi.ModTime().UnixMilli()}, nil
	}
	var r struct {
		Exists bool     `json:"exists"`
		Size   int64    `json:"size"`
		Info   FileInfo `json:"info"`
	}
	if err := e.call(ctx, "file-stat", map[string]any{"path": path}, &r); err != nil {
		return statResult{}, err
	}
	return statResult{exists: r.Exists, isDir: r.Info.isDirLike(), size: r.Size, perm: r.Info.Perm, mtime: r.Info.MTime}, nil
}

func (e fileEnd) partSize(ctx context.Context, path, id string) (int64, error) {
	if e.local() {
		fi, err := os.Lstat(partPath(path, id))
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
	var r struct {
		Size int64 `json:"size"`
	}
	err := e.call(ctx, "file-stat", map[string]any{"path": path, "part": true, "part_id": id}, &r)
	return r.Size, err
}

// hash is the sha256 of a range of the file at path, to its end when length
// is negative.
func (e fileEnd) hash(ctx context.Context, path string, off, length int64) (string, error) {
	if e.local() {
		sum, _, err := hashRange(ctx, path, off, length)
		return sum, err
	}
	params := map[string]any{"path": path, "offset": off}
	if length >= 0 {
		params["length"] = length
	}
	var r struct {
		SHA256 string `json:"sha256"`
	}
	err := e.callTimeout(ctx, "file-hash", params, &r, transferHashTimeout)
	return r.SHA256, err
}

// partHash is the sha256 of a range of the part a copy with part id id
// writes for path.
func (e fileEnd) partHash(ctx context.Context, path, id string, off, length int64) (string, error) {
	if e.local() {
		sum, _, err := hashRange(ctx, partPath(path, id), off, length)
		return sum, err
	}
	params := map[string]any{"path": path, "offset": off, "part": true, "part_id": id, "length": length}
	var r struct {
		SHA256 string `json:"sha256"`
	}
	err := e.callTimeout(ctx, "file-hash", params, &r, transferHashTimeout)
	return r.SHA256, err
}

// readStream is a file's bytes from an offset, as one end reads them.
type readStream struct {
	// r is the bytes: the file itself on this machine, or the stream from
	// the far daemon, uncompressed.
	r io.Reader
	// size and mtime are the file's as the reading side opened it.
	size, mtime int64
	compressed  bool
	// trailer reads the sha256 the reading side took, after the last byte:
	// "" from this machine, whose bytes this daemon hashes itself.
	trailer func() (string, error)
	close   func() error
}

func (s *readStream) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *readStream) Close() error               { return s.close() }

// openRead opens the file at path to read from off. compress asks the far
// side to compress the bytes when a sample says it is worth it.
func (e fileEnd) openRead(ctx context.Context, path string, off int64, compress bool) (*readStream, error) {
	if e.local() {
		f, fi, err := openRegular(path)
		if err != nil {
			return nil, err
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, err
		}
		return &readStream{r: f, size: fi.Size(), mtime: fi.ModTime().UnixMilli(), trailer: func() (string, error) { return "", nil }, close: f.Close}, nil
	}
	c, err := e.d.dialHostFiles(ctx, e.host, true)
	if err != nil {
		return nil, err
	}
	c.count(e.wire)
	params := map[string]any{"path": path, "mode": "read", "offset": off, "sum": off == 0}
	if compress {
		params["compress"] = "auto"
	}
	raw, err := c.call(ctx, "open-file-stream", params, 30*time.Second)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	var r struct {
		Size       int64 `json:"size"`
		MTime      int64 `json:"mtime"`
		Compressed bool  `json:"compressed"`
		Sum        bool  `json:"sum"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		_ = c.Close()
		return nil, err
	}
	s := &readStream{r: c.br, size: r.Size, mtime: r.MTime, compressed: r.Compressed, close: c.Close}
	var fr io.ReadCloser
	if r.Compressed {
		fr = getFlateReader(c.br)
		s.r = fr
	}
	s.trailer = func() (string, error) {
		if fr != nil {
			if err := flateEnd(fr); err != nil {
				return "", err
			}
			putFlateReader(fr)
			fr = nil
		}
		if !r.Sum {
			return "", nil
		}
		line, err := readBoundedLine(c.br, 4<<10)
		if err != nil {
			return "", err
		}
		var t struct {
			SHA256 string `json:"sha256"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(line, &t); err != nil {
			return "", err
		}
		if t.Error != "" {
			return "", permanent(ErrVerbSourceChanged, t.Error)
		}
		return t.SHA256, nil
	}
	return s, nil
}

// openWrite opens the part of a copy to path at off for length bytes, which
// the caller sends as one flate stream when compress is set. finish ends the
// write and reports whether every byte landed.
func (e fileEnd) openWrite(ctx context.Context, path, id string, off, length int64, compress bool) (io.WriteCloser, func() error, error) {
	if e.local() {
		dir := filepath.Dir(path)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			if err == nil {
				err = &fs.PathError{Op: "write", Path: dir, Err: fs.ErrNotExist}
			}
			return nil, nil, err
		}
		f, err := openPart(fileFS{}, partPath(path, id))
		if err != nil {
			return nil, nil, err
		}
		e.d.transfers.parts.note(dir)
		if err := f.Truncate(off); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		return f, func() error {
			if err := syncData(f); err != nil {
				_ = f.Close()
				return err
			}
			return f.Close()
		}, nil
	}
	c, err := e.d.dialHostFiles(ctx, e.host, true)
	if err != nil {
		return nil, nil, err
	}
	c.count(e.wire)
	params := map[string]any{"path": path, "mode": "write", "offset": off, "length": length, "part_id": id}
	if compress {
		params["compress"] = true
	}
	if _, err := c.call(ctx, "open-file-stream", params, 30*time.Second); err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	finish := func() error {
		defer func() { _ = c.Close() }()
		type res struct {
			line []byte
			err  error
		}
		got := make(chan res, 1)
		go func() {
			line, err := readBoundedLine(c.br, 64<<10)
			got <- res{line, err}
		}()
		var r res
		select {
		case r = <-got:
		case <-time.After(2 * time.Minute):
			return errors.New("the far side did not confirm the write")
		case <-ctx.Done():
			return ctx.Err()
		}
		if r.err != nil {
			return r.err
		}
		var out struct {
			PartSize int64  `json:"part_size"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal(r.line, &out); err != nil {
			return err
		}
		if out.Error != "" {
			return errors.New(out.Error)
		}
		if out.PartSize != off+length {
			return fmt.Errorf("the part on %s holds %d bytes, want %d", e.host, out.PartSize, off+length)
		}
		return nil
	}
	return c, finish, nil
}

// commit checks the part against sum and puts it in place. known is the
// part's hash as this daemon took it while writing, for a part on this
// machine.
func (e fileEnd) commit(ctx context.Context, path, id, sum, conflict string, perm uint32, mtime int64, label, known string) (string, bool, error) {
	if e.local() {
		if known == "" {
			known = e.d.transfers.sums.take(fileFS{}, partPath(path, id))
		}
		final, _, skipped, verr := commitPart(ctx, fileFS{}, path, id, sum, conflict, perm, mtime, commitOpts{known: known, label: label})
		if verr != nil {
			return "", false, &VerbCallError{Code: verr.Code, Message: verr.Message}
		}
		return final, skipped, nil
	}
	var r struct {
		Path    string `json:"path"`
		Skipped bool   `json:"skipped"`
	}
	params := map[string]any{"path": path, "sha256": sum, "conflict": conflict, "part_id": id}
	if perm != 0 {
		params["perm"] = perm
	}
	if mtime > 0 {
		params["mtime"] = mtime
	}
	if label != "" && conflict == "keep-both" {
		params["label"] = label
	}
	err := e.callTimeout(ctx, "file-commit", params, &r, transferHashTimeout)
	return r.Path, r.Skipped, err
}

func (e fileEnd) abort(ctx context.Context, path, id string) error {
	if e.local() {
		err := os.Remove(partPath(path, id))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return e.call(ctx, "file-abort", map[string]any{"path": path, "part_id": id}, nil)
}

func (e fileEnd) mkdir(ctx context.Context, path string) error {
	if e.local() {
		return os.MkdirAll(path, 0o755) //nolint:gosec // a folder of a copy the person asked for, with the mode mkdir gives
	}
	return e.call(ctx, "file-mkdir", map[string]any{"path": path}, nil)
}

func (e fileEnd) remove(ctx context.Context, path string, recursive bool) error {
	if e.local() {
		if recursive {
			return os.RemoveAll(path)
		}
		return os.Remove(path)
	}
	return e.call(ctx, "file-remove", map[string]any{"path": path, "recursive": recursive}, nil)
}

// walk lists a folder for a copy. A host streams the entries one line each,
// so a folder of up to fileWalkMax entries is no one answer.
func (e fileEnd) walk(ctx context.Context, path string) (walkResult, error) {
	if e.local() {
		w, err := walkTree(path, fileWalkMax)
		if isWalkTooLarge(err) {
			return walkResult{}, permanent(ErrVerbInvalidParams, err.Error())
		}
		return w, err
	}
	c, err := e.d.dialHostFiles(ctx, e.host, true)
	if err != nil {
		return walkResult{}, err
	}
	defer func() { _ = c.Close() }()
	c.count(e.wire)
	raw, err := c.call(ctx, "file-walk", map[string]any{"path": path, "stream": true}, 2*time.Minute)
	if err != nil {
		return walkResult{}, err
	}
	var reply struct {
		Stream       bool          `json:"stream"`
		Entries      []WalkEntry   `json:"entries"`
		Bytes        int64         `json:"bytes"`
		Skipped      []SkippedItem `json:"skipped"`
		SkippedCount int           `json:"skipped_count"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return walkResult{}, err
	}
	if !reply.Stream {
		// A daemon that answers the whole walk in its reply.
		if len(reply.Skipped) > fileWalkSkippedMax {
			reply.Skipped = reply.Skipped[:fileWalkSkippedMax]
		}
		return walkResult{entries: reply.Entries, total: reply.Bytes, skipped: reply.Skipped, skippedCount: max(reply.SkippedCount, len(reply.Skipped))}, nil
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	var out walkResult
	for {
		line, err := readBoundedLine(c.br, 64<<10)
		if err != nil {
			if ctx.Err() != nil {
				return walkResult{}, ctx.Err()
			}
			return walkResult{}, err
		}
		var rec struct {
			WalkEntry
			End          bool          `json:"end"`
			Bytes        int64         `json:"bytes"`
			Skipped      []SkippedItem `json:"skipped"`
			SkippedCount int           `json:"skipped_count"`
			Code         string        `json:"code"`
			Error        string        `json:"error"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return walkResult{}, fmt.Errorf("the walk of %s did not decode: %w", path, err)
		}
		if rec.End {
			if rec.Code != "" {
				return walkResult{}, &VerbCallError{Code: rec.Code, Message: rec.Error}
			}
			if len(rec.Skipped) > fileWalkSkippedMax {
				rec.Skipped = rec.Skipped[:fileWalkSkippedMax]
			}
			out.skipped, out.skippedCount = rec.Skipped, max(rec.SkippedCount, len(rec.Skipped))
			return out, nil
		}
		if len(out.entries) >= fileWalkMax {
			return walkResult{}, permanent(ErrVerbInvalidParams, errWalkTooLarge(fileWalkMax).Error())
		}
		if rec.Size < 0 {
			return walkResult{}, permanent(ErrVerbInvalidParams, "the walk of "+path+" names a file with a size below zero")
		}
		out.entries = append(out.entries, rec.WalkEntry)
		out.total += rec.Size
	}
}

// fileCheckBatch bounds the files one file-check asks about, and
// fileCheckHashBytes the bytes one file-check hashes.
const (
	fileCheckBatch     = 2000
	fileCheckHashBytes = 512 << 20
)

// check asks about many files under root at once: whether each is there, its
// kind, size and time, and with hash its sha256. It asks in batches, so a
// folder of any size is a few round trips.
func (e fileEnd) check(ctx context.Context, root string, rels []string, hash bool) ([]FileCheck, error) {
	if e.local() {
		return checkFiles(ctx, root, rels, hash)
	}
	out := make([]FileCheck, 0, len(rels))
	for len(rels) > 0 {
		n := min(len(rels), fileCheckBatch)
		var r struct {
			Entries []FileCheck `json:"entries"`
		}
		if err := e.callTimeout(ctx, "file-check", map[string]any{"root": root, "rels": rels[:n], "hash": hash}, &r, transferHashTimeout); err != nil {
			return nil, err
		}
		if len(r.Entries) != n {
			return nil, fmt.Errorf("file-check on %s answered %d entries for %d files", e.host, len(r.Entries), n)
		}
		out = append(out, r.Entries...)
		rels = rels[n:]
	}
	return out, nil
}

// freeName is the first free "name 2", "name 3" ... for a path on this end.
func (e fileEnd) freeName(ctx context.Context, path string) (string, error) {
	if e.local() {
		return freeName(path), nil
	}
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; i < 1000; i++ {
		cand := dir + fmt.Sprintf("%s %d%s", stem, i, ext)
		st, err := e.stat(ctx, cand)
		if err != nil {
			return "", err
		}
		if !st.exists {
			return cand, nil
		}
	}
	return "", permanent(ErrVerbFileExists, "no free name for "+path)
}

func (e fileEnd) call(ctx context.Context, verb string, params any, out any) error {
	return e.callTimeout(ctx, verb, params, out, 30*time.Second)
}

func (e fileEnd) callTimeout(ctx context.Context, verb string, params any, out any, timeout time.Duration) error {
	c, err := e.d.dialHostFiles(ctx, e.host, false)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	raw, err := c.call(ctx, verb, params, timeout)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// hostFiles is one verb connection to a host's daemon over the link.
type hostFiles struct {
	rw io.ReadWriteCloser
	br *bufio.Reader
	id int
	// wire counts the bytes both ways, when set.
	wire *atomic.Int64
}

func (c *hostFiles) Write(p []byte) (int, error) {
	n, err := c.rw.Write(p)
	if c.wire != nil {
		c.wire.Add(int64(n))
	}
	return n, err
}
func (c *hostFiles) Close() error { return c.rw.Close() }

// count makes the connection count its bytes into wire from now on.
func (c *hostFiles) count(wire *atomic.Int64) {
	if wire == nil {
		return
	}
	c.wire = wire
	c.br.Reset(&countingReader{r: c.rw, n: wire})
}

// countingReader counts what it reads.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// dialHostFiles opens a connection to host's daemon for the file verbs. A bulk
// connection carries file bytes after its first reply.
func (d *Daemon) dialHostFiles(ctx context.Context, host string, bulk bool) (*hostFiles, error) {
	if d.federation == nil {
		return nil, &VerbCallError{Code: ErrVerbUnknownHost, Message: "no hosts are configured"}
	}
	octx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rw, err := d.federation.OpenConnectionAs(octx, host, federation.StreamOpen{Bulk: bulk})
	if err != nil {
		msg, code := federationErrorText(err)
		if code == ErrVerbUnknownHost {
			return nil, &VerbCallError{Code: code, Message: msg}
		}
		return nil, &VerbCallError{Code: ErrVerbHostUnreachable, Message: msg}
	}
	return &hostFiles{rw: rw, br: bufio.NewReaderSize(rw, 256<<10)}, nil
}

// call sends one verb and reads its one reply line.
func (c *hostFiles) call(ctx context.Context, verb string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.id++
	req := map[string]any{"id": c.id, "verb": verb, "params": params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	type res struct {
		line []byte
		err  error
	}
	got := make(chan res, 1)
	go func() {
		if _, err := c.Write(append(line, '\n')); err != nil {
			got <- res{err: err}
			return
		}
		l, err := readBoundedLine(c.br, 16<<20)
		got <- res{l, err}
	}()
	var r res
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r = <-got:
	case <-t.C:
		_ = c.rw.Close()
		return nil, fmt.Errorf("%s did not answer in %v", verb, timeout)
	case <-ctx.Done():
		_ = c.rw.Close()
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.line, &resp); err != nil {
		return nil, fmt.Errorf("the answer to %s did not decode: %w", verb, err)
	}
	if resp.Error != nil {
		return nil, &VerbCallError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return resp.Result, nil
}

// readBoundedLine reads one line of at most limit bytes.
func readBoundedLine(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, errors.New("the answer is larger than the limit")
		}
		if err == nil {
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return nil, err
	}
}
