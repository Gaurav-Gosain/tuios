package tuie2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestWebPasswordGuardsTheSession runs tuios-web on loopback with
// --password-file, the way GHSA-jf76-3ff5-gjjv asks a user to run it: before
// v0.8.1 tuios-web had no login, and every visitor got a full session.
//
// A handshake with no credentials, or with a wrong password, must get a 401.
// The right password under the default user name must get a session, which
// is the positive half: a server that refuses everyone cannot pass, and the
// Host header names this machine in every case, so the Host check is not
// what answers.
//
// The unit tests in cmd/tuios-web call webAccess.apply themselves. This test
// is the one that fails when the binary does not hand the password to sip.
// See NEGATIVE_CONTROLS.md.
func TestWebPasswordGuardsTheSession(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	pwFile := filepath.Join(base, "web-password")
	if err := os.WriteFile(pwFile, []byte("e2e-horse-battery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// TUIOS_WEB_PASSWORD is set empty, which tuios-web reads as unset, so a
	// value in the environment of the run does not become a second password
	// source beside the file, which tuios-web refuses.
	addr, _ := startTuiosWebEnv(t, base, []string{"TUIOS_WEB_PASSWORD="}, "--ephemeral", "--password-file", pwFile)

	for _, tc := range []struct {
		name  string
		login string
		want  int
	}{
		{"no credentials", "", http.StatusUnauthorized},
		{"a wrong password", "tuios:e2e-wrong", http.StatusUnauthorized},
		{"another user name", "root:e2e-horse-battery", http.StatusUnauthorized},
		{"the password", "tuios:e2e-horse-battery", http.StatusSwitchingProtocols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := webHandshakeAs(t, addr, addr, tc.login); got != tc.want {
				t.Errorf("a handshake with %s got %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
