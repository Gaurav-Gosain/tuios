// A horizontal finger drag on the worker engine renderer scrolls the strip of
// the scrolling layout (cmd/tuios-web/strip_touch.js).
//
// The drag goes through Input.dispatchTouchEvent, one touch identity from the
// press to the lift, which is what the engine's touch layer reads: pointer
// events of type touch. The assertions are what the page handed to sip as
// input, window.sip.send, which is the boundary between the page and the
// session, and what sip's touch layer handed its worker, which is where a
// click or a right click starts. What tuios does with the steps is
// TestStripTouchSnapsWhenTheFingerLifts and the strip tests in the Go suite.
//
// How it could pass wrongly, written down first:
//   - The steps could come from sip's own vertical swipe. sip's swipe scrolls
//     through its worker, never through window.sip.send, so a step seen here
//     is the page script's.
//   - A drag of any direction could send steps. The vertical drag must send
//     none, and the two horizontal drags must send opposite buttons.

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

/** One finger from (x0, y0) to (x1, y1) in steps, as a phone reports it. */
async function drag(page, x0, y0, x1, y1, steps = 12) {
  const cdp = await page.context().newCDPSession(page);
  const point = (x, y) => [{ x, y, id: 1, radiusX: 4, radiusY: 4, force: 1 }];
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: point(x0, y0) });
  for (let i = 1; i <= steps; i++) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: point(x0 + ((x1 - x0) * i) / steps, y0 + ((y1 - y0) * i) / steps) });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
}

/** The canvas box and one cell's size, in CSS pixels. */
const geometry = (page) => page.evaluate(() => {
  const r = document.querySelector('canvas.wt-canvas').getBoundingClientRect();
  const s = window.sip.size();
  return { left: r.left, top: r.top, width: r.width, height: r.height, cw: r.width / s.cols, ch: r.height / s.rows };
});

/** The wheel buttons the page sent since the last clear: 90 left and 91 right
 *  (Ctrl and Alt), 94 the lift (Ctrl, Alt and Shift). */
const wheels = (page) => page.evaluate(() => {
  const out = [];
  for (const d of window.__sent) for (const m of d.matchAll(/\x1b\[<(\d+);\d+;\d+M/g)) out.push(Number(m[1]));
  window.__sent.length = 0;
  return out;
});

test('a horizontal drag scrolls the strip, a vertical one does not', async ({ page }) => {
  await boot(page);
  const g = await geometry(page);
  const y = g.top + g.height / 2;
  // niri_scroll_cells is 8 by default: 2.5 steps' worth of finger is 2 steps.
  const span = 8 * 2.5 * g.cw;
  const x0 = g.left + g.width / 2 + span / 2;

  await drag(page, x0, y, x0 - span, y);
  const left = await wheels(page);
  expect(left.filter((b) => b === 91).length, 'a drag to the left sends steps to the right').toBeGreaterThanOrEqual(2);
  expect(left.at(-1), 'the lift is the last report').toBe(94);
  expect(new Set(left), 'a drag to the left is steps to the right and the lift').toEqual(new Set([91, 94]));

  await drag(page, x0 - span, y, x0, y);
  const right = await wheels(page);
  expect(right.filter((b) => b === 90).length, 'a drag to the right sends steps to the left').toBeGreaterThanOrEqual(2);
  expect(new Set(right), 'a drag to the right is steps to the left and the lift').toEqual(new Set([90, 94]));

  await drag(page, x0, g.top + g.height / 4, x0, g.top + (3 * g.height) / 4);
  expect(await wheels(page), 'a vertical drag is sip\'s own swipe').toEqual([]);
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
