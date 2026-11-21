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
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

const notifyQueue = 32

const notifyEventQueue = 256

type pushNotifier struct {
	d *Daemon

	cfg     atomic.Pointer[config.NotifyConfig]
	waiting atomic.Bool

	mu    sync.Mutex
	sent  map[string]bool
	last  map[string]time.Time
	hour  []time.Time
	held  map[string]pushnotify.Item
	timer *time.Timer

	queue  chan pushnotify.Message
	sub    *eventSub
	done   chan struct{}
	wg     sync.WaitGroup
	client atomic.Pointer[pushnotify.Client]
	now    func() time.Time
	web    *webPusher
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
	n.web = newWebPusher(n)
	n.set(cfg)
	return n
}

func (n *pushNotifier) set(cfg config.NotifyConfig) {
	c := cfg
	n.cfg.Store(&c)
	n.client.Store(pushnotify.NewClient(c.AllowHTTPRedirects))
}

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

func (n *pushNotifier) waitingApply() bool { return n != nil && n.waiting.Load() }

func isSubset(a, b []string) bool {
	for _, s := range a {
		if !slices.Contains(b, s) {
			return false
		}
	}
	return true
}

func (n *pushNotifier) start() {
	if n == nil {
		return
	}
	n.sub = n.d.events.subscribe(eventFilter{types: map[string]bool{EventAttention: true}}, notifyEventQueue)
	n.wg.Add(2)
	go n.follow()
	go n.sender()
}

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
	n.web.stop()
}

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

func (n *pushNotifier) note(action string, it AttentionItem) {
	if it.Host != "" {
		return
	}
	if action != AttentionClosed && it.WithParentUntil != 0 {
		return
	}
	n.web.note(action, it)
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

func (n *pushNotifier) considerLocked(it pushnotify.Item) {
	cfg := n.cfg.Load()
	if cfg == nil || !cfg.On() || !cfg.HasProvider() || !cfg.Triggered(it.Kind) {
		return
	}
	if it.Session == "" && it.Window == "" && it.Kind == AttentionErrored {
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
	sortx.SortFunc(held, func(a, b pushnotify.Item) int { return compareIDs(a.ID, b.ID) })
	for _, it := range held {
		n.considerLocked(it)
	}
}

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

func (cs *connState) newestInput(act time.Time) time.Time {
	if in := cs.lastInput.Load(); in != 0 {
		if t := time.Unix(0, in); t.After(act) {
			return t
		}
	}
	return act
}

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
		act = cs.newestInput(act)
		if act.After(last) {
			last = act
		}
	}
	return last
}
