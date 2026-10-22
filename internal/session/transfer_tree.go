package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The tree stream: a folder's small files in one stream.
//
// A file sent by itself costs round trips: the part's size, the stream that
// reads it, the stream that writes it, its end, the commit. Each is a new
// stream on the link and waits for the far side, so a folder of 20,000 small
// files on a link of 50 ms spends over an hour waiting and seconds moving
// bytes. The tree stream sends every small file of a folder copy in one
// stream each way, with no wait between files.
//
// open-tree-stream mode read turns a connection into the files under a
// folder, in the order asked for: the caller writes one request line per file
// ({rel, size, mtime}) and {end} after the last, and the daemon answers with
// one record per file and {end}. mode write is the other end: the caller
// writes records, and the daemon puts each file in place and answers with one
// ack line per file and {end}.
//
// A record is a header line, the bytes, and a trailer line:
//
//	{"rel":"src/a.go","kind":"file","size":1234,"perm":420,"mtime":...,"z":true}
//	<size bytes, or one flate stream of them when z>
//	{"sha256":"..."}
//
// A folder is a header with kind dir and nothing after it. A file the reading
// side could not read is a header with code and error and nothing after it.
// The trailer carries the hash the reading side took while it read, or an
// error when the file changed under it, and the writing side hashes what it
// writes and puts the file in place only when the two match. Every name is
// checked on the writing side (safeRel), and a write that came over a link is
// held to the link's roots and deny list as every link write is
// (verb_files_confine.go).
//
// This daemon runs every copy, so it is in the middle: it reads records from
// the source's stream and writes them to the destination's. An end on this
// machine runs the same code over an in-process pipe, so there is one
// implementation of each side.

// treeSmallMax is the largest file the tree stream carries. A larger file goes
// by itself, which resumes from the middle after a dropped link; a file in the
// tree stream is sent again whole.
const treeSmallMax = 8 << 20

// treeLineMax bounds a header, trailer, request or ack line.
const treeLineMax = 64 << 10

// treeDirSyncEvery is how many files a tree write puts in place in one folder
// before it syncs the folder.
const treeDirSyncEvery = 256

type treeHeader struct {
	Rel      string `json:"rel,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Perm     uint32 `json:"perm,omitempty"`
	MTime    int64  `json:"mtime,omitempty"`
	Z        bool   `json:"z,omitempty"`
	Conflict string `json:"conflict,omitempty"`
	Label    string `json:"label,omitempty"`
	End      bool   `json:"end,omitempty"`
	Code     string `json:"code,omitempty"`
	Error    string `json:"error,omitempty"`
}

type treeTrailer struct {
	SHA256 string `json:"sha256,omitempty"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

type treeRequest struct {
	Rel   string `json:"rel,omitempty"`
	Size  int64  `json:"size,omitempty"`
	MTime int64  `json:"mtime,omitempty"`
	End   bool   `json:"end,omitempty"`
}

type treeAck struct {
	Rel     string `json:"rel,omitempty"`
	Path    string `json:"path,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
	End     bool   `json:"end,omitempty"`
}

// treeResult is a file of a tree stream that did not land.
type treeResult struct {
	rel, code, msg string
}

func readTreeLine(br *bufio.Reader, v any) error {
	line, err := readBoundedLine(br, treeLineMax)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(line, v); err != nil {
		return fmt.Errorf("a tree stream line did not decode: %w", err)
	}
	return nil
}

func writeTreeLine(w io.Writer, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(line, '\n'))
	return err
}

// errTreeStream is a stream that broke its own framing. Nothing after it can
// be read.
var errTreeStream = errors.New("the tree stream is out of step")

// serveTreeRead answers requests from in with records to out: the read side
// of a tree stream. compress lets it send a file as flate when a sample says
// it is worth it.
//
// cs is the connection the stream came on. For a link each file is opened
// under the link read rules (openLinkRead), and a refused file is a record
// that says so.
func serveTreeRead(ctx context.Context, d *Daemon, acc access, root string, compress bool, in *bufio.Reader, out io.Writer) error {
	bw := bufio.NewWriterSize(out, 256<<10)
	buf := make([]byte, 256<<10)
	for {
		if in.Buffered() == 0 {
			if err := bw.Flush(); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var req treeRequest
		if err := readTreeLine(in, &req); err != nil {
			return err
		}
		if req.End {
			if err := writeTreeLine(bw, treeHeader{End: true}); err != nil {
				return err
			}
			return bw.Flush()
		}
		if !safeRel(req.Rel) {
			if err := writeTreeLine(bw, treeHeader{Rel: req.Rel, Code: ErrVerbInvalidParams, Error: "the name " + echoName(req.Rel) + " is not inside the folder"}); err != nil {
				return err
			}
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(req.Rel))
		if err := sendTreeFile(d, acc, bw, p, req, compress, buf); err != nil {
			return err
		}
	}
}

// sendTreeFile writes one file's record. An error it returns is the stream's;
// a file it cannot read is a record that says so.
func sendTreeFile(d *Daemon, acc access, bw *bufio.Writer, p string, req treeRequest, compress bool, buf []byte) error {
	f, fi, verr, err := d.accOpenRead(acc, p)
	if verr != nil {
		return writeTreeLine(bw, treeHeader{Rel: req.Rel, Code: verr.Code, Error: verr.Message})
	}
	if err != nil {
		v := fileError("read", p, err)
		return writeTreeLine(bw, treeHeader{Rel: req.Rel, Code: v.Code, Error: v.Message})
	}
	defer func() { _ = f.Close() }()
	size := fi.Size()
	if req.MTime > 0 && (size != req.Size || fi.ModTime().UnixMilli() != req.MTime) {
		return writeTreeLine(bw, treeHeader{Rel: req.Rel, Code: ErrVerbSourceChanged, Error: echoName(req.Rel) + " changed after the copy listed it. Copy it again when it is not being written."})
	}
	z := compress && compressibleFile(f, p, size, 0)
	if err := writeTreeLine(bw, treeHeader{Rel: req.Rel, Kind: "file", Size: size, Perm: uint32(fi.Mode().Perm()), MTime: fi.ModTime().UnixMilli(), Z: z}); err != nil {
		return err
	}
	h := sha256.New()
	body := io.TeeReader(io.LimitReader(f, size), h)
	var w io.Writer = bw
	var fw interface{ Close() error }
	if z {
		f := getFlateWriter(bw)
		w, fw = f, f
		defer putFlateWriter(f)
	}
	n, rerr := io.CopyBuffer(w, body, buf)
	if rerr != nil {
		if _, ok := errors.AsType[*fs.PathError](rerr); !ok {
			// Not the file: the stream it writes to.
			return rerr
		}
	}
	trailer := treeTrailer{}
	if n < size {
		// The file got shorter while it was read. The record keeps its
		// length, and the trailer says to drop it.
		if _, err := io.CopyN(w, zeroReader{}, size-n); err != nil {
			return err
		}
		trailer = treeTrailer{Code: ErrVerbSourceChanged, Error: echoName(req.Rel) + " changed while it was copied. Copy it again when it is not being written."}
	}
	if fw != nil {
		if err := fw.Close(); err != nil {
			return err
		}
	}
	if trailer.Code == "" {
		if now, err := f.Stat(); err != nil || now.Size() != size || !now.ModTime().Equal(fi.ModTime()) {
			trailer = treeTrailer{Code: ErrVerbSourceChanged, Error: echoName(req.Rel) + " changed while it was copied. Copy it again when it is not being written."}
		} else {
			trailer.SHA256 = hex.EncodeToString(h.Sum(nil))
		}
	}
	return writeTreeLine(bw, trailer)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// serveTreeWrite takes records from in, puts each file in place under root,
// and acks each to out: the write side of a tree stream. partID names the
// parts, the copy's id. cs is the connection the stream came on: a write over
// a link is held to the link's roots, deny list and write limits for each
// file (linkWriteFS, claimLinkWrite), as an open-file-stream write is.
func serveTreeWrite(ctx context.Context, d *Daemon, acc access, root, partID string, in *bufio.Reader, out io.Writer) error {
	// The stream is one write stream of the link's, whatever the number of
	// files in it.
	releaseStream, verr := d.claimLinkStream(acc.cs)
	if verr != nil {
		_ = writeTreeLine(out, treeAck{End: true, Code: verr.Code, Error: verr.Message})
		return errors.New(verr.Message)
	}
	defer releaseStream()
	bw := bufio.NewWriterSize(out, 64<<10)
	buf := make([]byte, 256<<10)
	// The folders files were put in since their last sync.
	dirty := map[string]int{}
	syncDirs := func(fsys fileFS) {
		for dir := range dirty {
			syncDir(fsys, dir)
			delete(dirty, dir)
		}
	}
	var last fileFS
	// The files whose parts are written and wait to be synced and put in
	// place.
	var pending []*treePending
	var pendingBytes int64
	var pendingSince time.Time
	defer func() {
		for _, pf := range pending {
			_ = pf.f.Close()
			_ = pf.fsys.Remove(pf.confined)
			pf.release()
			pf.done()
			pf.fsys.Close()
		}
		syncDirs(last)
		last.Close()
	}()
	ack := func(a treeAck) error {
		if err := writeTreeLine(bw, a); err != nil {
			return err
		}
		if in.Buffered() == 0 {
			return bw.Flush()
		}
		return nil
	}
	// flush syncs the pending parts, puts each in place, and acks it.
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		batch := pending
		pending, pendingBytes = nil, 0
		syncPending(batch)
		var ackErr error
		for i, pf := range batch {
			a, dir := pf.finish(ctx, partID)
			if dir != "" {
				dirty[dir]++
			}
			if ackErr == nil {
				ackErr = writeTreeLine(bw, a)
			}
			if i == len(batch)-1 {
				// The newest root stays open for the folder syncs.
				last.Close()
				last = pf.fsys
			} else {
				pf.fsys.Close()
			}
		}
		if ackErr != nil {
			return ackErr
		}
		for dir, n := range dirty {
			if n >= treeDirSyncEvery {
				syncDir(last, dir)
				delete(dirty, dir)
			}
		}
		return bw.Flush()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if in.Buffered() == 0 {
			if err := bw.Flush(); err != nil {
				return err
			}
		}
		var hdr treeHeader
		if err := readTreeLine(in, &hdr); err != nil {
			return err
		}
		if hdr.End {
			if err := flush(); err != nil {
				return err
			}
			syncDirs(last)
			if err := writeTreeLine(bw, treeAck{End: true}); err != nil {
				return err
			}
			return bw.Flush()
		}
		if !safeRel(hdr.Rel) || hdr.Size < 0 {
			// The bytes that follow cannot be framed, so the stream ends.
			_ = writeTreeLine(bw, treeAck{Rel: hdr.Rel, Code: ErrVerbInvalidParams, Error: "the name " + echoName(hdr.Rel) + " is not inside the folder. Nothing outside the folder was written."})
			_ = bw.Flush()
			return errTreeStream
		}
		if hdr.Code != "" {
			// The reading side could not send this file.
			if err := ack(treeAck{Rel: hdr.Rel, Code: hdr.Code, Error: hdr.Error}); err != nil {
				return err
			}
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(hdr.Rel))
		if hdr.Kind == "dir" {
			fsys, verr := d.accWriteFS(acc, p)
			if verr == nil {
				last.Close()
				last = fsys
				if err := fsys.MkdirAll(fsys.confineDir(p), 0o755); err != nil {
					verr = fileError("make the folder", p, err)
				}
			}
			if verr != nil {
				if err := ack(treeAck{Rel: hdr.Rel, Code: verr.Code, Error: verr.Message}); err != nil {
					return err
				}
			}
			continue
		}
		if hdr.Kind != "file" {
			_ = writeTreeLine(bw, treeAck{Rel: hdr.Rel, Code: ErrVerbInvalidParams, Error: "a tree stream carries files and folders"})
			_ = bw.Flush()
			return errTreeStream
		}
		a, pf, err := takeTreeFile(d, acc, p, partID, hdr, in, buf)
		if err != nil {
			return err
		}
		if pf == nil {
			a.Rel = hdr.Rel
			if err := ack(a); err != nil {
				return err
			}
			continue
		}
		if len(pending) == 0 {
			pendingSince = time.Now()
		}
		pending = append(pending, pf)
		pendingBytes += hdr.Size
		// A batch that is full goes in place, and so does one the sender
		// has stopped adding to for a moment, so the copy's progress does
		// not stall behind a slow source.
		idle := in.Buffered() == 0 && time.Since(pendingSince) >= 250*time.Millisecond
		if len(pending) >= treeSyncBatch || pendingBytes >= treeSyncBytes || idle {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// treeSyncBatch and treeSyncBytes bound the files a tree write holds before it
// syncs them and puts them in place. A sync of each small file by itself cost
// half the time of a folder of 20,000 files; the files of a batch are synced
// side by side, which a disk's journal commits together.
const (
	treeSyncBatch   = 32
	treeSyncBytes   = 8 << 20
	treeSyncWorkers = 16
)

// treePending is a file of a tree write whose bytes are in its part, checked
// against the reading side's hash, and wait for the sync and the commit.
type treePending struct {
	rel, p    string
	hdr       treeHeader
	fsys      fileFS
	f         *os.File
	confined  string
	sum, want string
	werr      error
	release   func()
	done      func()
}

// finish syncs nothing: the caller synced the part. It closes the part and
// puts it in place, and returns the ack and the folder it landed in.
func (pf *treePending) finish(ctx context.Context, partID string) (treeAck, string) {
	defer pf.release()
	defer pf.done()
	werr := pf.werr
	if err := pf.f.Close(); werr == nil {
		werr = err
	}
	if werr != nil {
		_ = pf.fsys.Remove(pf.confined)
		v := fileError("write", pf.p, werr)
		return treeAck{Rel: pf.rel, Code: v.Code, Error: v.Message}, ""
	}
	conflict := pf.hdr.Conflict
	if conflict == "" {
		conflict = "fail"
	}
	final, _, skipped, verr := commitPart(ctx, pf.fsys, pf.fsys.confine(pf.p), partID, pf.want, conflict, pf.hdr.Perm, pf.hdr.MTime, commitOpts{known: pf.sum, label: pf.hdr.Label, noDirSync: true})
	if verr != nil {
		return treeAck{Rel: pf.rel, Code: verr.Code, Error: verr.Message, SHA256: pf.sum}, ""
	}
	dir := ""
	if !skipped {
		dir = filepath.Dir(final)
	}
	return treeAck{Rel: pf.rel, Path: final, SHA256: pf.sum, Skipped: skipped}, dir
}

// syncPending syncs the parts of a batch side by side, and keeps the first
// error of each.
func syncPending(batch []*treePending) {
	work := make(chan *treePending)
	var wg sync.WaitGroup
	for range min(treeSyncWorkers, len(batch)) {
		wg.Go(func() {
			for pf := range work {
				if pf.werr == nil {
					pf.werr = syncData(pf.f)
				}
			}
		})
	}
	for _, pf := range batch {
		work <- pf
	}
	close(work)
	wg.Wait()
}

// takeTreeFile reads one file's bytes and trailer from in into its part. An
// error it returns is the stream's. A file that could not land is an ack that
// says why, and its bytes are read and dropped so the next record is in step.
// A file that did land in its part is a treePending, which the caller syncs
// and puts in place with the rest of its batch.
func takeTreeFile(d *Daemon, acc access, p, partID string, hdr treeHeader, in *bufio.Reader, buf []byte) (treeAck, *treePending, error) {
	part := partPath(p, partID)
	var src io.Reader = in
	var fr io.ReadCloser
	if hdr.Z {
		fr = getFlateReader(in)
		defer putFlateReader(fr)
		src = fr
	}
	body := io.LimitReader(src, hdr.Size)
	drop := func(code, msg string) (treeAck, *treePending, error) {
		if _, err := io.CopyBuffer(io.Discard, body, buf); err != nil {
			return treeAck{}, nil, err
		}
		if fr != nil {
			if err := flateEnd(fr); err != nil {
				return treeAck{}, nil, err
			}
		}
		var t treeTrailer
		if err := readTreeLine(in, &t); err != nil {
			return treeAck{}, nil, err
		}
		return treeAck{Code: code, Error: msg}, nil, nil
	}
	if verr := checkKeepLabel(hdr.Label); verr != nil {
		return drop(verr.Code, verr.Message)
	}
	cs := acc.cs
	fsys, verr := d.accWriteFS(acc, p, part)
	if verr != nil {
		return drop(verr.Code, verr.Message)
	}
	keep := false
	defer func() {
		if !keep {
			fsys.Close()
		}
	}()
	dir := filepath.Dir(p)
	if err := fsys.MkdirAll(fsys.confineDir(dir), 0o755); err != nil {
		v := fileError("write", dir, err)
		return drop(v.Code, v.Message)
	}
	confined := fsys.confine(part)
	// The room is claimed before the part is made, so a write that does not
	// fit leaves nothing behind.
	release, verr := d.claimLinkWriteIn(cs, fsys.confine(p), confined, 0, hdr.Size, false)
	if verr != nil {
		return drop(verr.Code, verr.Message)
	}
	done := func() {
		if cs != nil && cs.viaLink {
			d.linkFiles.partDone(cs, confined)
		}
	}
	fail := func() {
		release()
		done()
	}
	f, err := openPart(fsys, confined)
	if err != nil {
		fail()
		v := fileError("write", part, err)
		return drop(v.Code, v.Message)
	}
	d.transfers.parts.note(filepath.Dir(confined))
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		fail()
		v := fileError("write", part, err)
		return drop(v.Code, v.Message)
	}
	h := sha256.New()
	sink := &firstErrWriter{w: f}
	gone := func(err error) (treeAck, *treePending, error) {
		_ = f.Close()
		_ = fsys.Remove(confined)
		fail()
		return treeAck{}, nil, err
	}
	n, err := io.CopyBuffer(io.MultiWriter(sink, h), body, buf)
	if err != nil {
		return gone(err)
	}
	if n < hdr.Size {
		return gone(io.ErrUnexpectedEOF)
	}
	if fr != nil {
		if err := flateEnd(fr); err != nil {
			return gone(err)
		}
	}
	var t treeTrailer
	if err := readTreeLine(in, &t); err != nil {
		return gone(err)
	}
	if t.Code != "" && sink.err == nil {
		_ = f.Close()
		_ = fsys.Remove(confined)
		fail()
		return treeAck{Code: t.Code, Error: t.Error}, nil, nil
	}
	keep = true
	return treeAck{}, &treePending{
		rel: hdr.Rel, p: p, hdr: hdr, fsys: fsys, f: f, confined: confined,
		sum: hex.EncodeToString(h.Sum(nil)), want: t.SHA256, werr: sink.err,
		release: release, done: done,
	}, nil
}

// firstErrWriter keeps the first write error and takes the rest of the bytes
// without writing them, so a full disk fails one file and the stream stays
// in step.
type firstErrWriter struct {
	w   io.Writer
	err error
}

func (f *firstErrWriter) Write(p []byte) (int, error) {
	if f.err == nil {
		if _, err := f.w.Write(p); err != nil {
			f.err = err
		}
	}
	return len(p), nil
}

// verbOpenTreeStream turns the connection into a tree stream.
func (d *Daemon) verbOpenTreeStream(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Root     string `json:"root"`
		Mode     string `json:"mode"`
		PartID   string `json:"part_id"`
		Compress bool   `json:"compress"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkPartID(p.PartID); verr != nil {
		return nil, verr
	}
	root, verr := expandPath(p.Root)
	if verr != nil {
		return nil, verr
	}
	switch p.Mode {
	case "read":
		real, verr := d.linkWalkRoot(cs, root)
		if verr != nil {
			return nil, verr
		}
		fi, err := os.Stat(real)
		if err != nil {
			return nil, fileError("read", root, err)
		}
		if !fi.IsDir() {
			return nil, invalidParam("root", echoName(root)+" is not a folder")
		}
		cs.takeover = func(br *bufio.Reader) {
			_ = cs.conn.SetDeadline(time.Time{})
			_ = serveTreeRead(d.ctx, d, access{cs: cs}, real, p.Compress, br, cs.conn)
			_ = cs.conn.Close()
		}
	case "write":
		if p.PartID == "" {
			return nil, invalidParam("part_id", "a tree write needs the copy's part id")
		}
		// The check is for a file inside root, not root itself: a link may
		// write into a root it may not change, such as the home folder with
		// files_roots = ["~"]. Each file is checked again as it lands.
		fsys, verr := d.linkWriteFS(cs, filepath.Join(root, ".tuios-tree"))
		if verr != nil {
			return nil, verr
		}
		fi, err := fsys.Stat(fsys.confineDir(root))
		fsys.Close()
		if err != nil {
			return nil, fileError("write", root, err)
		}
		if !fi.IsDir() {
			return nil, invalidParam("root", echoName(root)+" is not a folder")
		}
		cs.takeover = func(br *bufio.Reader) {
			_ = cs.conn.SetDeadline(time.Time{})
			if err := serveTreeWrite(d.ctx, d, access{cs: cs}, root, p.PartID, br, cs.conn); err != nil && !errors.Is(err, io.EOF) {
				LogBasic("A tree stream into %s ended: %v", root, err)
			}
			_ = cs.conn.Close()
		}
	default:
		return nil, invalidParam("mode", "mode is read or write", "read", "write")
	}
	return map[string]any{"root": root, "mode": p.Mode}, nil
}

// FileCheck is one file as file-check describes it.
type FileCheck struct {
	Rel    string `json:"rel"`
	Exists bool   `json:"exists"`
	Kind   string `json:"kind,omitempty"`
	Size   int64  `json:"size,omitempty"`
	MTime  int64  `json:"mtime,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// checkFiles describes each file under root, in order, and hashes each
// regular one when hash is set, up to fileCheckHashBytes in one call.
//
// For cs over a link a hash is a read, so each file is opened under the link
// read rules (openLinkRead) and a refused file is reported without its sum.
func (d *Daemon) checkFiles(ctx context.Context, acc access, root string, rels []string, hash bool) ([]FileCheck, error) {
	out := make([]FileCheck, len(rels))
	var hashed int64
	for i, rel := range rels {
		out[i].Rel = rel
		if !safeRel(rel) {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		// What a path is, or that it is there, is read like its bytes, as
		// file-stat judges it: a link learns nothing of a file it may not
		// read.
		if d.accCheckRead(acc, p) != nil {
			continue
		}
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		out[i].Exists, out[i].Kind, out[i].Size, out[i].MTime = true, kindOf(fi.Mode()), fi.Size(), fi.ModTime().UnixMilli()
		if hash && fi.Mode().IsRegular() && hashed+fi.Size() <= fileCheckHashBytes {
			f, _, verr, err := d.accOpenRead(acc, p)
			if verr != nil {
				continue
			}
			var sum string
			var n int64
			if err == nil {
				sum, n, err = hashOpen(ctx, f, 0, -1)
				_ = f.Close()
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			hashed += n
			out[i].SHA256 = sum
		}
	}
	return out, nil
}

// verbFileCheck describes many files under a folder at once, for a copy that
// leaves out what is there already.
func (d *Daemon) verbFileCheck(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Root string   `json:"root"`
		Rels []string `json:"rels"`
		Hash bool     `json:"hash"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Rels) > fileCheckBatch {
		return nil, invalidParam("rels", fmt.Sprintf("at most %d files in one call", fileCheckBatch))
	}
	root, verr := expandPath(p.Root)
	if verr != nil {
		return nil, verr
	}
	out, err := d.checkFiles(d.ctx, access{cs: cs}, root, p.Rels, p.Hash)
	if err != nil {
		return nil, fileError("check", root, err)
	}
	return map[string]any{"root": root, "entries": out}, nil
}

// ---- the middle -------------------------------------------------------------

// treeConn is one end of a tree stream as the middle sees it: w takes what
// the middle sends, r gives what the end answers.
type treeConn struct {
	w      io.Writer
	r      *bufio.Reader
	remote bool
	close  func()
}

// openTreeRead opens the read side of a tree stream of the folder root.
func (e fileEnd) openTreeRead(ctx context.Context, root string, compress bool) (*treeConn, error) {
	if e.local() {
		reqR, reqW := io.Pipe()
		recR, recW := io.Pipe()
		go func() {
			err := serveTreeRead(ctx, e.d, e.acc, root, false, bufio.NewReaderSize(reqR, 64<<10), recW)
			_ = recW.CloseWithError(err)
			_ = reqR.CloseWithError(err)
		}()
		return &treeConn{w: reqW, r: bufio.NewReaderSize(recR, 256<<10), close: func() {
			_ = reqW.Close()
			_ = recR.Close()
		}}, nil
	}
	c, err := e.d.dialHostFiles(ctx, e.host, true)
	if err != nil {
		return nil, err
	}
	c.count(e.wire)
	if _, err := c.call(ctx, "open-tree-stream", map[string]any{"root": root, "mode": "read", "compress": compress}, 30*time.Second); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &treeConn{w: c, r: c.br, remote: true, close: func() { _ = c.Close() }}, nil
}

// openTreeWrite opens the write side of a tree stream into the folder root.
func (e fileEnd) openTreeWrite(ctx context.Context, root, partID string) (*treeConn, error) {
	if e.local() {
		recR, recW := io.Pipe()
		ackR, ackW := io.Pipe()
		go func() {
			err := serveTreeWrite(ctx, e.d, e.acc, root, partID, bufio.NewReaderSize(recR, 256<<10), ackW)
			_ = ackW.CloseWithError(err)
			_ = recR.CloseWithError(err)
		}()
		return &treeConn{w: recW, r: bufio.NewReaderSize(ackR, 64<<10), close: func() {
			_ = recW.Close()
			_ = ackR.Close()
		}}, nil
	}
	c, err := e.d.dialHostFiles(ctx, e.host, true)
	if err != nil {
		return nil, err
	}
	c.count(e.wire)
	if _, err := c.call(ctx, "open-tree-stream", map[string]any{"root": root, "mode": "write", "part_id": partID}, 30*time.Second); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &treeConn{w: c, r: c.br, remote: true, close: func() { _ = c.Close() }}, nil
}

// treeCopy sends the folders dirs and the small files of a folder copy from
// src's srcRoot to dst's dstRoot in one tree stream each way. It returns the
// files that did not land, with why; a file that landed is recorded in the
// job as it is acked. An error it returns is the stream's, which the job
// waits out like a dropped link.
func (m *transferManager) treeCopy(ctx context.Context, j *transferJob, src, dst fileEnd, srcRoot, dstRoot string, dirs []string, files []fileTask, perm func(WalkEntry) uint32, mtime func(WalkEntry) int64) ([]treeResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	compressIn := !src.local() && j.compressionWanted()
	in, err := src.openTreeRead(ctx, srcRoot, compressIn)
	if err != nil {
		return nil, err
	}
	defer in.close()
	out, err := dst.openTreeWrite(ctx, dstRoot, j.id)
	if err != nil {
		return nil, err
	}
	defer out.close()
	stop := context.AfterFunc(ctx, func() {
		in.close()
		out.close()
	})
	defer stop()

	byRel := make(map[string]fileTask, len(files))
	for _, f := range files {
		byRel[f.e.Rel] = f
	}
	each := j.opts.Each
	if each == "" {
		each = j.opts.Conflict
	}

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	// The requests, all at once: the read side answers them in order, and
	// the window of the link holds back the ones it has not reached.
	wg.Go(func() {
		bw := bufio.NewWriterSize(in.w, 64<<10)
		for _, f := range files {
			if err := writeTreeLine(bw, treeRequest{Rel: f.e.Rel, Size: f.e.Size, MTime: f.e.MTime}); err != nil {
				errs <- err
				return
			}
		}
		if err := writeTreeLine(bw, treeRequest{End: true}); err != nil {
			errs <- err
			return
		}
		if err := bw.Flush(); err != nil {
			errs <- err
		}
	})
	// The acks, as the write side puts each file in place.
	var mu sync.Mutex
	var failed []treeResult
	// answered is every file the write side acked or the read side said it
	// could not send. A file in neither did not land, whatever the stream
	// said at its end.
	answered := make(map[string]bool, len(files))
	// relayed is every file the read side sent and this daemon passed on, so
	// a file that was never answered names the side that stopped.
	relayed := make(map[string]bool, len(files))
	acked := make(chan struct{})
	go func() {
		defer close(acked)
		for {
			var a treeAck
			if err := readTreeLine(out.r, &a); err != nil {
				errs <- err
				return
			}
			if a.End {
				if a.Code != "" {
					// The write side refused the whole stream.
					errs <- &VerbCallError{Code: a.Code, Message: a.Error}
				}
				return
			}
			mu.Lock()
			answered[a.Rel] = true
			mu.Unlock()
			if a.Code != "" {
				mu.Lock()
				failed = append(failed, treeResult{rel: a.Rel, code: a.Code, msg: a.Error})
				mu.Unlock()
				if a.Code != ErrVerbHashMismatch {
					m.fileEvent(j, a.Rel, "failed", "", a.Code, a.Error)
				}
				continue
			}
			if _, ok := byRel[a.Rel]; !ok {
				continue
			}
			m.noteFileResult(j, a.Rel, each, a.Skipped, a.SHA256)
		}
	}()

	// The records, from the read side to the write side.
	send := func() error {
		bw := bufio.NewWriterSize(out.w, 256<<10)
		for _, d := range dirs {
			if err := writeTreeLine(bw, treeHeader{Rel: d, Kind: "dir"}); err != nil {
				return err
			}
		}
		buf := make([]byte, 256<<10)
		for {
			if in.r.Buffered() == 0 {
				if err := bw.Flush(); err != nil {
					return err
				}
			}
			var hdr treeHeader
			if err := readTreeLine(in.r, &hdr); err != nil {
				return err
			}
			if hdr.End {
				if err := writeTreeLine(bw, treeHeader{End: true}); err != nil {
					return err
				}
				return bw.Flush()
			}
			task, ok := byRel[hdr.Rel]
			if !ok || hdr.Size < 0 {
				return fmt.Errorf("%w: %s sent a file the copy did not ask for", errTreeStream, src.name())
			}
			if hdr.Code != "" {
				mu.Lock()
				answered[hdr.Rel] = true
				failed = append(failed, treeResult{rel: hdr.Rel, code: hdr.Code, msg: hdr.Error})
				mu.Unlock()
				m.fileEvent(j, hdr.Rel, "failed", "", hdr.Code, hdr.Error)
				continue
			}
			if err := m.relayTreeFile(ctx, j, in, out.remote, bw, hdr, task, perm, mtime, buf); err != nil {
				return err
			}
			mu.Lock()
			relayed[hdr.Rel] = true
			mu.Unlock()
		}
	}
	if err := send(); err != nil {
		cancel()
		wg.Wait()
		return nil, err
	}
	select {
	case <-acked:
	case err := <-errs:
		cancel()
		wg.Wait()
		return nil, err
	case <-ctx.Done():
		wg.Wait()
		return nil, ctx.Err()
	}
	wg.Wait()
	select {
	case err := <-errs:
		return nil, err
	default:
	}
	// A far side that ended the stream early, or acked a file the copy did
	// not send, must not make the copy look done.
	mu.Lock()
	defer mu.Unlock()
	for _, f := range files {
		if !answered[f.e.Rel] {
			// Once more would meet the same far side, so the copy ends.
			msg := fmt.Sprintf("%s ended the copy before it put %s in place. The copy is not done.", dst.name(), echoName(f.e.Rel))
			if !relayed[f.e.Rel] {
				msg = fmt.Sprintf("%s ended the copy before it sent %s. The copy is not done.", src.name(), echoName(f.e.Rel))
			}
			return nil, permanent(ErrVerbInternal, msg)
		}
	}
	return failed, nil
}

// relayTreeFile passes one file's record from the read side to the write
// side, counting its bytes for the progress.
func (m *transferManager) relayTreeFile(ctx context.Context, j *transferJob, in *treeConn, toRemote bool, bw *bufio.Writer, hdr treeHeader, task fileTask, perm func(WalkEntry) uint32, mtime func(WalkEntry) int64, buf []byte) error {
	var src io.Reader = in.r
	var fr io.ReadCloser
	if hdr.Z {
		fr = getFlateReader(in.r)
		defer putFlateReader(fr)
		src = fr
	}
	body := io.LimitReader(src, hdr.Size)
	zOut := false
	if toRemote && j.compressionWanted() && hdr.Size >= compressMinSize {
		if in.remote {
			zOut = hdr.Z
		} else if compressibleName(hdr.Rel) {
			sample, whole, err := sampleReader(body, hdr.Size)
			if err != nil {
				return err
			}
			zOut = compressibleSample(sample)
			body = whole
		}
	}
	out := treeHeader{
		Rel: hdr.Rel, Kind: "file", Size: hdr.Size, Perm: perm(task.e), MTime: mtime(task.e), Z: zOut,
		Conflict: task.action,
	}
	if task.action == "keep-both" {
		out.Label = j.opts.Label
	}
	if err := writeTreeLine(bw, out); err != nil {
		return err
	}
	j.set(func(j *transferJob) {
		j.current, j.curBase, j.curSize = hdr.Rel, j.done.Load(), hdr.Size
	})
	var w io.Writer = bw
	var fw interface{ Close() error }
	if zOut {
		f := getFlateWriter(bw)
		w, fw = f, f
		defer putFlateWriter(f)
	}
	pw := &progressWriter{w: w, j: j, m: m, ctx: ctx}
	n, err := io.CopyBuffer(pw, body, buf)
	if err != nil {
		return err
	}
	if n < hdr.Size {
		return io.ErrUnexpectedEOF
	}
	if fw != nil {
		if err := fw.Close(); err != nil {
			return err
		}
	}
	if fr != nil {
		if err := flateEnd(fr); err != nil {
			return err
		}
	}
	var t treeTrailer
	if err := readTreeLine(in.r, &t); err != nil {
		return err
	}
	return writeTreeLine(bw, t)
}

func treeVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"file-check": {
			description: "Describe many files under a folder on this machine at once, for a copy that leaves out what is there already: whether each is there, its kind, size and time, and with hash its sha256. At most 2000 files, and 512 MiB hashed, in one call.",
			params: []verbParam{
				{Name: "root", Type: "string", Required: true, Description: "The folder. An absolute path, or one that starts with ~."},
				{Name: "rels", Type: "[]string", Required: true, Description: "The files, as paths under the folder, slash separated."},
				{Name: "hash", Type: "bool", Description: "Hash each regular file."},
			},
			returns: []verbParam{
				{Name: "entries", Type: "[]object", Description: "One per file, in order: rel, exists, kind (file, dir, symlink, other), size, mtime (Unix ms), sha256 when hashed."},
			},
			examples: []string{`{"id":1,"verb":"file-check","params":{"root":"~/src","rels":["a.go","b/c.go"],"hash":true}}`},
			handler:  (*Daemon).verbFileCheck,
		},
		"open-tree-stream": {
			description: "Turn this connection into a tree stream: many files of a folder in one stream. mode read: the caller writes one line {rel, size, mtime} per file and {end}, and gets one record per file and {end}. mode write: the caller writes records, and gets one ack {rel, path, sha256, skipped} or {rel, code, error} per file and {end}. A record is a header line {rel, kind, size, perm, mtime, z, conflict, label}, the bytes (one flate stream when z), and a trailer line {sha256}. docs/protocol.md has the whole format.",
			params: []verbParam{
				{Name: "root", Type: "string", Required: true, Description: "The folder the paths are under. An absolute path, or one that starts with ~."},
				{Name: "mode", Type: "string", Required: true, Description: "read or write.", Accepted: []string{"read", "write"}},
				{Name: "part_id", Type: "string", Description: "write: the copy's part id, which names the parts."},
				{Name: "compress", Type: "bool", Description: "read: send a file as flate when a sample of it compresses well."},
			},
			returns:  []verbParam{{Name: "root", Type: "string", Description: "The folder, made absolute."}},
			examples: []string{`{"id":1,"verb":"open-tree-stream","params":{"root":"~/src","mode":"read","compress":true}}`},
			handler:  (*Daemon).verbOpenTreeStream,
		},
	}
}
