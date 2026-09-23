// A static server for trying the browser build locally, the way the docs
// site's Worker serves it: a request for a file gets its .br or .gz twin
// with Content-Encoding when the browser accepts one, and the twin is enough
// on its own, so tuios.wasm works with only tuios.wasm.br and .gz on disk.
//
// Usage: node cmd/tuios-wasm/serve.mjs <dir> [port]
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';

const root = path.resolve(process.argv[2] || '.');
const port = +process.argv[3] || 8765;
const types = {
  '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css',
  '.wasm': 'application/wasm', '.ttf': 'font/ttf', '.woff2': 'font/woff2',
  '.json': 'application/json', '.gz': 'application/gzip',
};
const isFile = (p) => fs.existsSync(p) && fs.statSync(p).isFile();

const server = http.createServer((req, res) => {
  let p = decodeURIComponent(new URL(req.url, 'http://x').pathname);
  if (p.endsWith('/')) p += 'index.html';
  const file = path.join(root, p);
  if (!file.startsWith(root)) { res.writeHead(403); return res.end(); }
  const accepts = req.headers['accept-encoding'] || '';
  const headers = { 'content-type': types[path.extname(file)] || 'application/octet-stream', 'cache-control': 'no-store' };
  let body = null;
  if (accepts.includes('br') && isFile(file + '.br')) { body = file + '.br'; headers['content-encoding'] = 'br'; }
  else if (accepts.includes('gzip') && isFile(file + '.gz')) { body = file + '.gz'; headers['content-encoding'] = 'gzip'; }
  else if (isFile(file)) body = file;
  if (!body) { res.writeHead(404); return res.end(); }
  headers['content-length'] = fs.statSync(body).size;
  res.writeHead(200, headers);
  fs.createReadStream(body).pipe(res);
});

server.listen(port, '127.0.0.1', () => console.log(`serving ${root} on http://127.0.0.1:${port}/`));
