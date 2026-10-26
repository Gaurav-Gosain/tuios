// Real pane output, checked on the browser's composited screen. The native
// sixel addon keeps pictures when tuios rewrites their cells (#587).
import { test, expect } from '@playwright/test';

async function command(page, text, marker) {
  await page.keyboard.insertText(text);
  await page.keyboard.press('Enter');
  await expect.poll(() => page.evaluate(marker => {
    const t = window.sipTerm.term;
    const b = t.buffer.active;
    return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '')
      .some(line => line.split(/[│║]/).some(part => part.trim() === marker));
  }, marker), { timeout: 10_000 }).toBe(true);
}

async function redPixels(page) {
  // A screenshot includes both the sixel canvas and the kitty image overlay.
  const png = await page.screenshot();
  return page.evaluate(async bytes => {
    const image = await createImageBitmap(new Blob([new Uint8Array(bytes)], { type: 'image/png' }));
    const canvas = document.createElement('canvas');
    canvas.width = image.width;
    canvas.height = image.height;
    const ctx = canvas.getContext('2d');
    ctx.drawImage(image, 0, 0);
    image.close();
    const pixels = ctx.getImageData(0, 0, canvas.width, canvas.height).data;
    let red = 0;
    for (let i = 0; i < pixels.length; i += 4) {
      if (pixels[i] > 220 && pixels[i + 1] < 45 && pixels[i + 2] < 45) red++;
    }
    return red;
  }, Array.from(png));
}

for (const action of ['clear', 'scroll']) {
  test(`sixel pictures disappear after ${action}`, async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('sip-web-settings', JSON.stringify({
      transport: 'websocket', renderer: 'canvas', fontSize: 14, cursorBlink: false,
    })));
    await page.goto('/?renderer=canvas');
    await page.waitForFunction(() => window.sipTerm?.connected && window.sipTerm.webterm.keyboard?.flags > 0,
      null, { timeout: 40_000 });
    // The pane border can arrive before its shell is ready to read input.
    await page.waitForFunction(() => {
      const t = window.sipTerm.term;
      const b = t.buffer.active;
      return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '')
        .some(line => line.split(/[│║]/).some(part => /[#$]$/.test(part.trim())));
    }, null, { timeout: 30_000 });
    await page.locator('.xterm-helper-textarea').focus();
    await page.keyboard.press('Control+b');
    await page.keyboard.press('Escape');
    await page.keyboard.press('i');
    await page.waitForFunction(() => {
      const t = window.sipTerm.term;
      const b = t.buffer.active;
      return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '')
        .some(line => line.includes('Terminal mode'));
    }, null, { timeout: 10_000 });
    await command(page, "clear; printf 'BASELINE_READY\\n'", 'BASELINE_READY');
    await expect.poll(() => redPixels(page)).toBe(0);

    // A 120x96 solid-red sixel, far enough from the prompt to survive typing.
    const rows = '!120~-'.repeat(16);
    await command(page, `printf '\\033[8;10H\\033Pq"1;1;120;96#1;2;100;0;0#1${rows}\\033\\\\\\033[20;1HIMAGE_READY\\n'`, 'IMAGE_READY');
    await expect.poll(() => redPixels(page), { message: 'the sixel picture never reached the browser' })
      .toBeGreaterThan(10_000);
    await page.screenshot({ path: test.info().outputPath('image.png') });

    const clear = "clear; printf 'PICTURE_GONE\\n'";
    const scroll = 'i=0; while [ "$i" -lt 55 ]; do printf "scroll-%02d\\n" "$i"; i=$((i+1)); done; printf "PICTURE_GONE\\n"';
    await command(page, action === 'clear' ? clear : scroll, 'PICTURE_GONE');
    try {
      await expect.poll(() => redPixels(page), { message: `sixel picture remained after ${action} (#587)` })
        .toBe(0);
    } finally {
      await page.screenshot({ path: test.info().outputPath(`after-${action}.png`) });
    }
  });
}
