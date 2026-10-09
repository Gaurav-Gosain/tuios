package pushnotify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Every request goes through one dedicated http.Client, never
// http.DefaultClient. It reads HTTP_PROXY, HTTPS_PROXY and NO_PROXY, and uses
// the system certificate store. The token travels in a header and is never
// logged. This package logs nothing, and an error names no address beyond the
// host.

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
	http *http.Client
	// push sends Web Push: it follows no redirect, and its dialer checks
	// the address it connects to (webpush.go).
	push *http.Client
}

// errRedirectHTTP and errRedirectLimit are what CheckRedirect gives, so post
// can tell them from a transport failure.
var (
	errRedirectHTTP  = errors.New("redirect to plain http")
	errRedirectLimit = errors.New("too many redirects")
	errRedirectHost  = errors.New("redirect to another host")
)

// maxResponseBody bounds how much of an answer is read. The body is not shown,
// since a provider may echo what it was sent.
const maxResponseBody = 4 << 10

// NewClient is the client every provider sends with. It follows at most
// three redirects, only to the host the request was sent to, and only to
// https unless allowHTTP. A redirect cannot send the message and its token on
// in plain text, or to another host. net/http drops the Authorization header
// on a redirect to another host, but a 307 or 308 sends the body again, and
// the Pushover token is in the body.
func NewClient(allowHTTP bool) *Client {
	return &Client{http: &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errRedirectLimit
			}
			if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				return errRedirectHost
			}
			if req.URL.Scheme != "https" && !allowHTTP {
				return errRedirectHTTP
			}
			return nil
		},
	}, push: newPushHTTPClient()}
}

// post sends one request and turns any failure into an error that says what
// happened and what to do, with no address beyond the host.
func (c *Client) post(ctx context.Context, rawURL, contentType string, body []byte, headers []string) error {
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("the url is not an http or https address. Check it in config.toml")
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("the url is not an http or https address. Check it in config.toml")
	}
	req.Header.Set("User-Agent", "tuios-notify")
	req.Header.Set("Content-Type", contentType)
	for _, h := range headers {
		if name, value, ok := strings.Cut(h, ": "); ok {
			req.Header.Set(name, value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return describeTransport(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return describeStatus(resp.StatusCode)
}

// describeTransport says what a failed request means. The error from net/http
// is not shown, since it names the full address.
func describeTransport(ctx context.Context, err error) error {
	var dnsErr *net.DNSError
	var netErr net.Error
	var certErr *tls.CertificateVerificationError
	var alertErr tls.AlertError
	var recordErr tls.RecordHeaderError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalidErr x509.CertificateInvalidError
	switch {
	case errors.Is(err, errRedirectHTTP):
		return errors.New("the server redirected to a plain http address. Set notify.allow_http_redirects = true to allow it")
	case errors.Is(err, errRedirectHost):
		return errors.New("the server redirected to another host, and tuios sends a notification only to the host in the url. Set the url to the address the server redirects to")
	case errors.Is(err, errRedirectLimit):
		return fmt.Errorf("the server redirected more than %d times. Check the url", maxRedirects)
	case ctx.Err() != nil, errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Errorf("no answer in %s. Check that the server is up and this machine can reach it", SendTimeout)
	case errors.As(err, &dnsErr):
		return errors.New("the host name does not resolve. Check the url")
	case errors.Is(err, syscall.ECONNREFUSED):
		return errors.New("the connection was refused. Check that the server is up")
	case errors.As(err, &certErr), errors.As(err, &alertErr), errors.As(err, &recordErr),
		errors.As(err, &unknownAuth), errors.As(err, &hostErr), errors.As(err, &invalidErr):
		return errors.New("the TLS connection failed. Check the certificate of the server")
	}
	return errors.New("the request failed. Check the url and that this machine can reach the server")
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
		return errors.New("a Pushover message needs a user key and an application token. Set both in [notify.pushover]")
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
