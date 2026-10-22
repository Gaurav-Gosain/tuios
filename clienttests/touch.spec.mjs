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
  // tuios has to have drawn, and stopped redrawing, before a chord means
  // anything.
  await waitFor(page, 'tuios to draw', async () => (await screen(page)).some((l) => l.trim() !== ''), 40_000);
  await settle(page);
}

/** Everything sent since the last clear, each frame as a printable string. */
const wire = (page) => page.evaluate(() => window.__sentInput.map((b) =>
  b.map((c) => (c >= 32 && c < 127 ? String.fromCharCode(c) : '\\x' + c.toString(16).padStart(2, '0'))).join('')));

const clearWire = (page) => page.evaluate(() => { window.__sentInput.length = 0; });

/**
 * The keys sent since the last clear, as one string, without the replies the
 * terminal sends on its own. tuios asks for the keyboard protocol flags when
 * the size changes, and xterm.js answers with CSI ? flags u on the same wire.
 * That answer is not a key anyone pressed.
 */
const keysSent = async (page) => (await wire(page)).join('').replace(/\\x1b\[\?\d+u/g, '');

/** The visible terminal, line by line. */
const screen = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '');
});

/**
 * The centre of a bar button, by its label.
 *
 * The strip is wider than the phone and clips what it cannot show, so a button
 * behind a pan has a box that is off the visible strip and a tap at its centre
 * silently reaches nothing. Refuse that here: a test that means to pan should
 * say so, and a reordering that pushes a button off the screen should fail
 * loudly rather than assert against an empty wire.
 */
async function buttonCentre(page, label) {
  const btn = page.locator('#sip-keybar button', { hasText: new RegExp(`^${label}$`) }).first();
  const box = await btn.boundingBox();
  if (!box) throw new Error(`no bar button labelled ${label}`);
  const centre = { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) };
  const strip = await page.evaluate(() => {
    const s = document.querySelector('#sip-keybar .sip-keybar-scroll').getBoundingClientRect();
    return { left: s.left, right: s.right };
  });
  if (centre.x < strip.left || centre.x > strip.right) {
    throw new Error(`the ${label} button is behind a pan (centre ${centre.x}, strip ${strip.left}-${strip.right})`);
  }
  return centre;
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

/** The visible terminal as one string, to tell whether it changed. */
const snapshot = async (page) => (await screen(page)).join('\n');

/**
 * Wait until tuios stops drawing: the buffer reads the same twice, quiet
 * apart. A reading taken while tuios redraws can show a state that is gone a
 * frame later. On a slow runner that is what the fixed waits read: a board
 * that had not caught up with the last chord, and once a splash drawn with a
 * pane still open, so the reset left two panes and the gestures hit the
 * wrong one.
 */
async function settle(page, quiet = 400, timeout = 20_000) {
  const deadline = Date.now() + timeout;
  let last = await snapshot(page);
  for (;;) {
    await page.waitForTimeout(quiet);
    const now = await snapshot(page);
    if (now === last) return;
    if (Date.now() > deadline) throw new Error(`tuios never stopped drawing:\n${now}`);
    last = now;
  }
}

/** Poll cond until it holds or the time is up, and say which. */
async function waitFor(page, what, cond, timeout = 20_000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    if (await cond()) return true;
    if (Date.now() > deadline) return false;
    await page.waitForTimeout(100);
  }
}

/** The splash screen, which is what an empty desktop draws. */
const onSplash = async (page) =>
  (await screen(page)).some((l) => l.includes('Terminal UI Operating System'));

/**
 * Whether the one drawn pane is floating, read off where its frame sits. A
 * tiled pane fills the area from the left edge of the screen to the right
 * edge. A floating pane is inset from both. Asked of the pane rather than of
 * a title-bar glyph or the dock's mode letter: the window controls change
 * with the config, and the pane is what the gestures below are aimed at.
 */
async function paneIsFloating(page) {
  const frame = await paneFrame(page);
  if (frame === null || frame.top <= 0) return false;
  const cols = await page.evaluate(() => window.sipTerm.term.cols);
  return frame.left > 0 && frame.right < cols - 1;
}

/**
 * Toggle the layout between tiled and floating. Sent from the keyboard,
 * because the bar's tile button sits behind a pan on a phone. The tiling
 * toggle is leader Space. Leader t opens the window menu.
 */
async function toggleTiling(page) {
  await page.keyboard.press('Control+b');
  await page.keyboard.press(' ');
  await settle(page);
}

/**
 * Two taps on the same cell, close together. click_to_type ships as double,
 * so one tap on a pane focuses it and the second tap enters terminal mode.
 *
 * Sent as raw touch events, which xterm's own gesture recognizer turns into
 * taps. Input.synthesizeTapGesture puts about two seconds between two taps,
 * even with tapCount 2, and tuios counts a second click only within 300 ms.
 */
async function doubleTap(page, cdp, x, y) {
  for (let i = 0; i < 2; i++) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y, id: 1 }] });
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  }
}

/**
 * Close every pane, and wait until the board shows the splash and no pane.
 * Each close waits for a pane to go rather than for a fixed time, and the
 * board is read only once tuios stops drawing.
 */
async function clearBoard(page, cdp) {
  for (let i = 0; i < 10; i++) {
    await settle(page);
    const open = await paneCount(page);
    if (open === 0) return;
    const { x, y } = await buttonCentre(page, 'close');
    await tap(cdp, x, y);
    await waitFor(page, 'a pane to close', async () => (await paneCount(page)) < open, 5_000);
  }
  throw new Error(`could not close every pane:\n${await snapshot(page)}`);
}

/**
 * Open one pane, and wait until it is drawn and tuios stops drawing. It
 * returns how many panes are drawn then, which a caller checks: see
 * resetToOneFloatingPane.
 */
async function newPane(page, cdp) {
  const open = await paneCount(page);
  const { x, y } = await buttonCentre(page, 'new');
  await tap(cdp, x, y);
  const drawn = await waitFor(page, 'the new pane', async () => (await paneCount(page)) > open);
  await settle(page);
  if (!drawn) throw new Error(`the new pane was never drawn:\n${await snapshot(page)}`);
  return paneCount(page);
}

/**
 * Load the page again after the board showed the splash while a pane was
 * still open. About one run in 20 on this machine, the client drew the splash
 * while the daemon still held a pane on the same workspace, and the pane came
 * back with the next new one. Closing panes did not help, because the client
 * did not know the pane was there. A new page attaches again and reads every
 * pane from the daemon. The log keeps the screen, because the desync itself
 * looks like a product bug.
 */
async function rebuildAfterDesync(page, drawn) {
  console.log(`the board showed the splash with a pane still open; ${drawn} panes after one new:\n${await snapshot(page)}`);
  await boot(page);
}

/**
 * Put the board where a test needs it: nothing open, floating layout, one pane.
 *
 * The daemon session outlives a page load and every test in this file shares
 * it, so a test that assumed an empty desktop was reading the panes and the
 * layout mode the test before it left behind. A floating pane is what these
 * three want: it is inset, so there is room around it to drag into.
 *
 * Toggling the layout does not re-place a pane that already exists, so a
 * board that came up tiled is cleared and made again after the toggle.
 */
async function resetToOneFloatingPane(page, cdp) {
  for (let attempt = 0; attempt < 6; attempt++) {
    await clearBoard(page, cdp);
    const drawn = await newPane(page, cdp);
    if (drawn !== 1) {
      await rebuildAfterDesync(page, drawn);
      continue;
    }
    if (await paneIsFloating(page)) return;
    await toggleTiling(page);
  }
  throw new Error(`could not get the board to one floating pane:\n${await snapshot(page)}`);
}

/**
 * How many panes are drawn. The splash is what tuios draws when no pane is
 * open, and its block letters hold corner glyphs, so it counts as none.
 */
const paneCount = async (page) => ((await onSplash(page)) ? 0 : (await paneCorners(page)).length);

/**
 * Every pane corner on screen, in cells. A pane's top edge is its title bar,
 * with the window buttons on it. The drop preview a tiled drag draws is a
 * frame in the same glyphs with a bare top edge, so it is not a pane. See
 * paneFrame.
 */
const paneCorners = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  const titled = (line, col) => {
    const end = line.slice(col + 1).search(/[╮┐╗┓]/);
    const edge = end === -1 ? line.slice(col + 1) : line.slice(col + 1, col + 1 + end);
    return /[^─━═\s]/.test(edge);
  };
  const out = [];
  for (let row = 0; row < t.rows; row++) {
    const line = b.getLine(b.viewportY + row)?.translateToString(true) ?? '';
    for (let col = 0; col < line.length; col++) {
      if (/[╭┌╔┏]/.test(line[col]) && titled(line, col)) out.push({ col, row });
    }
  }
  return out;
});

/**
 * The drawn frame of the topmost pane, in terminal cells.
 *
 * Read off the buffer rather than asked of tuios, because a pane that has moved
 * has only moved if it is drawn somewhere else. The corner glyphs are the
 * border style's, so they are read from the line rather than assumed.
 *
 * A pane is a frame whose top edge is a title bar: it carries the window
 * buttons or a title. The preview a tiled drag draws where the pane would land
 * is a frame in the same border glyphs, with a bare top edge, so it is not
 * read as a pane.
 */
const paneFrame = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  const lines = Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '');
  const titled = (line, col) => {
    const end = line.slice(col + 1).search(/[╮┐╗┓]/);
    const edge = end === -1 ? line.slice(col + 1) : line.slice(col + 1, col + 1 + end);
    return /[^─━═\s]/.test(edge);
  };
  const top = lines.findIndex((l) => {
    const col = l.search(/[╭┌╔┏]/);
    return col !== -1 && titled(l, col);
  });
  if (top === -1) return null;
  const bottom = lines.findLastIndex((l) => /[╰└╚┗]/.test(l));
  const left = lines[top].search(/[╭┌╔┏]/);
  const right = left + lines[top].slice(left).search(/[╮┐╗┓]/);
  return { top, bottom, left, right: right < left ? -1 : right };
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
    expect(await keysSent(page)).toBe('\\x02c');
    expect((await screen(page)).join('\n'), 'a new window left the screen unchanged').not.toBe(before);
  });

  test('a typing key sends itself, with no leader in front', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);

    await clearWire(page);
    const { x, y } = await buttonCentre(page, 'esc');
    await tap(cdp, x, y);
    await page.waitForTimeout(400);
    expect(await keysSent(page)).toBe('\\x1b');
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
    // A chord button that is on screen without panning, since what is being
    // proved here is that the bar still sends with the keyboard up, not which
    // buttons the strip happens to show.
    const { x, y } = await buttonCentre(page, 'zoom');
    await tap(cdp, x, y);
    await page.waitForTimeout(600);
    expect(await keysSent(page)).toBe('\\x02z');
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
    await clearWire(page);
    const t0 = Date.now();
    await doubleTap(page, cdp, inside.x, inside.y);
    const tapMs = Date.now() - t0;
    await page.waitForTimeout(900);
    const afterTap = await wire(page);

    // Click-to-type: the double tap hands the keyboard to the pane, so what the
    // software keyboard sends next is the pane's, not a window-management key.
    // "m" would minimize the pane if the tap had not landed. The marker is
    // short because a floating pane on this viewport is about 20 columns wide.
    const beforeType = await screen(page);
    await clearWire(page);
    await page.keyboard.type('zqtap');
    await page.waitForTimeout(900);
    const typed = await wire(page);

    const lines = await screen(page);
    const hit = lines.findIndex((l) => l.includes('zqtap'));
    expect(hit, `what was typed after the tap never reached a pane. The double tap took ${tapMs} ms `
      + `and sent ${JSON.stringify(afterTap)}. Typing sent ${JSON.stringify(typed)}. Before typing:\n`
      + `${beforeType.join('\n')}\nAfter:\n${lines.join('\n')}`).toBeGreaterThan(-1);
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
    await doubleTap(page, cdp, inside.x, inside.y);
    await page.waitForTimeout(900);

    await tap(cdp, inside.x, inside.y, 900);
    await waitFor(page, 'the pane menu', async () => (await snapshot(page)).includes('Split right'), 5_000);

    const text = (await screen(page)).join('\n');
    // Asked of the items, because the menu's heading is the pane title, and
    // that is whatever the shell sets.
    expect(text, 'the long press opened no pane menu').toContain('Split right');
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
    await waitFor(page, 'the pane to move', async () => {
      const f = await paneFrame(page);
      return f !== null && f.top > before.top && f.left < before.left;
    }, 5_000);

    const after = await paneFrame(page);
    expect(after, 'the pane vanished during the drag').not.toBeNull();
    expect(after.top, 'the drag did not move the pane down').toBeGreaterThan(before.top);
    expect(after.left, 'the drag did not move the pane left').toBeLessThan(before.left);
  });

  // The same finger on a tiled pane, which is what a new install has: tiling
  // ships on. The pane has to be drawn following the finger while it is down,
  // and the drop has to leave the tiles where the layout puts them, swapped or
  // not. Read mid-drag rather than after, because a tiled pane always ends the
  // gesture in a slot: an end-state check cannot tell a drag from a tap.
  test('press, hold and drag moves a tiled pane', async ({ page }) => {
    await boot(page);
    const cdp = await page.context().newCDPSession(page);

    // Every pane corner on screen, in cells. A pane's top edge is its title
    // bar, with the window buttons on it. The drop preview draws a frame in
    // the same glyphs where the pane would land, over its own slot when the
    // drop would change nothing, and its top edge is bare: that frame is not
    // the pane. See paneFrame.
    const corners = () => paneCorners(page);
    const key = (c) => `${c.col},${c.row}`;
    const sorted = (cs) => cs.map(key).sort();
    // Two panes partition the box from its origin, so both corners sit on the
    // top row or the left column. Floating panes are inset and do neither.
    const tiled = (cs) => cs.length === 2 && cs.every((c) => c.col === 0 || c.row === 0);

    // Nothing open, then two panes. Tiling ships on, so they come up tiled
    // unless an earlier test left the session floating; the chord is sent
    // from the keyboard because the bar's tile button can sit behind a pan.
    // Each step waits for the board to show it, not for a fixed time: on a
    // slow runner the second pane was still being laid out when the corners
    // were read.
    // A board that came up with the wrong number of panes is made again,
    // and so is one that came up floating, after the toggle.
    for (let attempt = 0; attempt < 6; attempt++) {
      await clearBoard(page, cdp);
      let drawn = await newPane(page, cdp);
      if (drawn === 1) drawn = await newPane(page, cdp);
      if (drawn !== 2 && drawn !== 1) {
        await rebuildAfterDesync(page, drawn);
        continue;
      }
      if (drawn !== 2) continue;
      if (tiled(await corners())) break;
      await toggleTiling(page);
    }

    const before = await corners();
    expect(tiled(before), 'two tiled panes should draw two corners on the box edge').toBe(true);
    // On a phone the two tiles stack, so grab the top pane's title row: its top
    // edge is the screen's, not a shared divider a touch would resize instead.
    const target = before.reduce((a, b) => (b.row + b.col < a.row + a.col ? b : a));
    const other = before.find((c) => key(c) !== key(target));
    const cols = await page.evaluate(() => window.sipTerm.term.cols);
    const rows = await page.evaluate(() => window.sipTerm.term.rows);
    const g = await geom(page);
    // The middle of the title row, clear of the window buttons at the left end.
    const grab = cell(g, Math.floor(cols / 2), target.row);
    // Down onto the other tile, so the drop swaps the two.
    const dropRow = other.row + 4;

    const touch = (type, x, y) => cdp.send('Input.dispatchTouchEvent', {
      type, touchPoints: type === 'touchEnd' ? [] : [{ x, y, id: 1 }],
    });
    await touch('touchStart', grab.x, grab.y);
    await page.waitForTimeout(700);
    const stepRows = Math.max(1, dropRow - target.row);
    for (let i = 1; i <= stepRows; i++) await touch('touchMove', grab.x, grab.y + Math.round(i * g.h));
    await waitFor(page, 'the pane to follow the finger', async () => {
      const cs = await corners();
      return !cs.some((c) => key(c) === key(target)) && cs.some((c) => c.row > target.row + 1);
    }, 5_000);

    const mid = await corners();
    expect(mid.some((c) => key(c) === key(target)), 'the grabbed pane is still drawn in its slot mid-drag').toBe(false);
    expect(mid.some((c) => c.row > target.row + 1),
      'no pane corner is drawn lower than the slot mid-drag: the finger is not moving the pane').toBe(true);

    await touch('touchEnd', grab.x, grab.y + Math.round(stepRows * g.h));
    await waitFor(page, 'the drop to settle into two tiles', async () => tiled(await corners()), 5_000);
    expect(tiled(await corners()), 'the drop did not leave two tiled panes').toBe(true);
  });
});
