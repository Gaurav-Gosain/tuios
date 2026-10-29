// Drives lesson tracks in headless Chromium through the lesson engine in
// the browser build: the demo page runs ?track=ID, and for each step this
// asks the engine to "show me" (or reads an explainer through with "next"),
// waits for lesson.done, and saves a frame. A step that "show me" does not
// finish fails the run.
//
// Usage: node cmd/tuios-wasm/smoke-lessons.mjs <build-dir> <out-dir> [track...]
//
// With no track it runs basics. <out-dir> gets <track>/NN-<step>.png, one
// frame per step once it is done, and <track>/log.json: the events the
// engine was fed and the step each lesson.done finished, which
// internal/learn/lessons/testdata/replay holds for the replay test.
//
// Needs playwright-core, as smoke.mjs does.
import { spawn } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const dir = path.resolve(process.argv[2] || path.join(here, '../../.wasm-build/site'));
const out = path.resolve(process.argv[3] || 'lesson-frames');
const tracks = process.argv.slice(4);
if (tracks.length === 0) tracks.push('basics');
const port = 8700 + Math.floor(Math.random() * 200);

const modulePath = process.env.PLAYWRIGHT_CORE;
const { chromium } = await import(modulePath ? pathToFileURL(path.resolve(modulePath)).href : 'playwright-core');

const server = spawn(process.execPath, [path.join(here, 'serve.mjs'), dir, String(port)], { stdio: ['ignore', 'pipe', 'inherit'] });
await new Promise((resolve) => server.stdout.once('data', resolve));

let browser;
const failures = [];
try {
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM || undefined, args: ['--mute-audio'] });
  for (const track of tracks) {
    const page = await browser.newPage({ viewport: { width: 1500, height: 900 } });
    const errors = [];
    page.on('pageerror', (err) => errors.push(err.message));
    await page.goto(`http://127.0.0.1:${port}/?track=${encodeURIComponent(track)}`);
    await page.waitForFunction(() => window.tuios, null, { timeout: 60000 });
    await page.evaluate(() => window.tuios.lesson('log', 'on'));
    const lessonEvents = () => page.evaluate(() => window.tuiosEvents.filter((e) => e.type.startsWith('lesson.')));
    // The event of a type that arrives after the `from`th lesson event.
    const waitLesson = async (type, check, from, timeout) => {
      const h = await page.waitForFunction(({ type, check, from }) => {
        const ok = eval(check);
        const list = window.tuiosEvents.filter((e) => e.type.startsWith('lesson.'));
        const i = list.findIndex((e, k) => k >= from && e.type === type && ok(e.data));
        return i >= 0 ? { i, data: list[i].data } : null;
      }, { type, check, from }, { timeout });
      return h.jsonValue();
    };
    const start = await waitLesson('lesson.start', '() => true', 0, 60000);
    const total = start.data.total;
    await mkdir(path.join(out, track), { recursive: true });
    const done = [];
    let seen = start.i + 1;
    for (let i = 0; i < total; i++) {
      const step = await waitLesson('lesson.step', `(d) => d.index === ${i}`, seen, 30000).catch(() => null);
      if (!step) {
        failures.push(`${track}: step ${i} never started`);
        break;
      }
      seen = step.i + 1;
      // Let the card and the scene settle, as a reader would read the card.
      await page.waitForTimeout(400);
      // One show me per step. A step that waits on the scene, such as an
      // agent that asks after a few seconds, says so in its after events,
      // and show me waits for them.
      await page.evaluate((cmd) => window.tuios.lesson(cmd), step.data.explainer ? 'next' : 'showMe');
      const fin = await waitLesson('lesson.done', `(d) => d.index === ${i}`, seen, 60000).catch(() => null);
      if (!fin) {
        failures.push(`${track}: step ${i} (${step.data.id}) was not done by show me`);
        await page.screenshot({ path: path.join(out, track, `${String(i + 1).padStart(2, '0')}-${step.data.id}-stuck.png`) });
        break;
      }
      seen = fin.i + 1;
      done.push({ index: i, id: step.data.id, result: fin.data.result, event: fin.data.event?.type ?? null });
      // The frame after the step: what show me did.
      await page.waitForTimeout(300);
      await page.screenshot({ path: path.join(out, track, `${String(i + 1).padStart(2, '0')}-${step.data.id}.png`) });
    }
    const log = await page.evaluate(() => window.tuios.lesson('log', 'get'));
    await writeFile(path.join(out, track, 'log.json'), JSON.stringify({ track, done, events: log }, null, 1));
    if (errors.length) failures.push(`${track}: page errors: ${errors.join('; ')}`);
    console.log(`${track}: ${done.length}/${total} steps done by show me, ${log.length} events fed`);
    await page.close();
  }
} finally {
  if (browser) await browser.close();
  server.kill();
}
if (failures.length) {
  console.error(failures.join('\n'));
  process.exit(1);
}
