// A horizontal finger drag scrolls the strip of tuios's scrolling layout.
//
// It runs on the worker engine's canvas (?renderer=engine), whose touch layer
// already gives a tap, a vertical swipe and a long press their meanings. A
// drag that leaves the slop clearly sideways, before the long press would make
// it sip's drag, is taken here: the listeners run in the capture phase on the
// document, ahead of sip's on the canvas, and keep the rest of the gesture
// from reaching it. sip is sent a pointercancel as the drag is taken, so its
// own touch state ends there and its hold timer cannot turn the next tap into
// a right click. A second finger that lands while the drag is ours is kept
// from sip too, from its press to its lift. One that lands before is sip's
// gesture, a pinch, and the drag is left to sip.
//
// The finger is written as tuios's own report, CSI 7503 ; kind ; col ; row ;
// cells ~ (see internal/app/strip_touch.go). kind 2 brings the columns to the
// right into view, 1 those to the left, and 3 is the lift. cells is the cells
// the finger crossed, so the strip follows the finger one cell for one,
// whatever niri_scroll_cells and niri_reverse_scroll say, which are about
// wheels. tuios moves the strip with it in the scrolling layout only, and
// drops it everywhere else, since the page cannot tell which layout is up.
(() => {
  const SLOP = 10, HOLD_MS = 450;
  let g = null;
  // Pointers of fingers that landed while a drag was ours, kept from sip
  // until they lift.
  const kept = new Set();
  const onEngine = (ev) => ev.isTrusted && ev.pointerType === 'touch' && ev.target instanceof HTMLCanvasElement && ev.target.classList.contains('wt-canvas');
  // The cell the drag started on, 1-based as in a mouse report, and the
  // width of a cell.
  const cell = () => {
    const size = window.sip?.size();
    if (!size) return null;
    const r = g.canvas.getBoundingClientRect();
    const cw = r.width / size.cols, ch = r.height / size.rows;
    return { cw, col: Math.max(1, Math.floor((g.x - r.left) / cw) + 1), row: Math.max(1, Math.floor((g.y - r.top) / ch) + 1) };
  };
  const report = (kind, c, cells) => window.sip.send(`\x1b[7503;${kind};${c.col};${c.row}${cells ? ';' + cells : ''}~`);
  document.addEventListener('pointerdown', (ev) => {
    if (!ev.isTrusted) return;
    if (g?.mine) {
      kept.add(ev.pointerId);
      ev.stopImmediatePropagation();
      return;
    }
    if (g) { g = null; return; }
    if (!onEngine(ev)) return;
    g = { id: ev.pointerId, x: ev.clientX, y: ev.clientY, t: ev.timeStamp, lastX: ev.clientX, acc: 0, mine: false, canvas: ev.target };
  }, true);
  document.addEventListener('pointermove', (ev) => {
    if (!ev.isTrusted) return;
    if (kept.has(ev.pointerId)) { ev.stopImmediatePropagation(); return; }
    if (!g || ev.pointerId !== g.id) return;
    if (!g.mine) {
      // The first sample that leaves the slop decides. A busy page gets the
      // moves queued while it was busy as one event, whose own time is the
      // last of them, so the samples it carries are read in order.
      const samples = ev.getCoalescedEvents?.() ?? [];
      const out = (samples.length ? samples : [ev]).find((e) => Math.hypot(e.clientX - g.x, e.clientY - g.y) > SLOP);
      if (!out) return;
      const dx = out.clientX - g.x, dy = out.clientY - g.y;
      // A hold before the move is sip's drag, and a move that is not clearly
      // sideways is its scroll. Either way the gesture is sip's from here on.
      // The hold is timed by the events' own time, when the finger pressed
      // and moved: a busy page handles a quick move late, and the time it
      // was handled would read it as a hold.
      if (out.timeStamp - g.t >= HOLD_MS || Math.abs(dx) <= 2 * Math.abs(dy)) { g = null; return; }
      g.mine = true;
      g.canvas.dispatchEvent(new PointerEvent('pointercancel', { pointerId: g.id, pointerType: 'touch', bubbles: true }));
    }
    ev.stopImmediatePropagation();
    const c = cell();
    if (!c) return;
    // The finger going left brings the columns to the right into view.
    g.acc += (g.lastX - ev.clientX) / c.cw;
    g.lastX = ev.clientX;
    const cells = Math.trunc(g.acc);
    if (!cells) return;
    g.acc -= cells;
    report(cells > 0 ? 2 : 1, c, Math.abs(cells));
  }, true);
  const end = (ev) => {
    if (!ev.isTrusted) return;
    if (kept.delete(ev.pointerId)) { ev.stopImmediatePropagation(); return; }
    if (!g || ev.pointerId !== g.id) return;
    if (g.mine) {
      ev.stopImmediatePropagation();
      const c = cell();
      if (c) report(3, c);
    }
    g = null;
  };
  document.addEventListener('pointerup', end, true);
  document.addEventListener('pointercancel', end, true);
})();
