// Paste into a tuios pane through the browser terminal.
//
// tuios asks its host terminal for the kitty keyboard protocol. In the
// browser that host is webterm, and up to sip v0.9.1 webterm encoded
// Ctrl+Shift+V as CSI 118;6 u under the protocol and prevented the browser's
// paste, so no paste chord did anything in tuios-web. Up to sip v0.9.2 Ctrl+V
// went to the pane as a key, where fish ran its own paste on the server's
// clipboard. The paste is the browser's: the chord's default action fires a
// paste event that xterm reads, and tuios gets it as a bracketed paste and
// forwards it to the focused pane.
//
// The shell's echo is read off the screen, so the test proves the text
// reached the pane and not only the wire.

import { test, expect } from '@playwright/test';

const MSG_INPUT = 0x30;
const MARK = 'PASTE_MARK_5821';

async function boot(page) {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.addInitScript(() => {
    localStorage.setItem('sip-web-settings', JSON.stringify({
      transport: 'websocket', fontSize: 14, copyOnSelect: false, cursorBlink: false, renderer: 'canvas',
    }));
  });
  await page.goto('/');
  await page.waitForFunction(() => window.sipTerm?.connected, null, { timeout: 40_000 });
  await page.evaluate((msgInput) => {
    window.__sent = [];
    const ws = window.sipTerm.connection.ws;
    if (!ws) throw new Error('expected a WebSocket to hook');
    const send = ws.send.bind(ws);
    ws.send = (frame) => {
      const b = new Uint8Array(frame);
      if (b[0] === msgInput) window.__sent.push(...b.subarray(1));
      return send(frame);
    };
  }, MSG_INPUT);
  // tuios turns the protocol on when it starts.
  await page.waitForFunction(() => window.sipTerm.webterm.keyboard?.flags > 0, null, { timeout: 30_000 });
  await page.locator('.xterm-helper-textarea').focus();
}

const wire = (page) => page.evaluate(() => new TextDecoder().decode(new Uint8Array(window.__sent)));

/** The visible screen as text. */
const screen = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  const rows = [];
  for (let i = 0; i < t.rows; i++) rows.push(b.getLine(b.viewportY + i)?.translateToString(true) ?? '');
  return rows.join('\n');
});

/** Leader then Escape lands in window mode whatever the mode was, and i enters terminal mode. */
async function terminalMode(page) {
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Escape');
  await page.keyboard.press('i');
  await page.waitForTimeout(300);
}

for (const chord of ['Control+v', 'Control+Shift+V']) {
  test(`${chord} pastes into the focused pane`, async ({ page }) => {
    await boot(page);
    await terminalMode(page);
    const mark = `${MARK}_${chord.length}`;

    await page.evaluate((t) => navigator.clipboard.writeText(t), `${mark}_ONE\n${mark}_TWO`);
    await page.evaluate(() => { window.__sent.length = 0; });
    await page.keyboard.press(chord);

    // One bracketed paste, and nothing of the chord as a key.
    await expect.poll(() => wire(page), { timeout: 5_000 })
      .toContain(`\x1b[200~${mark}_ONE\r${mark}_TWO\x1b[201~`);
    expect(await wire(page)).not.toContain('118;');
    expect(await wire(page)).not.toContain('\x16');
    // And the pane's shell shows it.
    await expect.poll(() => screen(page), { timeout: 10_000 }).toContain(`${mark}_ONE`);
    await expect.poll(() => screen(page), { timeout: 10_000 }).toContain(`${mark}_TWO`);
    // The frame, kept with the run's results.
    await page.screenshot({ path: test.info().outputPath('pasted.png') });
    // Clear the line for the next test.
    await page.keyboard.press('Control+c');
  });
}

// tuios's own paste key asks the terminal for the clipboard, and a browser
// never answers. The note says which keys paste in this browser. The seeded
// config binds the paste key to alt+v, because the page keeps the default
// ones for the browser.
const NOTE = 'The browser does not give the clipboard to tuios.';

/**
 * Press the paste key and read the whole note back. The dock clips a long
 * note and offers "more", and a click on it shows the note whole, which is
 * what a person does.
 */
async function pasteKeyNote(page) {
  await page.keyboard.press('Alt+v');
  await expect.poll(() => screen(page), { timeout: 5_000 }).toContain(NOTE);
  const at = await page.evaluate((note) => {
    const t = window.sipTerm.term;
    const b = t.buffer.active;
    for (let y = t.rows - 1; y >= 0; y--) {
      const line = b.getLine(b.viewportY + y)?.translateToString(false) ?? '';
      if (!line.includes(note.slice(0, 20))) continue;
      const x = line.indexOf('more', line.indexOf(note.slice(0, 20)));
      if (x < 0) return null;
      const r = t.element.querySelector('.xterm-screen').getBoundingClientRect();
      return { x: r.left + (x + 1.5) * (r.width / t.cols), y: r.top + (y + 0.5) * (r.height / t.rows) };
    }
    return null;
  }, NOTE);
  if (at) await page.mouse.click(at.x, at.y);
  await expect.poll(() => screen(page), { timeout: 10_000 }).toContain('to paste.');
  // The note may wrap in the message view, so it is read across the rows
  // that follow its start, with the frame glyphs taken out.
  const lines = (await screen(page)).split('\n');
  const start = lines.findIndex((l) => l.includes(NOTE.slice(0, 20)) && !l.includes('more'));
  return lines.slice(start, start + 4).join(' ').replace(/[│║·]/g, ' ').replace(/\s+/g, ' ');
}

test('the paste key names the keys that paste', async ({ page }) => {
  await boot(page);
  await terminalMode(page);
  expect(await pasteKeyNote(page)).toContain(
    `${NOTE} Press ctrl+v, ctrl+shift+v or shift+insert to paste.`);
});

test('on a Mac the note names cmd+v', async ({ browser, baseURL }) => {
  const context = await browser.newContext({
    baseURL,
    viewport: { width: 1400, height: 900 },
    userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 '
      + '(KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36',
    extraHTTPHeaders: { 'Sec-CH-UA-Platform': '"macOS"' },
  });
  const page = await context.newPage();
  try {
    await boot(page);
    await terminalMode(page);
    expect(await pasteKeyNote(page)).toContain(`${NOTE} Press cmd+v to paste.`);
  } finally {
    await context.close();
  }
});
