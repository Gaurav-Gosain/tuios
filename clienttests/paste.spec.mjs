// Paste into a tuios pane through the browser terminal.
//
// tuios asks its host terminal for the kitty keyboard protocol. In the
// browser that host is webterm, and up to sip v0.9.1 webterm encoded
// Ctrl+Shift+V as CSI 118;6 u under the protocol and prevented the browser's
// paste, so no paste chord did anything in tuios-web. The paste is the
// browser's: the chord's default action fires a paste event that xterm reads,
// and tuios gets it as a bracketed paste and forwards it to the focused pane.
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

test('Ctrl+Shift+V pastes into the focused pane', async ({ page }) => {
  await boot(page);
  // The seeded config opens one window. Leader then Escape lands in window
  // mode whatever mode it started in, and i enters terminal mode on it.
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Escape');
  await page.keyboard.press('i');
  await page.waitForTimeout(300);

  await page.evaluate((t) => navigator.clipboard.writeText(t), `${MARK}_ONE\n${MARK}_TWO`);
  await page.evaluate(() => { window.__sent.length = 0; });
  await page.keyboard.press('Control+Shift+V');

  // One bracketed paste, and nothing of the chord as a key.
  await expect.poll(() => wire(page), { timeout: 5_000 })
    .toContain(`\x1b[200~${MARK}_ONE\r${MARK}_TWO\x1b[201~`);
  expect(await wire(page)).not.toContain('118;');
  // And the pane's shell shows it.
  await expect.poll(() => screen(page), { timeout: 10_000 }).toContain(`${MARK}_ONE`);
  await expect.poll(() => screen(page), { timeout: 10_000 }).toContain(`${MARK}_TWO`);
  // The frame, kept with the run's results.
  await page.screenshot({ path: test.info().outputPath('pasted.png') });
});
