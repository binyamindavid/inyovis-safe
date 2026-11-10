import test from 'node:test';
import assert from 'node:assert/strict';
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
