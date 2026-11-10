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
  ];
  for (const [method, path] of invalid) {
    const response = await forwardArchive(new Request(`https://localhost:3000${path}`, { method }));
    assert.equal(response.status, path.endsWith('/members') || path.endsWith('/upload-sessions') ? 405 : 404);
  }
});
