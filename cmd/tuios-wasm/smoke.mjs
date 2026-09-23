// A headless Chromium smoke test of the browser build. It serves a build.sh
// output directory, loads the demo page, opens a window, runs a command in
// the fake shell, checks the events that came back, and checks that q does
// not quit.
//
// Usage: node cmd/tuios-wasm/smoke.mjs <dir> [screenshot.png]
//
// Needs playwright-core. PLAYWRIGHT_CORE names the module to import when it is
// not installed where node looks, and CHROMIUM names a browser binary when
// Playwright's own is not installed.
import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const dir = path.resolve(process.argv[2] || path.join(here, '../../.wasm-build/site'));
const shot = process.argv[3];
const port = 8700 + Math.floor(Math.random() * 200);

const modulePath = process.env.PLAYWRIGHT_CORE;
const { chromium } = await import(modulePath ? pathToFileURL(path.resolve(modulePath)).href : 'playwright-core');

const server = spawn(process.execPath, [path.join(here, 'serve.mjs'), dir, String(port)], { stdio: ['ignore', 'pipe', 'inherit'] });
await new Promise((resolve) => server.stdout.once('data', resolve));

const fail = (msg) => { throw new Error(msg); };
let browser;
try {
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const logs = [];
  page.on('pageerror', (err) => logs.push('pageerror: ' + err.message));
  page.on('console', (m) => { if (m.type() === 'error') logs.push('console: ' + m.text()); });

  await page.goto(`http://127.0.0.1:${port}/`);
  await page.waitForFunction(() => window.tuiosTimings && window.tuiosTimings.firstFrame, null, { timeout: 60000 });

  // Wait for an event of a type that matches, counting from the events
  // already seen, and return it.
  let seen = 0;
  const waitEvent = async (type, match = '() => true', timeout = 20000) => {
    const handle = await page.waitForFunction(({ type, match, from }) => {
      const ok = eval(match);
      const i = window.tuiosEvents.findIndex((e, k) => k >= from && e.type === type && ok(e));
      return i >= 0 ? { i, e: window.tuiosEvents[i] } : null;
    }, { type, match, from: seen }, { timeout });
    const { i, e } = await handle.jsonValue();
    seen = i + 1;
    return e;
  };

  await page.locator('#terminal').click();
  await page.keyboard.press('n');
  const opened = await waitEvent('window.open');
  if (!opened.windowId) fail('window.open has no windowId');

  await page.keyboard.press('i');
  await waitEvent('mode', "(e) => e.data.to === 'terminal'");
  await page.keyboard.type('ls');
  await page.keyboard.press('Enter');
  const ran = await waitEvent('shell.command', "(e) => e.data.command === 'ls'");
  if (ran.data.exitCode !== 0) fail('ls exited ' + ran.data.exitCode);

  // Back to window mode with the leader and Esc; q there is a quit, which
  // Learn mode turns into a note.
  await page.keyboard.press('Control+b');
  await waitEvent('prefix', "(e) => e.data.to === 'prefix'");
  await page.keyboard.press('Escape');
  await waitEvent('mode', "(e) => e.data.to === 'window'");
  await page.keyboard.press('q');
  await waitEvent('action', "(e) => e.data.name === 'quit'");
  await waitEvent('notification', "(e) => e.data.message.includes('No need to quit')");

  const state = await page.evaluate(() => window.tuios.state());
  if (state.totalWindows !== 1 || state.mode !== 'window') fail('unexpected state ' + JSON.stringify(state));
  const actions = await page.evaluate(() => Object.keys(window.tuios.actions()).length);
  if (actions < 50) fail('tuios.actions() has only ' + actions + ' actions');

  if (shot) await page.screenshot({ path: shot });
  if (logs.length) fail('page errors:\n' + logs.join('\n'));
  const timings = await page.evaluate(() => window.tuiosTimings);
  console.log('smoke ok', JSON.stringify({ timings, events: seen, windows: state.totalWindows }));
} finally {
  if (browser) await browser.close();
  server.kill();
}
