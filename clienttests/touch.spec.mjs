// What a phone can actually do to tuios-web, driven with real touch.
//
// Two mechanisms, for two reasons. Input.dispatchTouchEvent injects touch
// points straight into the renderer and skips the gesture recognizer, so it
// never produces the compatibility mouse events a finger does; it is here only
// for multi-step sequences with one touch identity. Input.synthesizeTapGesture
// and the scroll and pinch gestures beside it go through the recognizer, which
// is the path a finger takes, so every claim about what a tap or a fling does
// is made through those.
//
// Assertions go through the transport boundary or the terminal buffer. A
// button that lit up proves nothing; a byte on the wire does.

import { test, expect } from '@playwright/test';

const MSG_INPUT = 0x30;

/** Pin the transport through the same stored setting the settings panel writes. */
function pinTransport(page) {
  return page.addInitScript(() => {
    localStorage.setItem('sip-web-settings', JSON.stringify({
      transport: 'websocket', fontSize: 14, copyOnSelect: false, cursorBlink: false, renderer: 'canvas',
    }));
  });
}

async function boot(page) {
  await pinTransport(page);
  await page.goto('/');
  await page.waitForFunction(() => window.sipTerm?.connected, null, { timeout: 40_000 });
  await page.evaluate((msgInput) => {
    window.__sentInput = [];
    const ws = window.sipTerm.connection.ws;
    if (!ws) throw new Error('expected a WebSocket to hook');
    const send = ws.send.bind(ws);
    ws.send = (frame) => {
      const b = new Uint8Array(frame);
      if (b[0] === msgInput) window.__sentInput.push(Array.from(b.subarray(1)));
      return send(frame);
    };
  }, MSG_INPUT);
  // tuios has to have drawn before a chord means anything.
  await page.waitForTimeout(3000);
}

/** Everything sent since the last clear, each frame as a printable string. */
const wire = (page) => page.evaluate(() => window.__sentInput.map((b) =>
  b.map((c) => (c >= 32 && c < 127 ? String.fromCharCode(c) : '\\x' + c.toString(16).padStart(2, '0'))).join('')));

const clearWire = (page) => page.evaluate(() => { window.__sentInput.length = 0; });

/** The visible terminal, line by line. */
const screen = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '');
});

/** The centre of a bar button, by its label. */
async function buttonCentre(page, label) {
  const box = await page.locator('#sip-keybar button', { hasText: new RegExp(`^${label}$`) }).first().boundingBox();
  if (!box) throw new Error(`no bar button labelled ${label}`);
  return { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) };
}

/** A tap through the gesture recognizer, which is the path a finger takes. */
async function tap(cdp, x, y, duration = 50) {
  await cdp.send('Input.synthesizeTapGesture', { x, y, duration, gestureSourceType: 'touch' });
}

test.describe('the touch key bar', () => {
  test('installs both rows, chords over the keys a phone lacks', async ({ page }) => {
    await boot(page);
    await expect(page.locator('#sip-keybar')).toHaveCount(1);
    expect(await page.evaluate(() => document.body.classList.contains('sip-touch'))).toBe(true);

    const labels = await page.evaluate(() =>
      [...document.querySelectorAll('#sip-keybar button')].map((b) => b.textContent.trim()));

    // The chord row, in the order a thumb meets it.
    for (const label of ['pfx', 'new', 'close', 'tile', 'prev', 'next', 'zoom', 'vsplit', 'hsplit', 'cmds', 'config', 'help']) {
      expect(labels, `missing the ${label} button`).toContain(label);
    }
    // And sip's typing row underneath it.
    for (const label of ['esc', 'tab', 'ctrl', 'alt', '←', '↓', '↑', '→']) {
      expect(labels, `missing the ${label} key`).toContain(label);
    }
    expect(labels.indexOf('pfx')).toBeLessThan(labels.indexOf('esc'));

    // The bar's height is published so the container can pad itself with it,
    // which is what keeps the bottom of the terminal out from under the strip.
    const barH = await page.evaluate(() =>
      parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--sip-keybar-h')));
    expect(barH).toBeGreaterThan(20);
  });

  test('a chord button sends the leader and the key, and tuios acts on it', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    const before = (await screen(page)).join('\n');

    await clearWire(page);
    const { x, y } = await buttonCentre(page, 'new');
    await tap(cdp, x, y);
    await page.waitForTimeout(1500);

    // Ctrl+B is 0x02, and the window key is c. One tap, both bytes.
    expect((await wire(page)).join('')).toBe('\\x02c');
    expect((await screen(page)).join('\n'), 'a new window left the screen unchanged').not.toBe(before);
  });

  test('a typing key sends itself, with no leader in front', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);

    await clearWire(page);
    const { x, y } = await buttonCentre(page, 'esc');
    await tap(cdp, x, y);
    await page.waitForTimeout(400);
    expect((await wire(page)).join('')).toBe('\\x1b');
  });

  test('survives the software keyboard taking the bottom of the screen', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    const tall = await page.evaluate(() => window.sipTerm.term.rows);

    // What is left of a 844px phone once a keyboard is up.
    await page.setViewportSize({ width: 390, height: 420 });
    await page.waitForTimeout(1500);

    const short = await page.evaluate(() => window.sipTerm.term.rows);
    expect(short, 'the terminal did not give the keyboard its rows back').toBeLessThan(tall);

    // The bar has to still be on screen and clear of the terminal.
    const boxes = await page.evaluate(() => {
      const bar = document.querySelector('#sip-keybar').getBoundingClientRect();
      const scr = document.querySelector('.xterm-screen').getBoundingClientRect();
      return { barTop: bar.top, barBottom: bar.bottom, screenBottom: scr.bottom, innerH: window.innerHeight };
    });
    expect(boxes.barBottom).toBeLessThanOrEqual(boxes.innerH + 1);
    expect(boxes.screenBottom).toBeLessThanOrEqual(boxes.barTop + 1);

    await clearWire(page);
    const { x, y } = await buttonCentre(page, 'tile');
    await tap(cdp, x, y);
    await page.waitForTimeout(600);
    expect((await wire(page)).join('')).toBe('\\x02 ');
  });
});

// The terminal surface itself. These are written as what a phone should get
// and marked failing, because sip's client has no touch layer on the terminal:
// xterm.js cancels every touch over it, so no mouse event is ever synthesized,
// and the tap it recognizes internally is dispatched to an element that does
// not listen for it. Drop the test.fail() when sip grows the layer; these then
// become the regression tests for it.
test.describe('the terminal surface', () => {
  test('a tap puts a mouse report on the wire', async ({ page }) => {
    test.fail();
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    const box = await page.evaluate(() => {
      const b = document.querySelector('.xterm-screen').getBoundingClientRect();
      return { x: b.x, y: b.y, w: b.width, h: b.height };
    });

    await clearWire(page);
    await tap(cdp, Math.round(box.x + box.w / 2), Math.round(box.y + box.h / 2));
    await page.waitForTimeout(700);

    // A press and a release at the cell under the finger, SGR encoded.
    expect((await wire(page)).join('')).toMatch(/\\x1b\[<0;\d+;\d+M/);
  });

  test('a fling scrolls without typing into the pane', async ({ page }) => {
    test.fail();
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    const box = await page.evaluate(() => {
      const b = document.querySelector('.xterm-screen').getBoundingClientRect();
      return { x: b.x, y: b.y, w: b.width, h: b.height };
    });

    await clearWire(page);
    await cdp.send('Input.synthesizeScrollGesture', {
      x: Math.round(box.x + box.w / 2), y: Math.round(box.y + box.h / 2),
      xDistance: 0, yDistance: 400, speed: 6000, preventFling: false, gestureSourceType: 'touch',
    });
    await page.waitForTimeout(2500);

    // Inertia dispatches its gesture events with a translation and no
    // position, so the coordinates come out NaN and the terminal receives the
    // letters of the word instead of a scroll.
    const sent = await wire(page);
    expect(sent.filter((s) => s.includes('NaN')), 'mouse reports with NaN coordinates').toHaveLength(0);
    expect((await screen(page)).filter((l) => l.includes('NaN')), 'NaN typed into a pane').toHaveLength(0);
  });
});
