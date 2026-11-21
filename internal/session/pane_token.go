package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const paneTokenBytes = 16

func newPaneTokenKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
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
