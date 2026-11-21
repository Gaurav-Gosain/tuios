package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

type imageClipboardEnv struct {
	getenv   func(string) string
	lookPath func(string) bool
	goos     string
	run      func(ctx context.Context, limit int64, name string, args ...string) ([]byte, error)
}

func systemImageClipboardEnv() imageClipboardEnv {
	return imageClipboardEnv{
		getenv:   os.Getenv,
		lookPath: hasExecutable,
		goos:     runtime.GOOS,
		run:      runClipTool,
	}
}

type imageClipboardBackend int

const (
	imageClipWayland imageClipboardBackend = iota + 1
	imageClipX11
	imageClipDarwin
	imageClipWindows
	imageClipWSL
)

type imageClipboard struct {
	backend    imageClipboardBackend
	env        imageClipboardEnv
	powershell string
}

const (
	imageClipTypesTimeout = 1500 * time.Millisecond
	imageClipKeyTimeout   = 300 * time.Millisecond
	imageClipReadTimeout  = 8 * time.Second
	imageClipTypesMax     = 64 << 10
	imageClipWaitDelay    = 500 * time.Millisecond
)

var errClipboardTooLarge = errfmt.Errorf("the image is larger than %s", units.Bytes(session.PasteImageMaxBytes))

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
			return nil, errfmt.Errorf("%w: %s", werr, msg)
		}
		return nil, werr
	}
	return out, nil
}

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

func detectImageClipboard(env imageClipboardEnv) *imageClipboard {
	if env.getenv("SSH_CONNECTION") != "" || env.getenv("SSH_TTY") != "" {
		if forwardedX11(env.getenv("DISPLAY")) && env.lookPath("xclip") {
			return &imageClipboard{backend: imageClipX11, env: env}
		}
		return nil
	}
	if ps := wslPowerShell(env); ps != "" {
		return &imageClipboard{backend: imageClipWSL, env: env, powershell: ps}
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
			return &imageClipboard{backend: imageClipWindows, env: env, powershell: "powershell"}
		}
	}
	return nil
}

const wslDefaultPowerShell = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"

func wslPowerShell(env imageClipboardEnv) string {
	if env.goos != "linux" || (env.getenv("WSL_DISTRO_NAME") == "" && env.getenv("WSL_INTEROP") == "") {
		return ""
	}
	for _, ps := range []string{"powershell.exe", wslDefaultPowerShell} {
		if env.lookPath(ps) {
			return ps
		}
	}
	return ""
}

const windowsClipTypes = `Add-Type -AssemblyName System.Windows.Forms; ` +
	`if ([System.Windows.Forms.Clipboard]::ContainsImage()) { 'image/png' }; ` +
	`if ([System.Windows.Forms.Clipboard]::ContainsText()) { 'text/plain' }`

const windowsClipImage = `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; ` +
	`$i = [System.Windows.Forms.Clipboard]::GetImage(); if ($i) { ` +
	`$s = New-Object System.IO.MemoryStream; $i.Save($s, [System.Drawing.Imaging.ImageFormat]::Png); ` +
	`[Convert]::ToBase64String($s.ToArray()) }`

func (c *imageClipboard) Types(ctx context.Context) ([]string, error) {
	return c.typesWithin(ctx, imageClipTypesTimeout)
}

func (c *imageClipboard) darwinHasText(ctx context.Context, limit time.Duration) (hasText, ok bool) {
	if c.backend != imageClipDarwin || !c.env.lookPath("pbpaste") {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := c.env.run(ctx, 0, "pbpaste")
	switch {
	case errors.Is(err, errClipboardTooLarge):
		return true, true
	case err != nil:
		return false, false
	}
	return len(out) > 0, true
}

var errClipTypesUnsupported = errors.New("the clipboard tool cannot list the clipboard here")

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
	case imageClipWindows, imageClipWSL:
		out, err = c.env.run(ctx, imageClipTypesMax, c.powershell, "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsClipTypes)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "not support") || strings.Contains(msg, "data-control") {
			return nil, errClipTypesUnsupported
		}
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

var imagePreference = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/tiff"}

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

var errNoClipboardImage = errors.New("the clipboard holds no image")

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
	case imageClipWindows, imageClipWSL:
		out, err = c.env.run(ctx, int64(base64.StdEncoding.EncodedLen(session.PasteImageMaxBytes))+64, c.powershell, "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsClipImage)
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
	out, err := c.env.run(ctx, 2*session.PasteImageMaxBytes+64, "osascript", "-e", "the clipboard as «class "+class+"»")
	if err != nil {
		return nil, err
	}
	return decodeAppleScriptData(out, class)
}

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
