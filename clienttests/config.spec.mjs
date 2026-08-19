// What the user's config file does to a session served over the browser.
//
// The server is started against an isolated XDG tree holding one config file
// (SEEDED_CONFIG in playwright.config.mjs), every value of which is
// deliberately not a default. Attaching over the web is meant to give the same
// tuios as attaching locally, so each test here names a setting and reads the
// terminal buffer for the thing it should have changed. A default on screen is
// the file having been ignored, which is what tuios-web did with everything
// that reaches the client through a package global.
//
// Claims are made against the buffer, never a pixel: the headless GL is
// software rasterization and the chrome is text either way.

import { test, expect } from '@playwright/test';

/** The visible terminal, line by line. */
const screen = (page) => page.evaluate(() => {
  const t = window.sipTerm.term;
  const b = t.buffer.active;
  return Array.from({ length: t.rows }, (_, i) => b.getLine(b.viewportY + i)?.translateToString(true) ?? '');
});

async function boot(page) {
  await page.addInitScript(() => {
    localStorage.setItem('sip-web-settings', JSON.stringify({
      transport: 'websocket', fontSize: 14, copyOnSelect: false, cursorBlink: false, renderer: 'canvas',
    }));
  });
  await page.goto('/');
  await page.waitForFunction(() => window.sipTerm?.connected, null, { timeout: 40_000 });
  // The seeded config opens a window at startup, so waiting for a border is
  // waiting for the first real frame rather than for a fixed number of seconds.
  await expect
    .poll(async () => (await screen(page)).some((l) => /[╔╭]/.test(l)), { timeout: 30_000 })
    .toBe(true);
  return screen(page);
}

test.describe('the config file reaches a browser session', () => {
  test('appearance: border_style draws the style the file asked for', async ({ page }) => {
    const s = await boot(page);
    const joined = s.join('\n');
    // double, not the rounded default.
    expect(joined).toContain('╔');
    expect(joined).toContain('╚');
    expect(joined).not.toContain('╭');
  });

  test('dock: dockbar_position puts the dock at the top', async ({ page }) => {
    const s = await boot(page);
    // The dock is the row carrying the workspace strip, and the rule under it.
    const dock = s.findIndex((l) => /\s1\s/.test(l) && l.includes('+'));
    expect(dock, 'no dock row found').toBeGreaterThanOrEqual(0);
    expect(dock, 'the dock is not at the top of the screen').toBeLessThan(3);
    expect(s[s.length - 1]).not.toMatch(/^─{20,}/);
  });

  test('sidebar: the rail is drawn, on the configured edge, at the configured width', async ({ page }) => {
    const s = await boot(page);
    const rail = s.find((l) => l.includes('sessions'));
    expect(rail, 'the sidebar was not drawn').toBeTruthy();
    // position = "left": the rail's own right-hand edge sits at width 24, and
    // there is nothing to its left.
    const edge = rail.indexOf('║');
    expect(edge).toBeGreaterThan(0);
    expect(edge).toBeLessThanOrEqual(24);
    expect(rail.indexOf('sessions'), 'the rail is not on the left edge').toBeLessThan(3);
    // show_agents = false: the section the rail draws by default is gone.
    expect(s.join('\n')).not.toContain('agents');
  });

  test('appearance: show_clock puts a clock on screen', async ({ page }) => {
    const s = await boot(page);
    expect(s.join('\n')).toMatch(/\d{2}:\d{2}:\d{2}/);
  });

  test('appearance: window_title_format formats the pane title', async ({ page }) => {
    const s = await boot(page);
    expect(s.join('\n')).toContain('seeded');
  });

  test('keybindings: leader_key is the one in the file, and settings say they will not be saved', async ({ page }) => {
    await boot(page);
    await page.locator('.xterm-screen').click();

    // ctrl+a is the configured leader. The default is ctrl+b, so the panel
    // opening at all is the keybinding having come from the file.
    await page.keyboard.press('Control+a');
    await page.keyboard.press(',');

    await expect
      .poll(async () => (await screen(page)).join('\n'), { timeout: 15_000 })
      // A served session applies a settings change to itself and never writes
      // the operator's config file; the panel has to say so.
      .toContain('this session only');
  });
});
