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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
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
	case errors.Is(err, errHardLinked):
		return newVerbError(ErrVerbForbidden, what+": "+echoName(path)+" has other names (hard links), so tuios does not write it. Remove it, then copy again")
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
	// A part with a second name is a hard link: its truncate and write, and
	// the mode and time commitPart sets, would land in the other file too,
	// such as a ~/.bashrc linked into the receive folder.
	if n, err := fileLinks(f); err != nil || n > 1 {
		_ = f.Close()
		return nil, &fs.PathError{Op: "write", Path: part, Err: errHardLinked}
	}
	return f, nil
}

// errHardLinked is a file with more than one name where a copy writes.
var errHardLinked = errors.New("the file has other names (hard links), so tuios does not write it")

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
func (d *Daemon) verbFileStat(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path       string   `json:"path"`
		Part       bool     `json:"part"`
		PartID     string   `json:"part_id"`
		RelationTo string   `json:"relation_to"`
		Names      []string `json:"names"`
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
	// What a path is, or that it is there, is read like its bytes: a link
	// may not look inside the folders it may not read.
	if verr := d.checkLinkRead(cs, path); verr != nil {
		return nil, verr
	}
	if p.Names != nil {
		return d.fileStatNames(cs, path, p.Names)
	}
	out := map[string]any{"path": path}
	if p.RelationTo != "" {
		// A copy between two paths of this machine asks here, where both
		// are, whether they are one file. Only this machine can tell.
		src, verr := expandPath(p.RelationTo)
		if verr != nil {
			return nil, verr
		}
		if verr := d.checkLinkRead(cs, src); verr != nil {
			return nil, verr
		}
		rel, err := pathRelation(src, path)
		if err != nil {
			return nil, fileError("stat", src, err)
		}
		out["relation"] = rel
	}
	target := path
	if p.Part {
		target = partPath(path, p.PartID)
	}
	fi, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			out["exists"] = false
			if p.Part {
				out["size"] = 0
			}
			return out, nil
		}
		return nil, fileError("stat", path, err)
	}
	info := describe(target, fi)
	out["exists"], out["info"], out["size"] = true, info, info.Size
	return out, nil
}

// fileStatNamesMax bounds the names one file-stat call describes.
const fileStatNamesMax = 256

// fileStatNames describes names in the folder dir, for the rows of a folder a
// preview shows: one call for a screen of rows, where file-list would stat the
// whole folder. A name that is not there, is not one name, or that a link may
// not read is left out.
func (d *Daemon) fileStatNames(cs *connState, dir string, names []string) (any, *verbError) {
	if len(names) > fileStatNamesMax {
		return nil, newVerbError(ErrVerbInvalidParams, "names holds more than 256 names.")
	}
	infos := make([]FileInfo, 0, len(names))
	for _, name := range names {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			continue
		}
		full := filepath.Join(dir, name)
		if d.checkLinkRead(cs, full) != nil {
			continue
		}
		fi, err := os.Lstat(full)
		if err != nil {
			continue
		}
		infos = append(infos, describe(full, fi))
	}
	return map[string]any{"path": dir, "infos": infos}, nil
}

// verbFileList lists a folder a page at a time: folders first, then names in
// natural order, with the details a file manager shows.
func (d *Daemon) verbFileList(cs *connState, params json.RawMessage) (any, *verbError) {
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
	if verr := d.checkLinkRead(cs, dir); verr != nil {
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
	// unstable one is several times faster on a large folder. It runs once per
	// file-list call, so it sorts through the shared sortx copy.
	sortx.SortFunc(entries, func(a, b FileInfo) int {
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
func (d *Daemon) verbFileRead(cs *connState, params json.RawMessage) (any, *verbError) {
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
	f, fi, verr, err := d.openLinkRead(cs, path)
	if verr != nil {
		return nil, verr
	}
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
		Path    string `json:"path"`
		Receive bool   `json:"receive"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Receive {
		// The receive folder: where the caller may write here when it
		// names no folder. A daemon from before receive makes path, which
		// a caller sets to ~.
		dir, verr := d.receiveFolder(cs)
		if verr != nil {
			return nil, verr
		}
		// A receive folder that is there is not made again: it may be the
		// home folder itself (files_roots = ["~"]), which a link may write
		// under but not change.
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return map[string]any{"path": dir}, nil
		}
		p.Path = dir
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	// A folder that is there is not made again, and nothing is written: a
	// copy into the home folder itself (files_roots = ["~"]) asks for it,
	// and a link may write under the home folder but not change it. That it
	// is there is read like a stat.
	if verr := d.checkLinkRead(cs, path); verr == nil {
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			return map[string]any{"path": path}, nil
		}
	}
	fsys, verr := d.linkWriteFS(cs, path)
	if verr != nil {
		return nil, verr
	}
	defer fsys.Close()
	if verr := d.claimLinkDirs(cs, fsys, fsys.confineDir(path)); verr != nil {
		return nil, verr
	}
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
	if cs != nil && cs.viaLink && (isPartName(filepath.Base(from)) || isPartName(filepath.Base(to))) {
		// A part is the copy's own. A rename onto a part name could put a
		// file the copy did not make, such as a hard link, where the copy
		// writes, and one off it could move a copy's bytes in place unchecked.
		return nil, newVerbError(ErrVerbForbidden, "move: "+echoName(filepath.Base(from))+" or "+echoName(filepath.Base(to))+" is a copy's part file, which a linked machine does not move")
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
	return hashOpen(ctx, f, offset, length)
}

// hashOpen is hashRange of a file that is open already.
func hashOpen(ctx context.Context, f *os.File, offset, length int64) (string, int64, error) {
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
func (d *Daemon) verbFileHash(cs *connState, params json.RawMessage) (any, *verbError) {
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
	if verr := d.checkLinkRead(cs, path); verr != nil {
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
	f, _, verr, err := d.openLinkRead(cs, target)
	if verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, fileError("hash", target, err)
	}
	defer func() { _ = f.Close() }()
	sum, n, err := hashOpen(d.ctx, f, max(p.Offset, 0), length)
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
		// Progress asks a write to say how many bytes it has written while
		// they land, so the sender's rate is what this side took.
		Progress bool `json:"progress"`
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
		f, fi, verr, err := d.openLinkRead(cs, path)
		if verr != nil {
			return nil, verr
		}
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
				line, _ := jsonx.Marshal(t)
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
		// The room is claimed before the part is made, so a write that
		// does not fit leaves nothing behind.
		confined := fsys.confine(part)
		release, verr := d.claimLinkWrite(cs, fsys.confine(path), confined, p.Offset, p.Length)
		if verr != nil {
			return nil, verr
		}
		f, err := openPart(fsys, confined)
		if err != nil {
			release()
			return nil, fileError("write", part, err)
		}
		d.transfers.parts.note(filepath.Dir(confined))
		fi, err := f.Stat()
		if err != nil {
			release()
			_ = f.Close()
			return nil, fileError("write", part, err)
		}
		if p.Offset > fi.Size() {
			release()
			_ = f.Close()
			return nil, invalidParam("offset", "offset "+strconv.FormatInt(p.Offset, 10)+" is past the part's "+strconv.FormatInt(fi.Size(), 10)+" bytes")
		}
		if err := f.Truncate(p.Offset); err != nil {
			release()
			_ = f.Close()
			return nil, fileError("write", part, err)
		}
		if _, err := f.Seek(p.Offset, io.SeekStart); err != nil {
			release()
			_ = f.Close()
			return nil, fileError("write", part, err)
		}
		cs.takeover = func(br *bufio.Reader) {
			defer release()
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
			var tell *writeProgress
			if p.Progress {
				tell = startWriteProgress(cs.conn)
				dst = &landedWriter{w: dst, n: &tell.written}
			}
			n, cerr := io.CopyBuffer(dst, io.LimitReader(src, p.Length), make([]byte, 256<<10))
			tell.stop()
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
				ve := fileError("write", path, cerr)
				reply["error"], reply["code"] = ve.Message, ve.Code
			case syncErr != nil:
				ve := fileError("write", path, syncErr)
				reply["error"], reply["code"] = ve.Message, ve.Code
			case n < p.Length:
				reply["error"] = "the sender stopped after " + strconv.FormatInt(n, 10) + " of " + strconv.FormatInt(p.Length, 10) + " bytes"
			default:
				if p.Offset == 0 {
					d.transfers.sums.put(confined, f, hex.EncodeToString(h.Sum(nil)))
				}
			}
			line, _ := jsonx.Marshal(reply)
			_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, _ = cs.conn.Write(append(line, '\n'))
			_ = cs.conn.Close()
		}
		return map[string]any{"path": path, "mode": "write", "part_size": fi.Size(), "offset": p.Offset}, nil
	default:
		return nil, invalidParam("mode", "mode is read or write", "read", "write")
	}
}

// writeProgressEvery is how often a write stream that asked for progress is
// told how many bytes have landed.
const writeProgressEvery = 250 * time.Millisecond

// writeProgress tells the sender of a write stream, every
// writeProgressEvery, how many of its bytes have been written to the part:
// one line {"written": N}. It writes only when the count moved, and stops at
// the first write that fails, which leaves the stream to end as it would.
type writeProgress struct {
	written atomic.Int64
	done    chan struct{}
	ended   chan struct{}
}

func startWriteProgress(conn net.Conn) *writeProgress {
	p := &writeProgress{done: make(chan struct{}), ended: make(chan struct{})}
	go func() {
		defer close(p.ended)
		t := time.NewTicker(writeProgressEvery)
		defer t.Stop()
		var told int64
		for {
			select {
			case <-p.done:
				return
			case <-t.C:
			}
			n := p.written.Load()
			if n == told {
				continue
			}
			line, _ := jsonx.Marshal(map[string]any{"written": n})
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := conn.Write(append(line, '\n')); err != nil {
				return
			}
			told = n
		}
	}()
	return p
}

// landedWriter counts into n the bytes its writer took.
type landedWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (l *landedWriter) Write(b []byte) (int, error) {
	n, err := l.w.Write(b)
	l.n.Add(int64(n))
	return n, err
}

// stop ends the progress lines before the last reply is written, so the two
// never interleave. A nil p is a stream that did not ask for progress.
func (p *writeProgress) stop() {
	if p == nil {
		return
	}
	close(p.done)
	<-p.ended
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
	if pf, err := fsys.OpenFile(part, os.O_RDONLY|oNoFollow|oNonBlock, 0); err == nil {
		n, lerr := fileLinks(pf)
		_ = pf.Close()
		if lerr != nil || n > 1 {
			return "", "", false, fileError("check", part, &fs.PathError{Op: "check", Path: part, Err: errHardLinked})
		}
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
		_ = fsys.Chmod(part, fsys.commitMode(perm))
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

// freeNameIn is the name keep-both gives a copy of path in fsys: with a
// label, "name (LABEL).ext", then "name (LABEL) 2.ext"; without one,
// "name 2.ext", then "name 3.ext". It is the first such name that is free.
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
	if _, err := os.Lstat(partPath(target, p.PartID)); err != nil {
		d.linkFiles.partDone(cs, partPath(target, p.PartID))
	}
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
	d.linkFiles.partDone(cs, fsys.confine(part))
	return map[string]any{"path": path}, nil
}

// fileWalkMax bounds the entries one folder copy carries. A walk on another
// machine streams its entries one line each (file-walk with stream), so the
// bound is the memory of the copy, not the size of one answer.
const fileWalkMax = 1_000_000

// FileWalkReplyMax bounds the entries of a walk answered in one JSON reply,
// which a reader takes as one line.
const FileWalkReplyMax = 20000

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
	// denied counts the entries a walk over a link left out because they
	// are in the read deny list. A denied folder counts once. They are not
	// named.
	denied int
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
// neither copied nor named. An entry denied reports true for is neither
// copied nor named either, and out.denied counts it; nil denies nothing.
func walkTree(root string, limit int, denied func(string) bool) (walkResult, error) {
	var out walkResult
	err := walkTreeTo(root, limit, countDenied(denied, &out.denied), func(e WalkEntry) error {
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

// countDenied is denied, counting each entry it denies in n. Nil stays nil.
func countDenied(denied func(string) bool, n *int) func(string) bool {
	if denied == nil {
		return nil
	}
	return func(p string) bool {
		if denied(p) {
			*n++
			return true
		}
		return false
	}
}

// walkTreeTo is walkTree that hands each entry to emit as it is found, and
// each skipped item to skip.
func walkTreeTo(root string, limit int, denied func(string) bool, emit func(WalkEntry) error, skip func(SkippedItem)) error {
	n := 0
	return filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if denied != nil && denied(p) {
			// Not named at all: the name of a key is already more than a
			// link may learn. A denied folder is not entered.
			if de.IsDir() {
				return filepath.SkipDir
			}
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
	root, verr := d.linkWalkRoot(cs, path)
	if verr != nil {
		return nil, verr
	}
	if p.Stream {
		fi, err := os.Stat(root)
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
			count, denied := 0, 0
			err := walkTreeTo(root, fileWalkMax, countDenied(d.linkWalkDenied(cs), &denied), func(e WalkEntry) error {
				total += e.Size
				return enc.Encode(e)
			}, func(s SkippedItem) {
				count++
				if len(skipped) < fileWalkSkippedMax {
					skipped = append(skipped, s)
				}
			})
			end := map[string]any{"end": true, "bytes": total, "skipped": skipped, "skipped_count": count, "denied": denied}
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
	w, err := walkTree(root, FileWalkReplyMax, d.linkWalkDenied(cs))
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
	return map[string]any{"path": path, "entries": w.entries, "bytes": w.total, "skipped": skipped, "skipped_count": w.skippedCount, "denied": w.denied}, nil
}

// fileVerbs is the file verb family.
func fileVerbs() map[string]verbEntry {
	pathParam := func(what string) verbParam {
		return verbParam{Name: "path", Type: "string", Required: true}
	}
	partIDParam := verbParam{Name: "part_id", Type: "string"}
	infoReturn := verbParam{Name: "info", Type: "object"}
	return map[string]verbEntry{
		"file-stat": {
			// The file verbs, transfers and drops are the person's explorer, not a
			// restricted caller's. A pane's own shell already reaches its files.
			scope: scopeDeny,
			// The file verbs. A listing is a listing. Reading a file's bytes out and
			// changing a file are files, a capability apart from write, so a person
			// can stop one machine's copies without stopping its typing. Every write
			// that arrives over a link is also confined to the home folder and kept
			// out of the deny list (verb_files_confine.go). See verb_files.go.
			link: needList,
			params: []verbParam{
				pathParam("The path."),
				{Name: "part", Type: "bool"},
				partIDParam,
				{Name: "relation_to", Type: "string"},
				{Name: "names", Type: "[]string"},
			},
			returns: []verbParam{
				{Name: "path", Type: "string"},
				{Name: "exists", Type: "bool"},
				{Name: "size", Type: "int"},
				infoReturn,
				{Name: "relation", Type: "string"},
				{Name: "infos", Type: "[]object"},
			},
			handler: (*Daemon).verbFileStat,
		},
		"file-list": {
			scope: scopeDeny,
			link:  needList,
			params: []verbParam{
				{Name: "dir", Type: "string"},
				{Name: "offset", Type: "int", Default: "0"},
				{Name: "limit", Type: "int", Default: "500"},
				{Name: "hidden", Type: "bool", Default: "true"},
			},
			returns: []verbParam{
				{Name: "dir", Type: "string"},
				{Name: "entries", Type: "[]object"},
				{Name: "total", Type: "int"},
				{Name: "next", Type: "int"},
				{Name: "parent", Type: "string"},
				{Name: "home", Type: "string"},
				{Name: "capped", Type: "bool"},
			},
			handler: (*Daemon).verbFileList,
		},
		"file-read": {
			scope: scopeDeny,
			link:  needFiles,
			params: []verbParam{
				pathParam("The file."),
				{Name: "offset", Type: "int", Default: "0"},
				{Name: "length", Type: "int", Default: "4194304"},
			},
			returns: []verbParam{
				{Name: "content", Type: "string"},
				{Name: "size", Type: "int"},
				{Name: "eof", Type: "bool"},
			},
			handler: (*Daemon).verbFileRead,
		},
		"file-mkdir": {
			scope: scopeDeny,
			link:  needFiles,
			params: []verbParam{
				pathParam("The folder."),
				{Name: "receive", Type: "bool"},
			},
			returns: []verbParam{{Name: "path", Type: "string"}},
			handler: (*Daemon).verbFileMkdir,
		},
		"file-rename": {
			scope: scopeDeny,
			link:  needFiles,
			params: []verbParam{
				{Name: "from", Type: "string", Required: true},
				{Name: "to", Type: "string", Required: true},
				{Name: "replace", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "from", Type: "string"},
				{Name: "to", Type: "string"},
			},
			handler: (*Daemon).verbFileRename,
		},
		"file-remove": {
			scope: scopeDeny,
			link:  needFiles,
			params: []verbParam{
				pathParam("The path."),
				{Name: "recursive", Type: "bool"},
			},
			returns: []verbParam{{Name: "path", Type: "string"}},
			handler: (*Daemon).verbFileRemove,
		},
		"file-hash": {
			scope: scopeDeny,
			link:  needFiles,
			params: []verbParam{
				pathParam("The file."),
				{Name: "offset", Type: "int", Default: "0"},
				{Name: "length", Type: "int"},
				{Name: "part", Type: "bool"},
				partIDParam,
			},
			returns: []verbParam{
				{Name: "sha256", Type: "string"},
				{Name: "bytes", Type: "int"},
			},
			handler: (*Daemon).verbFileHash,
		},
		"file-walk": {
			stream: &verbStream{Param: "stream", Values: []string{"true"}},
			scope:  scopeDeny,
			link:   needFiles,
			params: []verbParam{
				pathParam("The folder."),
				{Name: "stream", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "entries", Type: "[]object"},
				{Name: "bytes", Type: "int"},
				{Name: "skipped", Type: "[]object"},
				{Name: "skipped_count", Type: "int"},
				{Name: "denied", Type: "int"},
			},
			handler: (*Daemon).verbFileWalk,
		},
		"open-file-stream": {
			stream: &verbStream{},
			scope:  scopeDeny,
			link:   needFiles,
			params: []verbParam{
				pathParam("The file to read, or the file a write is for."),
				{Name: "mode", Type: "string", Accepted: []string{"read", "write"}, Default: "read"},
				{Name: "offset", Type: "int", Default: "0"},
				{Name: "length", Type: "int"},
				partIDParam,
				{Name: "sum", Type: "bool"},
				{Name: "progress", Type: "bool"},
				{Name: "compress", Type: "any"},
			},
			returns: []verbParam{
				{Name: "size", Type: "int"},
				{Name: "mtime", Type: "int"},
				{Name: "compressed", Type: "bool"},
				{Name: "sum", Type: "bool"},
				{Name: "part_size", Type: "int"},
			},
			handler: (*Daemon).verbOpenFileStream,
		},
		"file-commit": {
			scope: scopeDeny,
			link:  needFiles,
			params: []verbParam{
				pathParam("The file the copy is for."),
				{Name: "sha256", Type: "string"},
				{Name: "conflict", Type: "string", Accepted: []string{"replace", "keep-both", "skip", "fail"}, Default: "fail"},
				{Name: "label", Type: "string"},
				{Name: "perm", Type: "int"},
				{Name: "mtime", Type: "int"},
				partIDParam,
			},
			returns: []verbParam{
				{Name: "path", Type: "string"},
				{Name: "sha256", Type: "string"},
				{Name: "skipped", Type: "bool"},
			},
			handler: (*Daemon).verbFileCommit,
		},
		"file-abort": {
			scope:   scopeDeny,
			link:    needFiles,
			params:  []verbParam{pathParam("The file the copy was for."), partIDParam},
			returns: []verbParam{{Name: "path", Type: "string"}},
			handler: (*Daemon).verbFileAbort,
		},
	}
}
