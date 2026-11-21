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
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const pasteImageMaxBytes = stashTransferMaxBytes

// PasteImageMaxBytes is pasteImageMaxBytes, for the client's own check before
// it sends anything.
const PasteImageMaxBytes = pasteImageMaxBytes

const pasteFileTTL = time.Hour

const pasteReplyPrefix = pasteFilePrefix + "reply-"

const pasteReplyTTL = 24 * time.Hour

const (
	pasteMaxFiles    = 50
	pasteMaxDirBytes = 100 << 20
)

const (
	pasteFilePerm = 0o600
	pasteDirPerm  = 0o700
)

const pasteFilePrefix = "tuios-paste-"

const pasteHostedBudget = 60 * time.Second

var errPasteNotImage = errors.New("the content is not a PNG, JPEG, GIF, WebP, BMP or TIFF image")

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

type pasteStore struct {
	mu       sync.Mutex
	dir      func() (string, error)
	now      func() time.Time
	ttl      time.Duration
	replyTTL time.Duration
	maxFiles int
	maxBytes int64
	written  map[string]bool
	timer    *time.Timer
	seq      uint64
}

var errPasteDirLink = errors.New("the paste folder is a symbolic link or not a folder. Remove it and paste again")

func newPasteStore(socketPath func() string) *pasteStore {
	return &pasteStore{
		dir: func() (string, error) {
			sock := socketPath()
			if sock == "" {
				return "", errors.New("the daemon has no socket path, so it has nowhere to put a pasted image")
			}
			return filepath.Join(filepath.Dir(sock), "paste"), nil
		},
		now:      time.Now,
		ttl:      pasteFileTTL,
		replyTTL: pasteReplyTTL,
		written:  map[string]bool{},
	}
}

func (s *pasteStore) save(data []byte) (string, error) {
	if s == nil {
		return "", errors.New("this daemon has no store for pasted images")
	}
	if len(data) > pasteImageMaxBytes {
		return "", errfmt.Errorf("the image is %d bytes and the limit is %d bytes", len(data), pasteImageMaxBytes)
	}
	ext := sniffImage(data)
	if ext == "" {
		return "", errPasteNotImage
	}
	return s.write(data, pasteFilePrefix, ext, nil)
}

func (s *pasteStore) saveReply(data []byte, ext string, held map[string]bool) (string, error) {
	if s == nil {
		return "", errors.New("this daemon has no store for pasted files")
	}
	return s.write(data, pasteReplyPrefix, ext, held)
}

func (s *pasteStore) write(data []byte, prefix, ext string, held map[string]bool) (string, error) {
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
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errfmt.Errorf("%s: %w", dir, errPasteDirLink)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if opened, err := root.Stat("."); err != nil || !os.SameFile(opened, info) {
		return "", errfmt.Errorf("%s: %w", dir, errPasteDirLink)
	}
	_ = root.Chmod(".", pasteDirPerm)
	s.sweepLocked(dir)

	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	s.seq++
	name := prefix + s.now().Format("20060102-150405") + "-" + fmt.Sprintf("%010d", s.seq) + "-" + hex.EncodeToString(rnd[:]) + ext
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, pasteFilePerm)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = root.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(name)
		return "", err
	}
	path := filepath.Join(dir, name)
	s.written[path] = true
	if held != nil {
		held[path] = true
	}
	s.capLocked(dir, path, held)
	s.armLocked(dir)
	return path, nil
}

func (s *pasteStore) capLocked(dir, keep string, held map[string]bool) {
	maxFiles, maxBytes := s.maxFiles, s.maxBytes
	if maxFiles <= 0 {
		maxFiles = pasteMaxFiles
	}
	if maxBytes <= 0 {
		maxBytes = pasteMaxDirBytes
	}
	type file struct {
		path string
		key  string
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
		files = append(files, file{filepath.Join(dir, e.Name()), pasteNameKey(e.Name()), info.ModTime(), info.Size()})
		total += info.Size()
	}
	sortx.SortFunc(files, func(a, b file) int {
		if c := a.mod.Compare(b.mod); c != 0 {
			return c
		}
		if c := strings.Compare(a.key, b.key); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
	count := len(files)
	for i := 0; i < len(files) && (count > maxFiles || total > maxBytes); i++ {
		if files[i].path == keep || held[files[i].path] {
			continue
		}
		if err := os.Remove(files[i].path); err == nil || os.IsNotExist(err) {
			delete(s.written, files[i].path)
			total -= files[i].size
			count--
		}
	}
}

func pasteNameKey(name string) string {
	if rest, ok := strings.CutPrefix(name, pasteReplyPrefix); ok {
		return rest
	}
	return strings.TrimPrefix(name, pasteFilePrefix)
}

func (s *pasteStore) pastedEntries(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(entries, func(e os.DirEntry) bool {
		return !strings.HasPrefix(e.Name(), pasteFilePrefix) || !e.Type().IsRegular()
	})
}

func (s *pasteStore) ttlFor(name string) time.Duration {
	if strings.HasPrefix(name, pasteReplyPrefix) && s.replyTTL > 0 {
		return s.replyTTL
	}
	return s.ttl
}

func (s *pasteStore) armLocked(dir string) {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	var first time.Time
	for _, e := range s.pastedEntries(dir) {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if expiry := info.ModTime().Add(s.ttlFor(e.Name())); first.IsZero() || expiry.Before(first) {
			first = expiry
		}
	}
	if first.IsZero() {
		return
	}
	wait := max(first.Sub(s.now())+10*time.Millisecond, 10*time.Millisecond)
	s.timer = time.AfterFunc(wait, s.sweep)
}

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

func (s *pasteStore) sweepLocked(dir string) {
	now := s.now()
	for _, e := range s.pastedEntries(dir) {
		info, err := e.Info()
		if err != nil || info.ModTime().After(now.Add(-s.ttlFor(e.Name()))) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			delete(s.written, path)
		}
	}
}

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

func decodePasteContent(verb, content string) ([]byte, *verbError) {
	if content == "" {
		return nil, invalidParam("content", verb+" needs the image, base64 encoded, in content")
	}
	tooBig := hintedVerbError(ErrVerbInvalidParams, verb+": the image is larger than the limit", &VerbHint{
		Param:  "content",
		Detail: "A pasted image is limited to " + units.Bytes(int64(pasteImageMaxBytes)) + ". Save a smaller image and paste it again.",
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	if p.HumanNonce == "" || !d.verifyHumanNonce(p.HumanNonce, sess.ID, cs) {
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
			if far, ok := errors.AsType[*verbError](err); ok && far.Code == ErrVerbForbidden {
				return nil, hintedVerbError(ErrVerbForbidden, "paste-image: "+rp.host+" refused the image: "+far.Message, refusedOn(far.Hint, rp.host))
			}
			return nil, hintedVerbError(ErrVerbHostUnreachable, "paste-image: the image did not reach "+rp.host+": "+err.Error(), &VerbHint{
				Detail: "Nothing was pasted. Check that the link to " + rp.host + " is up and that its tuios is current.",
			})
		}
		return map[string]any{"type": "pasted_image", "session": sess.Name(), "window": w.ID, "host": rp.host, "path": path, "bytes": len(data)}, nil
	}

	host, elsewhere, err := d.paneRemoteHost(pty)
	if refusal, ok := errors.AsType[pasteRefusal](err); ok {
		hint := &VerbHint{Detail: refusal.detail}
		if refusal.noLink {
			hint.Command = "tuios hosts add <name> <addr>"
		}
		return nil, hintedVerbError(ErrVerbUnknownHost, "paste-image: "+refusal.detail, hint)
	}
	if elsewhere {
		ctx, cancel := context.WithTimeout(context.Background(), pasteHostedBudget)
		defer cancel()
		path, err := d.pasteToHost(ctx, host, data)
		if err != nil {
			if far, ok := errors.AsType[*VerbCallError](err); ok && far.Code == ErrVerbForbidden {
				return nil, hintedVerbError(ErrVerbForbidden, "paste-image: "+host+" refused the image: "+far.Message, refusedOn(far.Hint, host))
			}
			return nil, hintedVerbError(ErrVerbHostUnreachable, "paste-image: the image did not reach "+host+": "+err.Error(), &VerbHint{
				Detail: "Nothing was pasted. Check that the link to " + host + " is up and that its tuios is current.",
			})
		}
		return map[string]any{"type": "pasted_image", "session": sess.Name(), "window": w.ID, "host": host, "path": path, "bytes": len(data)}, nil
	}

	path, err := d.pastes.save(data)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "paste-image: cannot write the image: "+err.Error())
	}
	return map[string]any{"type": "pasted_image", "session": sess.Name(), "window": w.ID, "path": path, "bytes": len(data)}, nil
}

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

const maxPasteReply = 64 * 1024

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
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = stream.Close()
	}()

	params, err := jsonx.Marshal(map[string]string{
		"pane":    p.id,
		"token":   p.callsToken,
		"content": base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		return "", err
	}
	req, err := jsonx.Marshal(verbRequest{ID: json.RawMessage(`1`), Verb: "paste-pane-image", Params: params})
	if err != nil {
		return "", err
	}
	if _, err := stream.Write(append(req, '\n')); err != nil {
		return "", errfmt.Errorf("cannot send the image: %w", err)
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
	if err := jsonx.Unmarshal(line, &resp); err != nil {
		return "", errfmt.Errorf("the reply cannot be read by this build: %w", err)
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

func refusedOn(h *VerbHint, host string) *VerbHint {
	out := &VerbHint{}
	if h != nil {
		c := *h
		out = &c
	}
	out.Host = host
	return out
}
