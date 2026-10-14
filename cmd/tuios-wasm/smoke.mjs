// A headless Chromium smoke test of the browser build. It serves a build.sh
// output directory, loads the demo page, opens a window, runs a command in
// the fake shell, pastes a second one, checks the events that came back, and
// checks that q does not quit. Then it loads the page with ?renderer=vtgl and checks that vtgl
// draws it. For both renderers it checks that the bottom row is on screen
// when the host centres the grid, as tuios.dev/learn does.
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

// tuios.dev/learn centres the grid in its host (app/learn/learn.css in
// tuios-docs), and this page does not. A renderer whose screen element is not
// the size of the grid it draws is placed by the wrong box there: vtgl drew
// the grid about 200 px low and the dock fell off the stage. So the page is
// loaded with that rule added, and the grid it draws has to fit in the host.
const learnLayout = '#terminal { display: flex !important; flex-direction: column; justify-content: safe center; }'
  + ' #terminal > .xterm { flex: none; }';

const checkBottomRow = async (page, renderer) => {
  await page.addStyleTag({ content: learnLayout });
  // Two frames, so layout has run with the rule in place.
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  const box = await page.evaluate(() => {
    const rect = (el) => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom }; };
    const host = document.getElementById('terminal');
    const screen = host.querySelector('.xterm-screen');
    // The grid is the largest canvas in the screen element: the WebGL
    // renderer adds a small one for its link layer. The DOM renderer, the
    // fallback without WebGL, draws into the screen element itself.
    const canvases = [...screen.querySelectorAll('canvas')];
    const grid = canvases.reduce((a, b) => (b.clientHeight > a.clientHeight ? b : a), screen);
    return { host: rect(host), grid: rect(grid), rows: window.webterm.rows };
  });
  // Half a pixel for sub-pixel layout.
  if (box.grid.top < box.host.top - 0.5 || box.grid.bottom > box.host.bottom + 0.5) {
    fail(`${renderer}: the grid spans ${box.grid.top}..${box.grid.bottom} px, outside the host ${box.host.top}..${box.host.bottom} px, so row ${box.rows} is off screen`);
  }
};

let browser;
try {
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM || undefined,
    // tuios plays sounds. A test run has no business making any.
    args: ['--mute-audio'],
  });
  // The clipboard grant lets the test write the text it pastes. The paste
  // itself needs none: the browser hands it to the page as a paste event.
  const context = await browser.newContext({
    viewport: { width: 1400, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  const page = await context.newPage();
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

  // Paste with Ctrl+Shift+V. tuios asks for the kitty keyboard protocol, and
  // under it webterm once sent this chord to tuios as a key and stopped the
  // browser's paste, so nothing reached the shell.
  await page.evaluate(() => navigator.clipboard.writeText('echo pasted-5821'));
  await page.keyboard.press('Control+Shift+V');
  // The browser reads the clipboard after the key, so wait for the text to
  // reach the screen before Enter, or Enter runs the empty line first.
  await page.waitForFunction(() => {
    const b = window.webterm.xterm.buffer.active;
    for (let i = 0; i < b.length; i++) {
      if (b.getLine(i)?.translateToString(true).includes('pasted-5821')) return true;
    }
    return false;
  }, null, { timeout: 10000 }).catch(() => fail('Ctrl+Shift+V did not paste into the shell'));
  await page.keyboard.press('Enter');
  const pasted = await waitEvent('shell.command', "(e) => e.data.line === 'echo pasted-5821'", 10000);
  if (pasted.data.exitCode !== 0) fail('the pasted echo exited ' + pasted.data.exitCode);

  // And with plain Ctrl+V, which the page keeps for the browser by default.
  await page.evaluate(() => navigator.clipboard.writeText('echo ctrl-v-5821'));
  await page.keyboard.press('Control+v');
  await page.waitForFunction(() => {
    const b = window.webterm.xterm.buffer.active;
    for (let i = 0; i < b.length; i++) {
      if (b.getLine(i)?.translateToString(true).includes('ctrl-v-5821')) return true;
    }
    return false;
  }, null, { timeout: 10000 }).catch(() => fail('Ctrl+V did not paste into the shell'));
  await page.keyboard.press('Enter');
  await waitEvent('shell.command', "(e) => e.data.line === 'echo ctrl-v-5821'", 10000);

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
  await checkBottomRow(page, 'webgl');

  // ?renderer=vtgl must reach vtgl. A newer sip ships it as webterm-vtgl.js,
  // which the page has to load itself, and without it webterm falls back to
  // WebGL without an error.
  const vtglPage = await browser.newPage({ viewport: { width: 1000, height: 700 } });
  vtglPage.on('pageerror', (err) => logs.push('vtgl pageerror: ' + err.message));
  await vtglPage.goto(`http://127.0.0.1:${port}/?renderer=vtgl`);
  await vtglPage.waitForFunction(() => window.tuiosTimings && window.tuiosTimings.firstFrame, null, { timeout: 60000 });
  const vtglRenderer = await vtglPage.evaluate(() => window.webterm.renderer);
  if (vtglRenderer !== 'vtgl') fail('?renderer=vtgl drew with ' + vtglRenderer);
  await checkBottomRow(vtglPage, 'vtgl');
  await vtglPage.close();

  if (shot) await page.screenshot({ path: shot });
  if (logs.length) fail('page errors:\n' + logs.join('\n'));
  const timings = await page.evaluate(() => window.tuiosTimings);
  console.log('smoke ok', JSON.stringify({ timings, events: seen, windows: state.totalWindows }));
} finally {
  if (browser) await browser.close();
  server.kill();
}
