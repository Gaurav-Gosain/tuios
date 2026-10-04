package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestNotifySecretsStayOutOfDumps is the security boundary for the [notify]
// credentials: a config printed with any fmt verb, encoded as JSON or run
// through the validator never shows a token, a user key or a topic address,
// while the TOML a save writes keeps every one, so saving the settings does
// not wipe them from the file.
//
// The ways it could fail: a new secret field typed as a plain string; Secret
// gaining a TextMarshaler, which the TOML encoder would then use and so write
// [redacted] into the file; a validation message quoting the value; a
// Destinations entry carrying the value rather than where it comes from.
func TestNotifySecretsStayOutOfDumps(t *testing.T) {
	const (
		ntfyToken  = "test-ntfy-token-not-a-secret"
		pushUser   = "test-pushover-user-not-a-secret"
		pushToken  = "test-pushover-token-not-a-secret"
		hookToken  = "test-webhook-token-not-a-secret"
		topicPath  = "tuios-private-topic-5d1"
		hookSecret = "hook-path-secret"
	)
	src := `
[notify]
content = "loud"
web_url = "not a url"

[notify.ntfy]
url = "https://ntfy.example.test/` + topicPath + `"
token = "` + ntfyToken + `"
token_env = "SOME_VAR"

[notify.pushover]
user = "` + pushUser + `"
token = "` + pushToken + `"

[notify.webhook]
url = "https://user:pw@hooks.example.test/` + hookSecret + `"
token = "` + hookToken + `"
`
	var cfg UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	secrets := []string{ntfyToken, pushUser, pushToken, hookToken}

	t.Run("fmt", func(t *testing.T) {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			for name, v := range map[string]any{"config": cfg, "notify": cfg.Notify, "ntfy": *cfg.Notify.Ntfy, "pushover": *cfg.Notify.Pushover, "webhook": *cfg.Notify.Webhook} {
				out := fmt.Sprintf(verb, v)
				for _, s := range append(secrets, topicPath, hookSecret) {
					if strings.Contains(out, s) || strings.Contains(out, fmt.Sprintf("%x", s)) {
						t.Errorf("%s printed with %s shows a secret", name, verb)
					}
				}
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("json: %v", err)
		}
		for _, s := range secrets {
			if strings.Contains(string(data), s) {
				t.Errorf("the JSON dump shows a secret")
			}
		}
		if !strings.Contains(string(data), redactedText) {
			t.Errorf("the JSON dump does not mark the secrets as redacted")
		}
	})

	t.Run("validation", func(t *testing.T) {
		res := ValidateConfig(&cfg)
		var all strings.Builder
		for _, w := range append(res.Errors, res.Warnings...) {
			if w.Field == "notify" {
				all.WriteString(w.Key + ": " + w.Message + "\n")
			}
		}
		out := all.String()
		for _, s := range append(secrets, topicPath, hookSecret, "pw@") {
			if strings.Contains(out, s) {
				t.Errorf("a validation message shows %q:\n%s", s, out)
			}
		}
		// The positive half: the validator did look at the table.
		for _, key := range []string{"content", "web_url", "ntfy.token", "webhook.url"} {
			if !strings.Contains(out, key+":") {
				t.Errorf("no warning for %s:\n%s", key, out)
			}
		}
	})

	t.Run("destinations", func(t *testing.T) {
		out := strings.Join(cfg.Notify.Destinations(), "\n")
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("Destinations carries a secret value")
			}
		}
	})

	t.Run("save keeps them", func(t *testing.T) {
		data, err := MarshalUserConfig(&cfg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, s := range secrets {
			if !strings.Contains(string(data), s) {
				t.Errorf("the saved file lost a secret, so a settings save would wipe it")
			}
		}
		if strings.Contains(string(data), redactedText) {
			t.Errorf("the saved file holds %s in place of a secret", redactedText)
		}
	})
}
