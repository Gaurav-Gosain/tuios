package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Pasting an image from the clipboard into a pane.
//
// tuios reads the image here (clipboard_image.go), the daemon writes it to a
// file on the machine where the pane's process runs (session/paste_image.go),
// and the file's path is pasted into the pane as text. An agent that takes an
// image by path, such as Claude Code or Codex, then gets the image, on this
// machine or on another one.
//
// Three things start it:
//
//   - The paste_image action, bound to prefix V and in the command palette.
//     It always reads the image, even when the clipboard holds text as well.
//   - The paste key (terminal_paste_host, paste_clipboard). When the
//     clipboard holds an image and no text, the key pastes the image. When it
//     holds text, the key pastes the text exactly as before.
//   - A bracketed paste from the host terminal that is empty. Some terminals
//     send one when the clipboard holds only an image. Nothing else would come
//     of it, so tuios looks for an image.
//
// Plain ctrl+v is not one of them. It reaches the pane as 0x16, the same as
// before, so a program that reads the clipboard itself on ctrl+v, as Claude
// Code does, still gets the key.

// imagePasteMode is what started an image paste, which decides what happens
// when the clipboard holds no image.
type imagePasteMode int

const (
	// imagePasteAction is the paste_image action: no image is a message.
	imagePasteAction imagePasteMode = iota
	// imagePasteKey is the paste key: no image, or text beside it, is the
	// text paste it always was.
	imagePasteKey
	// imagePasteEmpty is an empty bracketed paste: no image is nothing.
	imagePasteEmpty
)

// imagePasteTimeout bounds the daemon's write, which for a pane on another
// machine includes the bytes crossing the link.
const imagePasteTimeout = 75 * time.Second

// imagePasteState is the image paste's test seams. Nil fields use the system.
type imagePasteState struct {
	// detect finds the clipboard tool.
	detect func() *imageClipboard
	// call reaches the daemon with a verb.
	call func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error)
	// save writes an image for a client with no daemon.
	save func([]byte) (string, error)
	// keyProbeOff stops the paste key from asking the clipboard for its
	// types, after an ask that was too slow or that the tool said it cannot
	// answer here. The text paste then runs at once, every press. The
	// paste_image action still asks.
	keyProbeOff bool
}

// SetImagePasteSeams replaces the clipboard, the daemon call and the local
// save. Tests use it. Nil restores the system's.
func (m *OS) SetImagePasteSeams(env *imageClipboardEnv, call func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error), save func([]byte) (string, error)) {
	m.imagePaste = imagePasteState{call: call, save: save}
	if env != nil {
		e := *env
		m.imagePaste.detect = func() *imageClipboard { return detectImageClipboard(e) }
	}
}

// imageClipboardHere is the clipboard tool, or nil.
func (m *OS) imageClipboardHere() *imageClipboard {
	if m.imagePaste.detect != nil {
		return m.imagePaste.detect()
	}
	return detectImageClipboard(systemImageClipboardEnv())
}

// imagePasteReason says why this client cannot read a clipboard image, or ""
// when it can.
func (m *OS) imagePasteReason() string {
	switch {
	case m.BrowserClient:
		return "The browser does not give a clipboard image to tuios. Save the image to a file and paste the path."
	case m.RemoteClient && !m.SSHIsLoopback:
		return "tuios runs on another machine, so it cannot read your clipboard image."
	case m.ProcessingRemoteKeys:
		return "An image paste that send-keys asked for is not done. Only your own key pastes an image."
	}
	return ""
}

// imageProbeMsg is what the clipboard held when an image paste looked.
type imageProbeMsg struct {
	window string
	mode   imagePasteMode
	data   []byte
	err    error
	// fallback asks for the text paste instead.
	fallback bool
	// keyProbeOff says the type ask was too slow or cannot work here, so the
	// paste key stops making it.
	keyProbeOff bool
}

// ImagePastedMsg is the daemon's answer: the path to paste into window.
type ImagePastedMsg struct {
	Window string
	Path   string
	Host   string
	Err    error
}

// RequestPaste is the paste key. It pastes the clipboard's image when the
// clipboard holds an image and no text, and asks for the text otherwise.
func (m *OS) RequestPaste() tea.Cmd {
	if m.imagePasteReason() != "" {
		return m.RequestHostPaste()
	}
	if m.imagePaste.keyProbeOff {
		return m.RequestHostPaste()
	}
	clip := m.imageClipboardHere()
	w := m.GetFocusedWindow()
	if clip == nil || w == nil {
		return m.RequestHostPaste()
	}
	return probeImageCmd(clip, w.ID, imagePasteKey)
}

// RequestImagePaste is the paste_image action.
func (m *OS) RequestImagePaste() tea.Cmd {
	w := m.GetFocusedWindow()
	if w == nil {
		return nil
	}
	if reason := m.imagePasteReason(); reason != "" {
		m.ShowNotification(reason, "warning", m.Settings.NotificationDuration)
		return nil
	}
	clip := m.imageClipboardHere()
	if clip == nil {
		m.ShowNotification("tuios cannot read a clipboard image here. Install wl-clipboard or xclip.", "warning", m.Settings.NotificationDuration*2)
		return nil
	}
	return probeImageCmd(clip, w.ID, imagePasteAction)
}

// PasteImageOnEmptyPaste looks for an image after the host terminal sent an
// empty bracketed paste. It says nothing when there is none.
func (m *OS) PasteImageOnEmptyPaste() tea.Cmd {
	w := m.GetFocusedWindow()
	if w == nil || m.imagePasteReason() != "" || m.imagePaste.keyProbeOff {
		return nil
	}
	clip := m.imageClipboardHere()
	if clip == nil {
		return nil
	}
	return probeImageCmd(clip, w.ID, imagePasteEmpty)
}

// probeImageCmd lists the clipboard's types and, when the mode takes what is
// there as an image, reads it. It runs off the update loop: the tools are
// separate processes.
func probeImageCmd(clip *imageClipboard, window string, mode imagePasteMode) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		msg := imageProbeMsg{window: window, mode: mode}
		// The paste key's text paste waits behind this ask, so the key gives
		// it a short bound. Past it the text is pasted, and the key does not
		// ask again.
		limit := imageClipTypesTimeout
		if mode != imagePasteAction {
			limit = imageClipKeyTimeout
		}
		types, err := clip.typesWithin(ctx, limit)
		if err != nil && mode != imagePasteAction {
			msg.fallback = mode == imagePasteKey
			msg.keyProbeOff = true
			return msg
		}
		image := bestImageType(types) != ""
		switch mode {
		case imagePasteKey:
			if !image || clipboardHasText(types) {
				msg.fallback = true
				return msg
			}
		case imagePasteEmpty:
			if !image {
				return nil
			}
		case imagePasteAction:
			if err != nil {
				msg.err = err
				return msg
			}
			if !image {
				msg.err = errNoClipboardImage
				return msg
			}
		}
		msg.data, msg.err = clip.ReadImage(ctx, types)
		return msg
	}
}

// applyImageProbe acts on what the clipboard held.
func (m *OS) applyImageProbe(msg imageProbeMsg) tea.Cmd {
	if msg.keyProbeOff {
		m.imagePaste.keyProbeOff = true
	}
	if msg.fallback {
		return m.RequestHostPaste()
	}
	if msg.data == nil && msg.err == nil {
		return nil
	}
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, errNoClipboardImage):
			m.ShowNotification("The clipboard holds no image.", "info", m.Settings.NotificationDuration)
		case errors.Is(msg.err, errClipboardTooLarge):
			m.ShowNotification(fmt.Sprintf("The image is larger than %d MB. Nothing was pasted.", session.PasteImageMaxBytes>>20), "warning", m.Settings.NotificationDuration*2)
		case errors.Is(msg.err, errClipTypesUnsupported):
			m.ShowNotification("The clipboard tool cannot read the clipboard here. Nothing was pasted.", "warning", m.Settings.NotificationDuration*2)
		default:
			m.ShowNotification("tuios cannot read the clipboard image: "+msg.err.Error(), "error", m.Settings.NotificationDuration*2)
		}
		return nil
	}
	return m.deliverImageCmd(msg.window, msg.data)
}

// deliverImageCmd has the image written where window's process runs.
func (m *OS) deliverImageCmd(window string, data []byte) tea.Cmd {
	if len(data) > session.PasteImageMaxBytes {
		m.ShowNotification(fmt.Sprintf("The image is %.1f MB. The limit is %d MB. Nothing was pasted.",
			float64(len(data))/(1<<20), session.PasteImageMaxBytes>>20), "warning", m.Settings.NotificationDuration*2)
		return nil
	}
	if m.imagePaste.call == nil && (m.DaemonClient == nil || !m.IsDaemonSession) {
		save := m.imagePaste.save
		if save == nil {
			save = session.SavePastedImage
		}
		return func() tea.Msg {
			path, err := save(data)
			return ImagePastedMsg{Window: window, Path: path, Err: err}
		}
	}

	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon.", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	sessionName := m.SessionName
	if m.DaemonClient != nil && m.DaemonClient.SessionName() != "" {
		sessionName = m.DaemonClient.SessionName()
	}
	params := map[string]any{
		"session":     sessionName,
		"window":      window,
		"content":     base64.StdEncoding.EncodeToString(data),
		"human_nonce": nonce,
	}
	call := m.imagePaste.call
	if call == nil {
		dial := m.agentMailDialer()
		call = func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
			c, err := dial()
			if err != nil {
				return nil, err
			}
			defer func() { _ = c.Close() }()
			return c.CallWithTimeout(verb, params, timeout)
		}
	}
	return func() tea.Msg {
		raw, err := call("paste-image", params, imagePasteTimeout)
		if err != nil {
			return ImagePastedMsg{Window: window, Err: err}
		}
		var res struct {
			Path string `json:"path"`
			Host string `json:"host"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return ImagePastedMsg{Window: window, Err: err}
		}
		if res.Path == "" {
			return ImagePastedMsg{Window: window, Err: errors.New("the daemon named no file")}
		}
		return ImagePastedMsg{Window: window, Path: res.Path, Host: res.Host}
	}
}

// applyImagePasted pastes the image's path into the window it was for.
func (m *OS) applyImagePasted(msg ImagePastedMsg) {
	if msg.Err != nil {
		if callErr, ok := errors.AsType[*session.VerbCallError](msg.Err); ok && callErr.Code == session.ErrVerbUnknownVerb {
			m.ShowNotification("This daemon cannot take a pasted image. Restart it with a newer tuios: tuios kill-server", "error", m.Settings.NotificationDuration*2)
			return
		}
		m.ShowNotification("The image was not pasted: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	w := m.windowByID(msg.Window)
	if w == nil {
		m.ShowNotification("The pane closed before the image was pasted.", "warning", m.Settings.NotificationDuration)
		return
	}
	if w.CopyModeVisible() {
		m.ShowNotification("Cannot paste in copy mode. Exit copy mode first.", "warning", m.Settings.NotificationDuration)
		return
	}
	if err := w.Paste(pastePathText(msg.Path)); err != nil {
		m.ShowNotification("The image was saved but its path was not pasted: "+err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	where := "this machine"
	if msg.Host != "" {
		where = msg.Host
	}
	m.ShowNotification("Pasted the image as a file on "+where+".", "success", m.Settings.NotificationDuration)
}

// pastePathText is the text pasted for a path: the path itself, quoted only
// when it holds a character a shell would split or expand. The directories
// tuios writes to on Linux and macOS hold none, so there it is the path as it
// is.
//
// A Windows path (C:\...) goes in double quotes when it needs quoting at all:
// cmd, PowerShell and the agents that read a path from a prompt all take that
// form, and a Windows file name cannot hold a double quote. The machine the
// path is for can be another one than this, so the shape of the path decides,
// not the platform tuios runs on.
func pastePathText(path string) string {
	windows := len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') &&
		(path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z')
	extra := "/._-+:@%,="
	if windows {
		extra += "\\"
	}
	for _, r := range path {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(extra, r) {
			continue
		}
		if windows {
			return `"` + path + `"`
		}
		return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
	return path
}
