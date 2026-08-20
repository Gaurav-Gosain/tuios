// Browser tests for tuios-web, run against real servers on real viewports.
//
// The browser is the system chromium, so nothing is downloaded. Everything
// here asserts what reached the wire or what the terminal buffer says, never a
// frame rate and never a pixel: the headless GL is software rasterization.
//
// Two servers, not one, because the two suites need opposite things from the
// config file. The touch tests want tuios as it ships; the config tests want a
// file full of values that are deliberately not the defaults. Sharing a server
// would also mean sharing a daemon and the session inside it, so whichever
// suite attached first would size the session for the other one's viewport.

import { defineConfig } from '@playwright/test';
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const CHROMIUM = process.env.TUIOS_CHROMIUM ?? '/usr/bin/chromium';
export const PORT = process.env.TUIOS_TEST_PORT ?? '7791';
export const BASE_URL = `http://127.0.0.1:${PORT}`;
// The config server sits two ports up: tuios-web also opens PORT+1 for
// WebTransport over UDP, so consecutive ports would collide.
export const CONFIG_PORT = String(Number(PORT) + 2);
export const CONFIG_BASE_URL = `http://127.0.0.1:${CONFIG_PORT}`;
// The multi-client suite needs a session no other suite has attached to, since
// what it measures is what a second viewport does to the first one's layout.
// Two ports up again, for the same WebTransport reason.
export const MULTI_PORT = String(Number(PORT) + 4);
export const MULTI_BASE_URL = `http://127.0.0.1:${MULTI_PORT}`;

// A throwaway XDG tree per server per run. tuios-web reads the user's config
// and writes session state, and a test must not touch either.
//
// XDG_RUNTIME_DIR is the one that decides which daemon this talks to
// (GetSocketPath joins it with tuios/tuios.sock), so leaving it out attached
// every run to the developer's live session: whatever their real windows held
// was what the tests read back, and whatever the tests typed stayed there.
// TUIOS_SOCKET is not read by anything, only exported into a pane, so it never
// isolated anything. It is also what keeps the two servers here apart: same
// binary, same machine, different socket.
//
// The trees live under the system temp dir rather than anywhere deeper: the
// socket path inside one has to stay under the 108-byte sockaddr_un limit.
// Made once and passed down: Playwright loads this config again in the
// processes it spawns, and a second mkdtemp there would hand the teardown a
// directory the servers never used.
function isolatedTree(envKey) {
  const home = process.env[envKey] ?? mkdtempSync(join(tmpdir(), 'tuios-ct-'));
  process.env[envKey] = home;
  const env = {
    XDG_CONFIG_HOME: join(home, 'config'),
    XDG_DATA_HOME: join(home, 'data'),
    XDG_STATE_HOME: join(home, 'state'),
    XDG_CACHE_HOME: join(home, 'cache'),
    XDG_RUNTIME_DIR: join(home, 'run'),
  };
  mkdirSync(env.XDG_RUNTIME_DIR, { recursive: true, mode: 0o700 });
  return { home, env };
}

const touch = isolatedTree('TUIOS_CT_HOME');
const cfg = isolatedTree('TUIOS_CT_CONFIG_HOME');
const multi = isolatedTree('TUIOS_CT_MULTI_HOME');

// The config the second server is served with. Written before it starts,
// because tuios-web reads the file once, at startup, for the whole process.
//
// Every value here is deliberately not a default, so a served session showing
// a default is showing that the file never reached it. They are also all
// readable off the terminal buffer: a box-drawing glyph, a row position, a pane
// of text down one side, a clock.
export const SEEDED_CONFIG = `[appearance]
border_style = "double"
dockbar_position = "top"
show_clock = true
window_title_format = "seeded {title}"

[appearance.sidebar]
enabled = true
position = "left"
width = 24
show_agents = false

[startup]
open_default_window = true

[keybindings]
leader_key = "ctrl+a"
`;

mkdirSync(join(cfg.env.XDG_CONFIG_HOME, 'tuios'), { recursive: true });
writeFileSync(join(cfg.env.XDG_CONFIG_HOME, 'tuios', 'config.toml'), SEEDED_CONFIG);

// Both, for the teardown: each server autostarts its own daemon.
export const ISOLATED_HOMES = [touch.home, cfg.home, multi.home];

const chromium = {
  executablePath: CHROMIUM,
  args: [
    '--use-gl=angle',
    '--use-angle=swiftshader',
    '--enable-unsafe-swiftshader',
    '--disable-lcd-text',
    '--force-device-scale-factor=1',
  ],
};

// Never reuse a server. The key bar is built in Go and handed to the page in
// the HTML, so a server left over from an earlier build serves the old bar
// while the source on disk says otherwise, and nothing reports it.
const server = (port, url, env) => ({
  command: `go run ./cmd/tuios-web --host 127.0.0.1 --port ${port}`,
  cwd: '..',
  url,
  env,
  reuseExistingServer: false,
  timeout: 180_000,
  stdout: 'ignore',
  stderr: 'pipe',
});

export default defineConfig({
  testDir: '.',
  testMatch: /.*\.spec\.mjs/,
  fullyParallel: false,
  workers: 1,
  timeout: 90_000,
  reporter: [['list']],
  globalTeardown: './teardown.mjs',
  projects: [
    {
      name: 'phone',
      testMatch: /touch\.spec\.mjs/,
      use: {
        baseURL: BASE_URL,
        hasTouch: true,
        isMobile: true,
        viewport: { width: 390, height: 844 },
        // A real phone's, because tuios-web reads the handshake's user agent to
        // decide whether the pointer is a finger. Headless Chromium's own says
        // X11 and Linux, so without this the server would size its hit targets
        // for a mouse while the test drives it with one.
        userAgent: 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 '
          + '(KHTML, like Gecko) Chrome/151.0.0.0 Mobile Safari/537.36',
        deviceScaleFactor: 1,
        launchOptions: chromium,
      },
    },
    {
      // The config tests need columns: the rail and the dock both collapse on a
      // phone, and a collapsed thing cannot be told apart from a missing one.
      name: 'desktop',
      testMatch: /config\.spec\.mjs/,
      use: {
        baseURL: CONFIG_BASE_URL,
        hasTouch: false,
        isMobile: false,
        viewport: { width: 1400, height: 900 },
        deviceScaleFactor: 1,
        launchOptions: chromium,
      },
    },
    {
      // No viewport here: the multi-client tests open their own contexts,
      // because the whole subject is two of them at sizes that differ.
      name: 'multiclient',
      testMatch: /multiclient\.spec\.mjs/,
      use: {
        baseURL: MULTI_BASE_URL,
        hasTouch: false,
        isMobile: false,
        deviceScaleFactor: 1,
        launchOptions: chromium,
      },
    },
  ],
  webServer: [
    server(PORT, BASE_URL, touch.env),
    server(CONFIG_PORT, CONFIG_BASE_URL, cfg.env),
    server(MULTI_PORT, MULTI_BASE_URL, multi.env),
  ],
});
