// The user's tuios theme, read back out of a real browser.
//
// Two servers. The one on APPEARANCE_BASE_URL is served with a config file
// that names a theme, and is what proves the theme travels. The one on
// CONFIG_BASE_URL names no theme and is the control: tuios does not know what
// an unthemed user's sixteen colours are, so it must send none and the page
// must keep sip's own.
//
// A colour is proved by reading the pixel the renderer painted or the style
// the browser computed, never by asserting that a field arrived. A field can
// arrive and still be dropped by xterm, by a stylesheet, or by a merge that
// puts the default back on top, and each of those looks like a pass from the
// wire's side.

import { test, expect } from '@playwright/test';
import { APPEARANCE_BASE_URL, CONFIG_BASE_URL } from './playwright.config.mjs';

// What the seeded theme is, as literals, so a failure names the colour that
// was expected rather than pointing at a Go file. tokyo_night sets no cursor
// colour and no selection colour, which is the case under test.
const TOKYO_NIGHT = {
  background: '#16161e',
  foreground: '#787c99',
  red: '#f7768e',
  blue: '#79a2f7',
};

// sip's own palette, which is what the page shows when nothing is sent.
const SIP_DEFAULT = {
  background: '#1e1e2e',
  red: '#f38ba8',
  cursor: '#f5e0dc',
};

/** {r,g,b} from '#rrggbb'. */
const rgb = (hex) => ({
  r: parseInt(hex.slice(1, 3), 16),
  g: parseInt(hex.slice(3, 5), 16),
  b: parseInt(hex.slice(5, 7), 16),
});

async function boot(page, url) {
  await page.goto(url);
  await page.waitForFunction(() => window.sipTerm?.connected, null, { timeout: 40_000 });
  // A session that has painted its first real frame. The config opens a window
  // at startup, so a border on screen is the frame arriving rather than a
  // fixed number of seconds.
  await expect
    .poll(async () => page.evaluate(() => {
      const t = window.sipTerm.term;
      const b = t.buffer.active;
      return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '')
        .some((l) => /[╔╭─│]/.test(l));
    }), { timeout: 30_000 })
    .toBe(true);
}

/** '#rrggbb' from a 'rgb(r, g, b)' computed value. */
function toHex(css) {
  const m = css.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
  if (!m) return css;
  return '#' + [1, 2, 3].map((i) => Number(m[i]).toString(16).padStart(2, '0')).join('');
}

/**
 * Paint through the terminal and read one cell's pixel back off the composited
 * canvases.
 *
 * The write is local to xterm, which is the point: it exercises the palette
 * the browser holds rather than anything tuios chose server-side. tuios
 * resolves its own indexed colours before they leave, so a cell it painted
 * carries 24-bit colour and says nothing about the browser's sixteen.
 *
 * Read from the middle of the cell, because a full block fills it. The caller
 * polls this: tuios owns the screen and repaints whenever it likes, and a
 * repaint between the write and the read leaves a cell of its own.
 */
async function cellPixel(page, { text = '', select = null, col, row, dx = 0.5, dy = 0.5 }) {
  return page.evaluate(async ({ text, select, col, row, dx, dy }) => {
    const t = window.sipTerm.term;
    t.clearSelection();
    t.write('\x1b[H\x1b[2J');
    await new Promise((r) => t.write(text, r));
    if (select) {
      // Focused, because an unfocused terminal paints the other selection
      // colour and the test would be reading a different field.
      t.focus();
      t.select(select.col, select.row, select.length);
    }
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));

    const canvases = [...document.querySelectorAll('#terminal canvas')];
    const merged = document.createElement('canvas');
    merged.width = canvases[0].width;
    merged.height = canvases[0].height;
    const ctx = merged.getContext('2d');
    for (const c of canvases) ctx.drawImage(c, 0, 0);

    const dpr = window.devicePixelRatio || 1;
    const cell = t._core._renderService.dimensions.css.cell;
    const x = Math.round(cell.width * dpr * (col + dx));
    const y = Math.round(cell.height * dpr * (row + dy));
    const d = ctx.getImageData(x, y, 1, 1).data;
    return '#' + [d[0], d[1], d[2]].map((v) => v.toString(16).padStart(2, '0')).join('');
  }, { text, select, col, row, dx, dy });
}

/** The ground the page paints behind and around the grid. */
function pageGround(page) {
  return page.evaluate(() => ({
    container: getComputedStyle(document.getElementById('terminal-container')).backgroundColor,
    grid: getComputedStyle(document.querySelector('.webterm') ?? document.getElementById('terminal')).backgroundColor,
  }));
}

test.describe('the theme in the config file reaches the browser', () => {
  test('the ANSI palette paints the cells', async ({ page }) => {
    await boot(page, `${APPEARANCE_BASE_URL}/?renderer=canvas`);

    await expect
      .poll(() => cellPixel(page, { text: '\x1b[31m██\x1b[0m', col: 0, row: 0 }),
        { timeout: 20_000, message: `ANSI red is not the theme's ${TOKYO_NIGHT.red}` })
      .toBe(TOKYO_NIGHT.red);

    await expect
      .poll(() => cellPixel(page, { text: '\x1b[34m██\x1b[0m', col: 0, row: 0 }),
        { timeout: 20_000, message: `ANSI blue is not the theme's ${TOKYO_NIGHT.blue}` })
      .toBe(TOKYO_NIGHT.blue);
  });

  test('the theme background paints the empty grid and the page behind it', async ({ page }) => {
    await boot(page, `${APPEARANCE_BASE_URL}/?renderer=canvas`);

    await expect
      .poll(() => cellPixel(page, { col: 40, row: 4 }),
        { timeout: 20_000, message: `the cleared grid is not the theme's ${TOKYO_NIGHT.background}` })
      .toBe(TOKYO_NIGHT.background);

    const ground = await pageGround(page);
    expect(toHex(ground.container), 'the page around the grid kept a colour the user did not pick')
      .toBe(TOKYO_NIGHT.background);
    expect(toHex(ground.grid), 'the strip under the grid kept a colour the user did not pick')
      .toBe(TOKYO_NIGHT.background);
  });

  test('a colour the theme does not set keeps sip\'s own, and never goes black', async ({ page }) => {
    await boot(page, `${APPEARANCE_BASE_URL}/?renderer=canvas`);

    // tokyo_night names no selection colour, and what must not appear here is
    // black: that is what a nil theme colour becomes the moment it is read as
    // a zero colour rather than as "unset".
    //
    // The claim is made on the direction rather than on an exact colour, since
    // xterm paints a selection at 30% over the cell and that 30% is xterm's
    // number and not sip's. A highlight is lighter than its ground. A black
    // selection is darker, which is the whole difference this proves.
    const selection = { select: { col: 10, row: 0, length: 6 }, col: 12, row: 0 };
    const ground = rgb(TOKYO_NIGHT.background);
    await expect
      .poll(async () => {
        const p = rgb(await cellPixel(page, selection));
        return p.r > ground.r && p.g > ground.g && p.b > ground.b;
      }, { timeout: 20_000, message: 'the selection never painted lighter than the ground' })
      .toBe(true);

    // Said again as the value the rule would have produced, so a failure names
    // it. #000000 at 30% over this ground is #0f0f15: a selection that makes
    // the cell darker instead of lighter.
    const pixel = await cellPixel(page, selection);
    expect(pixel, 'a theme that names no selection colour painted a black selection block')
      .not.toBe('#0f0f15');

    // The cursor is the other colour tokyo_night leaves out, and sip's own is
    // what must still be standing.
    const cursor = await page.evaluate(() => window.sipTerm.term.options.theme.cursor);
    expect(cursor, 'a theme that names no cursor colour lost sip\'s cursor').toBe(SIP_DEFAULT.cursor);
  });

  test('the tab carries the name of the program it is running', async ({ page }) => {
    await boot(page, APPEARANCE_BASE_URL);
    expect(await page.title()).toBe('tuios');
  });
});

test.describe('a server with no theme sends no colour', () => {
  test('the page keeps sip\'s palette', async ({ page }) => {
    // The control. This server's config names no theme, so tuios leaves
    // indexed colours indexed for the far end to resolve and sends none of its
    // own. What it must not send is the xterm defaults those indices carry
    // in-process: red would arrive as #800000.
    await boot(page, `${CONFIG_BASE_URL}/?renderer=canvas`);

    const ground = await pageGround(page);
    expect(toHex(ground.container), 'an unthemed server changed the page ground')
      .toBe(SIP_DEFAULT.background);

    await expect
      .poll(() => cellPixel(page, { text: '\x1b[31m██\x1b[0m', col: 0, row: 0 }),
        { timeout: 20_000, message: 'an unthemed server changed the browser palette' })
      .toBe(SIP_DEFAULT.red);
  });

  test('the tab is still named, because a name is not a colour', async ({ page }) => {
    await boot(page, CONFIG_BASE_URL);
    expect(await page.title()).toBe('tuios');
  });
});
