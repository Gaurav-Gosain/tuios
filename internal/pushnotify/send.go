package pushnotify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The requests go through curl rather than net/http, for the reason
// internal/release does: net/http and crypto/tls are over 2 MB of the binary,
// and nothing else in tuios makes an HTTPS request. curl honours HTTPS_PROXY,
// NO_PROXY and the system certificate store.
//
// Everything that varies goes in a curl config on stdin, never in an
// argument, so another user's ps cannot read a token, an address or the
// message. The body goes as data-raw, which unlike data never reads a file
// for a value that starts with @.

// Provider is one place a notification goes.
type Provider struct {
	// Name is ntfy, pushover or webhook.
	Name string
	// Host is the host the provider sends to, which is what an error or a
	// report may name.
	Host string
	send func(ctx context.Context, c *Client, m Message) error
}

// Label is the provider as a report names it.
func (p Provider) Label() string {
	if p.Host == "" {
		return p.Name
	}
	return p.Name + " (" + p.Host + ")"
}

// Send delivers m through this provider within SendTimeout.
func (p Provider) Send(ctx context.Context, c *Client, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, SendTimeout+2*time.Second)
	defer cancel()
	return p.send(ctx, c, m)
}

// Providers lists the providers [notify] sets, in a fixed order.
func Providers(n *config.NotifyConfig) []Provider {
	var out []Provider
	if p := n.Ntfy; p != nil {
		cfg := *p
		out = append(out, Provider{Name: "ntfy", Host: hostOf(cfg.URL), send: func(ctx context.Context, c *Client, m Message) error {
			return sendNtfy(ctx, c, cfg, m)
		}})
	}
	if p := n.Pushover; p != nil {
		cfg := *p
		if cfg.URL == "" {
			cfg.URL = config.PushoverAPI
		}
		out = append(out, Provider{Name: "pushover", Host: hostOf(cfg.URL), send: func(ctx context.Context, c *Client, m Message) error {
			return sendPushover(ctx, c, cfg, m)
		}})
	}
	if p := n.Webhook; p != nil {
		cfg := *p
		out = append(out, Provider{Name: "webhook", Host: hostOf(cfg.URL), send: func(ctx context.Context, c *Client, m Message) error {
			return sendWebhook(ctx, c, cfg, m)
		}})
	}
	return out
}

// hostOf is the host of an address, or "" when it has none.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Client is how every provider sends.
type Client struct {
	// allowHTTP lets a redirect go to a plain http address.
	allowHTTP bool
}

// NewClient is the client every provider sends with. It follows at most
// three redirects, and only to https unless allowHTTP, so a redirect cannot
// send the message and its token on in plain text.
func NewClient(allowHTTP bool) *Client { return &Client{allowHTTP: allowHTTP} }

// configQuote is a value as a quoted string of a curl config file, which
// takes \\, \", \n, \r and \t as escapes.
var configQuote = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

// post sends one request and turns any failure into an error that says what
// happened and what to do, with no address beyond the host.
func (c *Client) post(ctx context.Context, rawURL, contentType string, body []byte, headers []string) error {
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("the url is not an http or https address. Check it in config.toml")
	}
	curl, err := exec.LookPath("curl")
	if err != nil {
		return errors.New("tuios sends notifications with curl, which is not on PATH. Install curl")
	}
	var cfg strings.Builder
	line := func(key, val string) { fmt.Fprintf(&cfg, "%s = \"%s\"\n", key, configQuote.Replace(val)) }
	line("url", rawURL)
	line("user-agent", "tuios-notify")
	line("header", "Content-Type: "+contentType)
	for _, h := range headers {
		line("header", h)
	}
	line("data-raw", string(body))
	redir := "=https"
	if c.allowHTTP {
		redir = "=http,https"
	}
	cmd := exec.CommandContext(ctx, curl,
		"--config", "-",
		"--silent", "--show-error",
		"--location", "--max-redirs", strconv.Itoa(maxRedirects), "--proto-redir", redir,
		"--max-time", strconv.Itoa(int(SendTimeout/time.Second)),
		"--output", nullDevice(),
		"--write-out", "%{http_code}",
	)
	var out, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(cfg.String())
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("no answer in %s. Check that the server is up and this machine can reach it", SendTimeout)
		}
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return describeCurlExit(code)
	}
	status, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	if status >= 200 && status < 300 {
		return nil
	}
	return describeStatus(status)
}

// nullDevice is where curl writes the answer's body, which is not shown: a
// provider may echo what it was sent.
func nullDevice() string {
	if isWindows {
		return "NUL"
	}
	return "/dev/null"
}

// describeCurlExit says what a curl exit status means. curl's own message is
// not shown, since it can name the address.
func describeCurlExit(code int) error {
	switch code {
	case 6:
		return errors.New("the host name does not resolve. Check the url")
	case 7:
		return errors.New("the connection was refused. Check that the server is up")
	case 28:
		return fmt.Errorf("no answer in %s. Check that the server is up and this machine can reach it", SendTimeout)
	case 1:
		return errors.New("the server redirected to a plain http address. Set notify.allow_http_redirects = true to allow it")
	case 47:
		return fmt.Errorf("the server redirected more than %d times. Check the url", maxRedirects)
	case 35, 51, 53, 54, 58, 59, 60, 77, 80, 83, 90, 91:
		return errors.New("the TLS connection failed. Check the certificate of the server")
	}
	return fmt.Errorf("the request failed: curl exited with status %d", code)
}

// statusText names the status codes a provider is likely to answer.
func statusText(code int) string {
	switch code {
	case 0:
		return "no status"
	case 400:
		return "400 Bad Request"
	case 401:
		return "401 Unauthorized"
	case 403:
		return "403 Forbidden"
	case 404:
		return "404 Not Found"
	case 413:
		return "413 Content Too Large"
	case 429:
		return "429 Too Many Requests"
	case 500:
		return "500 Internal Server Error"
	case 502:
		return "502 Bad Gateway"
	case 503:
		return "503 Service Unavailable"
	}
	return strconv.Itoa(code)
}

// describeStatus says what a status code means for the person.
func describeStatus(code int) error {
	text := statusText(code)
	switch {
	case code == 401 || code == 403:
		return fmt.Errorf("the server answered %s. Check the token", text)
	case code == 404:
		return fmt.Errorf("the server answered %s. Check the url", text)
	case code == 429:
		return fmt.Errorf("the server answered %s. Send fewer notifications, or wait", text)
	case code >= 500:
		return fmt.Errorf("the server answered %s. The problem is on the server. Try again later", text)
	case code >= 300 && code < 400:
		return fmt.Errorf("the server answered %s and gave no address to follow. Check the url", text)
	}
	return fmt.Errorf("the server answered %s. Check the provider settings", text)
}

// headerText makes a header value safe: a line break would end the header,
// and a non-ASCII title goes as an RFC 2047 encoded word, which ntfy decodes.
func headerText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, s)
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || s[i] < 0x20 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

func sendNtfy(ctx context.Context, c *Client, p config.NtfyConfig, m Message) error {
	token, err := config.ResolveSecret(p.Token, p.TokenEnv, p.TokenFile)
	if err != nil {
		return err
	}
	prio := p.Priority
	if prio == 0 {
		prio = 3
		if m.Urgent {
			prio = 4
		}
	}
	h := []string{"Title: " + headerText(m.Title), "Priority: " + strconv.Itoa(prio), "Tags: tuios"}
	if m.Link != "" {
		h = append(h, "Click: "+headerText(m.Link))
	}
	if token != "" {
		h = append(h, "Authorization: Bearer "+token)
	}
	body := m.Body
	if body == "" {
		// ntfy shows "triggered" for an empty body.
		body = m.Title
	}
	return c.post(ctx, p.URL, "text/plain; charset=utf-8", []byte(body), h)
}

func sendPushover(ctx context.Context, c *Client, p config.PushoverConfig, m Message) error {
	user, err := config.ResolveSecret(p.User, p.UserEnv, p.UserFile)
	if err != nil {
		return err
	}
	token, err := config.ResolveSecret(p.Token, p.TokenEnv, p.TokenFile)
	if err != nil {
		return err
	}
	if user == "" || token == "" {
		return errors.New("Pushover needs a user key and an application token. Set both in [notify.pushover]")
	}
	form := url.Values{"token": {token}, "user": {user}, "title": {m.Title}}
	msg := m.Body
	if msg == "" {
		msg = m.Title
	}
	form.Set("message", msg)
	if m.Link != "" {
		form.Set("url", m.Link)
		form.Set("url_title", "Open the Inbox")
	}
	if m.Urgent {
		form.Set("priority", "1")
	}
	return c.post(ctx, p.URL, "application/x-www-form-urlencoded", []byte(form.Encode()), nil)
}

// WebhookBody is the JSON a webhook receives.
type WebhookBody struct {
	Event   string `json:"event"`
	Test    bool   `json:"test,omitempty"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Link    string `json:"link,omitempty"`
	Urgent  bool   `json:"urgent"`
	ItemID  string `json:"item_id,omitempty"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Harness string `json:"harness,omitempty"`
}

func sendWebhook(ctx context.Context, c *Client, p config.WebhookConfig, m Message) error {
	token, err := config.ResolveSecret(p.Token, p.TokenEnv, p.TokenFile)
	if err != nil {
		return err
	}
	data, err := json.Marshal(WebhookBody{
		Event: "tuios.inbox", Test: m.Test, Kind: m.Item.Kind,
		Title: m.Title, Body: m.Body, Link: m.Link, Urgent: m.Urgent,
		ItemID: m.Item.ID, Session: m.Item.Session, Window: m.Item.Window, Harness: m.Item.Harness,
	})
	if err != nil {
		return err
	}
	var h []string
	if token != "" {
		h = []string{"Authorization: Bearer " + token}
	}
	return c.post(ctx, p.URL, "application/json", data, h)
}
