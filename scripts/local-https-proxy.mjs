import { readFileSync } from 'node:fs';
import http from 'node:http';
import https from 'node:https';

const certificate = new URL('../.local-certs/localhost.pem', import.meta.url);
const key = new URL('../.local-certs/localhost-key.pem', import.meta.url);

https.createServer({ cert: readFileSync(certificate), key: readFileSync(key) }, (request, response) => {
  const upstream = http.request({
    hostname: '127.0.0.1', port: 3002, method: request.method,
    path: request.url, headers: request.headers,
  }, (result) => {
    response.writeHead(result.statusCode ?? 502, result.headers);
    result.pipe(response);
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end(); });
  request.pipe(upstream);
}).listen(3000, '127.0.0.1', () => {
  process.stdout.write('Local HTTPS proxy listening on https://127.0.0.1:3000\n');
});
