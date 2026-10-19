// Writes the goldens: pele's SVG for every source of its documentation corpus.
// Usage: node gen.mjs PELE_DIR OUT_DIR
import { readFileSync, readdirSync, writeFileSync } from 'node:fs';

const [pele, out] = process.argv.slice(2);
if (!pele || !out) {
  console.error('usage: node gen.mjs PELE_DIR OUT_DIR');
  process.exit(2);
}
const { render } = await import(`${pele}/dist/pele.js`);
const dir = `${pele}/tests/corpus`;
// The time gantt charts treat as now, as TestGolden sets it.
const opts = { now: Date.UTC(2026, 0, 15, 12) };
let n = 0;
const errors = {};
for (const file of readdirSync(dir).filter((f) => f.endsWith('-docs.json')).sort()) {
  const name = file.replace('-docs.json', '');
  JSON.parse(readFileSync(`${dir}/${file}`, 'utf8')).forEach((source, i) => {
    try {
      writeFileSync(`${out}/${name}-${String(i).padStart(3, '0')}.svg`, render(source, opts).svg);
      n++;
    } catch {
      errors[name] = (errors[name] ?? 0) + 1;
    }
  });
}
console.log('rendered', n, 'errors', JSON.stringify(errors));
