package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const stashTransferMaxBytes = 8 << 20

func (d *Daemon) verbStashPut(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string  `json:"session"`
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Path == "" {
		return nil, invalidParam("path", "path is required: name the file to store")
	}
	viaLink := cs != nil && cs.viaLink
	if viaLink && p.Content == nil {
		return nil, hintedVerbError(ErrVerbForbidden, "stash put "+echoName(p.Path)+": a linked machine sends the file's bytes as content", &VerbHint{
			Param:  "content",
			Detail: "Nothing was stored. Send the file's bytes, base64, in content.",
		})
	}
	content := ""
	if p.Content != nil {
		content = *p.Content
	}

	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	referenced := func() map[string]bool {
		return d.agents.referencedPaths(sess.Name())
	}
	var res stashResult
	var err error
	if content != "" || viaLink {
		if len(content) > base64.StdEncoding.EncodedLen(stashTransferMaxBytes) {
			return nil, hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(p.Path)+": the content is larger than the transfer cap", &VerbHint{
				Param:  "content",
				Detail: "A file that crosses the socket as bytes is capped at " + units.Bytes(int64(stashTransferMaxBytes)) + ".",
			})
		}
		data, derr := base64.StdEncoding.DecodeString(content)
		if derr != nil {
			return nil, invalidParam("content", "content is not base64")
		}
		if len(data) > stashTransferMaxBytes {
			return nil, hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(p.Path)+": the content is larger than the transfer cap", &VerbHint{
				Param:  "content",
				Detail: "A file that crosses the socket as bytes is capped at " + units.Bytes(int64(stashTransferMaxBytes)) + ".",
			})
		}
		res, err = d.stash.putFrom(sess.ID, p.Path, bytes.NewReader(data), referenced)
	} else {
		res, err = d.stash.put(sess.ID, p.Path, referenced)
	}
	if err != nil {
		return nil, stashPutError(p.Path, err)
	}

	return map[string]any{
		"type":            "stash_entry",
		"session":         sess.Name(),
		"path":            res.Entry.Path,
		"name":            res.Entry.Name,
		"hash":            res.Entry.Hash,
		"bytes":           res.Entry.Bytes,
		"media_type":      res.Entry.MediaType,
		"kind":            res.Entry.Kind,
		"stored_at":       res.Entry.StoredAt,
		"deduped":         res.Deduped,
		"evicted":         res.Evicted,
		"evictions":       res.Evictions,
		"session_bytes":   res.Bytes,
		"session_entries": res.Entries,
		"max_file_bytes":  stashMaxFileBytes,
		"max_bytes":       stashMaxSessionBytes,
	}, nil
}

func (d *Daemon) verbStashList(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	listing, err := d.stash.list(sess.ID, func() map[string]bool {
		return d.agents.referencedPaths(sess.Name())
	})
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot read the stash directory: "+err.Error())
	}

	entries := make([]map[string]any, 0, len(listing.Entries))
	for _, e := range listing.Entries {
		entries = append(entries, map[string]any{
			"path":       e.Path,
			"name":       e.Name,
			"hash":       e.Hash,
			"bytes":      e.Bytes,
			"media_type": e.MediaType,
			"kind":       e.Kind,
			"source":     e.Source,
			"stored_at":  e.StoredAt,
			"referenced": listing.Reference[e.Path],
			"missing":    stashMissing(e.Path),
		})
	}

	return map[string]any{
		"type":           "stash_list",
		"session":        sess.Name(),
		"dir":            listing.Dir,
		"entries":        entries,
		"total":          len(entries),
		"bytes":          listing.Bytes,
		"evicted":        listing.Evicted,
		"max_file_bytes": stashMaxFileBytes,
		"max_bytes":      stashMaxSessionBytes,
	}, nil
}

func (d *Daemon) verbStashGet(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Path    string `json:"path"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Path == "" {
		return nil, invalidParam("path", "path is required: name the stashed file to read")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	f, entry, err := d.stash.open(sess.ID, p.Path)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "stash get "+echoName(p.Path)+": not a file in this session's stash", &VerbHint{
			Param:   "path",
			Command: "tuios stash list -s " + sess.Name(),
			Detail:  "Only a path the stash printed can be read back. The listing shows them.",
		})
	}
	defer func() { _ = f.Close() }()
	if entry.Bytes > stashTransferMaxBytes {
		return nil, hintedVerbError(ErrVerbInvalidParams, "stash get "+echoName(p.Path)+": the file is larger than the transfer cap", &VerbHint{
			Param:  "path",
			Detail: "A file that crosses the socket as bytes is capped at " + units.Bytes(int64(stashTransferMaxBytes)) + ". Copy it another way.",
		})
	}
	data, err := io.ReadAll(io.LimitReader(f, stashTransferMaxBytes+1))
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot read the stashed file: "+err.Error())
	}
	if len(data) > stashTransferMaxBytes {
		return nil, newVerbError(ErrVerbInternal, "the stashed file grew past the transfer cap")
	}
	return map[string]any{
		"type":       "stash_content",
		"session":    sess.Name(),
		"path":       entry.Path,
		"name":       entry.Name,
		"hash":       entry.Hash,
		"bytes":      len(data),
		"media_type": entry.MediaType,
		"kind":       entry.Kind,
		"content":    base64.StdEncoding.EncodeToString(data),
	}, nil
}

func stashMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil
}

func stashPutError(path string, err error) *verbError {
	switch {
	case errors.Is(err, errStashNotAbsolute):
		return hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(path)+": the path is not absolute", &VerbHint{
			Param:  "path",
			Detail: "The daemon may run in another directory than you do. Give the full path.",
		})
	case errors.Is(err, errStashIsDirectory):
		return hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(path)+": the path is a directory", &VerbHint{
			Param:  "path",
			Detail: "The stash stores one file at a time. Make an archive of the directory and stash that.",
		})
	case errors.Is(err, errStashNotRegular):
		return hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(path)+": the path is not a regular file", &VerbHint{
			Param:  "path",
			Detail: "A pipe, socket or device has no fixed content to copy. Write the bytes to a file first.",
		})
	case errors.Is(err, os.ErrNotExist):
		return hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(path)+": no such file", &VerbHint{
			Param:  "path",
			Detail: "The daemon opens the file itself, on its own host. Check the path exists there.",
		})
	case errors.Is(err, os.ErrPermission):
		return hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(path)+": the daemon cannot read the file", &VerbHint{
			Param:  "path",
			Detail: "The daemon opens the file as the user that started it. Give it read permission, or copy the file somewhere it can read.",
		})
	}
	return hintedVerbError(ErrVerbInvalidParams, "stash put "+echoName(path)+": "+err.Error(), &VerbHint{
		Param: "path",
		Detail: "One file is capped at " + units.Bytes(int64(stashMaxFileBytes)) + " and a session at " +
			units.Bytes(int64(stashMaxSessionBytes)) + ". Attach the file by its own path instead: an attachment is never copied.",
	})
}
