package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/units"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type imagePasteMode int

const (
	imagePasteAction imagePasteMode = iota
	imagePasteKey
	imagePasteEmpty
)

const imagePasteTimeout = 75 * time.Second

type imagePasteState struct {
	detect      func() *imageClipboard
	call        func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error)
	save        func([]byte) (string, error)
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

func (m *OS) imageClipboardHere() *imageClipboard {
	if m.imagePaste.detect != nil {
		return m.imagePaste.detect()
	}
	return detectImageClipboard(systemImageClipboardEnv())
}

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

type imageProbeMsg struct {
	window      string
	mode        imagePasteMode
	data        []byte
	err         error
	fallback    bool
	keyProbeOff bool
	reply       *composer.Model
}

// ImagePastedMsg is the daemon's answer: the path to paste into window.
type ImagePastedMsg struct {
	Window string
	Path   string
	Host   string
	Err    error
	// Data is the image that was pasted, for the paste preview.
	Data []byte
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

func probeImageCmd(clip *imageClipboard, window string, mode imagePasteMode) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		msg := imageProbeMsg{window: window, mode: mode}
		limit := imageClipTypesTimeout
		if mode == imagePasteKey {
			limit = imageClipKeyTimeout
		}
		nothingWaits := false
		if mode != imagePasteAction {
			if hasText, ok := clip.darwinHasText(ctx, imageClipKeyTimeout); ok {
				if hasText && mode == imagePasteKey {
					msg.fallback = true
					return msg
				}
				limit = imageClipTypesTimeout
				nothingWaits = !hasText || mode == imagePasteEmpty
			}
		}
		slowStart := clip.backend == imageClipWSL
		types, err := clip.typesWithin(ctx, limit)
		if err != nil && mode != imagePasteAction {
			if (nothingWaits || slowStart) && !errors.Is(err, errClipTypesUnsupported) {
				msg.fallback = mode == imagePasteKey
				return msg
			}
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

func (m *OS) applyImageProbe(msg imageProbeMsg) tea.Cmd {
	if msg.reply != nil {
		return m.applyReplyImageProbe(msg)
	}
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
			m.ShowNotification("The image is larger than "+units.Bytes(session.PasteImageMaxBytes)+". Nothing was pasted.", "warning", m.Settings.NotificationDuration*2)
		case errors.Is(msg.err, errClipTypesUnsupported):
			m.ShowNotification("The clipboard tool cannot read the clipboard here. Nothing was pasted.", "warning", m.Settings.NotificationDuration*2)
		default:
			m.ShowNotification("tuios cannot read the clipboard image: "+msg.err.Error(), "error", m.Settings.NotificationDuration*2)
		}
		return nil
	}
	return m.deliverImageCmd(msg.window, msg.data)
}

func (m *OS) deliverImageCmd(window string, data []byte) tea.Cmd {
	if len(data) > session.PasteImageMaxBytes {
		m.ShowNotification("The image is "+units.Bytes(int64(len(data)))+". The limit is "+units.Bytes(session.PasteImageMaxBytes)+". Nothing was pasted.",
			"warning", m.Settings.NotificationDuration*2)
		return nil
	}
	if m.imagePaste.call == nil && (m.DaemonClient == nil || !m.IsDaemonSession) {
		save := m.imagePaste.save
		if save == nil {
			save = session.SavePastedImage
		}
		return func() tea.Msg {
			path, err := save(data)
			return ImagePastedMsg{Window: window, Path: path, Err: err, Data: data}
		}
	}

	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification(m.noNonceText()+".", "error", m.Settings.NotificationDuration*2)
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
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return ImagePastedMsg{Window: window, Err: err}
		}
		if res.Path == "" {
			return ImagePastedMsg{Window: window, Err: errors.New("the daemon named no file")}
		}
		return ImagePastedMsg{Window: window, Path: res.Path, Host: res.Host, Data: data}
	}
}

func (m *OS) applyImagePasted(msg ImagePastedMsg) tea.Cmd {
	if msg.Err != nil {
		if session.VerbErrCode(msg.Err) == session.ErrVerbUnknownVerb {
			m.ShowNotification("This daemon cannot take a pasted image. "+m.restartHint(), "error", m.Settings.NotificationDuration*2)
			return nil
		}
		if callErr, ok := errors.AsType[*session.VerbCallError](msg.Err); ok && callErr.Code == session.ErrVerbUnknownHost && callErr.Hint != nil && callErr.Hint.Detail != "" {
			m.ShowNotification("Cannot paste the image. "+callErr.Hint.Detail, "error", m.Settings.NotificationDuration*3)
			return nil
		}
		if text := m.imagePasteRefusal(msg.Window, msg.Err); text != "" {
			m.ShowNotification(text, "error", m.Settings.NotificationDuration*3)
			return nil
		}
		m.ShowNotification("The image was not pasted: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	w := m.windowByID(msg.Window)
	if w == nil {
		m.ShowNotification("The pane closed before the image was pasted.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	if w.CopyModeVisible() {
		m.ShowNotification("Cannot paste in copy mode. Exit copy mode first.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	if err := w.Paste(pastePathText(msg.Path)); err != nil {
		m.ShowNotification("The image was saved but its path was not pasted: "+err.Error(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	where := thisMachineLabel()
	if msg.Host != "" {
		where = msg.Host
	}
	m.ShowNotification("Pasted the image as a file on "+where+".", "success", m.Settings.NotificationDuration)
	return m.showPastePreview(msg.Window, msg.Path, msg.Data)
}

func pastePathText(path string) string {
	windows := pastePathIsWindows(path)
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

func pastePathIsWindows(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') &&
		(path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z')
}

func (m *OS) imagePasteRefusal(window string, err error) string {
	host := m.AttachedHost
	if w := m.windowByID(window); w != nil && w.Host != "" {
		host = w.Host
	}
	if refusal := linkRefusal(host, err); refusal != "" {
		return "Cannot paste the image. " + refusal
	}
	return ""
}
