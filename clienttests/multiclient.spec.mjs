// What a second browser window of a different size does to the first one.
//
// A session is drawn at the smallest viewport looking at it, so opening it in a
// narrower window takes columns away from the window already there. The one
// that has to change is the one where nothing happened, and the only thing that
// can tell it so is the daemon's session-resize broadcast.
//
// The claim is made against the terminal buffer: how many columns the session
// actually draws into, which is what a stale layout gets wrong. The pane
// divider ends up somewhere the pane's contents were never laid out for, and
// the shell's own lines run on past it.

import { test, expect } from '@playwright/test';

const WIDE = { width: 1400, height: 900 };
const NARROW = { width: 640, height: 560 };

/** The visible terminal, line by line. */
const screen = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '');
});

/** The grid the browser sized for this viewport, which never changes here. */
const cols = (page) => page.evaluate(() => window.sipTerm.term.cols);

/** How many columns the session draws into: the longest line with anything on it. */
const drawnWidth = async (page) => (await screen(page))
  .reduce((widest, line) => Math.max(widest, line.replace(/\s+$/, '').length), 0);

async function open(browser, viewport) {
  const context = await browser.newContext({ viewport, deviceScaleFactor: 1 });
  const page = await context.newPage();
  await page.addInitScript(() => {
    localStorage.setItem('sip-web-settings', JSON.stringify({
      transport: 'websocket', fontSize: 14, copyOnSelect: false, cursorBlink: false, renderer: 'canvas',
    }));
  });
  await page.goto('/');
  await page.waitForFunction(() => window.sipTerm?.connected, null, { timeout: 40_000 });
  // A border is the first real frame, rather than a fixed number of seconds.
  await expect
    .poll(async () => (await screen(page)).some((l) => /[╭─]/.test(l)), { timeout: 30_000 })
    .toBe(true);
  return { context, page };
}

test.describe('a session drawn into two browser windows at once', () => {
  test('the window already open gives up the columns the narrow one does not have', async ({ browser }) => {
    const wide = await open(browser, WIDE);
    try {
      const wideCols = await cols(wide.page);
      await expect
        .poll(() => drawnWidth(wide.page), { timeout: 30_000 })
        .toBeGreaterThan(wideCols - 4);

      const narrow = await open(browser, NARROW);
      try {
        const narrowCols = await cols(narrow.page);
        expect(narrowCols, 'the two viewports came out the same width').toBeLessThan(wideCols - 10);

        // The wide window keeps its own grid; what it draws into shrinks to the
        // narrow one's columns. Left alone it goes on drawing at its old width,
        // and its panes carry contents laid out for a divider that has moved.
        await expect
          .poll(() => drawnWidth(wide.page), { timeout: 30_000 })
          .toBeLessThanOrEqual(narrowCols);
        expect(await cols(wide.page), 'the wide window resized its own grid').toBe(wideCols);
      } finally {
        await narrow.context.close();
      }

      // And back again once the narrow window is gone.
      await expect
        .poll(() => drawnWidth(wide.page), { timeout: 30_000 })
        .toBeGreaterThan(wideCols - 4);
    } finally {
      await wide.context.close();
    }
  });
});
