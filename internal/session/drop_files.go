package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Drop files on a pane: the files a person drops from the desktop onto a pane
// whose shell is on another machine have to be on that machine before a path
// to them means anything there.
//
// Each drop goes into a folder of its own inside one private folder under the
// far daemon's runtime directory: the folder is the user's alone (0700), each
// copy is owner only, and the folder keeps at most dropKeepBytes and nothing
// older than dropKeepAge. The bytes travel as transfers, so a large file shows
// its progress like any other copy and survives a dropped link. paste-image
// is the same idea for one image of at most 8 MiB in one request; this has no
// such cap.

const (
	dropKeepBytes = 256 << 20
	dropKeepAge   = 24 * time.Hour
)

// dropRoot is this machine's drop folder, made private if it is missing.
func (d *Daemon) dropRoot() (string, error) {
	root := filepath.Join(filepath.Dir(d.manager.SocketPath()), "drop")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	// MkdirAll keeps the mode of a folder that was there. A drop folder that
	// others can read is not one to write into.
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

// pruneDrops removes whole drops, oldest first, until the folder holds no
// drop older than dropKeepAge and at most keep bytes.
func pruneDrops(root string, keep int64, now time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type drop struct {
		path string
		at   time.Time
		size int64
	}
	var drops []drop
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		var size int64
		_ = filepath.WalkDir(p, func(_ string, de fs.DirEntry, err error) error {
			if err == nil && de.Type().IsRegular() {
				if info, err := de.Info(); err == nil {
					size += info.Size()
				}
			}
			return nil
		})
		drops = append(drops, drop{p, fi.ModTime(), size})
		total += size
	}
	sort.Slice(drops, func(i, j int) bool { return drops[i].at.Before(drops[j].at) })
	for _, dr := range drops {
		if now.Sub(dr.at) <= dropKeepAge && total <= keep {
			break
		}
		if os.RemoveAll(dr.path) == nil {
			total -= dr.size
		}
	}
}

// verbFileDropDir makes a new private folder for one drop and answers its
// path. The caller copies the dropped files into it.
func (d *Daemon) verbFileDropDir(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Bytes int64 `json:"bytes"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	root, err := d.dropRoot()
	if err != nil {
		return nil, fileError("make the drop folder", root, err)
	}
	// Room for this drop comes out of what the older drops may keep.
	pruneDrops(root, max(dropKeepBytes-p.Bytes, 0), time.Now())
	var b [6]byte
	_, _ = rand.Read(b[:])
	dir := filepath.Join(root, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(b[:]))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fileError("make the drop folder", dir, err)
	}
	return map[string]any{"dir": dir, "keep_bytes": dropKeepBytes, "keep_hours": int(dropKeepAge / time.Hour)}, nil
}

// verbDropFiles gives a pane on a machine the paths of files that are on this
// one. For this machine the paths are the answer. For a host, each file is
// copied into a new drop folder there, and the answer has the paths there and
// the transfers that carry them.
func (d *Daemon) verbDropFiles(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host  string   `json:"host"`
		Paths []string `json:"paths"`
		Dir   string   `json:"dir"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Paths) == 0 {
		return nil, invalidParam("paths", "name at least one file")
	}
	if p.Host == federation.LocalHostName {
		p.Host = ""
	}
	var total int64
	local := make([]string, 0, len(p.Paths))
	for _, raw := range p.Paths {
		path, verr := expandPath(raw)
		if verr != nil {
			return nil, verr
		}
		fi, err := os.Stat(path)
		if err != nil {
			return nil, fileError("drop", path, err)
		}
		if fi.Mode().IsRegular() {
			total += fi.Size()
		}
		local = append(local, path)
	}
	if p.Host == "" {
		return map[string]any{"paths": local, "transfers": []TransferRow{}}, nil
	}
	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}
	dir := p.Dir
	if dir == "" {
		ctx, cancel := context.WithTimeout(d.ctx, 15*time.Second)
		var out struct {
			Dir string `json:"dir"`
		}
		err := fileEnd{d: d, host: p.Host}.call(ctx, "file-drop-dir", map[string]any{"bytes": total}, &out)
		cancel()
		if err != nil {
			var ve *VerbCallError
			if errors.As(err, &ve) {
				return nil, newVerbError(ve.Code, ve.Message)
			}
			return nil, newVerbError(ErrVerbHostUnreachable, err.Error())
		}
		dir = out.Dir
	}
	remote := make([]string, 0, len(local))
	rows := make([]TransferRow, 0, len(local))
	used := map[string]bool{}
	for _, path := range local {
		dst := strings.TrimSuffix(dir, "/") + "/" + dropName(filepath.Base(path), used)
		j, err := d.transfers.start(Endpoint{Path: path}, Endpoint{Host: p.Host, Path: dst}, false, "keep-both", true)
		if err != nil {
			return nil, busyTransferError(err)
		}
		remote = append(remote, dst)
		rows = append(rows, j.row())
	}
	return map[string]any{"paths": remote, "dir": dir, "transfers": rows}, nil
}

// dropName is the name a dropped file gets in its drop folder. The client
// types the path into a shell, so a control character in it, which can act
// as a key there, becomes "_", and so does a backslash: fish reads \' inside
// single quotes as a quote, which ends the quoting a POSIX quote gives, and
// the rest of the name would run as a command. Two files of one name in one
// drop get " 2", " 3" ..., so each path the answer gives is the file that
// lands there.
func dropName(base string, used map[string]bool) string {
	name := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '\\' {
			return '_'
		}
		return r
	}, base)
	if name == "" || name == "." || name == ".." || name == "/" {
		name = "dropped"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	cand := name
	for i := 2; used[cand]; i++ {
		cand = stem + " " + strconv.Itoa(i) + ext
	}
	used[cand] = true
	return cand
}

func dropVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"file-drop-dir": {
			description: "Make a private folder on this machine for files dropped on a pane, and answer its path. Drops are kept for 24 hours and 256 MiB together, oldest removed first.",
			params:      []verbParam{{Name: "bytes", Type: "int", Description: "The size of the drop, so older drops make room for it."}},
			returns: []verbParam{
				{Name: "dir", Type: "string", Description: "The new folder, owner only."},
				{Name: "keep_bytes", Type: "int", Description: "How many bytes the drops together may hold."},
				{Name: "keep_hours", Type: "int", Description: "How long a drop is kept."},
			},
			examples: []string{`{"id":1,"verb":"file-drop-dir","params":{"bytes":1048576}}`},
			handler:  (*Daemon).verbFileDropDir,
		},
		"drop-files": {
			description: "Give a pane on a machine the paths of files on this machine. For this machine the answer is the paths. For a host, the files are copied into a new private drop folder there, and the answer has their paths there and the transfers that copy them. Paste a path once its transfer is done.",
			params: []verbParam{
				{Name: "host", Type: "string", Description: "The machine the pane is on. Omit for this machine."},
				{Name: "paths", Type: "[]string", Required: true, Description: "The files on this machine."},
				{Name: "dir", Type: "string", Description: "A folder on the host to copy into instead of a drop folder, such as the pane's own folder."},
			},
			returns: []verbParam{
				{Name: "paths", Type: "[]string", Description: "The files' paths on the pane's machine, in order."},
				{Name: "dir", Type: "string", Description: "The folder they go into on the host."},
				{Name: "transfers", Type: "[]object", Description: "One transfer row per file, as transfer-list. Empty for this machine."},
			},
			examples: []string{`{"id":1,"verb":"drop-files","params":{"host":"build","paths":["~/Desktop/screenshot.png"]}}`},
			handler:  (*Daemon).verbDropFiles,
		},
	}
}
