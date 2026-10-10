package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// The file verbs: what an explorer needs from the machine the files are on.
//
// read-dir is the rail's listing and stays as it is: names and a folder flag,
// for a section a few cells wide. The explorer needs what a file manager
// shows, so these verbs are a family of their own, each named file-*, and each
// acts on this machine only. A client reaches another machine's files by
// running the same verbs there, through open-host-connection, so the far
// machine's link policy decides what the hub may do (link_policy.go): reading
// a listing is list, and reading or changing a file's bytes is write.
//
// open-file-stream moves bytes. After its reply the connection carries raw
// file bytes, in one direction, with the count given up front, so neither end
// needs framing of its own. A write lands in a part file beside the
// destination, owner only; file-commit checks the part's hash and renames it
// into place. That is the stage, hash and rename every transfer goes through,
// and a part left behind by a dropped link is where the next try resumes.

// Error codes of the file verbs.
const (
	// ErrVerbNoFile is a path that does not exist.
	ErrVerbNoFile = "no_file"
	// ErrVerbFileExists is a destination that exists, for a call that was
	// told not to replace it.
	ErrVerbFileExists = "file_exists"
	// ErrVerbNoPermission is a path this machine's user may not read or
	// change.
	ErrVerbNoPermission = "no_permission"
	// ErrVerbHashMismatch is a part file whose bytes are not the bytes the
	// sender hashed. The part is removed and the copy starts again.
	ErrVerbHashMismatch = "hash_mismatch"
	// ErrVerbCrossDevice is a rename between two file systems, which only a
	// copy can do.
	ErrVerbCrossDevice = "cross_device"
	// ErrVerbDiskFull is a write the disk had no room for.
	ErrVerbDiskFull = "disk_full"
)

// partSuffix names the file a copy writes into before it is checked. A copy
// that gives its part id writes ".NAME.tuios-part-ID", so two copies to one
// path each write a part of their own. A caller that gives none gets
// ".NAME.tuios-part", as before part ids.
const partSuffix = ".tuios-part"

// partIDMax bounds a part id. It names a file, so it is short hex.
const partIDMax = 32

// checkPartID refuses a part id that is not short hex.
func checkPartID(id string) *verbError {
	if len(id) > partIDMax {
		return invalidParam("part_id", "a part id is at most 32 hex digits")
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return invalidParam("part_id", "a part id is hex digits, 0-9 and a-f")
		}
	}
	return nil
}

// isPartName reports whether a file name is a copy's part file.
func isPartName(name string) bool {
	return strings.HasPrefix(name, ".") && (strings.HasSuffix(name, partSuffix) || strings.Contains(name, partSuffix+"-"))
}

// fileListMax bounds one page of a listing, and fileScanMax how many names a
// listing reads from one directory before it says the folder is too large to
// sort.
const (
	fileListDefault = 500
	fileListMax     = 5000
	fileScanMax     = 50000
)

// fileReadMax bounds one file-read, which carries the bytes base64 in a line.
const fileReadMax = 4 << 20

// FileInfo is one file as the file verbs describe it.
type FileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Kind is file, dir, symlink or other. A link's target kind is in
	// LinkKind, so a link to a folder can be opened as one.
	Kind     string `json:"kind"`
	LinkKind string `json:"link_kind,omitempty"`
	Link     string `json:"link,omitempty"`
	Size     int64  `json:"size"`
	// MTime is the modification time in Unix milliseconds.
	MTime int64 `json:"mtime"`
	// Mode is the permission string ls prints: drwxr-xr-x. Perm is the
	// permission bits as a number, for a copy to keep.
	Mode   string `json:"mode"`
	Perm   uint32 `json:"perm"`
	Hidden bool   `json:"hidden,omitempty"`
}

func kindOf(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "dir"
	case m&fs.ModeSymlink != 0:
		return "symlink"
	case m.IsRegular():
		return "file"
	default:
		return "other"
	}
}

// describe fills a FileInfo from an lstat, following a link one step for its
// target's kind.
func describe(path string, fi fs.FileInfo) FileInfo {
	out := FileInfo{
		Name:   fi.Name(),
		Path:   path,
		Kind:   kindOf(fi.Mode()),
		Size:   fi.Size(),
		MTime:  fi.ModTime().UnixMilli(),
		Mode:   fi.Mode().String(),
		Perm:   uint32(fi.Mode().Perm()),
		Hidden: strings.HasPrefix(fi.Name(), "."),
	}
	if out.Kind == "symlink" {
		if target, err := os.Readlink(path); err == nil {
			out.Link = target
		}
		if ti, err := os.Stat(path); err == nil {
			out.LinkKind = kindOf(ti.Mode())
			if ti.Mode().IsRegular() {
				out.Size = ti.Size()
			}
		}
	}
	return out
}

// expandPath turns ~ and an empty path into this machine's home, and makes the
// path absolute and clean. Every file verb takes paths through it, so "~/src"
// means the same folder whichever machine is asked.
func expandPath(p string) (string, *verbError) {
	if p == "" || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", newVerbError(ErrVerbInternal, "this machine has no home folder")
		}
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", newVerbError(ErrVerbInternal, "this machine has no home folder")
		}
		p = filepath.Join(home, p[2:])
	}
	if strings.ContainsRune(p, 0) {
		return "", invalidParam("path", "a path cannot hold a NUL byte")
	}
	if !filepath.IsAbs(p) {
		return "", invalidParam("path", "give an absolute path, or one that starts with ~: "+echoName(p))
	}
	return filepath.Clean(p), nil
}

// errNotRegular is a path that is a pipe, a device or a socket where a file's
// bytes were asked for.
var errNotRegular = errors.New("not a regular file")

// openRegular opens a file to read its bytes, and refuses anything that is
// not a regular file. The open does not wait: opening a named pipe for
// reading blocks until something writes to it, and a device such as
// /dev/zero never ends, so either would hold the verb, and a hash of it a
// CPU, for as long as the daemon runs.
func openRegular(path string) (*os.File, fs.FileInfo, error) {
	return openRegularIn(fileFS{}, path)
}

// openRegularIn is openRegular through fsys.
func openRegularIn(fsys fileFS, path string) (*os.File, fs.FileInfo, error) {
	f, err := fsys.OpenFile(path, os.O_RDONLY|oNonBlock, 0)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		if fi.IsDir() {
			return nil, fi, &fs.PathError{Op: "read", Path: path, Err: syscall.EISDIR}
		}
		return nil, fi, &fs.PathError{Op: "read", Path: path, Err: errNotRegular}
	}
	return f, fi, nil
}

// fileError turns a file system error into the verb error a person can act on.
func fileError(what, path string, err error) *verbError {
	switch {
	case errors.Is(err, errNotRegular):
		return newVerbError(ErrVerbInvalidParams, what+": "+echoName(path)+" is not a regular file, so tuios does not read or write it")
	case errors.Is(err, syscall.EISDIR):
		return newVerbError(ErrVerbInvalidParams, what+": "+echoName(path)+" is a folder")
	case errors.Is(err, fs.ErrNotExist):
		return newVerbError(ErrVerbNoFile, what+": "+echoName(path)+" does not exist")
	case errors.Is(err, fs.ErrPermission):
		return newVerbError(ErrVerbNoPermission, what+": no permission for "+echoName(path))
	case errors.Is(err, fs.ErrExist):
		return newVerbError(ErrVerbFileExists, what+": "+echoName(path)+" already exists")
	case errors.Is(err, syscall.EXDEV):
		return newVerbError(ErrVerbCrossDevice, what+": "+echoName(path)+" is on another disk")
	case errors.Is(err, syscall.ENOSPC):
		return newVerbError(ErrVerbDiskFull, what+": the disk is full")
	default:
		return newVerbError(ErrVerbInternal, what+": "+err.Error())
	}
}

// openPart opens the part file of a copy to write it, owner only. It never
// follows a link: in a folder that others can write to, a link put where the
// part goes would turn the copy into a write to the link's target. A part
// that is not a regular file is refused for the same reason.
//
// O_NOFOLLOW is not enough on its own: an os.Root follows a relative link
// inside the root whatever the flags say. So the file that was opened must
// also be the file that is at the part's name now, and a regular one.
func openPart(fsys fileFS, part string) (*os.File, error) {
	notRegular := &fs.PathError{Op: "write", Path: part, Err: errNotRegular}
	f, err := fsys.OpenFile(part, os.O_CREATE|os.O_WRONLY|oNoFollow|oNonBlock, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, notRegular
		}
		if li, lerr := fsys.Lstat(part); lerr == nil && !li.Mode().IsRegular() {
			return nil, notRegular
		}
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	li, err := fsys.Lstat(part)
	if err != nil || !fi.Mode().IsRegular() || !li.Mode().IsRegular() || !os.SameFile(fi, li) {
		_ = f.Close()
		return nil, notRegular
	}
	return f, nil
}

// partPath is the part file a copy to dst writes into: hidden, beside it, and
// named by the copy's part id when it has one.
func partPath(dst, id string) string {
	name := "." + filepath.Base(dst) + partSuffix
	if id != "" {
		name += "-" + id
	}
	return filepath.Join(filepath.Dir(dst), name)
}

// verbFileStat describes one path.
func (d *Daemon) verbFileStat(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path   string `json:"path"`
		Part   bool   `json:"part"`
		PartID string `json:"part_id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkPartID(p.PartID); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	target := path
	if p.Part {
		target = partPath(path, p.PartID)
	}
	fi, err := os.Lstat(target)
	if err != nil {
		if p.Part && errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"path": path, "exists": false, "size": 0}, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"path": path, "exists": false}, nil
		}
		return nil, fileError("stat", path, err)
	}
	info := describe(target, fi)
	return map[string]any{"path": path, "exists": true, "info": info, "size": info.Size}, nil
}

// verbFileList lists a folder a page at a time: folders first, then names in
// natural order, with the details a file manager shows.
func (d *Daemon) verbFileList(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Dir    string `json:"dir"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
		Hidden *bool  `json:"hidden"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	dir, verr := expandPath(p.Dir)
	if verr != nil {
		return nil, verr
	}
	limit := p.Limit
	if limit <= 0 {
		limit = fileListDefault
	}
	limit = min(limit, fileListMax)
	showHidden := p.Hidden == nil || *p.Hidden

	f, err := os.Open(dir)
	if err != nil {
		return nil, fileError("list", dir, err)
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(fileScanMax + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fileError("list", dir, err)
	}
	capped := len(names) > fileScanMax
	if capped {
		names = names[:fileScanMax]
	}
	entries := make([]FileInfo, 0, len(names))
	for _, name := range names {
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		// A part file is a copy in flight, not a file of the folder's own.
		if isPartName(name) {
			continue
		}
		full := filepath.Join(dir, name)
		fi, err := os.Lstat(full)
		if err != nil {
			continue
		}
		entries = append(entries, describe(full, fi))
	}
	// Names in one folder are unique, so the sort need not be stable, and an
	// unstable one is several times faster on a large folder.
	slices.SortFunc(entries, func(a, b FileInfo) int {
		da, db := a.isDirLike(), b.isDirLike()
		switch {
		case da != db && da:
			return -1
		case da != db:
			return 1
		case a.Name == b.Name:
			return 0
		case naturalLess(a.Name, b.Name):
			return -1
		}
		return 1
	})
	total := len(entries)
	start := min(max(p.Offset, 0), total)
	end := min(start+limit, total)
	out := map[string]any{
		"dir":     dir,
		"entries": entries[start:end],
		"total":   total,
		"capped":  capped,
	}
	if parent := filepath.Dir(dir); parent != dir {
		out["parent"] = parent
	}
	if end < total {
		out["next"] = end
	}
	if home, err := os.UserHomeDir(); err == nil {
		out["home"] = home
	}
	return out, nil
}

func (f FileInfo) isDirLike() bool {
	return f.Kind == "dir" || (f.Kind == "symlink" && f.LinkKind == "dir")
}

// naturalLess orders names as a person reads them: case is ignored, and a run
// of digits compares as a number, so "file2" comes before "file10". It reads
// the names in place: a listing sorts tens of thousands of them, and a copy of
// each name as runes per comparison was most of a large folder's listing time.
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, wa := utf8.DecodeRuneInString(a[i:])
		cb, wb := utf8.DecodeRuneInString(b[j:])
		if unicode.IsDigit(ca) && unicode.IsDigit(cb) {
			si := i
			for i < len(a) {
				r, w := utf8.DecodeRuneInString(a[i:])
				if !unicode.IsDigit(r) {
					break
				}
				i += w
			}
			sj := j
			for j < len(b) {
				r, w := utf8.DecodeRuneInString(b[j:])
				if !unicode.IsDigit(r) {
					break
				}
				j += w
			}
			na := strings.TrimLeft(a[si:i], "0")
			nb := strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		la, lb := unicode.ToLower(ca), unicode.ToLower(cb)
		if la != lb {
			return la < lb
		}
		i += wa
		j += wb
	}
	ra, rb := utf8.RuneCountInString(a[i:]), utf8.RuneCountInString(b[j:])
	if ra != rb {
		return ra < rb
	}
	return a < b
}

// verbFileRead returns a range of a file's bytes.
func (d *Daemon) verbFileRead(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Length int64  `json:"length"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	length := p.Length
	if length <= 0 || length > fileReadMax {
		length = fileReadMax
	}
	f, fi, err := openRegular(path)
	if errors.Is(err, syscall.EISDIR) {
		return nil, invalidParam("path", echoName(path)+" is a folder: list it with file-list")
	}
	if err != nil {
		return nil, fileError("read", path, err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, max(p.Offset, 0))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fileError("read", path, err)
	}
	return map[string]any{
		"path":    path,
		"offset":  p.Offset,
		"size":    fi.Size(),
		"eof":     p.Offset+int64(n) >= fi.Size(),
		"content": base64.StdEncoding.EncodeToString(buf[:n]),
	}, nil
}

// verbFileMkdir makes a folder and its parents.
func (d *Daemon) verbFileMkdir(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path string `json:"path"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	fsys, verr := d.linkWriteFS(cs, path)
	if verr != nil {
		return nil, verr
	}
	defer fsys.Close()
	if err := fsys.MkdirAll(fsys.confineDir(path), 0o755); err != nil {
		return nil, fileError("make the folder", path, err)
	}
	return map[string]any{"path": path}, nil
}

// verbFileRename moves a path on this machine. It never replaces a file that
// is there unless told to.
func (d *Daemon) verbFileRename(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Replace bool   `json:"replace"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	from, verr := expandPath(p.From)
	if verr != nil {
		return nil, verr
	}
	to, verr := expandPath(p.To)
	if verr != nil {
		return nil, verr
	}
	if from == to {
		return map[string]any{"from": from, "to": to}, nil
	}
	if strings.HasPrefix(to+string(filepath.Separator), from+string(filepath.Separator)) {
		return nil, invalidParam("to", "a folder cannot move into itself")
	}
	fsys, verr := d.linkWriteFS(cs, from, to)
	if verr != nil {
		return nil, verr
	}
	defer fsys.Close()
	if !p.Replace {
		if _, err := fsys.Lstat(fsys.confine(to)); err == nil {
			return nil, newVerbError(ErrVerbFileExists, "move: "+echoName(to)+" already exists")
		}
	}
	if err := fsys.Rename(fsys.confine(from), fsys.confine(to)); err != nil {
		return nil, fileError("move", from, err)
	}
	return map[string]any{"from": from, "to": to}, nil
}

// verbFileRemove deletes a path. A folder with files in it needs recursive.
func (d *Daemon) verbFileRemove(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	if home, err := os.UserHomeDir(); err == nil && (path == home || path == "/") {
		return nil, invalidParam("path", "tuios does not remove "+echoName(path))
	}
	fsys, verr := d.linkWriteFS(cs, path)
	if verr != nil {
		return nil, verr
	}
	defer fsys.Close()
	var err error
	if p.Recursive {
		err = fsys.RemoveAll(fsys.confine(path))
	} else {
		err = fsys.Remove(fsys.confine(path))
	}
	if err != nil {
		return nil, fileError("remove", path, err)
	}
	return map[string]any{"path": path}, nil
}

// hashRange is the sha256 of length bytes of the file at offset, or of
// everything from offset when length is negative. It stops when ctx ends: a
// hash of a large file runs for minutes, and a copy that was cancelled or
// lost its link must not leave one reading the disk.
func hashRange(ctx context.Context, path string, offset, length int64) (string, int64, error) {
	return hashRangeIn(ctx, fileFS{}, path, offset, length)
}

// hashRangeIn is hashRange through fsys.
func hashRangeIn(ctx context.Context, fsys fileFS, path string, offset, length int64) (string, int64, error) {
	f, _, err := openRegularIn(fsys, path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", 0, err
	}
	h := sha256.New()
	var r io.Reader = ctxReader{ctx: ctx, r: f}
	if length >= 0 {
		r = io.LimitReader(r, length)
	}
	n, err := io.CopyBuffer(h, r, make([]byte, 1<<20))
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ctxReader is a reader that stops when its context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// verbFileHash hashes a file, or a range of it, or of the part a copy to it
// is writing.
func (d *Daemon) verbFileHash(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Length *int64 `json:"length"`
		Part   bool   `json:"part"`
		PartID string `json:"part_id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkPartID(p.PartID); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	target := path
	if p.Part {
		target = partPath(path, p.PartID)
	}
	length := int64(-1)
	if p.Length != nil {
		length = *p.Length
	}
	sum, n, err := hashRange(d.ctx, target, max(p.Offset, 0), length)
	if err != nil {
		return nil, fileError("hash", target, err)
	}
	return map[string]any{"path": path, "sha256": sum, "bytes": n}, nil
}

// verbOpenFileStream turns the connection into a byte stream of one file.
//
// mode read: the reply says the size, then the connection carries the bytes
// from offset to that size and closes. With compress "auto" this daemon reads
// a sample of the file, and when it compresses well the bytes go as one
// flate stream and the reply says compressed. mode write: the part file is
// cut to offset (which must not be past the part's end), the reply says where
// it starts, then the connection carries length bytes into it, as one flate
// stream when compress is set. When they are all written and synced the
// daemon writes one JSON line with the part's size and closes. Either side
// ending early leaves the part as it is, which is what a resume starts from.
//
// A write from offset 0 is hashed as it lands, so file-commit does not read
// the part again (partSums).
func (d *Daemon) verbOpenFileStream(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path     string          `json:"path"`
		Mode     string          `json:"mode"`
		Offset   int64           `json:"offset"`
		Length   int64           `json:"length"`
		PartID   string          `json:"part_id"`
		Compress json.RawMessage `json:"compress"`
		Sum      bool            `json:"sum"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkPartID(p.PartID); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	if p.Offset < 0 || p.Length < 0 {
		return nil, invalidParam("offset", "offset and length cannot be negative")
	}
	compress := ""
	switch c := strings.TrimSpace(string(p.Compress)); c {
	case "", "false", "null":
	case "true":
		compress = "on"
	case `"auto"`:
		compress = "auto"
	default:
		return nil, invalidParam("compress", "compress is true, false or \"auto\"")
	}
	switch p.Mode {
	case "read", "":
		f, fi, err := openRegular(path)
		if err != nil {
			return nil, fileError("read", path, err)
		}
		size := fi.Size()
		if p.Offset > size {
			_ = f.Close()
			return nil, invalidParam("offset", "offset is past the end of the file")
		}
		z := compress == "on" || compress == "auto" && compressibleFile(f, path, size-p.Offset, p.Offset)
		if _, err := f.Seek(p.Offset, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, fileError("read", path, err)
		}
		cs.takeover = func(_ *bufio.Reader) {
			defer func() { _ = f.Close() }()
			_ = cs.conn.SetWriteDeadline(time.Time{})
			h := sha256.New()
			body := io.TeeReader(io.LimitReader(f, size-p.Offset), h)
			bw := bufio.NewWriterSize(cs.conn, 256<<10)
			var n int64
			var err error
			if z {
				fw := getFlateWriter(bw)
				n, err = io.CopyBuffer(fw, body, make([]byte, 256<<10))
				if err == nil {
					err = fw.Close()
				}
				putFlateWriter(fw)
			} else {
				n, err = io.CopyBuffer(bw, body, make([]byte, 256<<10))
			}
			if err == nil && p.Sum {
				// The hash of what was read and sent, and whether the file
				// is still the one the reply described.
				t := map[string]any{"sha256": hex.EncodeToString(h.Sum(nil))}
				if now, serr := f.Stat(); n < size-p.Offset || serr != nil || now.Size() != size || !now.ModTime().Equal(fi.ModTime()) {
					t = map[string]any{"error": echoName(path) + " changed while it was copied. Copy it again when it is not being written."}
				}
				line, _ := json.Marshal(t)
				_, _ = bw.Write(append(line, '\n'))
			}
			_ = bw.Flush()
			_ = cs.conn.Close()
		}
		out := map[string]any{"path": path, "mode": "read", "size": size, "offset": p.Offset, "mtime": fi.ModTime().UnixMilli()}
		if z {
			out["compressed"] = true
		}
		if p.Sum {
			out["sum"] = true
		}
		return out, nil
	case "write":
		if compress == "auto" {
			return nil, invalidParam("compress", "a write says whether its bytes are compressed: true or false")
		}
		// The destination is checked with the part, so a copy that file-commit
		// would refuse is refused before its bytes move.
		fsys, verr := d.linkWriteFS(cs, path, partPath(path, p.PartID))
		if verr != nil {
			return nil, verr
		}
		defer fsys.Close()
		dir := filepath.Dir(path)
		if fi, err := fsys.Stat(fsys.confineDir(dir)); err != nil || !fi.IsDir() {
			if err == nil {
				err = fs.ErrNotExist
			}
			return nil, fileError("write", dir, err)
		}
		part := partPath(path, p.PartID)
		confined := fsys.confine(part)
		f, err := openPart(fsys, confined)
		if err != nil {
			return nil, fileError("write", part, err)
		}
		d.transfers.parts.note(filepath.Dir(confined))
		fi, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, fileError("write", part, err)
		}
		if p.Offset > fi.Size() {
			_ = f.Close()
			return nil, invalidParam("offset", "offset "+strconv.FormatInt(p.Offset, 10)+" is past the part's "+strconv.FormatInt(fi.Size(), 10)+" bytes")
		}
		if err := f.Truncate(p.Offset); err != nil {
			_ = f.Close()
			return nil, fileError("write", part, err)
		}
		if _, err := f.Seek(p.Offset, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, fileError("write", part, err)
		}
		cs.takeover = func(br *bufio.Reader) {
			defer func() { _ = f.Close() }()
			_ = cs.conn.SetReadDeadline(time.Time{})
			var src io.Reader = br
			var fr io.ReadCloser
			if compress == "on" {
				fr = getFlateReader(br)
				src = fr
			}
			h := sha256.New()
			var dst io.Writer = f
			if p.Offset == 0 {
				dst = io.MultiWriter(f, h)
			}
			n, cerr := io.CopyBuffer(dst, io.LimitReader(src, p.Length), make([]byte, 256<<10))
			if fr != nil {
				if cerr == nil && n == p.Length {
					cerr = flateEnd(fr)
				}
				putFlateReader(fr)
			}
			syncErr := syncData(f)
			reply := map[string]any{"part_size": p.Offset + n, "written": n}
			switch {
			case cerr != nil:
				reply["error"] = fileError("write", path, cerr).Message
			case syncErr != nil:
				reply["error"] = fileError("write", path, syncErr).Message
			case n < p.Length:
				reply["error"] = "the sender stopped after " + strconv.FormatInt(n, 10) + " of " + strconv.FormatInt(p.Length, 10) + " bytes"
			default:
				if p.Offset == 0 {
					d.transfers.sums.put(confined, f, hex.EncodeToString(h.Sum(nil)))
				}
			}
			line, _ := json.Marshal(reply)
			_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, _ = cs.conn.Write(append(line, '\n'))
			_ = cs.conn.Close()
		}
		return map[string]any{"path": path, "mode": "write", "part_size": fi.Size(), "offset": p.Offset}, nil
	default:
		return nil, invalidParam("mode", "mode is read or write", "read", "write")
	}
}

// commitOpts are the rest of what commitPart takes.
type commitOpts struct {
	// known is the part's sha256 as the writer computed it while the bytes
	// went in, so the part is not read again. Empty reads it.
	known string
	// label names the copy keep-both makes: "name (LABEL).ext". Empty
	// numbers it: "name 2.ext".
	label string
	// noDirSync leaves the folder's sync to the caller, which syncs each
	// folder once after many files.
	noDirSync bool
}

// commitPart checks a finished part against the sender's hash and renames it
// into place. conflict says what to do when path exists: replace it, keep both
// (the new file gets a free name, see freeNameIn), skip (the part is removed
// and the file there stays), or fail. perm, when not zero, is the original's
// permission bits: the part is owner only while it is written, and the
// finished file gets the original's. mtime, when not zero, is the original's
// modification time in Unix ms, which the finished file gets too. skipped
// reports a copy that conflict skip left out.
func commitPart(ctx context.Context, fsys fileFS, path, partID, want, conflict string, perm uint32, mtime int64, o commitOpts) (final, sum string, skipped bool, verr *verbError) {
	part := partPath(path, partID)
	// The part is checked and moved by its name, so it must still be the
	// regular file the copy wrote, not a link put there since.
	if fi, err := fsys.Lstat(part); err != nil {
		return "", "", false, fileError("check", part, err)
	} else if !fi.Mode().IsRegular() {
		return "", "", false, fileError("check", part, &fs.PathError{Op: "check", Path: part, Err: errNotRegular})
	}
	got := o.known
	if got == "" {
		var err error
		got, _, err = hashRangeIn(ctx, fsys, part, 0, -1)
		if err != nil {
			return "", "", false, fileError("check", part, err)
		}
	}
	if want != "" && !strings.EqualFold(got, want) {
		_ = fsys.Remove(part)
		return "", got, false, newVerbError(ErrVerbHashMismatch, "the copy of "+echoName(filepath.Base(path))+" does not match the original, so it was removed")
	}
	final = path
	if fi, err := fsys.Lstat(path); err == nil {
		if fi.IsDir() {
			_ = fsys.Remove(part)
			return "", got, false, newVerbError(ErrVerbFileExists, echoName(path)+" is a folder, so a file cannot go there")
		}
		switch conflict {
		case "replace":
		case "keep-both":
			final = freeNameIn(fsys, path, o.label)
		case "skip":
			_ = fsys.Remove(part)
			return path, got, true, nil
		default:
			return "", got, false, newVerbError(ErrVerbFileExists, echoName(path)+" already exists")
		}
	}
	if perm != 0 {
		_ = fsys.Chmod(part, os.FileMode(perm&0o777))
	}
	if mtime > 0 {
		t := time.UnixMilli(mtime)
		_ = fsys.Chtimes(part, t, t)
	}
	if err := fsys.Rename(part, final); err != nil {
		return "", got, false, fileError("finish", final, err)
	}
	if !o.noDirSync {
		syncDir(fsys, filepath.Dir(final))
	}
	return final, got, false, nil
}

// syncDir writes a folder's entries to the disk, so a rename in it lasts.
func syncDir(fsys fileFS, dir string) {
	if f, err := fsys.OpenFile(dir, os.O_RDONLY, 0); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}

// freeName is path with " 2", " 3" ... before its extension, the first that is
// free.
func freeName(path string) string {
	return freeNameIn(fileFS{}, path, "")
}

// keepLabelMax bounds a keep-both label.
const keepLabelMax = 64

// checkKeepLabel refuses a label that could not be part of one file name.
func checkKeepLabel(label string) *verbError {
	if len(label) > keepLabelMax {
		return invalidParam("label", "a label is at most 64 bytes")
	}
	for _, r := range label {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return invalidParam("label", "a label cannot hold a slash or a control character")
		}
	}
	return nil
}

// freeNameIn is the name keep-both gives a copy of path: with a label,
// "name (LABEL).ext", then "name (LABEL) 2.ext"; without one, "name 2.ext",
// then "name 3.ext". It is the first such name that is free.
func freeNameIn(fsys fileFS, path, label string) string {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if ext == base {
		stem, ext = base, ""
	}
	free := func(cand string) bool {
		_, err := fsys.Lstat(cand)
		return errors.Is(err, fs.ErrNotExist)
	}
	if label != "" {
		stem += " (" + label + ")"
		if cand := filepath.Join(dir, stem+ext); free(cand) {
			return cand
		}
	}
	for i := 2; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s %d%s", stem, i, ext))
		if free(cand) {
			return cand
		}
	}
}

// verbFileCommit finishes a copy written with open-file-stream.
func (d *Daemon) verbFileCommit(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path     string `json:"path"`
		SHA256   string `json:"sha256"`
		Conflict string `json:"conflict"`
		Perm     uint32 `json:"perm"`
		MTime    int64  `json:"mtime"`
		PartID   string `json:"part_id"`
		Label    string `json:"label"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkPartID(p.PartID); verr != nil {
		return nil, verr
	}
	if verr := checkKeepLabel(p.Label); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	switch p.Conflict {
	case "", "fail", "replace", "keep-both", "skip":
	default:
		return nil, invalidParam("conflict", "conflict is replace, keep-both, skip or fail", "replace", "keep-both", "skip", "fail")
	}
	fsys, verr := d.linkWriteFS(cs, path, partPath(path, p.PartID))
	if verr != nil {
		return nil, verr
	}
	defer fsys.Close()
	target := fsys.confine(path)
	known := d.transfers.sums.take(fsys, partPath(target, p.PartID))
	final, sum, skipped, verr := commitPart(d.ctx, fsys, target, p.PartID, p.SHA256, p.Conflict, p.Perm, p.MTime, commitOpts{known: known, label: p.Label})
	if verr != nil {
		return nil, verr
	}
	out := map[string]any{"path": final, "sha256": sum}
	if skipped {
		out["skipped"] = true
	}
	return out, nil
}

// verbFileAbort removes the part a copy left, for a copy that was cancelled.
func (d *Daemon) verbFileAbort(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path   string `json:"path"`
		PartID string `json:"part_id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkPartID(p.PartID); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	part := partPath(path, p.PartID)
	fsys, verr := d.linkWriteFS(cs, part)
	if verr != nil {
		return nil, verr
	}
	defer fsys.Close()
	err := fsys.Remove(fsys.confine(part))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fileError("remove the part of", path, err)
	}
	return map[string]any{"path": path}, nil
}

// fileWalkMax bounds the entries one folder copy carries. A walk on another
// machine streams its entries one line each (file-walk with stream), so the
// bound is the memory of the copy, not the size of one answer.
const fileWalkMax = 1_000_000

// fileWalkReplyMax bounds the entries of a walk answered in one JSON reply,
// which a reader takes as one line.
const fileWalkReplyMax = 20000

// fileWalkSkippedMax bounds the skipped items a walk names. It counts all of
// them.
const fileWalkSkippedMax = 200

// WalkEntry is one file of a folder being copied, relative to the folder.
type WalkEntry struct {
	Rel  string `json:"rel"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
	Perm uint32 `json:"perm,omitempty"`
	// MTime is the file's modification time in Unix ms, for the copy to
	// keep.
	MTime int64 `json:"mtime,omitempty"`
}

// SkippedItem is something in a folder that a copy does not carry: a link,
// a named pipe, a socket or a device.
type SkippedItem struct {
	Rel  string `json:"rel"`
	Kind string `json:"kind"`
}

// walkResult is a folder as a copy sees it.
type walkResult struct {
	entries []WalkEntry
	total   int64
	skipped []SkippedItem
	// skippedCount counts every skipped item, also those past
	// fileWalkSkippedMax.
	skippedCount int
}

// skippedKind names what a mode is, for a skipped item.
func skippedKind(m fs.FileMode) string {
	switch {
	case m&fs.ModeSymlink != 0:
		return "symlink"
	case m&fs.ModeNamedPipe != 0:
		return "pipe"
	case m&fs.ModeSocket != 0:
		return "socket"
	case m&fs.ModeDevice != 0:
		return "device"
	}
	return "other"
}

// walkTree lists every file and folder under root, folders first in each
// folder, so a copy can make them in order. Links are not followed: a link,
// a pipe, a socket and a device are not copied, and the walk names them, so
// the person learns what stayed behind. A part file of a copy in flight is
// neither copied nor named.
func walkTree(root string, limit int) (walkResult, error) {
	var out walkResult
	err := walkTreeTo(root, limit, func(e WalkEntry) error {
		out.entries = append(out.entries, e)
		out.total += e.Size
		return nil
	}, func(s SkippedItem) {
		out.skippedCount++
		if len(out.skipped) < fileWalkSkippedMax {
			out.skipped = append(out.skipped, s)
		}
	})
	return out, err
}

// walkTreeTo is walkTree that hands each entry to emit as it is found, and
// each skipped item to skip.
func walkTreeTo(root string, limit int, emit func(WalkEntry) error, skip func(SkippedItem)) error {
	n := 0
	return filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if n >= limit {
			return errWalkTooLarge(limit)
		}
		switch {
		case de.IsDir():
			n++
			return emit(WalkEntry{Rel: rel, Dir: true})
		case de.Type().IsRegular():
			if isPartName(de.Name()) {
				return nil
			}
			fi, err := de.Info()
			if err != nil {
				return err
			}
			n++
			return emit(WalkEntry{Rel: rel, Size: fi.Size(), Perm: uint32(fi.Mode().Perm()), MTime: fi.ModTime().UnixMilli()})
		default:
			skip(SkippedItem{Rel: rel, Kind: skippedKind(de.Type())})
		}
		return nil
	})
}

// walkTooLarge is a folder with more entries than a walk carries.
type walkTooLarge struct{ limit int }

func (e walkTooLarge) Error() string {
	return "the folder holds more than " + strconv.Itoa(e.limit) + " files and folders"
}

func errWalkTooLarge(limit int) error { return walkTooLarge{limit} }

func isWalkTooLarge(err error) bool {
	var w walkTooLarge
	return errors.As(err, &w)
}

// verbFileWalk lists every file under a folder, for a folder copy. With
// stream, the reply is the folder alone, and then the connection carries one
// JSON line per entry and a last line with the totals, so a folder of a
// million files is not one answer.
func (d *Daemon) verbFileWalk(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path   string `json:"path"`
		Stream bool   `json:"stream"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	if p.Stream {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, fileError("list", path, err)
		}
		if !fi.IsDir() {
			return nil, invalidParam("path", echoName(path)+" is not a folder")
		}
		cs.takeover = func(_ *bufio.Reader) {
			_ = cs.conn.SetWriteDeadline(time.Time{})
			bw := bufio.NewWriterSize(cs.conn, 64<<10)
			enc := json.NewEncoder(bw)
			var total int64
			var skipped []SkippedItem
			count := 0
			err := walkTreeTo(path, fileWalkMax, func(e WalkEntry) error {
				total += e.Size
				return enc.Encode(e)
			}, func(s SkippedItem) {
				count++
				if len(skipped) < fileWalkSkippedMax {
					skipped = append(skipped, s)
				}
			})
			end := map[string]any{"end": true, "bytes": total, "skipped": skipped, "skipped_count": count}
			if err != nil {
				v := fileError("list", path, err)
				if isWalkTooLarge(err) {
					v = newVerbError(ErrVerbInvalidParams, "copy: "+err.Error())
				}
				end = map[string]any{"end": true, "code": v.Code, "error": v.Message}
			}
			_ = enc.Encode(end)
			_ = bw.Flush()
			_ = cs.conn.Close()
		}
		return map[string]any{"path": path, "stream": true}, nil
	}
	w, err := walkTree(path, fileWalkReplyMax)
	if isWalkTooLarge(err) {
		return nil, newVerbError(ErrVerbInvalidParams, "copy: "+err.Error()+". Ask with stream for a larger folder")
	}
	if err != nil {
		return nil, fileError("list", path, err)
	}
	skipped := w.skipped
	if skipped == nil {
		skipped = []SkippedItem{}
	}
	return map[string]any{"path": path, "entries": w.entries, "bytes": w.total, "skipped": skipped, "skipped_count": w.skippedCount}, nil
}

// fileVerbs is the file verb family.
func fileVerbs() map[string]verbEntry {
	pathParam := func(what string) verbParam {
		return verbParam{Name: "path", Type: "string", Required: true, Description: what + " An absolute path, or one that starts with ~."}
	}
	partIDParam := verbParam{Name: "part_id", Type: "string", Description: "The copy's part id, up to 32 hex digits: the part is .NAME.tuios-part-ID, so two copies to one path write two parts. Omit for .NAME.tuios-part."}
	infoReturn := verbParam{Name: "info", Type: "object", Description: "name, path, kind (file, dir, symlink, other), link_kind and link for a link, size, mtime (Unix ms), mode (drwxr-xr-x), perm (the permission bits as a number), hidden."}
	return map[string]verbEntry{
		"file-stat": {
			description: "Describe one path on this machine. With part, describe the part file a copy to the path is writing.",
			params: []verbParam{
				pathParam("The path."),
				{Name: "part", Type: "bool", Description: "Describe the part file of a copy to this path instead."},
				partIDParam,
			},
			returns: []verbParam{
				{Name: "path", Type: "string", Description: "The path, made absolute."},
				{Name: "exists", Type: "bool", Description: "False when nothing is there."},
				{Name: "size", Type: "int", Description: "The size in bytes. 0 for a part that is not there."},
				infoReturn,
			},
			examples: []string{`{"id":1,"verb":"file-stat","params":{"path":"~/notes.md"}}`},
			handler:  (*Daemon).verbFileStat,
		},
		"file-list": {
			description: "List a folder on this machine a page at a time, folders first, then names in natural order (file2 before file10), with size, time and mode.",
			params: []verbParam{
				{Name: "dir", Type: "string", Description: "The folder. Omit for the home folder."},
				{Name: "offset", Type: "int", Description: "The first entry of the page.", Default: "0"},
				{Name: "limit", Type: "int", Description: "Entries in the page, at most 5000.", Default: "500"},
				{Name: "hidden", Type: "bool", Description: "Include names that start with a dot.", Default: "true"},
			},
			returns: []verbParam{
				{Name: "dir", Type: "string", Description: "The folder, made absolute."},
				{Name: "entries", Type: "[]object", Description: "One info object per entry, as file-stat's info."},
				{Name: "total", Type: "int", Description: "Entries in the folder."},
				{Name: "next", Type: "int", Description: "The offset of the next page. Absent on the last page."},
				{Name: "parent", Type: "string", Description: "The folder above. Absent at the root."},
				{Name: "home", Type: "string", Description: "This machine's home folder."},
				{Name: "capped", Type: "bool", Description: "The folder holds more than 50000 names, and only those were read."},
			},
			examples: []string{`{"id":1,"verb":"file-list","params":{"dir":"~/src","limit":200}}`},
			handler:  (*Daemon).verbFileList,
		},
		"file-read": {
			description: "Read up to 4 MiB of a file on this machine, base64.",
			params: []verbParam{
				pathParam("The file."),
				{Name: "offset", Type: "int", Description: "Where to start.", Default: "0"},
				{Name: "length", Type: "int", Description: "How many bytes, at most 4 MiB.", Default: "4194304"},
			},
			returns: []verbParam{
				{Name: "content", Type: "string", Description: "The bytes, base64."},
				{Name: "size", Type: "int", Description: "The file's size."},
				{Name: "eof", Type: "bool", Description: "The read reached the end of the file."},
			},
			examples: []string{`{"id":1,"verb":"file-read","params":{"path":"~/notes.md","length":65536}}`},
			handler:  (*Daemon).verbFileRead,
		},
		"file-mkdir": {
			description: "Make a folder on this machine, with its parents.",
			params:      []verbParam{pathParam("The folder.")},
			returns:     []verbParam{{Name: "path", Type: "string", Description: "The folder, made absolute."}},
			examples:    []string{`{"id":1,"verb":"file-mkdir","params":{"path":"~/new"}}`},
			handler:     (*Daemon).verbFileMkdir,
		},
		"file-rename": {
			description: "Move or rename a path on this machine. It does not replace a path that is there unless replace is set. A move to another disk answers cross_device: copy it with transfer-start and move set.",
			params: []verbParam{
				{Name: "from", Type: "string", Required: true, Description: "The path to move."},
				{Name: "to", Type: "string", Required: true, Description: "The new path."},
				{Name: "replace", Type: "bool", Description: "Replace what is at to."},
			},
			returns: []verbParam{
				{Name: "from", Type: "string", Description: "The old path."},
				{Name: "to", Type: "string", Description: "The new path."},
			},
			examples: []string{`{"id":1,"verb":"file-rename","params":{"from":"~/a.txt","to":"~/b.txt"}}`},
			handler:  (*Daemon).verbFileRename,
		},
		"file-remove": {
			description: "Delete a path on this machine. A folder that holds files needs recursive. The home folder and / are refused.",
			params: []verbParam{
				pathParam("The path."),
				{Name: "recursive", Type: "bool", Description: "Delete a folder and all it holds."},
			},
			returns:  []verbParam{{Name: "path", Type: "string", Description: "The path deleted."}},
			examples: []string{`{"id":1,"verb":"file-remove","params":{"path":"~/old","recursive":true}}`},
			handler:  (*Daemon).verbFileRemove,
		},
		"file-hash": {
			description: "The sha256 of a file on this machine, of a range of it, or of the part a copy to it is writing.",
			params: []verbParam{
				pathParam("The file."),
				{Name: "offset", Type: "int", Description: "Where the range starts.", Default: "0"},
				{Name: "length", Type: "int", Description: "How many bytes. Omit for the rest of the file."},
				{Name: "part", Type: "bool", Description: "Hash the part file of a copy to this path."},
				partIDParam,
			},
			returns: []verbParam{
				{Name: "sha256", Type: "string", Description: "The hash, hex."},
				{Name: "bytes", Type: "int", Description: "How many bytes were hashed."},
			},
			examples: []string{`{"id":1,"verb":"file-hash","params":{"path":"~/big.iso"}}`},
			handler:  (*Daemon).verbFileHash,
		},
		"file-walk": {
			description: "List every file and folder under a folder on this machine, for a folder copy. At most 20000 entries in one reply; with stream, up to 1000000 entries, one JSON line each after the reply, and a last line {end, bytes, skipped, skipped_count} or {end, code, error}. Links are not followed: a link, a named pipe, a socket and a device are not copied, and skipped names them.",
			params: []verbParam{
				pathParam("The folder."),
				{Name: "stream", Type: "bool", Description: "Send the entries after the reply, one JSON line each, for a folder of more than 20000 entries."},
			},
			returns: []verbParam{
				{Name: "entries", Type: "[]object", Description: "rel (the path under the folder, slash separated), size, dir, perm, mtime (Unix ms). A folder comes before what it holds."},
				{Name: "bytes", Type: "int", Description: "The bytes of every file together."},
				{Name: "skipped", Type: "[]object", Description: "The first 200 items a copy does not carry: rel and kind (symlink, pipe, socket, device)."},
				{Name: "skipped_count", Type: "int", Description: "How many items a copy does not carry, all of them."},
			},
			examples: []string{`{"id":1,"verb":"file-walk","params":{"path":"~/photos"}}`},
			handler:  (*Daemon).verbFileWalk,
		},
		"open-file-stream": {
			description: "Turn this connection into the bytes of one file. mode read: after the reply the connection carries the file from offset to size, then closes. mode write: the part file beside path is cut to offset, then the connection takes length bytes into it, answers one line {part_size, written, error} and closes. file-commit puts the part in place.",
			params: []verbParam{
				pathParam("The file to read, or the file a write is for."),
				{Name: "mode", Type: "string", Description: "read or write.", Accepted: []string{"read", "write"}, Default: "read"},
				{Name: "offset", Type: "int", Description: "Where the bytes start. A write's offset cannot be past the part's end.", Default: "0"},
				{Name: "length", Type: "int", Description: "write only: how many bytes the caller sends."},
				partIDParam,
				{Name: "sum", Type: "bool", Description: "read: after the last byte, one line {sha256} with the hash of the bytes sent, or {error} when the file changed while it was read."},
				{Name: "compress", Type: "any", Description: "read: \"auto\" lets this machine compress the bytes with flate when a sample of the file compresses well, and the reply says compressed. write: true when the caller sends the bytes as one flate stream."},
			},
			returns: []verbParam{
				{Name: "size", Type: "int", Description: "read: the file's size. The connection carries size minus offset bytes."},
				{Name: "mtime", Type: "int", Description: "read: the file's modification time, Unix ms."},
				{Name: "compressed", Type: "bool", Description: "read: the bytes come as one flate stream."},
				{Name: "sum", Type: "bool", Description: "read: a {sha256} line follows the bytes."},
				{Name: "part_size", Type: "int", Description: "write: the part's size before it was cut to offset."},
			},
			examples: []string{
				`{"id":1,"verb":"open-file-stream","params":{"path":"~/big.iso","mode":"read","offset":0}}`,
				`{"id":1,"verb":"open-file-stream","params":{"path":"~/big.iso","mode":"write","offset":1048576,"length":2097152}}`,
			},
			handler: (*Daemon).verbOpenFileStream,
		},
		"file-commit": {
			description: "Finish a copy written with open-file-stream: check the part's sha256 against the sender's, then rename it into place. A part that does not match is removed.",
			params: []verbParam{
				pathParam("The file the copy is for."),
				{Name: "sha256", Type: "string", Description: "The sender's hash, hex. Omit to skip the check."},
				{Name: "conflict", Type: "string", Description: "When the path exists: replace it, keep both (the copy gets a new name), skip (the part is removed and the file there stays), or fail.", Accepted: []string{"replace", "keep-both", "skip", "fail"}, Default: "fail"},
				{Name: "label", Type: "string", Description: "keep-both names the copy \"name (LABEL).ext\", such as \"report (from build).pdf\". Omit to name it \"name 2.ext\"."},
				{Name: "perm", Type: "int", Description: "The original's permission bits, which the finished file gets. Omit to keep it owner only."},
				{Name: "mtime", Type: "int", Description: "The original's modification time, Unix ms, which the finished file gets. Omit to keep the time of the copy."},
				partIDParam,
			},
			returns: []verbParam{
				{Name: "path", Type: "string", Description: "Where the file is now."},
				{Name: "sha256", Type: "string", Description: "The part's hash."},
				{Name: "skipped", Type: "bool", Description: "conflict skip found a file at path, so the copy was removed and the file there stays."},
			},
			examples: []string{`{"id":1,"verb":"file-commit","params":{"path":"~/big.iso","sha256":"9f86d0...","conflict":"keep-both"}}`},
			handler:  (*Daemon).verbFileCommit,
		},
		"file-abort": {
			description: "Remove the part a copy to a path left on this machine.",
			params:      []verbParam{pathParam("The file the copy was for."), partIDParam},
			returns:     []verbParam{{Name: "path", Type: "string", Description: "The path."}},
			examples:    []string{`{"id":1,"verb":"file-abort","params":{"path":"~/big.iso"}}`},
			handler:     (*Daemon).verbFileAbort,
		},
	}
}
