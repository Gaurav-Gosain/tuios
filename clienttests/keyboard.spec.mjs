// What a desktop keyboard puts on the wire through the browser terminal.
//
// tuios asks its host terminal for the kitty keyboard protocol, and in the
// browser that host is webterm. webterm runs the protocol from xterm's custom
// key event handler, a slot that holds one handler. sip up to v0.8.3 put its
// copy chord in that slot, so webterm still answered the protocol query while
// every key went out in legacy form: Escape arrived as a lone ESC byte and
// Ctrl+C as a bare ETX. These tests read the bytes the page sends, because the
// screen cannot show which form a key took.

import { test, expect } from '@playwright/test';

const MSG_INPUT = 0x30;

async function boot(page) {
  await page.addInitScript(() => {
    localStorage.setItem('sip-web-settings', JSON.stringify({
      transport: 'websocket', fontSize: 14, copyOnSelect: false, cursorBlink: false, renderer: 'canvas',
    }));
  });
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
  // tuios turns the protocol on when it starts, so wait for its first frame.
  await expect
    .poll(() => page.evaluate(() => {
      const t = window.sipTerm.term;
      const b = t.buffer.active;
      for (let i = 0; i < t.rows; i++) {
        if (/[╔╭]/.test(b.getLine(b.viewportY + i)?.translateToString(true) ?? '')) return true;
      }
      return false;
    }), { timeout: 30_000 })
    .toBe(true);
  await page.locator('.xterm-helper-textarea').focus();
}

/** Everything sent since the last clear, as one printable string. */
const wire = (page) => page.evaluate(() => window.__sentInput.map((b) =>
  b.map((c) => (c >= 32 && c < 127 ? String.fromCharCode(c) : '\\x' + c.toString(16).padStart(2, '0'))).join('')).join(''));

const clearWire = (page) => page.evaluate(() => { window.__sentInput.length = 0; });

test.describe('the kitty keyboard protocol reaches tuios', () => {
  test('Escape goes out as a kitty key, not a bare ESC', async ({ page }) => {
    await boot(page);
    await clearWire(page);
    await page.keyboard.press('Escape');
    await expect.poll(() => wire(page), { timeout: 5_000 }).not.toBe('');
    // Under the protocol's first flag, which tuios always asks for, Escape is
    // CSI 27u. In legacy form it is a lone ESC byte.
    expect(await wire(page)).toContain('\\x1b[27u');
  });

  test('Ctrl+C with no selection is still an interrupt for tuios', async ({ page }) => {
    await boot(page);
    await page.evaluate(() => window.sipTerm.term.clearSelection());
    await clearWire(page);
    await page.keyboard.press('Control+c');
    await expect.poll(() => wire(page), { timeout: 5_000 }).not.toBe('');
    // Under the protocol the chord is CSI 99;5u. The copy handler must let it
    // through when nothing is selected.
    expect(await wire(page)).toContain('\\x1b[99;5u');
  });
});
