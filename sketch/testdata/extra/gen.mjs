// Writes the extra goldens: pele's SVG for every case of each JSON file here.
// A case is {"src": "...", "maxWidth": 200, "fontSize": 13.5, "padding": 7,
// "idPrefix": "p1", "autoDirection": false, "monospace": ["members"], "math": true}; all but src
// are optional. A case with math is drawn with fakeMath as the math resolver.
// Usage: TZ=Asia/Dubai node gen.mjs PELE_DIR OUT_DIR
import { readFileSync, readdirSync, writeFileSync } from 'node:fs';

const [pele, out] = process.argv.slice(2);
if (!pele || !out) {
  console.error('usage: node gen.mjs PELE_DIR OUT_DIR');
  process.exit(2);
}
const { render } = await import(`${pele}/dist/pele.js`);
// A fixed resolver for $$...$$ labels. TestGoldenExtra has the same one in Go
// (goldenMath). TeX that holds "none" has no markup and "nan" a bad width, so
// those labels show their TeX source.
const fakeMath = (tex, size) => {
  if (tex.includes('none')) return null;
  if (tex.includes('nan')) return { svg: '', width: NaN, height: 10 };
  const width = tex.length * 7 + size / 2;
  const height = tex.includes('\\frac') || tex.includes('tall') ? size * 2.5 : size + 4;
  if (tex.startsWith('h:')) return { html: `<span class="m">${tex.length}</span>`, width, height };
  return { svg: `<path d="M0,0H${tex.length}"/>`, width, height };
};
const dir = new URL('.', import.meta.url).pathname;
let n = 0;
for (const file of readdirSync(dir).filter((f) => f.endsWith('.json')).sort()) {
  const name = file.replace('.json', '');
  JSON.parse(readFileSync(`${dir}/${file}`, 'utf8')).forEach((c, i) => {
    const opts = { now: Date.UTC(2026, 0, 15, 12) };
    if (c.maxWidth) opts.maxWidth = c.maxWidth;
    if (c.fontSize !== undefined) opts.fontSize = c.fontSize;
    if (c.padding !== undefined) opts.padding = c.padding;
    if (c.idPrefix !== undefined) opts.idPrefix = c.idPrefix;
    if (c.autoDirection !== undefined) opts.autoDirection = c.autoDirection;
    if (c.monospace !== undefined) opts.monospace = c.monospace;
    if (c.math) opts.math = fakeMath;
    writeFileSync(`${out}/${name}-${String(i).padStart(3, '0')}.svg`, render(c.src, opts).svg);
    n++;
  });
}
console.log('rendered', n);
