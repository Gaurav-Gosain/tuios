package session

import (
	"context"
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pushnotify"
)

// Push notifications for the Inbox: the [notify] table.
//
// The daemon follows its own event stream for attention events and sends a
// notification for an item that opens with a kind [notify.triggers] names. It
// is a subscriber like any other, so the transition that opened the item never
// waits on it, and the sends run on one goroutine of their own, each bounded
// by pushnotify.SendTimeout.
//
// An item is sent at most once, whatever its updates say: a harness repeating
// its state, or the summary changing under a held prompt, is the same thing
// waiting. A pane whose items close and open again is held to one notification
// per kind per cooldown, and every notification together to max_per_hour.
//
// A person typing at an attached client in the last quiet_active_seconds is at
// the desk and already sees the Inbox, so the item waits. When they stay away
// that long and the item is still open, it is sent then.
//
// Nothing is logged but the provider, its host and what failed. The message
// text is pane content, and the address and token are secrets.

// notifyQueue bounds the notifications waiting to be sent. A burst past it is
// dropped and logged rather than held, since by then the rate cap would drop
// it anyway.
const notifyQueue = 32

// notifyEventQueue is the notifier's subscription buffer. Attention events
// come at the speed a person or an agent changes state, not output speed.
const notifyEventQueue = 256

// pushNotifier is the daemon's [notify] sender.
type pushNotifier struct {
	d *Daemon

	// cfg is the [notify] table in force, replaced whole on a reload.
	cfg atomic.Pointer[config.NotifyConfig]
	// waiting is set when config.toml names a destination that waits for
	// tuios config apply (reload).
	waiting atomic.Bool

	mu sync.Mutex
	// sent are the items already sent or dropped by a limit, by id, so an
	// update never sends twice. An item leaves it when it closes.
	sent map[string]bool
	// last is when each pane last sent a notification of a kind, for the
	// cooldown.
	last map[string]time.Time
	// hour are the send times in the last hour, for max_per_hour.
	hour []time.Time
	// held are the items waiting for the person to leave the desk, by id.
	held  map[string]pushnotify.Item
	timer *time.Timer

	queue  chan pushnotify.Message
	sub    *eventSub
	done   chan struct{}
	wg     sync.WaitGroup
	client atomic.Pointer[pushnotify.Client]
	now    func() time.Time
}

func newPushNotifier(d *Daemon, cfg config.NotifyConfig) *pushNotifier {
	n := &pushNotifier{
		d:     d,
		sent:  make(map[string]bool),
		last:  make(map[string]time.Time),
		held:  make(map[string]pushnotify.Item),
		queue: make(chan pushnotify.Message, notifyQueue),
		done:  make(chan struct{}),
		now:   time.Now,
	}
	n.set(cfg)
	return n
}

// set installs a [notify] table.
func (n *pushNotifier) set(cfg config.NotifyConfig) {
	c := cfg
	n.cfg.Store(&c)
	n.client.Store(pushnotify.NewClient(c.AllowHTTPRedirects))
}

// reload applies a [notify] table read from config.toml. From a file change
// (byPerson false) a table that sends to a destination the running one does
// not have waits for tuios config apply, since a process in a pane can write
// the file: it applies only what narrows. It reports whether a change waits.
func (n *pushNotifier) reload(next config.NotifyConfig, byPerson bool) bool {
	if n == nil {
		return false
	}
	cur := n.cfg.Load()
	if byPerson || cur == nil || isSubset(next.Destinations(), cur.Destinations()) {
		n.set(next)
		n.waiting.Store(false)
		return false
	}
	// What narrows applies at once: notifications turned off.
	if !next.On() && cur.On() {
		off := *cur
		f := false
		off.Enabled = &f
		n.set(off)
	}
	n.waiting.Store(true)
	log.Printf("[NOTIFY] config.toml names a new notification destination. It applies after tuios config apply from outside tuios, or a daemon restart")
	return true
}

// waitingApply reports whether a [notify] change waits for tuios config
// apply.
func (n *pushNotifier) waitingApply() bool { return n != nil && n.waiting.Load() }

// isSubset reports whether every entry of a is in b.
func isSubset(a, b []string) bool {
	for _, s := range a {
		if !slices.Contains(b, s) {
			return false
		}
	}
	return true
}

// start subscribes to the attention events and starts the sender. A table
// with no provider still subscribes, so a provider added by tuios config
// apply works without a restart.
func (n *pushNotifier) start() {
	if n == nil {
		return
	}
	n.sub = n.d.events.subscribe(eventFilter{types: map[string]bool{EventAttention: true}}, notifyEventQueue)
	n.wg.Add(2)
	go n.follow()
	go n.sender()
}

// stop ends both goroutines. Notifications still queued are dropped: the
// daemon is going away, and the items they are about are saved.
func (n *pushNotifier) stop() {
	if n == nil || n.sub == nil {
		return
	}
	n.d.events.unsubscribe(n.sub)
	close(n.done)
	n.mu.Lock()
	if n.timer != nil {
		n.timer.Stop()
	}
	n.mu.Unlock()
	n.wg.Wait()
}

// follow reads the attention events.
func (n *pushNotifier) follow() {
	defer n.wg.Done()
	for {
		select {
		case <-n.sub.stop:
			return
		case ev := <-n.sub.ch:
			if ev.Attention != nil {
				n.note(ev.Action, *ev.Attention)
			}
		}
	}
}

// note folds one attention event in.
func (n *pushNotifier) note(action string, it AttentionItem) {
	if it.Host != "" {
		// Another machine's item. That machine sends its own.
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if action == AttentionClosed {
		delete(n.sent, it.ID)
		delete(n.held, it.ID)
		return
	}
	if n.sent[it.ID] {
		return
	}
	n.considerLocked(pushnotify.Item{
		ID: it.ID, Kind: it.Kind, Session: it.Session, Window: it.Window,
		Name: it.Name, Summary: it.Summary, Harness: it.Harness,
	})
}

// considerLocked sends it, holds it while the person is at the desk, or drops
// it. The caller holds mu.
func (n *pushNotifier) considerLocked(it pushnotify.Item) {
	cfg := n.cfg.Load()
	if cfg == nil || !cfg.On() || !cfg.HasProvider() || !cfg.Triggered(it.Kind) {
		return
	}
	if it.Session == "" && it.Window == "" && it.Kind == AttentionErrored {
		// A notice about config.toml, not about an agent.
		return
	}
	now := n.now()
	if quiet := time.Duration(cfg.QuietActive()) * time.Second; quiet > 0 {
		if last := n.d.lastHumanInput(); !last.IsZero() && now.Sub(last) < quiet {
			n.held[it.ID] = it
			n.armLocked(last.Add(quiet).Sub(now))
			return
		}
	}
	delete(n.held, it.ID)
	n.sent[it.ID] = true
	key := it.Session + "\x00" + it.Window + "\x00" + it.Kind
	if cool := time.Duration(cfg.Cooldown()) * time.Second; cool > 0 {
		if t, ok := n.last[key]; ok && now.Sub(t) < cool {
			LogBasic("[NOTIFY] item %s: the pane sent one %s in the last %s", it.ID, it.Kind, cool)
			return
		}
	}
	if limit := cfg.HourlyCap(); limit > 0 {
		cut := 0
		for cut < len(n.hour) && now.Sub(n.hour[cut]) >= time.Hour {
			cut++
		}
		n.hour = n.hour[cut:]
		if len(n.hour) >= limit {
			log.Printf("[NOTIFY] item %s not sent: %d notifications in the last hour is the max_per_hour limit", it.ID, limit)
			return
		}
	}
	if len(n.last) >= 1024 {
		// Panes come and go. A cooldown an hour old holds nothing back.
		for k, t := range n.last {
			if now.Sub(t) >= time.Hour {
				delete(n.last, k)
			}
		}
	}
	n.last[key] = now
	n.hour = append(n.hour, now)
	msg := pushnotify.Build(it, cfg.ContentLevel(), cfg.WebURL)
	select {
	case n.queue <- msg:
	default:
		log.Printf("[NOTIFY] item %s not sent: %d notifications are waiting already", it.ID, notifyQueue)
	}
}

// armLocked sets the timer that looks at the held items again. One timer
// serves them all: every hold ends at the same time, quiet after the last
// input. The caller holds mu.
func (n *pushNotifier) armLocked(after time.Duration) {
	if after < 0 {
		after = 0
	}
	after += 50 * time.Millisecond
	if n.timer != nil {
		n.timer.Stop()
	}
	n.timer = time.AfterFunc(after, n.recheck)
}

// recheck sends the held items whose wait is over, or holds them again when
// the person typed since.
func (n *pushNotifier) recheck() {
	select {
	case <-n.done:
		return
	default:
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	held := make([]pushnotify.Item, 0, len(n.held))
	for _, it := range n.held {
		held = append(held, it)
	}
	slices.SortFunc(held, func(a, b pushnotify.Item) int { return compareIDs(a.ID, b.ID) })
	for _, it := range held {
		n.considerLocked(it)
	}
}

// compareIDs orders attention ids, which are decimal counters.
func compareIDs(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// sender sends the queued notifications, one at a time.
func (n *pushNotifier) sender() {
	defer n.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-n.done
		cancel()
	}()
	for {
		select {
		case <-n.done:
			return
		case msg := <-n.queue:
			cfg := n.cfg.Load()
			if cfg == nil {
				continue
			}
			client := n.client.Load()
			for _, p := range pushnotify.Providers(cfg) {
				if err := p.Send(ctx, client, msg); err != nil {
					log.Printf("[NOTIFY] %s failed for item %s: %v", p.Label(), msg.Item.ID, err)
					continue
				}
				LogBasic("[NOTIFY] %s sent item %s", p.Label(), msg.Item.ID)
			}
		}
	}
}

// lastHumanInput is the newest input a person gave at any attached client:
// a key typed into a pane, or the activity a client reports.
func (d *Daemon) lastHumanInput() time.Time {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	var last time.Time
	for _, cs := range d.clients {
		cs.mu.Lock()
		attached := cs.sessionID != "" && cs.isTUIClient
		act := cs.lastActivity
		cs.mu.Unlock()
		if !attached {
			continue
		}
		if in := cs.lastInput.Load(); in != 0 {
			if t := time.Unix(0, in); t.After(act) {
				act = t
			}
		}
		if act.After(last) {
			last = act
		}
	}
	return last
}
