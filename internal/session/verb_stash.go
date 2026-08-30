package session

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
)

// This file holds the two stash verbs. They are the whole surface: put bytes in,
// list what is in. There is no get and no delete on purpose.
//
// No get, because the answer to a put is a path, and a path is already the thing
// every reader on this host can open. A verb that streamed the bytes back
// through the socket would be the copy the design exists to avoid.
//
// No delete, because the lifetime is the session's and an agent that could
// delete could delete a file another agent's message still names. The only
// deletions are the session ending, the daemon stopping, and the cap forcing a
// reclaim, and all three are the daemon's own.

// verbStashPut copies a file into the session's store and answers with the
// stored path.
func (d *Daemon) verbStashPut(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Path    string `json:"path"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Path == "" {
		return nil, invalidParam("path", "path is required: name the file to store")
	}

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	res, err := d.stash.put(sess.ID, p.Path, func() map[string]bool {
		return d.agents.referencedPaths(sess.Name)
	})
	if err != nil {
		return nil, stashPutError(p.Path, err)
	}

	return map[string]any{
		"type":    "stash_entry",
		"session": sess.Name,
		"path":    res.Entry.Path,
		"name":    res.Entry.Name,
		"hash":    res.Entry.Hash,
		"bytes":   res.Entry.Bytes,
		// media_type and kind are the same two fields an attachment carries, read
		// from the extension, so a caller can decide what to do with the file
		// without opening it.
		"media_type": res.Entry.MediaType,
		"kind":       res.Entry.Kind,
		"stored_at":  res.Entry.StoredAt,
		// deduped says the bytes were already here and this put stored nothing
		// new. The path is the one that already existed.
		"deduped": res.Deduped,
		// evicted is how many files this put deleted to make room; evictions is
		// the session's total since it started. A caller that sees either move
		// knows a file it stashed earlier may be gone.
		"evicted":         res.Evicted,
		"evictions":       res.Evictions,
		"session_bytes":   res.Bytes,
		"session_entries": res.Entries,
		"max_file_bytes":  stashMaxFileBytes,
		"max_bytes":       stashMaxSessionBytes,
	}, nil
}

// verbStashList reports what a session's store holds.
func (d *Daemon) verbStashList(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	listing, err := d.stash.list(sess.ID, func() map[string]bool {
		return d.agents.referencedPaths(sess.Name)
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
			// referenced says a message still in this session's ring names this
			// file. Those are never evicted, so this is also the answer to "is
			// this one safe".
			"referenced": listing.Reference[e.Path],
			// missing says the file is not on disk any more. It should never be
			// true while the daemon is running, and it is resolved rather than
			// assumed for the same reason an attachment's is.
			"missing": stashMissing(e.Path),
		})
	}

	return map[string]any{
		"type":           "stash_list",
		"session":        sess.Name,
		"dir":            listing.Dir,
		"entries":        entries,
		"total":          len(entries),
		"bytes":          listing.Bytes,
		"evicted":        listing.Evicted,
		"max_file_bytes": stashMaxFileBytes,
		"max_bytes":      stashMaxSessionBytes,
	}, nil
}

// stashMissing reports whether a stored file has gone from disk.
func stashMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil
}

// stashPutError turns a store failure into the code and the remedy for it. Each
// class has a different thing the caller has to do, so each gets its own hint
// rather than one generic refusal.
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
		Detail: "One file is capped at " + strconv.Itoa(stashMaxFileBytes>>20) + " MB and a session at " +
			strconv.Itoa(stashMaxSessionBytes>>20) + " MB. Attach the file by its own path instead: an attachment is never copied.",
	})
}
