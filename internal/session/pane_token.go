package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// Pane tokens: a second way for a process to say which pane it runs in.
//
// The first way is the kernel's. A unix socket records the pid of the process
// that connected, and the daemon walks that pid up to a pane's shell (see
// peerPaneWindow). Nothing the process sends can change that answer, so it is
// the one restrict-connection trusts first.
//
// Some platforms do not give the peer's pid (Windows, the BSDs), and a process
// can leave its pane's process tree. For those the pane's environment carries
// TUIOS_PANE_TOKEN beside TUIOS_PANE_ID. The token is an HMAC of the window id
// under a key this daemon picks at start and never writes anywhere, so a token
// names exactly one window of this daemon start, cannot be made for another
// window without the key, and stops working when the daemon restarts.
//
// A token is only as secret as the pane's environment. Any process of the same
// user can read another process's environment on most systems, so a token
// scopes accidents and prompt-injected agents, not a determined local
// attacker. It is why the kernel's answer wins whenever there is one: a
// process the kernel places in pane A cannot become pane B by presenting B's
// token.

// paneTokenBytes is how much of the HMAC a token carries: 128 bits.
const paneTokenBytes = 16

// newPaneTokenKey returns a fresh random key for one daemon start.
func newPaneTokenKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// No randomness means no token can be trusted. A nil key makes
		// PaneToken return "" and VerifyPaneToken refuse everything.
		return nil
	}
	return key
}

// PaneToken is the token a pane with windowID is started with, or "" when
// this manager has no key.
func (m *Manager) PaneToken(windowID string) string {
	if len(m.paneTokenKey) == 0 || windowID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, m.paneTokenKey)
	mac.Write([]byte("tuios-pane-token\x00" + windowID))
	return hex.EncodeToString(mac.Sum(nil)[:paneTokenBytes])
}

// VerifyPaneToken reports whether token is the one windowID's pane was
// started with. The comparison takes the same time whatever the token.
func (m *Manager) VerifyPaneToken(windowID, token string) bool {
	want := m.PaneToken(windowID)
	if want == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(want), []byte(token))
}
