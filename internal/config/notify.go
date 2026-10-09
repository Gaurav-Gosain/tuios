package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
)

// NotifyConfig is the [notify] table: push notifications the daemon sends to
// a phone when the Inbox gets something that waits for the person.
//
// The daemon sends them, because the daemon runs when no client is attached,
// and that is when a phone is the only way to reach the person. It sits
// outside the option registry for the reason [hosts] does: where the Inbox's
// text goes, and with which credentials, is not for a pane to change over the
// control protocol.
type NotifyConfig struct {
	// Enabled is the master switch. Nothing is sent without a provider, so
	// the default is on. Default: true.
	Enabled *bool `toml:"enabled,omitempty"`

	// WebURL is the public address of tuios-web. When it is set, every
	// notification links to the Inbox item there. Default: empty (no link).
	WebURL string `toml:"web_url,omitempty"`

	// Content is how much a notification says: "summary" (the title and the
	// one line the Inbox shows) or "title" (only what kind of item it is and
	// the session). Default: "summary".
	Content string `toml:"content,omitempty"`

	// QuietActiveSeconds holds a notification while a person typed at an
	// attached client in the last this many seconds, so the person at the
	// desk is not told twice. When they stay away that long and the item is
	// still open, it is sent. Zero sends at once. Default: 120.
	QuietActiveSeconds *int `toml:"quiet_active_seconds,omitempty"`

	// CooldownSeconds is the shortest gap between two notifications for the
	// same pane and kind. Default: 60.
	CooldownSeconds *int `toml:"cooldown_seconds,omitempty"`

	// MaxPerHour bounds every notification together. Default: 30.
	MaxPerHour *int `toml:"max_per_hour,omitempty"`

	// AllowHTTPRedirects lets a provider's https address redirect to a plain
	// http one. Default: false.
	AllowHTTPRedirects bool `toml:"allow_http_redirects,omitempty"`

	// Triggers says which Inbox kinds send a notification.
	Triggers NotifyTriggers `toml:"triggers,omitempty"`

	// The providers. Each one set gets every notification.
	Ntfy     *NtfyConfig     `toml:"ntfy,omitempty"`
	Pushover *PushoverConfig `toml:"pushover,omitempty"`
	Webhook  *WebhookConfig  `toml:"webhook,omitempty"`

	// WebPush sets how the daemon sends to the phones registered with
	// register-push. The phones themselves are not in config.toml: only the
	// person registers one.
	WebPush *WebPushConfig `toml:"webpush,omitempty"`
}

// WebPushConfig is [notify.webpush].
type WebPushConfig struct {
	// Subject is the VAPID subject a push service may use to contact the
	// sender: an https URL or a mailto: address. Default: an https URL that
	// names tuios and this machine (WebPushSubject).
	Subject string `toml:"subject,omitempty"`
	// AllowInsecure lets the daemon send to a push service on a loopback or
	// private address, by https or by http (an IP literal or localhost),
	// for a push service on your own network.
	// Default: false.
	AllowInsecure bool `toml:"allow_insecure,omitempty"`
}

// WebPushSubject is the VAPID subject in force: Subject when set, else an
// https URL with the machine name in it. It never carries an address.
func (n *NotifyConfig) WebPushSubject(machine string) string {
	if n.WebPush != nil && ValidWebPushSubject(strings.TrimSpace(n.WebPush.Subject)) {
		return strings.TrimSpace(n.WebPush.Subject)
	}
	label := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r - 'A' + 'a'
		}
		return '-'
	}, machine)
	label = strings.Trim(label, "-")
	if label == "" {
		label = "machine"
	}
	return "https://tuios.dev/push/" + label
}

// WebPushInsecure reports whether [notify.webpush] allow_insecure is set.
func (n *NotifyConfig) WebPushInsecure() bool {
	return n.WebPush != nil && n.WebPush.AllowInsecure
}

// ValidWebPushSubject reports whether s is a subject RFC 8292 takes: an https
// URL with a host, or a mailto: URI.
func ValidWebPushSubject(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != ""
	}
	return false
}

// NotifyTriggers is the [notify.triggers] table. A kind that waits on the
// person is on by default. A kind that only reports is off.
type NotifyTriggers struct {
	Approval *bool `toml:"approval,omitempty"` // a held or shown permission prompt. Default: true.
	Plan     *bool `toml:"plan,omitempty"`     // a plan to approve. Default: true.
	Question *bool `toml:"question,omitempty"` // an agent asking in its pane. Default: true.
	Ask      *bool `toml:"ask,omitempty"`      // tuios ask-human. Default: true.
	Mail     *bool `toml:"mail,omitempty"`     // agent mail to you. Default: false.
	Errored  *bool `toml:"errored,omitempty"`  // an agent that stopped on an error. Default: false.
	Finished *bool `toml:"finished,omitempty"` // a finished turn. Default: false.
}

// NtfyConfig is [notify.ntfy]: a topic on an ntfy server.
type NtfyConfig struct {
	// URL is the topic's address, such as https://ntfy.sh/my-topic. On a
	// public server the topic name is the secret, so it is never logged.
	URL string `toml:"url"`
	// Priority is the ntfy priority, 1 to 5. Zero leaves it to the kind:
	// 4 for an item that waits on the person, 3 for the rest.
	Priority  int    `toml:"priority,omitempty"`
	Token     Secret `toml:"token,omitempty"`
	TokenEnv  string `toml:"token_env,omitempty"`
	TokenFile string `toml:"token_file,omitempty"`
}

// PushoverConfig is [notify.pushover]: the Pushover API.
type PushoverConfig struct {
	User      Secret `toml:"user,omitempty"`
	UserEnv   string `toml:"user_env,omitempty"`
	UserFile  string `toml:"user_file,omitempty"`
	Token     Secret `toml:"token,omitempty"`
	TokenEnv  string `toml:"token_env,omitempty"`
	TokenFile string `toml:"token_file,omitempty"`
	// URL replaces the Pushover API address, for a relay. Default: the
	// Pushover messages endpoint.
	URL string `toml:"url,omitempty"`
}

// WebhookConfig is [notify.webhook]: a JSON POST to an address of your own.
type WebhookConfig struct {
	URL string `toml:"url"`
	// The token, when set, goes in an Authorization: Bearer header.
	Token     Secret `toml:"token,omitempty"`
	TokenEnv  string `toml:"token_env,omitempty"`
	TokenFile string `toml:"token_file,omitempty"`
}

// PushoverAPI is where a Pushover message goes when [notify.pushover] names
// no url.
const PushoverAPI = "https://api.pushover.net/1/messages.json"

// Notify content levels.
const (
	NotifyContentSummary = "summary"
	NotifyContentTitle   = "title"
)

// NotifyContentNames lists the accepted content values.
var NotifyContentNames = []string{NotifyContentSummary, NotifyContentTitle}

// Secret is a credential read from config.toml. It prints as [redacted]
// everywhere fmt or encoding/json reaches it, so a config that is logged or
// dumped does not carry it. The TOML encoder writes the value itself, which
// is what keeps it in the file when tuios saves the config. Reveal is the one
// way to read it.
type Secret string

// redactedText is what a set secret prints as.
const redactedText = "[redacted]"

// String hides the value.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redactedText
}

// GoString hides the value from %#v.
func (s Secret) GoString() string { return `config.Secret("` + s.String() + `")` }

// Format hides the value from every fmt verb, %x and %q included.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(s.String())) }

// MarshalJSON hides the value from a JSON dump.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }

// Reveal is the value. Call it only to send it to the provider it is for.
func (s Secret) Reveal() string { return string(s) }

// A provider table prints as its name and host only. Secret already hides
// itself, but fmt prints a pointer field with a verb other than %v or %p
// through its error path, which skips every String and Format method, so the
// tables hide themselves as a whole. The url is left out too: an ntfy topic
// name is a secret of its own.

// Format prints the table as its name and host.
func (p NtfyConfig) Format(f fmt.State, _ rune) { formatProvider(f, "ntfy", p.URL) }

// Format prints the table as its name and host.
func (p PushoverConfig) Format(f fmt.State, _ rune) { formatProvider(f, "pushover", p.URL) }

// Format prints the table as its name and host.
func (p WebhookConfig) Format(f fmt.State, _ rune) { formatProvider(f, "webhook", p.URL) }

func formatProvider(f fmt.State, name, raw string) {
	host := ""
	if u, err := url.Parse(raw); err == nil {
		host = u.Hostname()
	}
	_, _ = f.Write([]byte("[notify." + name + " " + host + "]"))
}

// On reports whether notifications are on.
func (n *NotifyConfig) On() bool { return n.Enabled == nil || *n.Enabled }

// ContentLevel is Content, or the default.
func (n *NotifyConfig) ContentLevel() string {
	if strings.TrimSpace(n.Content) == NotifyContentTitle {
		return NotifyContentTitle
	}
	return NotifyContentSummary
}

func intOr(p *int, def int) int {
	if p == nil || *p < 0 {
		return def
	}
	return *p
}

// QuietActive is QuietActiveSeconds, or the default.
func (n *NotifyConfig) QuietActive() int { return intOr(n.QuietActiveSeconds, 120) }

// Cooldown is CooldownSeconds, or the default.
func (n *NotifyConfig) Cooldown() int { return intOr(n.CooldownSeconds, 60) }

// HourlyCap is MaxPerHour, or the default. Zero means no cap.
func (n *NotifyConfig) HourlyCap() int { return intOr(n.MaxPerHour, 30) }

// HasProvider reports whether any provider is set.
func (n *NotifyConfig) HasProvider() bool {
	return n.Ntfy != nil || n.Pushover != nil || n.Webhook != nil
}

// Triggered reports whether an Inbox item of this kind sends a notification.
// The kinds are the session package's attention kinds.
func (n *NotifyConfig) Triggered(kind string) bool {
	t := n.Triggers
	on := func(p *bool, def bool) bool {
		if p == nil {
			return def
		}
		return *p
	}
	switch kind {
	case "approval":
		return on(t.Approval, true)
	case "plan":
		return on(t.Plan, true)
	case "question":
		return on(t.Question, true)
	case "ask":
		return on(t.Ask, true)
	case "mail":
		return on(t.Mail, false)
	case "errored":
		return on(t.Errored, false)
	case "finished":
		return on(t.Finished, false)
	}
	return false
}

// Destinations names everything that decides where a notification goes and
// with which credentials: the addresses, and where each secret is read from.
// A config reload that changes it gives the Inbox's text to someone new, so
// the daemon applies such a change only from tuios config apply or a
// restart. The secrets themselves are not in it, only where they come from.
func (n *NotifyConfig) Destinations() []string {
	var out []string
	if n.Ntfy != nil {
		out = append(out, "ntfy\x00"+n.Ntfy.URL+"\x00"+secretOrigin(n.Ntfy.Token, n.Ntfy.TokenEnv, n.Ntfy.TokenFile))
	}
	if n.Pushover != nil {
		p := n.Pushover
		out = append(out, "pushover\x00"+p.URL+"\x00"+secretOrigin(p.User, p.UserEnv, p.UserFile)+"\x00"+secretOrigin(p.Token, p.TokenEnv, p.TokenFile))
	}
	if n.Webhook != nil {
		out = append(out, "webhook\x00"+n.Webhook.URL+"\x00"+secretOrigin(n.Webhook.Token, n.Webhook.TokenEnv, n.Webhook.TokenFile))
	}
	if n.WebURL != "" {
		out = append(out, "web\x00"+n.WebURL)
	}
	// Plain http lets the Inbox's text go to a push service in clear.
	if n.WebPushInsecure() {
		out = append(out, "webpush\x00insecure")
	}
	return out
}

// secretOrigin says where a secret comes from without saying what it is.
// Two inline values are not told apart, since a digest of one would leak a
// little of it. A changed inline value can only go to the address already
// approved.
func secretOrigin(inline Secret, env, file string) string {
	switch {
	case inline != "":
		return "inline"
	case env != "":
		return "env:" + env
	case file != "":
		return "file:" + file
	}
	return ""
}

// ResolveSecret reads a secret from the first source that is set: the value
// in the file, then the environment variable, then the file. A secret with no
// source is empty with no error. An error names the source, never the value.
func ResolveSecret(inline Secret, env, file string) (string, error) {
	if inline != "" {
		return strings.TrimSpace(inline.Reveal()), nil
	}
	if env != "" {
		v, ok := os.LookupEnv(env)
		if !ok || strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("the environment variable %s is not set in this process", env)
		}
		return strings.TrimSpace(v), nil
	}
	if file != "" {
		path := ExpandHome(file)
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("cannot read the secret file %s: %w", file, errors.Unwrap(err))
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("other users can read the secret file %s. Run chmod 600 on it", file)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read the secret file %s", file)
		}
		v := strings.TrimSpace(string(data))
		if v == "" {
			return "", fmt.Errorf("the secret file %s is empty", file)
		}
		return v, nil
	}
	return "", nil
}

// ExpandHome replaces a leading ~/ with the home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// checkNotifyURL says what is wrong with a provider address, or "".
func checkNotifyURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "the url is empty. Set it to the address that takes the notification"
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "the url is not an http or https address"
	}
	if u.User != nil {
		return "the url holds a user name or password. Put the credential in token, token_env or token_file"
	}
	return ""
}

// validateNotify reports the [notify] mistakes that would make a notification
// fail or go nowhere. No message holds a secret, an address or a topic.
func validateNotify(cfg *UserConfig, result *ValidationResult) {
	n := &cfg.Notify
	warn := func(key, msg string) {
		result.Warnings = append(result.Warnings, ValidationError{Field: "notify", Key: key, Message: msg})
	}
	if c := strings.TrimSpace(n.Content); c != "" && c != NotifyContentSummary && c != NotifyContentTitle {
		warn("content", fmt.Sprintf("%q is not one of %s. The summary is sent", c, strings.Join(NotifyContentNames, ", ")))
	}
	if n.WebURL != "" {
		if u, err := url.Parse(n.WebURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			warn("web_url", "web_url is not an http or https address. Notifications are sent with no link")
		}
	}
	sources := func(key string, inline Secret, env, file string) {
		set := 0
		for _, s := range []bool{inline != "", env != "", file != ""} {
			if s {
				set++
			}
		}
		if set > 1 {
			warn(key, "set only one of "+key+", "+key+"_env and "+key+"_file. The first one in that order is used")
		}
	}
	if p := n.Ntfy; p != nil {
		if why := checkNotifyURL(p.URL); why != "" {
			warn("ntfy.url", why)
		}
		if p.Priority < 0 || p.Priority > 5 {
			warn("ntfy.priority", "priority must be 1 to 5, or 0 for the default")
		}
		sources("ntfy.token", p.Token, p.TokenEnv, p.TokenFile)
	}
	if p := n.Pushover; p != nil {
		if p.URL != "" {
			if why := checkNotifyURL(p.URL); why != "" {
				warn("pushover.url", why)
			}
		}
		if p.User == "" && p.UserEnv == "" && p.UserFile == "" {
			warn("pushover.user", "Pushover needs your user key. Set user, user_env or user_file")
		}
		if p.Token == "" && p.TokenEnv == "" && p.TokenFile == "" {
			warn("pushover.token", "Pushover needs an application token. Set token, token_env or token_file")
		}
		sources("pushover.user", p.User, p.UserEnv, p.UserFile)
		sources("pushover.token", p.Token, p.TokenEnv, p.TokenFile)
	}
	if p := n.Webhook; p != nil {
		if why := checkNotifyURL(p.URL); why != "" {
			warn("webhook.url", why)
		}
		sources("webhook.token", p.Token, p.TokenEnv, p.TokenFile)
	}
	if p := n.WebPush; p != nil {
		if sub := strings.TrimSpace(p.Subject); sub != "" && !ValidWebPushSubject(sub) {
			warn("webpush.subject", "subject must be an https URL or a mailto: URI. The default https URL is sent")
		}
	}
}
