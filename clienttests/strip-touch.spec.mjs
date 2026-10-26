// A horizontal finger drag on the worker engine renderer scrolls the strip of
// the scrolling layout (cmd/tuios-web/strip_touch.js).
//
// The drag goes through Input.dispatchTouchEvent, one touch identity from the
// press to the lift, which is what the engine's touch layer reads: pointer
// events of type touch. The assertion is what the page handed to sip as
// input, window.sip.send, which is the boundary between the page and the
// session. What tuios does with the steps is TestStripSnapsToTheNearestColumn
// and the strip's wheel tests in the Go suite.
//
// How it could pass wrongly, written down first:
//   - The steps could come from sip's own vertical swipe. sip's swipe scrolls
//     through its worker, never through window.sip.send, so a step seen here
//     is the page script's.
//   - A drag of any direction could send steps. The vertical drag must send
//     none, and the two horizontal drags must send opposite buttons.

import { test, expect } from '@playwright/test';

async function boot(page) {
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

/** The wheel buttons the page sent since the last clear: 74 left, 75 right. */
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
  expect(left.length, 'a drag to the left sends steps').toBeGreaterThanOrEqual(2);
  expect(new Set(left), 'a drag to the left is the wheel to the right, with Alt').toEqual(new Set([75]));

  await drag(page, x0 - span, y, x0, y);
  const right = await wheels(page);
  expect(right.length, 'a drag to the right sends steps').toBeGreaterThanOrEqual(2);
  expect(new Set(right), 'a drag to the right is the wheel to the left, with Alt').toEqual(new Set([74]));

  await drag(page, x0, g.top + g.height / 4, x0, g.top + (3 * g.height) / 4);
  expect(await wheels(page), 'a vertical drag is sip\'s own swipe').toEqual([]);
});
