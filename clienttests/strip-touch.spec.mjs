// A horizontal finger drag on the worker engine renderer scrolls the strip of
// the scrolling layout (cmd/tuios-web/strip_touch.js).
//
// The drag goes through Input.dispatchTouchEvent, one touch identity from the
// press to the lift, which is what the engine's touch layer reads: pointer
// events of type touch. The assertions are what the page handed to sip as
// input, window.sip.send, which is the boundary between the page and the
// session, and what sip's touch layer handed its worker, which is where a
// click or a right click starts. What tuios does with the reports is
// strip_snap_test.go in the Go suite.
//
// How it could pass wrongly, written down first:
//   - The reports could come from sip's own vertical swipe. sip's swipe
//     scrolls through its worker, never through window.sip.send, so a report
//     seen here is the page script's.
//   - A drag of any direction could send reports. The vertical and the
//     diagonal drag must send none, and the two horizontal drags must send
//     opposite kinds.

import { test, expect } from '@playwright/test';

async function boot(page) {
  // What sip's touch layer posts to its worker: a click, a right click.
  await page.addInitScript(() => {
    window.__pointer = [];
    const post = Worker.prototype.postMessage;
    Worker.prototype.postMessage = function (m, ...rest) {
      if (m && m.type === 'pointer' && m.action === 0) window.__pointer.push(m.button);
      return post.call(this, m, ...rest);
    };
  });
  await page.goto('/?renderer=engine');
  await page.waitForFunction(() => window.sip?.size() && document.querySelector('canvas.wt-canvas'), null, { timeout: 40_000 });
  await page.evaluate(() => {
    window.__sent = [];
    const send = window.sip.send;
    window.sip.send = (d) => { window.__sent.push(d); return send(d); };
  });
}

/** A finger's clock for events: each call is one frame, 16 ms, after the one
 *  before, in seconds since the epoch as CDP takes them. Without a timestamp
 *  an event's time is when this script got to send it, and each send waits for
 *  the page to take the one before. A page slow to take the press then put
 *  the first move past the long press, and a quick drag read as a hold. The
 *  clock ends at the time it was made, so no event is dated in the future of
 *  one sent after the gesture without a timestamp. */
const fingerClock = (events) => {
  let at = Date.now() / 1000 - (events * 16) / 1000;
  return () => (at += 0.016);
};

/** One finger from (x0, y0) to (x1, y1) in steps, as a phone reports it. */
async function drag(page, x0, y0, x1, y1, steps = 12) {
  const cdp = await page.context().newCDPSession(page);
  const point = (x, y) => [{ x, y, id: 1, radiusX: 4, radiusY: 4, force: 1 }];
  const at = fingerClock(steps + 2);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: point(x0, y0), timestamp: at() });
  for (let i = 1; i <= steps; i++) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: point(x0 + ((x1 - x0) * i) / steps, y0 + ((y1 - y0) * i) / steps), timestamp: at() });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [], timestamp: at() });
  await cdp.detach();
}

/** The canvas box and one cell's size, in CSS pixels. */
const geometry = (page) => page.evaluate(() => {
  const r = document.querySelector('canvas.wt-canvas').getBoundingClientRect();
  const s = window.sip.size();
  return { left: r.left, top: r.top, width: r.width, height: r.height, cw: r.width / s.cols, ch: r.height / s.rows };
});

/** The finger reports the page sent since the last clear, CSI 7503 ; kind ;
 *  col ; row ; cells ~: kind 2 brings the columns to the right into view, 1
 *  those to the left, 3 is the lift. */
const reports = (page) => page.evaluate(() => {
  const out = [];
  for (const d of window.__sent) {
    for (const m of d.matchAll(/\x1b\[7503;(\d);\d+;\d+(?:;(\d+))?~/g)) out.push({ kind: Number(m[1]), cells: Number(m[2] || 0) });
  }
  window.__sent.length = 0;
  return out;
});

const cellsOf = (rs, kind) => rs.filter((r) => r.kind === kind).reduce((n, r) => n + r.cells, 0);

test('a horizontal drag scrolls the strip, a vertical or a diagonal one does not', async ({ page }) => {
  await boot(page);
  const g = await geometry(page);
  const y = g.top + g.height / 2;
  // Twenty cells of finger. The first ones go to the slop.
  const span = 20 * g.cw;
  const x0 = g.left + g.width / 2 + span / 2;

  await drag(page, x0, y, x0 - span, y);
  const left = await reports(page);
  expect(cellsOf(left, 2), 'a drag to the left brings the columns to the right, a cell for each cell').toBeGreaterThanOrEqual(15);
  expect(cellsOf(left, 2), 'no more cells than the finger crossed').toBeLessThanOrEqual(20);
  expect(left.at(-1).kind, 'the lift is the last report').toBe(3);
  expect(new Set(left.map((r) => r.kind)), 'a drag to the left is steps to the right and the lift').toEqual(new Set([2, 3]));

  await drag(page, x0 - span, y, x0, y);
  const right = await reports(page);
  expect(cellsOf(right, 1), 'a drag to the right brings the columns to the left').toBeGreaterThanOrEqual(15);
  expect(new Set(right.map((r) => r.kind)), 'a drag to the right is steps to the left and the lift').toEqual(new Set([1, 3]));

  await drag(page, x0, g.top + g.height / 4, x0, g.top + (3 * g.height) / 4);
  expect(await reports(page), 'a vertical drag is sip\'s own swipe').toEqual([]);

  // Sideways a little more than it is down: not clearly sideways, so sip's.
  await drag(page, x0, g.top + g.height / 4, x0 - 60, g.top + g.height / 4 + 40);
  expect(await reports(page), 'a diagonal drag is sip\'s own swipe').toEqual([]);
});

test('a second finger during the drag reaches neither sip nor the lift', async ({ page }) => {
  await boot(page);
  const g = await geometry(page);
  const y = g.top + g.height / 2;
  const x0 = g.left + g.width / 2 + 80;
  const cdp = await page.context().newCDPSession(page);
  const one = (x) => ({ x, y, id: 1, radiusX: 4, radiusY: 4, force: 1 });
  const two = { x: g.left + 40, y: g.top + 40, id: 2, radiusX: 4, radiusY: 4, force: 1 };
  const at = fingerClock(16);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [one(x0)], timestamp: at() });
  for (let i = 1; i <= 6; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [one(x0 - i * 10)], timestamp: at() });
  await page.evaluate(() => { window.__pointer.length = 0; });
  // A second finger lands and lifts while the first is dragging: a tap, if
  // sip saw it. CDP lifts one finger of two with a move that leaves it out.
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [one(x0 - 60), two], timestamp: at() });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [one(x0 - 60)], timestamp: at() });
  for (let i = 7; i <= 12; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [one(x0 - i * 10)], timestamp: at() });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [], timestamp: at() });
  await cdp.detach();
  await page.waitForTimeout(300);
  const rs = await reports(page);
  expect(rs.at(-1)?.kind, 'the first finger\'s lift is still the last report').toBe(3);
  expect(rs.filter((r) => r.kind === 3).length, 'one lift, the first finger\'s').toBe(1);
  expect(await page.evaluate(() => window.__pointer.slice()), 'the second finger did not reach sip').toEqual([]);
});

test('a quick swipe on a busy page is still a swipe', async ({ page }) => {
  await boot(page);
  const g = await geometry(page);
  const y = g.top + g.height / 2;
  const x0 = g.left + g.width / 2 + 80;
  const cdp = await page.context().newCDPSession(page);
  const point = (x) => [{ x, y, id: 1, radiusX: 4, radiusY: 4, force: 1 }];
  // Each event says when the finger did it, in seconds since the epoch. Without
  // that, an event's time is when this script got to send it, and each send
  // waits for the page to take the one before. A page slow to take the press
  // then put the first move past the long press, and the swipe read as a hold
  // because of the driver, not the finger.
  const t0 = Date.now() / 1000;
  const at = (ms) => t0 + ms / 1000;
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: point(x0), timestamp: at(0) });
  // The page is busy for longer than the long press right after the press,
  // as a phone drawing a frame can be. The finger moves at once all the same,
  // so the moves wait in the queue: their time is when the finger moved, not
  // when the page got to them, and the swipe must not read as a long press.
  const busy = page.evaluate(() => { const t = performance.now(); while (performance.now() - t < 700) { /* busy */ } });
  await page.waitForTimeout(50);
  for (let i = 1; i <= 6; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: point(x0 - i * 15), timestamp: at(50 + i * 16) });
  await busy;
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [], timestamp: at(200) });
  await cdp.detach();
  const rs = await reports(page);
  expect(cellsOf(rs, 2), 'the swipe sent steps').toBeGreaterThan(0);
  expect(rs.at(-1)?.kind, 'and its lift').toBe(3);
});

test('a hold, then a sideways move, is sip\'s long press', async ({ page }) => {
  await boot(page);
  const g = await geometry(page);
  const y = g.top + g.height / 2;
  const x0 = g.left + g.width / 2 + 80;
  const cdp = await page.context().newCDPSession(page);
  const point = (x) => [{ x, y, id: 1, radiusX: 4, radiusY: 4, force: 1 }];
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: point(x0) });
  await page.waitForTimeout(700);
  for (let i = 1; i <= 6; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: point(x0 - i * 15) });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
  expect(await reports(page), 'a hold is sip\'s, and the page sends nothing').toEqual([]);
});

test('a tap right after a quick sideways swipe is a click, not a right click', async ({ page }) => {
  await boot(page);
  const g = await geometry(page);
  const y = g.top + g.height / 2;
  const x0 = g.left + g.width / 2;
  // A quick swipe, well inside sip's long press, then at once a tap that is
  // still down when the swipe's long press would have fired. sip is sent a
  // pointercancel as the swipe is taken, so no hold timer is left to fire.
  await drag(page, x0 + 60, y, x0 - 60, y, 4);
  await page.evaluate(() => { window.__pointer.length = 0; });
  const cdp = await page.context().newCDPSession(page);
  const point = [{ x: x0, y, id: 2, radiusX: 4, radiusY: 4, force: 1 }];
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: point });
  await page.waitForTimeout(400);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
  await page.waitForTimeout(200);
  const presses = await page.evaluate(() => window.__pointer.slice());
  expect(presses, 'the tap is a press of button 1').toContain(1);
  expect(presses, 'the tap is not a right click').not.toContain(2);
});
