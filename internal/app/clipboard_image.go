package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Reading an image from the system clipboard.
//
// The host terminal cannot carry an image. A bracketed paste is text, and the
// OSC 52 read answers with text, so a screenshot on the clipboard reaches tuios
// as nothing at all. tuios reads it itself, with the tool each platform has for
// it, on the machine where the client runs:
//
//   - Wayland: wl-paste --list-types, then wl-paste --type image/png.
//   - X11: xclip -selection clipboard -t TARGETS -o, then -t image/png -o.
//     xsel has no way to ask for a type, so it reads no image.
//   - macOS: osascript's clipboard info, then pngpaste when it is installed
//     and the clipboard as «class PNGf» when it is not.
//   - Windows: PowerShell's System.Windows.Forms.Clipboard.
//
// The client runs on the person's machine in every case this is used for:
// imagePasteReason refuses a browser tab and a remote SSH client, whose
// clipboard is somewhere else. A client started inside ssh is checked first:
// a WAYLAND_DISPLAY or DISPLAY it inherited (from a tmux started on the far
// desktop, say) names that desktop's clipboard, not the person's. The one
// display that is the person's inside ssh is the one ssh -X forwards, which
// ssh names localhost:N.

// imageClipboardEnv is everything the decision and the reads depend on, so a
// test can drive every platform with fake commands and no real clipboard.
type imageClipboardEnv struct {
	getenv   func(string) string
	lookPath func(string) bool
	goos     string
	// run runs a command and returns its stdout, at most limit bytes of it.
	// It must honour ctx, and it returns errClipboardTooLarge when the
	// command writes more than limit.
	run func(ctx context.Context, limit int64, name string, args ...string) ([]byte, error)
}

// systemImageClipboardEnv is the real environment.
func systemImageClipboardEnv() imageClipboardEnv {
	return imageClipboardEnv{
		getenv:   os.Getenv,
		lookPath: hasExecutable,
		goos:     runtime.GOOS,
		run:      runClipTool,
	}
}

// imageClipboardBackend names the tool family.
type imageClipboardBackend int

const (
	imageClipWayland imageClipboardBackend = iota + 1
	imageClipX11
	imageClipDarwin
	imageClipWindows
)

// imageClipboard reads images from one platform's clipboard.
type imageClipboard struct {
	backend imageClipboardBackend
	env     imageClipboardEnv
}

// Bounds on the helper processes. Listing the types is a question the paste
// key waits on, so it is short, and shorter still for the paste key, where a
// text paste waits behind it. Reading a large screenshot through osascript
// is slow, so the read is given longer.
const (
	imageClipTypesTimeout = 1500 * time.Millisecond
	imageClipKeyTimeout   = 300 * time.Millisecond
	imageClipReadTimeout  = 8 * time.Second
	// imageClipTypesMax bounds the type list. A real one is a few hundred
	// bytes.
	imageClipTypesMax = 64 << 10
	// imageClipWaitDelay is how long a helper's pipes are waited on after it
	// is killed. A child it started could otherwise hold them open.
	imageClipWaitDelay = 500 * time.Millisecond
)

// errClipboardTooLarge is a clipboard image over the paste limit. The tool
// that was writing it is killed, and nothing past the limit is read.
var errClipboardTooLarge = fmt.Errorf("the image is larger than %d MB", session.PasteImageMaxBytes>>20)

// runClipTool runs one helper and returns at most limit bytes of its output.
//
// The helper gets a process group of its own where the platform has them, and
// the whole group is killed when ctx ends or the output passes limit. Killing
// only the helper is not enough: a wl-paste that forks, or a script standing
// in for one, leaves a child holding the pipe, and the read would wait on that
// child long after the deadline.
func runClipTool(ctx context.Context, limit int64, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = &capWriter{max: 4 << 10}
	cmd.WaitDelay = imageClipWaitDelay
	prepareClipCmd(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var once sync.Once
	kill := func() {
		once.Do(func() {
			killClipCmd(cmd)
			// Closing the read end ends a read that a surviving child
			// still holds open.
			_ = stdout.Close()
		})
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			kill()
		case <-done:
		}
	}()
	out, rerr := io.ReadAll(io.LimitReader(stdout, limit+1))
	over := int64(len(out)) > limit
	if over {
		kill()
	}
	close(done)
	werr := cmd.Wait()
	switch {
	case over:
		return nil, errClipboardTooLarge
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case rerr != nil:
		return nil, rerr
	case werr != nil:
		if msg := strings.TrimSpace(cmd.Stderr.(*capWriter).String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", werr, msg)
		}
		return nil, werr
	}
	return out, nil
}

// capWriter keeps the first max bytes written to it and drops the rest.
type capWriter struct {
	bytes.Buffer
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.Len(); room > 0 {
		w.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// forwardedX11 reports whether display is the one ssh -X or -Y forwards to
// the person's own X server, which ssh names localhost:N.
func forwardedX11(display string) bool {
	host, _, ok := strings.Cut(display, ":")
	if !ok {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// detectImageClipboard finds the tool that can read an image here, or nil.
func detectImageClipboard(env imageClipboardEnv) *imageClipboard {
	// Inside ssh every clipboard this process can reach is the far
	// machine's, except an X display ssh forwards back to the person.
	if env.getenv("SSH_CONNECTION") != "" || env.getenv("SSH_TTY") != "" {
		if forwardedX11(env.getenv("DISPLAY")) && env.lookPath("xclip") {
			return &imageClipboard{backend: imageClipX11, env: env}
		}
		return nil
	}
	if env.getenv("XDG_RUNTIME_DIR") != "" && env.getenv("WAYLAND_DISPLAY") != "" && env.lookPath("wl-paste") {
		return &imageClipboard{backend: imageClipWayland, env: env}
	}
	if env.getenv("DISPLAY") != "" && env.lookPath("xclip") {
		return &imageClipboard{backend: imageClipX11, env: env}
	}
	switch env.goos {
	case "darwin":
		if env.lookPath("osascript") {
			return &imageClipboard{backend: imageClipDarwin, env: env}
		}
	case "windows":
		if env.lookPath("powershell") {
			return &imageClipboard{backend: imageClipWindows, env: env}
		}
	}
	return nil
}

// windowsClipTypes prints image/png and text/plain, one per line, for what
// the clipboard holds.
const windowsClipTypes = `Add-Type -AssemblyName System.Windows.Forms; ` +
	`if ([System.Windows.Forms.Clipboard]::ContainsImage()) { 'image/png' }; ` +
	`if ([System.Windows.Forms.Clipboard]::ContainsText()) { 'text/plain' }`

// windowsClipImage prints the clipboard's image as base64 PNG, or nothing.
const windowsClipImage = `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; ` +
	`$i = [System.Windows.Forms.Clipboard]::GetImage(); if ($i) { ` +
	`$s = New-Object System.IO.MemoryStream; $i.Save($s, [System.Drawing.Imaging.ImageFormat]::Png); ` +
	`[Convert]::ToBase64String($s.ToArray()) }`

// Types lists what the clipboard holds, as media types where the platform
// names them that way. An empty clipboard is no types and no error.
func (c *imageClipboard) Types(ctx context.Context) ([]string, error) {
	return c.typesWithin(ctx, imageClipTypesTimeout)
}

// errClipTypesUnsupported is a tool that says it cannot list the clipboard
// here, such as wl-paste on a compositor without the data-control protocol.
var errClipTypesUnsupported = errors.New("the clipboard tool cannot list the clipboard here")

// typesWithin is Types with its own deadline.
func (c *imageClipboard) typesWithin(ctx context.Context, limit time.Duration) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var out []byte
	var err error
	switch c.backend {
	case imageClipWayland:
		out, err = c.env.run(ctx, imageClipTypesMax, "wl-paste", "--list-types")
	case imageClipX11:
		out, err = c.env.run(ctx, imageClipTypesMax, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	case imageClipDarwin:
		out, err = c.env.run(ctx, imageClipTypesMax, "osascript", "-e", "clipboard info")
		if err == nil {
			return darwinClipTypes(string(out)), nil
		}
	case imageClipWindows:
		out, err = c.env.run(ctx, imageClipTypesMax, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsClipTypes)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A tool that says the compositor lacks what it needs will say so
		// on every press. The caller stops asking.
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "not support") || strings.Contains(msg, "data-control") {
			return nil, errClipTypesUnsupported
		}
		// wl-paste and xclip exit non-zero on an empty clipboard. That is
		// an answer, not a failure.
		return nil, nil
	}
	var types []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			types = append(types, line)
		}
	}
	return types, nil
}

// darwinClipTypes reads osascript's clipboard info, which is a list such as
// «class PNGf», 1234, «class utf8», 12, string, 12, into media types.
func darwinClipTypes(info string) []string {
	var types []string
	add := func(t string) {
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	for field := range strings.SplitSeq(info, ",") {
		f := strings.TrimSpace(field)
		switch {
		case strings.Contains(f, "PNGf"):
			add("image/png")
		case strings.Contains(f, "TIFF"):
			add("image/tiff")
		case strings.Contains(f, "JPEG"):
			add("image/jpeg")
		case strings.Contains(f, "GIFf"):
			add("image/gif")
		case strings.Contains(f, "utf8"), strings.Contains(f, "ut16"), f == "string", f == "Unicode text":
			add("text/plain")
		}
	}
	return types
}

// imagePreference is the order an image type is picked in when the clipboard
// offers several. PNG first: it is lossless and every agent reads it.
var imagePreference = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/tiff"}

// bestImageType returns the image type to ask for, or "" when the clipboard
// holds no image.
func bestImageType(types []string) string {
	for _, want := range imagePreference {
		for _, t := range types {
			if strings.EqualFold(strings.TrimSpace(strings.SplitN(t, ";", 2)[0]), want) {
				return want
			}
		}
	}
	for _, t := range types {
		if strings.HasPrefix(strings.ToLower(t), "image/") {
			return strings.ToLower(strings.SplitN(t, ";", 2)[0])
		}
	}
	return ""
}

// clipboardHasText reports whether the clipboard offers plain text. X11 names
// it with atoms rather than media types.
func clipboardHasText(types []string) bool {
	for _, t := range types {
		base := strings.ToLower(strings.TrimSpace(strings.SplitN(t, ";", 2)[0]))
		switch base {
		case "text/plain", "utf8_string", "string", "text", "compound_text":
			return true
		}
	}
	return false
}

// errNoClipboardImage is a read that found no image.
var errNoClipboardImage = errors.New("the clipboard holds no image")

// ReadImage returns the clipboard's image bytes, in the type types names.
func (c *imageClipboard) ReadImage(ctx context.Context, types []string) ([]byte, error) {
	kind := bestImageType(types)
	if kind == "" {
		return nil, errNoClipboardImage
	}
	ctx, cancel := context.WithTimeout(ctx, imageClipReadTimeout)
	defer cancel()
	var out []byte
	var err error
	switch c.backend {
	case imageClipWayland:
		out, err = c.env.run(ctx, session.PasteImageMaxBytes, "wl-paste", "--no-newline", "--type", kind)
	case imageClipX11:
		out, err = c.env.run(ctx, session.PasteImageMaxBytes, "xclip", "-selection", "clipboard", "-t", kind, "-o")
	case imageClipDarwin:
		out, err = c.readDarwin(ctx, kind)
	case imageClipWindows:
		out, err = c.env.run(ctx, int64(base64.StdEncoding.EncodedLen(session.PasteImageMaxBytes))+64, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsClipImage)
		if err == nil {
			out, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
		}
	}
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errNoClipboardImage
	}
	return out, nil
}

// readDarwin reads the image on macOS. pngpaste writes the bytes as they are.
// osascript prints them as a hex literal, «data PNGf89504E47...», which is
// twice the size and is decoded here.
func (c *imageClipboard) readDarwin(ctx context.Context, kind string) ([]byte, error) {
	if kind == "image/png" && c.env.lookPath("pngpaste") {
		out, err := c.env.run(ctx, session.PasteImageMaxBytes, "pngpaste", "-")
		if errors.Is(err, errClipboardTooLarge) {
			return nil, err
		}
		if err == nil && len(out) > 0 {
			return out, nil
		}
	}
	class := map[string]string{"image/png": "PNGf", "image/tiff": "TIFF", "image/jpeg": "JPEG", "image/gif": "GIFf"}[kind]
	if class == "" {
		return nil, errNoClipboardImage
	}
	// Two hex digits a byte, and the «data CLASS» around them.
	out, err := c.env.run(ctx, 2*session.PasteImageMaxBytes+64, "osascript", "-e", "the clipboard as «class "+class+"»")
	if err != nil {
		return nil, err
	}
	return decodeAppleScriptData(out, class)
}

// decodeAppleScriptData decodes «data CLASShex» into bytes.
func decodeAppleScriptData(out []byte, class string) ([]byte, error) {
	s := bytes.TrimSpace(out)
	prefix := []byte("«data " + class)
	if !bytes.HasPrefix(s, prefix) || !bytes.HasSuffix(s, []byte("»")) {
		return nil, errors.New("osascript did not return image data")
	}
	s = bytes.TrimSuffix(bytes.TrimPrefix(s, prefix), []byte("»"))
	data := make([]byte, hex.DecodedLen(len(s)))
	if _, err := hex.Decode(data, s); err != nil {
		return nil, err
	}
	return data, nil
}
