package transcriptview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/transcript"
)

// Identity is what a transcript says about the session that wrote it: the
// folder the agent ran in and when the session started. It holds nothing of
// the conversation. The daemon's hookless search uses it to decide which
// pane a file belongs to.
type Identity struct {
	// CWD is the agent's working directory, "" when the format does not
	// write it.
	CWD string
	// ProjectHash is Gemini CLI's sha256 of the project folder, written in
	// place of the folder.
	ProjectHash string
	// Start is when the session started, as the file says. Zero when the
	// file does not say.
	Start time.Time
}

// In reports whether the session ran in cwd.
func (id Identity) In(cwd string) bool {
	if cwd == "" {
		return false
	}
	if id.CWD != "" {
		return id.CWD == cwd
	}
	if id.ProjectHash != "" {
		sum := sha256.Sum256([]byte(cwd))
		return id.ProjectHash == hex.EncodeToString(sum[:])
	}
	return false
}

// identityScanMax bounds what Identify reads. A Codex session_meta carries
// the whole instructions and AGENTS.md, so its first record can be hundreds
// of kilobytes, but the fields Identify wants come before them.
const identityScanMax = 4 << 20

// Identify reads the identity of the transcript at path in the named format,
// from the start of its first record. It reports false for a format that
// writes no identity near its start, and for a file it cannot read.
func Identify(name, path string) (Identity, bool) {
	f, ok := lookupFormat(name)
	if !ok || f.identity == nil {
		return Identity{}, false
	}
	file, err := transcript.OpenRegular(path)
	if err != nil {
		return Identity{}, false
	}
	defer func() { _ = file.Close() }()
	id := f.identity(io.LimitReader(file, identityScanMax), filepath.Base(path))
	return id, id.Start != (time.Time{}) && (id.CWD != "" || id.ProjectHash != "")
}

// scanFields reads the first JSON value of r token by token and returns the
// string values at the dotted paths given ("payload.cwd"). It stops as soon
// as it has them all, so a long field after them is never read, and a long
// field before them costs only its own string. Values inside arrays are not
// reached.
func scanFields(r io.Reader, paths ...string) map[string]string {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	out := make(map[string]string, len(paths))
	dec := json.NewDecoder(r)
	type frame struct {
		obj     bool
		key     string
		wantKey bool
	}
	var stack []frame
	path := func() (string, bool) {
		var keys []string
		for _, f := range stack {
			if !f.obj {
				return "", false
			}
			keys = append(keys, f.key)
		}
		return strings.Join(keys, "."), true
	}
	// valueDone marks the value of the open object's key as read.
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].obj {
			stack[n-1].wantKey = true
		}
	}
	for len(out) < len(want) {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				stack = append(stack, frame{obj: true, wantKey: true})
			case '[':
				stack = append(stack, frame{})
			default:
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				if len(stack) == 0 {
					return out
				}
				valueDone()
			}
		case string:
			if n := len(stack); n > 0 && stack[n-1].obj && stack[n-1].wantKey {
				stack[n-1].key, stack[n-1].wantKey = v, false
				continue
			}
			if p, ok := path(); ok && want[p] {
				out[p] = v
			}
			valueDone()
		default:
			valueDone()
		}
	}
	return out
}

// parseTime is an RFC 3339 time, zero when it is not one.
//
//go:noinline
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// codexIdentity reads a rollout's session_meta: payload.cwd, and the
// session's start from payload.timestamp, the line's timestamp, or the time
// in the file name.
func codexIdentity(r io.Reader, name string) Identity {
	f := scanFields(r, "type", "timestamp", "payload.cwd", "payload.timestamp")
	if f["type"] != "session_meta" {
		return Identity{}
	}
	id := Identity{CWD: f["payload.cwd"], Start: parseTime(f["payload.timestamp"])}
	if id.Start.IsZero() {
		id.Start = parseTime(f["timestamp"])
	}
	if id.Start.IsZero() {
		// rollout-2026-10-18T09-00-00-<thread id>.jsonl
		if rest, ok := strings.CutPrefix(name, "rollout-"); ok && len(rest) >= 19 {
			if t, err := time.ParseInLocation("2006-01-02T15-04-05", rest[:19], time.Local); err == nil {
				id.Start = t
			}
		}
	}
	return id
}

// geminiIdentity reads a chat's projectHash and startTime, from the first
// line of the JSONL or the top of the older document.
func geminiIdentity(r io.Reader, _ string) Identity {
	f := scanFields(r, "projectHash", "startTime")
	return Identity{ProjectHash: f["projectHash"], Start: parseTime(f["startTime"])}
}

// qwenIdentity reads the first record's cwd and timestamp.
func qwenIdentity(r io.Reader, _ string) Identity {
	f := scanFields(r, "cwd", "timestamp")
	return Identity{CWD: f["cwd"], Start: parseTime(f["timestamp"])}
}
