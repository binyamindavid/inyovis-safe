import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { forwardArchive } from './archive-gateway.mjs';

test('archive gateway rejects paths and methods outside the route allowlist', async () => {
  const invalid = [
    ['GET', '/api/v1/archives/not-an-id'],
    ['GET', '/api/v1/archives/000000000000-0000-0000-0000-000000000000'],
    ['GET', '/api/v1/archives/00000000-0000-0000-0000-000000000001/records'],
    ['POST', '/api/v1/archives/00000000-0000-0000-0000-000000000001/members'],
    ['GET', '/api/v1/archives/00000000-0000-0000-0000-000000000001/upload-sessions'],
    ['POST', '/api/v1/archives/00000000-0000-0000-0000-000000000001/upload-sessions/not-a-uuid/complete'],
    ['POST', '/api/v1/archives/00000000-0000-0000-0000-000000000001/documents'],
    ['GET', '/api/v1/archives/00000000-0000-0000-0000-000000000001/documents/not-a-uuid/status'],
    ['GET', '/api/v1/archives/00000000-0000-0000-0000-000000000001/documents/00000000-0000-0000-0000-000000000002/preview/0'],
    ['POST', '/api/v1/archives/00000000-0000-0000-0000-000000000001/documents/00000000-0000-0000-0000-000000000002/preview/1'],
  ];
  for (const [method, path] of invalid) {
    const response = await forwardArchive(new Request(`https://localhost:3000${path}`, { method }));
    assert.equal(response.status, method === 'POST' && path.endsWith('/preview/1') || path.endsWith('/members') || path.endsWith('/upload-sessions') || path.endsWith('/documents') ? 405 : 404);
  }
});
test('cold preview can finish after the ordinary five-second gateway deadline', async () => {
  const server = createServer((_request, response) => {
    setTimeout(() => {
      response.setHeader('Content-Type', 'image/png');
      response.end(Buffer.from([137, 80, 78, 71]));
    }, 6100);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const oldBase = process.env.API_BASE_URL;
  const oldOrigin = process.env.BROWSER_ORIGIN;
  process.env.API_BASE_URL = `http://127.0.0.1:${server.address().port}`;
  process.env.BROWSER_ORIGIN = 'https://localhost:3000';
  try {
    const archive = '00000000-0000-4000-8000-000000000001';
    const document = '00000000-0000-4000-8000-000000000002';
    const response = await forwardArchive(new Request(`https://localhost:3000/api/v1/archives/${archive}/documents/${document}/preview/1`));
    assert.equal(response.status, 200);
    assert.equal(response.headers.get('content-type'), 'image/png');
  } finally {
    if (oldBase === undefined) delete process.env.API_BASE_URL; else process.env.API_BASE_URL = oldBase;
    if (oldOrigin === undefined) delete process.env.BROWSER_ORIGIN; else process.env.BROWSER_ORIGIN = oldOrigin;
    await new Promise(resolve => server.close(resolve));
  }
});
