// Writes want.json: what pele does with each case of cases.json, the error it
// throws or "ok". A case is {"src": "...", "maxEdges": 10}; maxEdges is optional.
// Usage: TZ=Asia/Dubai node gen.mjs PELE_DIR
import { readFileSync, writeFileSync } from 'node:fs';

const [pele] = process.argv.slice(2);
if (!pele) {
  console.error('usage: node gen.mjs PELE_DIR');
  process.exit(2);
}
const { render } = await import(`${pele}/dist/pele.js`);
const dir = new URL('.', import.meta.url).pathname;
const cases = JSON.parse(readFileSync(`${dir}/cases.json`, 'utf8'));
const want = cases.map((c) => {
  const opts = { now: Date.UTC(2026, 0, 15, 12) };
  if (c.maxEdges !== undefined) opts.maxEdges = c.maxEdges;
  try {
    render(c.src, opts);
    return { ok: true };
  } catch (e) {
    if (e?.name !== 'PeleError') throw e;
    const { message, code, type, mermaidType, line, column, snippet } = e;
    return { message, code, type: type ?? '', mermaidType: mermaidType ?? '', line: line ?? 0, column: column ?? 0, snippet: snippet ?? '' };
  }
});
writeFileSync(`${dir}/want.json`, JSON.stringify(want, null, 1) + '\n');
console.log('cases', want.length);
