package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
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
// clipboard is somewhere else. A client started inside ssh has no display
// variable, so the Linux tools are not found, and the macOS and Windows tools
// are refused outright there, because they would read the far machine's
// clipboard.

// imageClipboardEnv is everything the decision and the reads depend on, so a
// test can drive every platform with fake commands and no real clipboard.
type imageClipboardEnv struct {
	getenv   func(string) string
	lookPath func(string) bool
	goos     string
	// run runs a command and returns its stdout. It must honour ctx.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// systemImageClipboardEnv is the real environment.
func systemImageClipboardEnv() imageClipboardEnv {
	return imageClipboardEnv{
		getenv:   os.Getenv,
		lookPath: hasExecutable,
		goos:     runtime.GOOS,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
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
// key waits on, so it is short. Reading a large screenshot through osascript
// is slow, so the read is given longer.
const (
	imageClipTypesTimeout = 1500 * time.Millisecond
	imageClipReadTimeout  = 8 * time.Second
)

// detectImageClipboard finds the tool that can read an image here, or nil.
func detectImageClipboard(env imageClipboardEnv) *imageClipboard {
	if env.getenv("XDG_RUNTIME_DIR") != "" && env.getenv("WAYLAND_DISPLAY") != "" && env.lookPath("wl-paste") {
		return &imageClipboard{backend: imageClipWayland, env: env}
	}
	if env.getenv("DISPLAY") != "" && env.lookPath("xclip") {
		return &imageClipboard{backend: imageClipX11, env: env}
	}
	// A client inside ssh on a Mac or a Windows box would read that box's
	// clipboard, which is not the person's.
	if env.getenv("SSH_CONNECTION") != "" || env.getenv("SSH_TTY") != "" {
		return nil
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
	ctx, cancel := context.WithTimeout(ctx, imageClipTypesTimeout)
	defer cancel()
	var out []byte
	var err error
	switch c.backend {
	case imageClipWayland:
		out, err = c.env.run(ctx, "wl-paste", "--list-types")
	case imageClipX11:
		out, err = c.env.run(ctx, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	case imageClipDarwin:
		out, err = c.env.run(ctx, "osascript", "-e", "clipboard info")
		if err == nil {
			return darwinClipTypes(string(out)), nil
		}
	case imageClipWindows:
		out, err = c.env.run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsClipTypes)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
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
		out, err = c.env.run(ctx, "wl-paste", "--no-newline", "--type", kind)
	case imageClipX11:
		out, err = c.env.run(ctx, "xclip", "-selection", "clipboard", "-t", kind, "-o")
	case imageClipDarwin:
		out, err = c.readDarwin(ctx, kind)
	case imageClipWindows:
		out, err = c.env.run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsClipImage)
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
		if out, err := c.env.run(ctx, "pngpaste", "-"); err == nil && len(out) > 0 {
			return out, nil
		}
	}
	class := map[string]string{"image/png": "PNGf", "image/tiff": "TIFF", "image/jpeg": "JPEG", "image/gif": "GIFf"}[kind]
	if class == "" {
		return nil, errNoClipboardImage
	}
	out, err := c.env.run(ctx, "osascript", "-e", "the clipboard as «class "+class+"»")
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
