// Static server for the demo. Serves precompressed .gz/.br when the client
// accepts it, so the timings include a realistic transfer size.
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
const root = process.argv[2];
const port = +process.argv[3] || 8765;
const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.wasm': 'application/wasm', '.ttf': 'font/ttf' };
http.createServer((req, res) => {
  let p = decodeURIComponent(new URL(req.url, 'http://x').pathname);
  if (p.endsWith('/')) p += 'index.html';
  const file = path.join(root, p);
  if (!file.startsWith(root) || !fs.existsSync(file)) { res.writeHead(404); return res.end(); }
  const ae = req.headers['accept-encoding'] || '';
  const headers = { 'content-type': types[path.extname(file)] || 'application/octet-stream', 'cache-control': 'no-store' };
  let body = file;
  if (ae.includes('br') && fs.existsSync(file + '.br')) { body = file + '.br'; headers['content-encoding'] = 'br'; }
  else if (ae.includes('gzip') && fs.existsSync(file + '.gz')) { body = file + '.gz'; headers['content-encoding'] = 'gzip'; }
  headers['content-length'] = fs.statSync(body).size;
  console.log(req.method, p, headers['content-encoding'] || 'identity', headers['content-length']);
  res.writeHead(200, headers);
  fs.createReadStream(body).pipe(res);
}).listen(port, '127.0.0.1', () => console.log('listening', port));
