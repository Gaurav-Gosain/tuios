package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// browseTimeout is how long kero browser waits, unless told: for an
// element to show, or a page to load.
const browseTimeout = 10 * time.Second

// browse does what kero browser asks of a browser pane's page, and
// answers: what waits on the page waits off the main thread. It is a
// remote control of the app, as kero do is, and takes the user's leave
// as kero do does.
func (a *App) browse(h *host, r *proto.BrowserRequest, answer func(*proto.BrowserResult, error)) {
	fail := func(format string, args ...any) { answer(nil, fmt.Errorf(format, args...)) }
	if !a.remoteControl(h) {
		if h.remote() {
			fail("Remote Control of %s is off: turn it on in guios's Settings › Hosts", h.name)
		} else {
			fail("Remote Control is off: turn it on in guios's Settings › Agents")
		}
		return
	}
	s := a.sessionOfPane(r.Pane)
	// The page shows on this machine, which reaches what the host may
	// not: a host drives the browsers of its own sessions alone.
	if s != nil && a.hostOf(s.ID) != h {
		s = nil
	}
	if s == nil {
		fail("no pane %s in the app", r.Pane)
		return
	}
	p := s.PaneByID(r.Pane)
	if !p.Browser() {
		fail("pane %s is a terminal, not a browser", r.Pane)
		return
	}
	// The cookies are every page's, not this one's, which a host's
	// sessions have no business with: they stay on this machine.
	if r.Op == "cookies" {
		if h.remote() {
			fail("cookies stay on this machine: browser cookies work in its own sessions, not %s's", h.name)
			return
		}
		go func() { answer(cookies(r.Text, r.Domain, r.Cookies)) }()
		return
	}
	// A pane never shown has no page yet: it gets one now, on the page
	// kerod has, which loads though no one sees it, before it is used.
	_, had := a.webs[p.ID]
	e := a.ensureWeb(p)
	if e.view == nil {
		if e.err != "" {
			fail("%s", e.err)
		} else {
			fail("this window shows no pages")
		}
		return
	}
	timeout := time.Duration(r.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = browseTimeout
	}
	var loaded <-chan error
	if !had && p.URL != proto.BlankURL {
		loaded = e.nextLoad()
	}
	view, page := e.view, e.view.Page()
	// do does what waits on the page off the main thread, once it loaded.
	do := func(fn func() (*proto.BrowserResult, error)) {
		go func() {
			if loaded != nil {
				if err := settle(page, loaded, timeout); err != nil {
					answer(nil, err)
					return
				}
			}
			answer(fn())
		}()
	}
	eval := func(script string) {
		do(func() (*proto.BrowserResult, error) { return evalPage(page, script, timeout) })
	}
	switch r.Op {
	case "open", "back", "forward", "reload":
		switch {
		case r.Op == "open" && strings.TrimSpace(r.Text) == "":
			fail("open: which page?")
			return
		case r.Op == "back" && !page.CanGoBack():
			fail("no page to go back to")
			return
		case r.Op == "forward" && !page.CanGoForward():
			fail("no page to go forward to")
			return
		}
		// What it loads is what is waited for, in place of the first page.
		loaded = nil
		done := e.nextLoad()
		switch r.Op {
		case "open":
			e.load(r.Text)
			a.tellPage(r.Pane, e)
		case "back":
			page.GoBack()
		case "forward":
			page.GoForward()
		case "reload":
			page.Reload()
			e.loading = true
		}
		do(func() (*proto.BrowserResult, error) { return nil, settle(page, done, timeout) })
	case "wait":
		if r.Selector == "" && r.Ref == "" && r.Text == "" {
			do(func() (*proto.BrowserResult, error) { return nil, settle(page, nil, timeout) })
			return
		}
		eval(pageScript(r.BrowserParams, timeout))
	case "text", "html", "snapshot", "click", "type":
		if (r.Op == "click" || r.Op == "type") && r.Selector == "" && r.Ref == "" {
			fail("%s: which element?", r.Op)
			return
		}
		eval(pageScript(r.BrowserParams, timeout))
	case "eval":
		if strings.TrimSpace(r.Text) == "" {
			fail("eval: what script?")
			return
		}
		eval(r.Text)
	case "screenshot":
		do(func() (*proto.BrowserResult, error) {
			png, err := view.CapturePage()
			if err == nil && len(png) == 0 {
				// A pane never shown has no size: the window lays its page
				// out unseen, and keeps it, until it has a picture. Its
				// page takes a moment to lay out at that size.
				a.update(func() { e.staged = true })
				for range 20 {
					time.Sleep(50 * time.Millisecond)
					if png, err = view.CapturePage(); err != nil || len(png) > 0 {
						break
					}
				}
				a.update(func() { e.staged = false })
			}
			switch {
			case err != nil:
				return nil, err
			case len(png) == 0:
				return nil, fmt.Errorf("pane %s has no picture: show it in guios, then try again", r.Pane)
			}
			return &proto.BrowserResult{PNG: png}, nil
		})
	default:
		fail("no browser command %q", r.Op)
	}
}

func cookieCount(n int) string {
	if n == 1 {
		return "1 cookie"
	}
	return itoa(n) + " cookies"
}

// cookies saves the cookies of the browser panes' pages, those of a
// domain if given, or loads some: every pane shares them, and keeps them
// as the system's web view does. A cookie the engine takes no part of is
// left out, and told of.
func cookies(op, domain string, load []proto.Cookie) (*proto.BrowserResult, error) {
	switch op {
	case "save":
		list, err := mygo.Cookies.List(mygo.CookieFilter{Domain: domain})
		if err != nil {
			return nil, err
		}
		out := make([]proto.Cookie, 0, len(list))
		for _, c := range list {
			pc := proto.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Expires: -1,
				HTTPOnly: c.HTTPOnly, Secure: c.Secure, SameSite: "Lax"}
			if pc.Path == "" {
				pc.Path = "/"
			}
			if !c.Expires.IsZero() {
				pc.Expires = float64(c.Expires.Unix())
			}
			// WebKit tells a cookie set with no SameSite as None, which
			// takes Secure: such a one goes as Lax.
			switch {
			case c.SameSite == mygo.CookieSameSiteStrict:
				pc.SameSite = "Strict"
			case c.SameSite == mygo.CookieSameSiteNone && c.Secure:
				pc.SameSite = "None"
			}
			out = append(out, pc)
		}
		return &proto.BrowserResult{Cookies: out}, nil
	case "load":
		var skipped []string
		for _, pc := range load {
			c := mygo.Cookie{Name: pc.Name, Value: pc.Value, Domain: pc.Domain, Path: pc.Path,
				Secure: pc.Secure, HTTPOnly: pc.HTTPOnly, SameSite: mygo.CookieSameSite(strings.ToLower(pc.SameSite))}
			if c.SameSite == mygo.CookieSameSiteNone && !c.Secure {
				c.SameSite = mygo.CookieSameSiteLax
			}
			if pc.Expires > 0 {
				c.Expires = time.Unix(int64(pc.Expires), 0)
			}
			if err := mygo.Cookies.Set(c); err != nil {
				skipped = append(skipped, fmt.Sprintf("skipped %s of %s: %s", pc.Name, pc.Domain, strings.TrimPrefix(err.Error(), "mygo: cookies: ")))
			}
		}
		text := fmt.Sprintf("loaded %d of %s", len(load)-len(skipped), cookieCount(len(load)))
		return &proto.BrowserResult{Text: strings.Join(append([]string{text}, skipped...), "\n")}, nil
	}
	return nil, fmt.Errorf("cookies: save or load, not %q", op)
}

// evalPage runs a script on the page and returns its value as text: a
// string as it is, else as JSON.
func evalPage(page *mygo.Page, script string, timeout time.Duration) (*proto.BrowserResult, error) {
	// The script's own wait ends first, with its own error.
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()
	v, err := page.EvalContext(ctx, script)
	if err != nil {
		var js *mygo.EvalError
		if errors.As(err, &js) {
			err = errors.New(js.Message)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("the page did not answer")
		}
		return nil, err
	}
	var text string
	switch v := v.(type) {
	case nil:
	case string:
		text = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	return &proto.BrowserResult{Text: text}, nil
}

// nextLoad returns what the page's next load sends its end to: nil, or
// why it failed.
func (e *webEntry) nextLoad() <-chan error {
	ch := make(chan error, 1)
	e.loads = append(e.loads, ch)
	return ch
}

// loadEnded tells those waiting for the page's load that it ended.
func (e *webEntry) loadEnded(err error) {
	for _, ch := range e.loads {
		ch <- err
	}
	e.loads = nil
}

// settle waits for the page to load what it was sent to: until its load
// ends (done), failed or not, or, for a navigation that loads nothing, as
// to an anchor of the page, until it has not loaded for a moment. With
// no done, it waits for what the page loads to end.
func settle(page *mygo.Page, done <-chan error, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	start := time.Now()
	for {
		select {
		case err := <-done:
			return err
		case <-deadline.C:
			return errors.New("timed out waiting for the page to load")
		case <-tick.C:
			if page.IsLoading() {
				continue
			}
			if done == nil {
				return nil
			}
			// A navigation takes a moment to start; one that ended
			// tells, a moment after.
			if time.Since(start) > time.Second {
				select {
				case err := <-done:
					return err
				case <-time.After(200 * time.Millisecond):
					return nil
				}
			}
		}
	}
}

// pageScript is the script that does a kero browser command on the page:
// it waits for the element the command needs, then does as a user would,
// with the events a page listens to.
func pageScript(p proto.BrowserParams, timeout time.Duration) string {
	args, _ := json.Marshal(map[string]any{
		"op": p.Op, "selector": p.Selector, "ref": p.Ref, "text": p.Text, "enter": p.Enter, "timeout": timeout.Milliseconds(),
	})
	return "(" + pageOps + ")(" + string(args) + ")"
}

// pageOps does the commands on the page. Clicks go as a mouse's would:
// pointer and mouse events down and up, the focus, then the click, which
// follows links and checks boxes. Typing goes as the keyboard's would,
// through the editing command, which sends the input events frameworks
// listen to; where that types nothing, the field's own value setter, which
// React's tracking sees past, and an input event.
const pageOps = `async (a) => {
  const deadline = Date.now() + a.timeout;
  const pause = () => new Promise((r) => setTimeout(r, 50));
  const visible = (el) => el.checkVisibility ? el.checkVisibility() : el.getClientRects().length > 0;
  // The refs a snapshot gave the elements it showed, which last as long
  // as the page: an element keeps its ref, and one gone fails.
  const refs = window.__keroRefs || Object.defineProperty(window, "__keroRefs",
    { value: { next: 0, ids: new WeakMap(), els: new Map() } }).__keroRefs;
  const what = a.ref ? "ref " + a.ref : a.selector;
  const find = async (shown) => {
    for (;;) {
      let el;
      if (a.ref) {
        const r = refs.els.get(a.ref);
        if (!r) throw new Error("no ref " + a.ref + " on this page: take a snapshot for its refs");
        el = r.deref();
        if (!el || !el.isConnected) throw new Error("ref " + a.ref + " is gone from the page: take a new snapshot");
      } else {
        el = document.querySelector(a.selector);
      }
      if (el && (!shown || visible(el))) return el;
      if (Date.now() > deadline) {
        if (a.ref) throw new Error(what + " is not visible");
        throw new Error((el ? "no visible element matches " : "no element matches ") + a.selector);
      }
      await pause();
    }
  };
  const target = a.selector || a.ref;
  const root = () => document.body || document.documentElement;
  const fire = (el, type, init) => el.dispatchEvent(new Event(type, { bubbles: true, cancelable: true, composed: true, ...init }));
  const mouse = (el, type, init) => {
    const r = el.getBoundingClientRect();
    const o = { bubbles: true, cancelable: true, composed: true, view: window, button: 0,
      clientX: r.left + r.width / 2, clientY: r.top + r.height / 2, ...init };
    if (type.startsWith("pointer")) return el.dispatchEvent(new PointerEvent(type, { pointerId: 1, pointerType: "mouse", isPrimary: true, ...o }));
    return el.dispatchEvent(new MouseEvent(type, o));
  };
  const key = (el, type) => el.dispatchEvent(new KeyboardEvent(type, { key: "Enter", code: "Enter", keyCode: 13, which: 13,
    charCode: type === "keypress" ? 13 : 0, bubbles: true, cancelable: true, composed: true }));
  const field = (el) => el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement;
  const snapshot = (top) => {
    for (const [id, r] of refs.els) if (!r.deref()) refs.els.delete(id);
    const refOf = (el) => {
      let id = refs.ids.get(el);
      if (!id) {
        id = "e" + ++refs.next;
        refs.ids.set(el, id);
        refs.els.set(id, new WeakRef(el));
      }
      return id;
    };
    const norm = (s) => (s || "").replace(/\s+/g, " ").trim();
    const quote = (s, n) => JSON.stringify(s.length > n ? s.slice(0, n - 1) + "…" : s);
    const byIds = (el) => norm((el.getAttribute("aria-labelledby") || "").split(/\s+/).map((id) => {
      const t = id && el.ownerDocument.getElementById(id);
      return t ? t.innerText || t.textContent : "";
    }).join(" "));
    const tags = { BUTTON: "button", SUMMARY: "button", UL: "list", OL: "list", MENU: "list", LI: "listitem",
      NAV: "navigation", MAIN: "main", ASIDE: "complementary", ARTICLE: "article", DIALOG: "dialog",
      HEADER: "banner", FOOTER: "contentinfo", FORM: "form", SECTION: "region", TABLE: "table", TR: "row",
      TD: "cell", TH: "columnheader", TEXTAREA: "textbox", OPTION: "option", DETAILS: "group", FIELDSET: "group",
      HR: "separator", PROGRESS: "progressbar", METER: "meter", IFRAME: "iframe", BLOCKQUOTE: "blockquote" };
    const inputs = { button: "button", submit: "button", reset: "button", image: "button", file: "button",
      color: "button", checkbox: "checkbox", radio: "radio", range: "slider", number: "spinbutton", search: "searchbox" };
    const labelled = (el) => el.hasAttribute("aria-label") || el.hasAttribute("aria-labelledby") || el.hasAttribute("title");
    const roleOf = (el) => {
      const r = (el.getAttribute("role") || "").trim().split(/\s+/)[0];
      if (r) return r === "none" ? "presentation" : r;
      const t = el.tagName;
      if (t === "INPUT") return el.type === "hidden" ? "" : inputs[el.type] || "textbox";
      if (t === "SELECT") return el.multiple || el.size > 1 ? "listbox" : "combobox";
      if (t === "A" || t === "AREA") return el.hasAttribute("href") ? "link" : "";
      if (t === "IMG") return el.getAttribute("alt") === "" ? "presentation" : "img";
      if (/^H[1-6]$/.test(t)) return "heading";
      if (t.toLowerCase() === "svg") return labelled(el) ? "img" : "presentation";
      if ((t === "HEADER" || t === "FOOTER") && el.parentElement && el.parentElement.closest("article, aside, main, nav, section")) return "";
      if ((t === "FORM" || t === "SECTION") && !labelled(el)) return "";
      if (el.isContentEditable && !(el.parentElement && el.parentElement.isContentEditable)) return "textbox";
      if (tags[t]) return tags[t];
      // What a page makes clickable of its own, as a div with a handler.
      if (el.hasAttribute("onclick") || el.hasAttribute("tabindex") && el.tabIndex >= 0) return "generic";
      const parent = el.parentElement;
      if (getComputedStyle(el).cursor === "pointer" && !(parent && getComputedStyle(parent).cursor === "pointer")) return "generic";
      return "";
    };
    // The roles named by what they hold, whose text is the name.
    const fromContent = new Set(["button", "link", "heading", "tab", "menuitem", "menuitemcheckbox", "menuitemradio",
      "option", "checkbox", "radio", "switch", "treeitem", "cell", "columnheader", "rowheader", "tooltip", "generic"]);
    const nameOf = (el, role) => {
      const n = norm(el.getAttribute("aria-label")) || byIds(el);
      if (n) return n;
      const t = el.tagName;
      if (t === "INPUT" && /^(button|submit|reset)$/.test(el.type)) return norm(el.value) || (el.type === "reset" ? "Reset" : el.type === "submit" ? "Submit" : "");
      if (t === "INPUT" && el.type === "image") return norm(el.alt) || "Submit";
      if (t === "INPUT" || t === "TEXTAREA" || t === "SELECT") {
        return norm([...(el.labels || [])].map((l) => l.innerText).join(" ")) || norm(el.getAttribute("placeholder")) || norm(el.title);
      }
      if (t === "IMG" || t === "AREA") return norm(el.alt) || norm(el.title);
      if (t === "FIELDSET" && el.querySelector(":scope > legend")) return norm(el.querySelector(":scope > legend").innerText);
      if (t === "TABLE" && el.caption) return norm(el.caption.innerText);
      if (fromContent.has(role)) return norm(el.innerText) || norm(el.title);
      return norm(el.title);
    };
    const statesOf = (el, role) => {
      const s = [];
      if (role === "heading") s.push("level=" + (el.getAttribute("aria-level") || (/^H([1-6])$/.exec(el.tagName) || [0, 2])[1]));
      let checked = el.getAttribute("aria-checked");
      if (checked == null && el.tagName === "INPUT" && (el.type === "checkbox" || el.type === "radio")) checked = el.indeterminate ? "mixed" : String(el.checked);
      if (checked === "true") s.push("checked");
      if (checked === "mixed") s.push("checked=mixed");
      if (el.disabled || el.getAttribute("aria-disabled") === "true") s.push("disabled");
      let expanded = el.getAttribute("aria-expanded");
      if (expanded == null && el.tagName === "SUMMARY" && el.parentElement && el.parentElement.tagName === "DETAILS") expanded = String(el.parentElement.open);
      if (expanded) s.push("expanded=" + expanded);
      if (el.getAttribute("aria-selected") === "true" || el.tagName === "OPTION" && el.selected) s.push("selected");
      const pressed = el.getAttribute("aria-pressed");
      if (pressed === "true") s.push("pressed");
      if (pressed === "mixed") s.push("pressed=mixed");
      if (role === "link" && el.getAttribute("href")) s.push("url=" + quote(el.getAttribute("href"), 120));
      if (el.tagName === "SELECT" && el.selectedOptions.length) s.push("value=" + quote(norm(el.selectedOptions[0].text), 80));
      else if (field(el) && el.type !== "password" && !(role in { button: 1, checkbox: 1, radio: 1 }) && el.value) s.push("value=" + quote(el.value, 80));
      return s;
    };
    const skip = new Set(["SCRIPT", "STYLE", "TEMPLATE", "NOSCRIPT", "HEAD", "DATALIST"]);
    const inline = new Set(["A", "ABBR", "B", "BDI", "BDO", "CITE", "CODE", "DATA", "DFN", "EM", "FONT", "I", "KBD",
      "LABEL", "MARK", "Q", "S", "SAMP", "SMALL", "SPAN", "STRONG", "SUB", "SUP", "TIME", "U", "VAR"]);
    const shows = (el) => {
      if (el.getAttribute("aria-hidden") === "true" || el.hidden) return false;
      if (el.closest("select")) return true;
      return visible(el) || getComputedStyle(el).display === "contents";
    };
    const lines = [];
    // Text runs of what shows no role of its own go together, at the
    // depth of what holds them.
    let text = "", textAt = 0;
    const flush = () => {
      const t = norm(text);
      if (t) lines.push("  ".repeat(textAt) + "- text " + quote(t, 200));
      text = "";
    };
    const walk = (node, depth, quiet) => {
      if (node.nodeType === Node.TEXT_NODE) {
        // Elements hide by their boxes; a closed details' own text, by its.
        const up = node.parentElement;
        if (quiet || up && up.tagName === "DETAILS" && !up.open) return;
        if (!norm(text)) textAt = depth;
        text += node.data;
        return;
      }
      if (node.nodeType !== Node.ELEMENT_NODE && node.nodeType !== Node.DOCUMENT_FRAGMENT_NODE) return;
      const el = node;
      const kids = () => el.tagName === "SLOT" && el.assignedNodes({ flatten: true }).length ? el.assignedNodes({ flatten: true })
        : el.shadowRoot ? el.shadowRoot.childNodes : el.childNodes;
      if (node.nodeType === Node.DOCUMENT_FRAGMENT_NODE) {
        for (const k of node.childNodes) walk(k, depth, quiet);
        return;
      }
      if (skip.has(el.tagName) || !shows(el)) return;
      if (el.tagName === "BR") {
        text += " ";
        return;
      }
      const role = roleOf(el);
      if (role === "presentation" && el.tagName.toLowerCase() === "svg") return;
      if (!role || role === "presentation") {
        const block = !inline.has(el.tagName);
        if (block) flush();
        for (const k of kids()) walk(k, depth, quiet);
        if (block) flush();
        return;
      }
      flush();
      const name = nameOf(el, role);
      const parts = ["  ".repeat(depth) + "- " + role];
      if (name) parts.push(quote(name, 100));
      parts.push(...statesOf(el, role).map((s) => "[" + s + "]"), "[ref=" + refOf(el) + "]");
      lines.push(parts.join(" "));
      // A field shows its value, an image what it is named.
      if (field(el) || role === "textbox" || role === "img") return;
      if (el.tagName === "IFRAME") {
        try {
          if (el.contentDocument && el.contentDocument.body) walk(el.contentDocument.body, depth + 1, false);
        } catch {}
        return;
      }
      const hush = quiet || !!name && fromContent.has(role);
      for (const k of kids()) walk(k, depth + 1, hush);
      flush();
    };
    walk(top, 0, false);
    flush();
    return lines.join("\n");
  };

  switch (a.op) {
  case "text":
    return (target ? await find() : root()).innerText;
  case "html": {
    if (target) return (await find()).outerHTML;
    const dt = document.doctype ? "<!DOCTYPE " + document.doctype.name + ">\n" : "";
    return dt + document.documentElement.outerHTML;
  }
  case "click": {
    const el = await find(true);
    if (el.disabled) throw new Error(what + " is disabled");
    el.scrollIntoView({ block: "center", inline: "center" });
    mouse(el, "pointerover"); mouse(el, "mouseover");
    mouse(el, "pointerdown", { buttons: 1 });
    if (mouse(el, "mousedown", { buttons: 1 })) {
      const f = el.closest("a[href], button, input, select, textarea, summary, [tabindex], [contenteditable]");
      if (f && f.focus) f.focus({ preventScroll: true });
    }
    mouse(el, "pointerup"); mouse(el, "mouseup");
    mouse(el, "click", { detail: 1 });
    return;
  }
  case "type": {
    const el = await find(true);
    if (el.disabled || el.readOnly) throw new Error(what + " takes no typing");
    el.scrollIntoView({ block: "center" });
    el.focus({ preventScroll: true });
    if (el instanceof HTMLSelectElement) {
      const o = [...el.options].find((o) => o.value === a.text || o.text.trim() === a.text);
      if (!o) throw new Error(what + " has no option " + JSON.stringify(a.text));
      el.value = o.value;
      fire(el, "input"); fire(el, "change");
    } else if (field(el)) {
      try { el.select(); } catch {}
      const typed = a.text === "" ? document.execCommand("delete") : document.execCommand("insertText", false, a.text);
      if (!typed || el.value !== a.text) {
        Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value").set.call(el, a.text);
        el.dispatchEvent(new InputEvent("input", { bubbles: true, composed: true, inputType: "insertText", data: a.text }));
      }
      fire(el, "change");
    } else if (el.isContentEditable) {
      const range = document.createRange();
      range.selectNodeContents(el);
      const sel = getSelection();
      sel.removeAllRanges(); sel.addRange(range);
      if (!document.execCommand("insertText", false, a.text)) {
        el.textContent = a.text;
        el.dispatchEvent(new InputEvent("input", { bubbles: true, composed: true, inputType: "insertText", data: a.text }));
      }
    } else {
      throw new Error(what + " is no field to type in");
    }
    if (a.enter) {
      const at = document.activeElement || el;
      if (key(at, "keydown")) {
        // WebKit does what Enter does for a keypress the page sends too:
        // it submits the form, or breaks the line, unless the page
        // prevents it. What it did is not done again.
        const form = el instanceof HTMLInputElement ? el.form : null;
        const content = () => field(el) ? el.value : el.innerHTML;
        const before = content();
        let submitted = false;
        const mark = () => { submitted = true; };
        if (form) form.addEventListener("submit", mark, true);
        const allowed = key(at, "keypress");
        if (form) form.removeEventListener("submit", mark, true);
        const done = !allowed || submitted || !form && content() !== before;
        if (!done && form) {
          form.requestSubmit ? form.requestSubmit() : form.submit();
        } else if (!done && (el instanceof HTMLTextAreaElement || el.isContentEditable)) {
          document.execCommand("insertLineBreak");
        }
      }
      key(at, "keyup");
    }
    return;
  }
  case "wait": {
    if (target) await find(true);
    if (!a.text) return;
    for (;;) {
      const el = a.ref ? await find() : a.selector ? document.querySelector(a.selector) : root();
      if (el && el.innerText.includes(a.text)) return;
      if (Date.now() > deadline) throw new Error("no " + JSON.stringify(a.text) + " on the page");
      await pause();
    }
  }
  case "snapshot":
    return snapshot(target ? await find() : root());
  }
}`
