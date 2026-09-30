package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pasting an image into a pane.
//
// A terminal pastes text. When the person's clipboard holds a screenshot, the
// host terminal has nothing to send, and an agent in the pane that takes an
// image by path (Claude Code, Codex and the rest) never gets it. The tuios
// client reads the image itself, on the machine where the person is, and hands
// the bytes to the daemon with paste-image. The daemon writes them to a file on
// the machine where the pane's process runs and answers with that file's path,
// which the client then pastes as ordinary text.
//
// Where the file is written is the whole point. For a pane of this daemon it is
// here. For a pane whose process runs on another machine (a window of a global
// session) a path here means nothing to that process, so the bytes cross the
// link on a connection of their own and the far daemon writes them with
// paste-pane-image. Either way the path the client pastes is one the pane's
// process can open.
//
// Who may do it. paste-image is the person's act: it carries the attach nonce
// and is refused from inside a pane, the same rule a reply as the person
// follows (human_sender.go, human_origin.go). A pane that could call it could
// not read the person's clipboard with it, since the client does the reading,
// but it could plant files and have a path typed as if the person had pasted
// it. paste-pane-image, on the far machine, is taken only with the calls token
// of the hosted pane it names. That token is in the open-pane reply and nowhere
// else, so only the daemon that owns the window can use it, and that daemon
// forwards only a paste-image it has already taken as the person's.

// pasteImageMaxBytes bounds one pasted image. The bytes travel base64 in one
// request line, which is capped at 16 MiB, so this is the stash's transfer cap.
// A screenshot of a 5K display is a few megabytes.
const pasteImageMaxBytes = stashTransferMaxBytes

// PasteImageMaxBytes is pasteImageMaxBytes, for the client's own check before
// it sends anything.
const PasteImageMaxBytes = pasteImageMaxBytes

// pasteFileTTL is how long a pasted image stays on disk. An agent reads the
// file when the prompt it was pasted into is sent, which is seconds or minutes
// later. An hour covers a prompt that is written slowly and still keeps the
// directory from growing for the life of the daemon.
const pasteFileTTL = time.Hour

// pasteMaxFiles and pasteMaxDirBytes bound the paste directory. It is under
// the runtime directory, which is often a small tmpfs, and a run of pastes
// inside the TTL must not fill it. The oldest pasted files go first.
const (
	pasteMaxFiles    = 50
	pasteMaxDirBytes = 100 << 20
)

// pasteFilePerm and pasteDirPerm keep the files the owner's alone. A
// screenshot can hold anything that was on the screen.
const (
	pasteFilePerm = 0o600
	pasteDirPerm  = 0o700
)

// pasteFilePrefix starts every pasted file's name. The sweep deletes only
// names with this prefix, so a directory shared with a standalone tuios, or a
// file somebody put there by hand, is left alone.
const pasteFilePrefix = "tuios-paste-"

// pasteHostedBudget bounds a paste that crosses a link: dialing a stream, the
// bytes, and the far daemon's write. It is longer than a control call because
// eight megabytes over a slow ssh link takes a while.
const pasteHostedBudget = 60 * time.Second

// errPasteNotImage is a paste whose bytes are not an image tuios recognises.
var errPasteNotImage = errors.New("the content is not a PNG, JPEG, GIF, WebP, BMP or TIFF image")

// sniffImage returns the extension for an image's bytes, from their magic
// number, or "" when they are not an image this accepts. The extension is read
// from the bytes and never taken from the caller, so a paste cannot name a
// file .sh or .desktop.
func sniffImage(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return ".png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return ".gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return ".webp"
	case bytes.HasPrefix(data, []byte("BM")) && len(data) >= 26:
		return ".bmp"
	case bytes.HasPrefix(data, []byte("II*\x00")), bytes.HasPrefix(data, []byte("MM\x00*")):
		return ".tiff"
	}
	return ""
}

// pasteStore writes pasted images to one directory and deletes them again.
//
// Files are deleted four ways: a timer deletes each pasted file once it is
// older than the TTL, a save deletes the oldest files past the directory's
// caps, the daemon deletes the files it wrote when it stops, and a daemon
// that starts deletes the expired ones a killed daemon left behind. On Linux
// and macOS the directory is under the runtime directory or /tmp, which a
// reboot clears. On Windows it is under the user's local app data, which a
// reboot keeps, so there the start sweep is what clears a killed daemon's
// files.
type pasteStore struct {
	mu  sync.Mutex
	dir func() (string, error)
	now func() time.Time
	ttl time.Duration
	// maxFiles and maxBytes cap the directory. Zero means the defaults.
	maxFiles int
	maxBytes int64
	// written is every file this store wrote that is still on disk, which is
	// what a stop deletes. A directory shared with another process keeps that
	// process's files.
	written map[string]bool
	// timer runs the next TTL sweep, nil when nothing is waiting for one.
	timer *time.Timer
}

// errPasteDirLink is a paste directory that is a symbolic link. Following it
// would write the images wherever it points.
var errPasteDirLink = errors.New("the paste folder is a symbolic link or not a folder. Remove it and paste again")

// newPasteStore returns a store in the "paste" directory beside the socket.
func newPasteStore(socketPath func() string) *pasteStore {
	return &pasteStore{
		dir: func() (string, error) {
			sock := socketPath()
			if sock == "" {
				return "", errors.New("the daemon has no socket path, so it has nowhere to put a pasted image")
			}
			return filepath.Join(filepath.Dir(sock), "paste"), nil
		},
		now:     time.Now,
		ttl:     pasteFileTTL,
		written: map[string]bool{},
	}
}

// save writes one image and returns its absolute path.
func (s *pasteStore) save(data []byte) (string, error) {
	if s == nil {
		return "", errors.New("this daemon has no store for pasted images")
	}
	if len(data) > pasteImageMaxBytes {
		return "", fmt.Errorf("the image is %d bytes and the limit is %d bytes", len(data), pasteImageMaxBytes)
	}
	ext := sniffImage(data)
	if ext == "" {
		return "", errPasteNotImage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), pasteDirPerm); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, pasteDirPerm); err != nil && !os.IsExist(err) {
		return "", err
	}
	// A link planted where the directory goes is refused, not followed.
	if info, err := os.Lstat(dir); err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", fmt.Errorf("%s: %w", dir, errPasteDirLink)
	}
	// Mkdir leaves an existing directory's mode alone. This one is ours, so
	// it is put back to the owner's alone.
	_ = os.Chmod(dir, pasteDirPerm)
	s.sweepLocked(dir)

	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	name := pasteFilePrefix + s.now().Format("20060102-150405") + "-" + hex.EncodeToString(rnd[:]) + ext
	path := filepath.Join(dir, name)
	// O_EXCL, so a name that somehow exists, or a link planted under it, is
	// refused rather than written through.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, pasteFilePerm)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	s.written[path] = true
	s.capLocked(dir, path)
	s.armLocked(dir)
	return path, nil
}

// capLocked deletes the oldest pasted files until the directory is within
// its caps. keep, the file just written, is never deleted.
func (s *pasteStore) capLocked(dir, keep string) {
	maxFiles, maxBytes := s.maxFiles, s.maxBytes
	if maxFiles <= 0 {
		maxFiles = pasteMaxFiles
	}
	if maxBytes <= 0 {
		maxBytes = pasteMaxDirBytes
	}
	type file struct {
		path string
		mod  time.Time
		size int64
	}
	var files []file
	var total int64
	for _, e := range s.pastedEntries(dir) {
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), info.ModTime(), info.Size()})
		total += info.Size()
	}
	slices.SortFunc(files, func(a, b file) int {
		if c := a.mod.Compare(b.mod); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
	for i := 0; i < len(files) && (len(files)-i > maxFiles || total > maxBytes); i++ {
		if files[i].path == keep {
			continue
		}
		if err := os.Remove(files[i].path); err == nil || os.IsNotExist(err) {
			delete(s.written, files[i].path)
			total -= files[i].size
		}
	}
}

// pastedEntries lists the pasted files in dir.
func (s *pasteStore) pastedEntries(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(entries, func(e os.DirEntry) bool {
		return !strings.HasPrefix(e.Name(), pasteFilePrefix) || !e.Type().IsRegular()
	})
}

// armLocked sets the timer for the next TTL sweep: when the oldest pasted
// file in dir expires. With no pasted file left it sets none.
func (s *pasteStore) armLocked(dir string) {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	var oldest time.Time
	for _, e := range s.pastedEntries(dir) {
		if info, err := e.Info(); err == nil && (oldest.IsZero() || info.ModTime().Before(oldest)) {
			oldest = info.ModTime()
		}
	}
	if oldest.IsZero() {
		return
	}
	wait := max(oldest.Add(s.ttl).Sub(s.now())+10*time.Millisecond, 10*time.Millisecond)
	s.timer = time.AfterFunc(wait, s.sweep)
}

// sweep deletes the pasted files older than the TTL.
func (s *pasteStore) sweep() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.dir()
	if err != nil {
		return
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return
	}
	s.sweepLocked(dir)
	s.armLocked(dir)
}

// sweepLocked is sweep with s.mu held.
func (s *pasteStore) sweepLocked(dir string) {
	cutoff := s.now().Add(-s.ttl)
	for _, e := range s.pastedEntries(dir) {
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			delete(s.written, path)
		}
	}
}

// removeWritten deletes every file this store wrote. The daemon calls it when
// it stops.
func (s *pasteStore) removeWritten() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	for path := range s.written {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			LogError("Failed to remove pasted image %s: %v", path, err)
		}
	}
	s.written = map[string]bool{}
}

// localPasteStore is the store a client with no daemon writes to. It is made
// on first use, beside the socket a daemon would use.
var (
	localPasteOnce  sync.Once
	localPasteStore *pasteStore
)

// SavePastedImage writes an image to this machine's paste directory and
// returns the path. A client with no daemon uses it: its panes run here.
func SavePastedImage(data []byte) (string, error) {
	localPasteOnce.Do(func() {
		localPasteStore = newPasteStore(func() string {
			p, err := GetSocketPath()
			if err != nil {
				return ""
			}
			return p
		})
	})
	return localPasteStore.save(data)
}

// decodePasteContent reads the base64 content of a paste request and holds it
// to the size cap.
func decodePasteContent(verb, content string) ([]byte, *verbError) {
	if content == "" {
		return nil, invalidParam("content", verb+" needs the image, base64 encoded, in content")
	}
	tooBig := hintedVerbError(ErrVerbInvalidParams, verb+": the image is larger than the limit", &VerbHint{
		Param:  "content",
		Detail: "A pasted image is limited to " + strconv.Itoa(pasteImageMaxBytes>>20) + " MB. Save a smaller image and paste it again.",
	})
	if len(content) > base64.StdEncoding.EncodedLen(pasteImageMaxBytes) {
		return nil, tooBig
	}
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, invalidParam("content", "content is not base64")
	}
	if len(data) > pasteImageMaxBytes {
		return nil, tooBig
	}
	if sniffImage(data) == "" {
		return nil, invalidParam("content", verb+": "+errPasteNotImage.Error())
	}
	return data, nil
}

// verbPasteImage writes an image the person pasted to the machine where a
// pane's process runs, and answers with the path there. See the file comment.
func (d *Daemon) verbPasteImage(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		Window     string `json:"window"`
		Content    string `json:"content"`
		HumanNonce string `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := refuseForwardedPane(cs, "paste-image"); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.HumanNonce == "" || !d.matchHumanNonce(p.HumanNonce, sess.ID, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "paste-image is the person's act, and human_nonce does not belong to a client attached to this session right now", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was written. Only the person's attached client pastes an image, and a process inside a pane cannot.",
		})
	}
	data, verr := decodePasteContent("paste-image", p.Content)
	if verr != nil {
		return nil, verr
	}
	w, pty, verr := d.resolveWindowPTY(sess, p.Window)
	if verr != nil {
		return nil, verr
	}

	if rp, ok := pty.pty.(*remotePane); ok {
		ctx, cancel := context.WithTimeout(context.Background(), pasteHostedBudget)
		defer cancel()
		path, err := rp.pasteImage(ctx, data)
		if err != nil {
			return nil, hintedVerbError(ErrVerbHostUnreachable, "paste-image: the image did not reach "+rp.host+": "+err.Error(), &VerbHint{
				Detail: "Nothing was pasted. Check that the link to " + rp.host + " is up and that its tuios is current.",
			})
		}
		return map[string]any{"type": "pasted_image", "session": sess.Name(), "window": w.ID, "host": rp.host, "path": path, "bytes": len(data)}, nil
	}

	path, err := d.pastes.save(data)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "paste-image: cannot write the image: "+err.Error())
	}
	return map[string]any{"type": "pasted_image", "session": sess.Name(), "window": w.ID, "path": path, "bytes": len(data)}, nil
}

// verbPastePaneImage writes an image for a pane this machine runs for another
// machine. Only that machine's daemon holds the token it needs.
func (d *Daemon) verbPastePaneImage(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane    string `json:"pane"`
		Token   string `json:"token"`
		Content string `json:"content"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "paste-pane-image needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	if hp.callsToken == "" || subtle.ConstantTimeCompare([]byte(hp.callsToken), []byte(p.Token)) != 1 {
		return nil, newVerbError(ErrVerbForbidden, "paste-pane-image: the token is not this pane's. Only the daemon that owns the window can paste into it.")
	}
	data, verr := decodePasteContent("paste-pane-image", p.Content)
	if verr != nil {
		return nil, verr
	}
	path, err := d.pastes.save(data)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "paste-pane-image: cannot write the image: "+err.Error())
	}
	return map[string]any{"pane": p.Pane, "path": path, "bytes": len(data)}, nil
}

// maxPasteReply bounds the far daemon's answer to paste-pane-image.
const maxPasteReply = 64 * 1024

// pasteImage sends an image to the far machine and returns the path it was
// written to there. The bytes go on a connection of their own: the link's
// control stream carries every other call, and eight megabytes on it would
// hold them all up.
func (p *remotePane) pasteImage(ctx context.Context, data []byte) (string, error) {
	if p.fed == nil {
		return "", errors.New("this daemon has no links")
	}
	if p.callsToken == "" {
		return "", errors.New("the tuios there is too old to take a pasted image. Update it, then restart its daemon")
	}
	stream, err := p.fed.OpenConnection(ctx, p.host)
	if err != nil {
		return "", err
	}
	// Closing the stream is what ends a read that ctx has given up on.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = stream.Close()
	}()

	params, err := json.Marshal(map[string]string{
		"pane":    p.id,
		"token":   p.callsToken,
		"content": base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		return "", err
	}
	req, err := json.Marshal(verbRequest{ID: json.RawMessage(`1`), Verb: "paste-pane-image", Params: params})
	if err != nil {
		return "", err
	}
	if _, err := stream.Write(append(req, '\n')); err != nil {
		return "", fmt.Errorf("cannot send the image: %w", err)
	}
	line, err := readLimitedLine(bufio.NewReader(stream), maxPasteReply)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, io.EOF) {
			return "", errors.New("the far machine closed the connection")
		}
		return "", err
	}
	var resp struct {
		Result *struct {
			Path string `json:"path"`
		} `json:"result"`
		Error *verbError `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", fmt.Errorf("the reply cannot be read by this build: %w", err)
	}
	if resp.Error != nil {
		if resp.Error.Code == ErrVerbUnknownVerb {
			return "", errors.New("the tuios there is too old to take a pasted image. Update it, then restart its daemon")
		}
		return "", resp.Error
	}
	if resp.Result == nil || resp.Result.Path == "" {
		return "", errors.New("the reply named no path")
	}
	return resp.Result.Path, nil
}
