// Reap the daemons the run started.
//
// tuios-web spawns a daemon and Playwright only knows about the servers it
// launched itself, so without this every run leaves one behind per server,
// holding a socket in a temp directory that is about to be deleted.

import { readFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { ISOLATED_HOMES } from './playwright.config.mjs';

const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

async function reap(home) {
  const pidFile = join(home, 'run', 'tuios', 'tuios.sock.pid');
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
  rmSync(home, { recursive: true, force: true, maxRetries: 5 });
}

export default async function teardown() {
  await Promise.all(ISOLATED_HOMES.map(reap));
}
