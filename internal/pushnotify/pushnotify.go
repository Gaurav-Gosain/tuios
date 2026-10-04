// Package pushnotify sends an Inbox notification to a phone, through ntfy,
// Pushover or a webhook of the person's own.
//
// The daemon calls it when the Inbox gets an item that waits for the person,
// and tuios notify test calls it to try each provider. It builds the message
// from what the Inbox already shows and nothing more, so no pane content goes
// off the machine beyond the one line a row of the Inbox carries.
//
// Nothing here logs. An error names the provider and the host it tried, never
// the full address (an ntfy topic name is its own secret) and never a token.
package pushnotify

import (
	"net/url"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// SendTimeout bounds one request to one provider.
const SendTimeout = 10 * time.Second

// maxRedirects bounds the redirects one request follows.
const maxRedirects = 3

// Item is what a notification is about: the facts of an Inbox item that the
// Inbox shows in its row.
type Item struct {
	ID      string
	Kind    string
	Session string
	Window  string
	Name    string
	Summary string
	Harness string
}

// Message is one notification, ready to send.
type Message struct {
	Title string
	// Body is empty at content level title.
	Body string
	// Link opens tuios-web on the item, empty with no web_url.
	Link string
	// Urgent is set for an item that waits on the person.
	Urgent bool
	// Item is what the webhook carries as fields.
	Item Item
	// Test marks the message tuios notify test sends.
	Test bool
}

// waits reports whether an item of this kind blocks until the person acts.
func waits(kind string) bool {
	switch kind {
	case "approval", "plan", "question", "ask":
		return true
	}
	return false
}

// kindTitle is the start of the title for each kind.
func kindTitle(kind string) string {
	switch kind {
	case "approval":
		return "Approval needed"
	case "plan":
		return "Plan to approve"
	case "question", "ask":
		return "Question for you"
	case "mail":
		return "Mail for you"
	case "errored":
		return "Agent error"
	case "finished":
		return "Agent finished"
	}
	return "Inbox item"
}

// titleCap and bodyCap bound what a notification says. The Inbox summary is
// already cut to 160 bytes and masked for secrets.
const (
	titleCap = 120
	bodyCap  = 240
)

// Build makes the message for an Inbox item. content is a [notify] content
// level and webURL is notify.web_url.
func Build(it Item, content, webURL string) Message {
	msg := Message{Item: it, Urgent: waits(it.Kind), Link: InboxLink(webURL, it.ID)}
	where := "session " + it.Session
	if it.Session == "" {
		where = "tuios"
	}
	if content == config.NotifyContentTitle {
		msg.Title = clip(kindTitle(it.Kind)+" in "+where, titleCap)
		return msg
	}
	who := it.Name
	if who == "" {
		who = it.Harness
	}
	if who == "" {
		who = where
	}
	msg.Title = clip(kindTitle(it.Kind)+": "+who, titleCap)
	body := it.Summary
	if body == "" {
		body = "Open the tuios Inbox to see it."
	}
	if it.Session != "" {
		body += "\n" + strings.ToUpper(where[:1]) + where[1:] + "."
	}
	msg.Body = clip(body, bodyCap)
	return msg
}

// TestMessage is what tuios notify test sends.
func TestMessage(webURL string) Message {
	return Message{
		Title: "tuios test notification",
		Body:  "tuios can reach this device. No action is necessary.",
		Link:  InboxLink(webURL, ""),
		Item:  Item{Kind: "test"},
		Test:  true,
	}
}

// InboxLink is the tuios-web address that opens the Inbox on an item, or
// "" with no usable web_url. tuios-web serves it at /inbox.
func InboxLink(webURL, id string) string {
	if webURL == "" {
		return ""
	}
	base, err := url.Parse(webURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return ""
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	base.RawQuery, base.Fragment = "", ""
	link := base.JoinPath("inbox")
	if id != "" {
		link.RawQuery = url.Values{"item": {id}}.Encode()
	}
	return link.String()
}

// clip cuts s to n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
