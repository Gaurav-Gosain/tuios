// A horizontal finger drag scrolls the strip of tuios's scrolling layout.
//
// It runs on the worker engine's canvas (?renderer=engine), whose touch layer
// already gives a tap, a vertical swipe and a long press their meanings. A
// drag that leaves the slop mostly sideways, before the long press would make
// it sip's drag, is taken here: the listeners run in the capture phase on the
// document, ahead of sip's on the canvas, and keep the rest of the gesture
// from reaching it. sip is sent a pointercancel as the drag is taken, so its
// own touch state ends there and its hold timer cannot turn the next tap into
// a right click.
//
// Every niri_scroll_cells cells the finger crosses is one sideways wheel
// report with Ctrl and Alt, and the lift is one with Ctrl, Alt and Shift.
// tuios moves the strip with the first and snaps it with the second, in the
// scrolling layout only; anywhere else it drops both, since the page cannot
// tell which layout is up. The strip follows the finger whatever
// niri_reverse_scroll says, which is about wheels.
(() => {
  const cfg = window.tuiosStripTouch || { cells: 8, reverse: false };
  const SLOP = 10, HOLD_MS = 450;
  let g = null;
  const onEngine = (ev) => ev.isTrusted && ev.pointerType === 'touch' && ev.target instanceof HTMLCanvasElement && ev.target.classList.contains('wt-canvas');
  // The cell the drag started on, 1-based, as an SGR mouse report counts.
  const cell = () => {
    const size = window.sip?.size();
    if (!size) return null;
    const r = g.canvas.getBoundingClientRect();
    const cw = r.width / size.cols, ch = r.height / size.rows;
    return { cw, col: Math.max(1, Math.floor((g.x - r.left) / cw) + 1), row: Math.max(1, Math.floor((g.y - r.top) / ch) + 1) };
  };
  document.addEventListener('pointerdown', (ev) => {
    if (!onEngine(ev)) return;
    g = { id: ev.pointerId, x: ev.clientX, y: ev.clientY, t: performance.now(), lastX: ev.clientX, acc: 0, mine: false, canvas: ev.target };
  }, true);
  document.addEventListener('pointermove', (ev) => {
    if (!ev.isTrusted || !g || ev.pointerId !== g.id) return;
    if (!g.mine) {
      const dx = ev.clientX - g.x, dy = ev.clientY - g.y;
      if (Math.hypot(dx, dy) <= SLOP) return;
      // A hold before the move is sip's drag, a mostly vertical move its
      // scroll. Either way the gesture is sip's from here on.
      if (performance.now() - g.t >= HOLD_MS || Math.abs(dx) <= Math.abs(dy)) { g = null; return; }
      g.mine = true;
      g.canvas.dispatchEvent(new PointerEvent('pointercancel', { pointerId: g.id, pointerType: 'touch', bubbles: true }));
    }
    ev.stopImmediatePropagation();
    const c = cell();
    if (!c) return;
    // The finger going left brings the columns to the right into view.
    g.acc += (g.lastX - ev.clientX) / c.cw;
    g.lastX = ev.clientX;
    const steps = Math.trunc(g.acc / cfg.cells);
    if (!steps) return;
    g.acc -= steps * cfg.cells;
    let right = steps > 0;
    if (cfg.reverse) right = !right;
    // SGR wheel left (66) or right (67), plus 8 for Alt and 16 for Ctrl.
    const button = (right ? 67 : 66) + 8 + 16;
    window.sip.send(`\x1b[<${button};${c.col};${c.row}M`.repeat(Math.abs(steps)));
  }, true);
  const end = (ev) => {
    if (!ev.isTrusted || !g || ev.pointerId !== g.id) return;
    if (g.mine) {
      ev.stopImmediatePropagation();
      const c = cell();
      // The lift: wheel left with Shift, Alt and Ctrl (66 + 4 + 8 + 16).
      if (c) window.sip.send(`\x1b[<94;${c.col};${c.row}M`);
    }
    g = null;
  };
  document.addEventListener('pointerup', end, true);
  document.addEventListener('pointercancel', end, true);
})();
