// Writes the extra goldens: pele's SVG for every case of each JSON file here.
// A case is {"src": "...", "maxWidth": 200}; maxWidth is optional.
// Usage: TZ=Asia/Dubai node gen.mjs PELE_DIR OUT_DIR
import { readFileSync, readdirSync, writeFileSync } from 'node:fs';

const [pele, out] = process.argv.slice(2);
if (!pele || !out) {
  console.error('usage: node gen.mjs PELE_DIR OUT_DIR');
  process.exit(2);
}
const { render } = await import(`${pele}/dist/pele.js`);
const dir = new URL('.', import.meta.url).pathname;
let n = 0;
for (const file of readdirSync(dir).filter((f) => f.endsWith('.json')).sort()) {
  const name = file.replace('.json', '');
  JSON.parse(readFileSync(`${dir}/${file}`, 'utf8')).forEach((c, i) => {
    const opts = { now: Date.UTC(2026, 0, 15, 12) };
    if (c.maxWidth) opts.maxWidth = c.maxWidth;
    writeFileSync(`${out}/${name}-${String(i).padStart(3, '0')}.svg`, render(c.src, opts).svg);
    n++;
  });
}
console.log('rendered', n);
