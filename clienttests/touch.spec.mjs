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

/** Where the terminal grid is on the page, and how big one cell of it is. */
const geom = (page) => page.evaluate(() => {
  const r = document.querySelector('.xterm-screen').getBoundingClientRect();
  const t = window.sipTerm.term;
  return { left: r.left, top: r.top, w: r.width / t.cols, h: r.height / t.rows };
});

/** The centre of terminal cell (col, row) in viewport pixels. */
const cell = (g, col, row) => ({
  x: Math.round(g.left + (col + 0.5) * g.w),
  y: Math.round(g.top + (row + 0.5) * g.h),
});

/** Tap a bar button and give tuios time to act on the chord. */
async function press(page, cdp, label, wait = 1400) {
  const { x, y } = await buttonCentre(page, label);
  await tap(cdp, x, y);
  await page.waitForTimeout(wait);
}

/** The splash screen, which is what an empty desktop draws. */
const onSplash = async (page) =>
  (await screen(page)).some((l) => l.includes('Terminal UI Operating System'));

/**
 * Whether the drawn pane is floating, read off its own title bar: only a
 * floating pane carries the maximize button, and a tiled one has nothing to
 * maximize into. Asked of the pane rather than of the dock's mode letter,
 * because the pane is what the gestures below are aimed at.
 */
async function paneIsFloating(page) {
  const frame = await paneFrame(page);
  return frame !== null && frame.top > 0 && (await screen(page))[frame.top].includes('□');
}

/**
 * Put the board where a test needs it: nothing open, floating layout, one pane.
 *
 * The daemon session outlives a page load and every test in this file shares
 * it, so a test that assumed an empty desktop was reading the panes and the
 * layout mode the test before it left behind. A floating pane is what these
 * three want: it is inset, so there is room around it to drag into.
 *
 * Toggling the layout does not re-place a pane that already exists, so the
 * second attempt makes a fresh one rather than trusting the toggle to move it.
 */
async function resetToOneFloatingPane(page, cdp) {
  const clear = async () => {
    for (let i = 0; i < 8 && !(await onSplash(page)); i++) {
      await press(page, cdp, 'close');
    }
  };
  await clear();
  await press(page, cdp, 'new', 2500);
  if (!(await paneIsFloating(page))) {
    await press(page, cdp, 'tile');
    await clear();
    await press(page, cdp, 'new', 2500);
  }
  expect(await paneIsFloating(page), 'could not get the board to one floating pane').toBe(true);
}

/**
 * The drawn frame of the topmost pane, in terminal cells.
 *
 * Read off the buffer rather than asked of tuios, because a pane that has moved
 * has only moved if it is drawn somewhere else. The corner glyphs are the
 * border style's, so they are read from the line rather than assumed.
 */
const paneFrame = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  const lines = Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '');
  const top = lines.findIndex((l) => /[╭┌╔┏]/.test(l));
  if (top === -1) return null;
  const bottom = lines.findLastIndex((l) => /[╰└╚┗]/.test(l));
  return { top, bottom, left: lines[top].search(/[╭┌╔┏]/) };
});

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

// The terminal surface itself. These were expected failures: xterm.js cancels
// every touch over it, so no mouse event was ever synthesized and the tap it
// recognizes internally was dispatched to an element that does not listen for
// it. sip v0.7.0's touch layer picks those events up, so these are the
// regression tests for it now.
test.describe('the terminal surface', () => {
  test('a tap puts a mouse report on the wire', async ({ page }) => {
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
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    const box = await page.evaluate(() => {
      const b = document.querySelector('.xterm-screen').getBoundingClientRect();
      return { x: b.x, y: b.y, w: b.width, h: b.height };
    });

    await clearWire(page);
    // Three of them, the way the corruption was measured: a fast swipe the page
    // lets go of, so xterm's Gesture runs its own inertia afterwards.
    for (let i = 0; i < 3; i++) {
      await cdp.send('Input.synthesizeScrollGesture', {
        x: Math.round(box.x + box.w / 2), y: Math.round(box.y + box.h / 2),
        xDistance: 0, yDistance: 400, speed: 6000, preventFling: false, gestureSourceType: 'touch',
      });
      await page.waitForTimeout(500);
    }
    await page.waitForTimeout(2000);

    // Inertia dispatches its gesture events with a translation and no position,
    // so the coordinates came out NaN and the terminal received the letters of
    // the word instead of a scroll.
    const sent = await wire(page);
    const reports = sent.join('').match(/\\x1b\[<\d+;[^;]*;[^Mm]*[Mm]/g) ?? [];
    // The fling has to have reported something, or a clean wire proves nothing.
    expect(reports.length, 'the fling put no mouse reports on the wire at all').toBeGreaterThan(0);
    expect(sent.filter((s) => s.includes('NaN')), 'mouse reports with NaN coordinates').toHaveLength(0);
    expect((await screen(page)).filter((l) => l.includes('NaN')), 'NaN typed into a pane').toHaveLength(0);
  });
});

// What the gestures reach once they are inside tuios. Everything above stops at
// the wire; these follow the same finger through to a pane moving, a menu
// opening, or a keystroke arriving where the tap said it should.
test.describe('a finger on tuios itself', () => {
  test('a tap on a pane focuses it, and typing lands there', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    await resetToOneFloatingPane(page, cdp);

    const frame = await paneFrame(page);
    expect(frame, 'no pane was drawn to tap on').not.toBeNull();

    const g = await geom(page);
    const inside = cell(g, frame.left + 4, frame.top + 6);
    await tap(cdp, inside.x, inside.y);
    await page.waitForTimeout(900);

    // Click-to-type: the tap hands the keyboard to the pane, so what the
    // software keyboard sends next is the pane's, not a window-management key.
    // "m" would minimize the pane if the tap had not landed. The marker is
    // short because a floating pane on this viewport is 24 columns and the
    // prompt has already spent ten of them.
    await page.keyboard.type('zqtap');
    await page.waitForTimeout(900);

    const lines = await screen(page);
    const hit = lines.findIndex((l) => l.includes('zqtap'));
    expect(hit, 'what was typed after the tap never reached a pane').toBeGreaterThan(-1);
    expect(hit, 'it landed outside the pane the tap was in').toBeGreaterThan(frame.top);
    expect(hit).toBeLessThan(frame.bottom);
  });

  test('a long press opens tuios own pane menu', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    await resetToOneFloatingPane(page, cdp);

    const frame = await paneFrame(page);
    const g = await geom(page);
    const inside = cell(g, frame.left + 4, frame.top + 6);

    // Into the pane first, so the press happens in the mode a user is in while
    // they are typing, which is where it used to reach nothing.
    await tap(cdp, inside.x, inside.y);
    await page.waitForTimeout(900);

    await tap(cdp, inside.x, inside.y, 900);
    await page.waitForTimeout(1200);

    const text = (await screen(page)).join('\n');
    expect(text, 'the long press opened no menu').toContain('Pane');
    expect(text, 'the menu that opened is not the pane menu').toContain('Close pane');
  });

  // Turning the phone is the one gesture that is not a touch at all, and it was
  // the one that did nothing. tuios kept drawing the portrait width: the daemon
  // recalculated the session size and said so, and the message sat in a channel
  // whose listener the previous resize had failed to re-arm. Height appeared to
  // follow only because the render size is the minimum of the two, and 16 rows
  // is less than the 42 it was stuck on while 105 columns is more than 48.
  test('turning the phone sideways gives tuios the columns', async ({ page }) => {
    await boot(page);
    // The widest line tuios drew is how wide it thinks it is. Asked of the
    // buffer, because a session that resized but did not redraw has not
    // resized as far as anyone holding the phone is concerned.
    const drawn = async () => Math.max(...(await screen(page)).map((l) => l.length));

    const portrait = await drawn();
    await page.setViewportSize({ width: 844, height: 390 });
    await page.waitForTimeout(2500);

    const cols = await page.evaluate(() => window.sipTerm.term.cols);
    expect(cols, 'the browser did not give the terminal a landscape width').toBeGreaterThan(portrait);
    expect(await drawn(), 'tuios kept drawing at the portrait width').toBe(cols);

    // And back, which is the direction that always worked and must keep doing so.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(2500);
    expect(await drawn()).toBe(portrait);
  });

  test('press, hold and drag moves a pane', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);
    await resetToOneFloatingPane(page, cdp);

    const before = await paneFrame(page);
    const g = await geom(page);
    // The title bar is the drag handle, and its left half is clear of the
    // buttons on the right.
    const grab = cell(g, before.left + 4, before.top);

    const touch = (type, x, y) => cdp.send('Input.dispatchTouchEvent', {
      type, touchPoints: type === 'touchEnd' ? [] : [{ x, y, id: 1 }],
    });
    await touch('touchStart', grab.x, grab.y);
    // Sit still past the hold. A move before it is a pan, which scrolls.
    await page.waitForTimeout(700);
    for (let i = 1; i <= 8; i++) await touch('touchMove', grab.x - i * 8, grab.y + i * 18);
    await touch('touchEnd', grab.x - 64, grab.y + 144);
    await page.waitForTimeout(1500);

    const after = await paneFrame(page);
    expect(after, 'the pane vanished during the drag').not.toBeNull();
    expect(after.top, 'the drag did not move the pane down').toBeGreaterThan(before.top);
    expect(after.left, 'the drag did not move the pane left').toBeLessThan(before.left);
  });
});
