// Reap the daemon the run started.
//
// tuios-web spawns a daemon and Playwright only knows about the server it
// launched itself, so without this every run leaves one behind, holding a
// socket in a temp directory that is about to be deleted.

import { readFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { ISOLATED_HOME } from './playwright.config.mjs';

const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

export default async function teardown() {
  const pidFile = join(ISOLATED_HOME, 'run', 'tuios', 'tuios.sock.pid');
  let pid = 0;
  try {
    pid = Number(readFileSync(pidFile, 'utf8').trim());
    process.kill(pid, 'SIGTERM');
  } catch {
    // No daemon, or it is already gone. Either is the postcondition.
  }
  // The tree cannot go while the daemon is still writing into it, which is
  // what a bare rm right after the signal raced with.
  for (let i = 0; pid && i < 50 && alive(pid); i++) {
    await new Promise((r) => setTimeout(r, 100));
  }
  rmSync(ISOLATED_HOME, { recursive: true, force: true, maxRetries: 5 });
}
