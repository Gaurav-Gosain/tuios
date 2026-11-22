//go:build linux || darwin

package main

// Clipboard sync with the desktop, while tuios-rd has control:
//   - the engine sends a type 7 notice when a desktop app changes the
//     clipboard; tuios-rd reads GET /clipboard and sets the host clipboard with
//     OSC 52 (tuios passes it on to the host terminal);
//   - a bracketed paste from the host terminal goes to POST /clipboard, then
//     tuios-rd presses the desktop's paste key (Cmd+V on macOS, Ctrl+V else).
//     If the desktop has no clipboard, the text is typed instead.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// clipLoop answers clipboard notices. Several notices close together read
// the clipboard once.
func (a *app) clipLoop() {
	var last string
	var lastAt time.Time
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.clipNotice:
		}
		text, err := a.getClipboard()
		if err != nil {
			a.setNotice("The desktop copied text, but tuios-rd cannot read it: " + shortErr(err))
			continue
		}
		// One change can arrive as more than one notice.
		if text == "" || (text == last && time.Since(lastAt) < 2*time.Second) {
			continue
		}
		last, lastAt = text, time.Now()
		_, _ = a.out.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
		a.clipCopies.Add(1)
		a.setNotice(fmt.Sprintf("Copied %s to your clipboard.", byteCount(len(text))))
	}
}

func byteCount(n int) string {
	switch {
	case n == 1:
		return "1 byte"
	case n < 10000:
		return fmt.Sprintf("%d bytes", n)
	default:
		return fmt.Sprintf("%d KiB", (n+1023)/1024)
	}
}

func (a *app) clipReq(method string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(a.ctx, method, a.cfg.base+"/clipboard", body)
	if err != nil {
		return nil, err
	}
	if a.cfg.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, fmt.Errorf("%s", e.Error)
	}
	return resp, nil
}

func (a *app) getClipboard() (string, error) {
	resp, err := a.clipReq("GET", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var c struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return "", err
	}
	return c.Text, nil
}

func (a *app) setClipboard(s string) error {
	resp, err := a.clipReq("POST", bytes.NewReader([]byte(s)))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// pasteText puts a bracketed paste on the desktop clipboard and presses the
// paste key. Without a clipboard it types the text.
func (a *app) pasteText(s string) {
	// Terminals send line ends as CR in a paste.
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if a.clipOK.Load() && !strings.ContainsRune(s, 0) {
		err := a.setClipboard(s)
		if err == nil {
			a.clipPastes.Add(1)
			a.send(keyRecords(a.pasteKey()))
			return
		}
		a.setNotice("The desktop clipboard refused the paste (" + shortErr(err) + "). tuios-rd types it instead.")
	}
	a.send(textRecords(s))
}

// pasteKey is Cmd+V for a Mac and Ctrl+V for other desktops.
func (a *app) pasteKey() *keyEv {
	if b, _ := a.backend.Load().(string); b == "macos" {
		return &keyEv{name: "v", mods: modSuper}
	}
	return &keyEv{name: "v", mods: modCtrl}
}
