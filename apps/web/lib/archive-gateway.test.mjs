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
test('original and export responses retain attachment headers and exact bytes', async () => {
  const archive = '00000000-0000-4000-8000-000000000001';
  const document = '00000000-0000-4000-8000-000000000002';
  const oldBase = process.env.API_BASE_URL;
  const oldOrigin = process.env.BROWSER_ORIGIN;
  process.env.API_BASE_URL = 'http://127.0.0.1:1';
  process.env.BROWSER_ORIGIN = 'https://localhost:3000';
  const originalFetch = globalThis.fetch;
  const payload = new Uint8Array([0, 255, 1, 2, 42]);
  globalThis.fetch = async (url) => new Response(payload, { headers: {
    'Content-Type': String(url).includes('/exports/') ? 'application/zip' : 'application/pdf',
    'Content-Disposition': 'attachment; filename="safe.pdf"',
  } });
  try {
    const response = await forwardArchive(new Request(`https://localhost:3000/api/v1/archives/${archive}/documents/${document}/original`));
    assert.equal(response.status, 200);
    assert.equal(response.headers.get('content-disposition'), 'attachment; filename="safe.pdf"');
    assert.equal(response.headers.get('cache-control'), 'private, no-store');
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()), payload);
    const bundle = await forwardArchive(new Request(`https://localhost:3000/api/v1/archives/${archive}/exports/${document}`));
    assert.equal(bundle.headers.get('content-type'), 'application/zip');
    assert.equal(bundle.headers.get('cache-control'), 'private, no-store');
    assert.deepEqual(new Uint8Array(await bundle.arrayBuffer()), payload);
    const deniedMethod = await forwardArchive(new Request(`https://localhost:3000/api/v1/archives/${archive}/documents/${document}/original`, { method: 'POST' }));
    assert.equal(deniedMethod.status, 405);
  } finally {
    globalThis.fetch = originalFetch;
    if (oldBase === undefined) delete process.env.API_BASE_URL; else process.env.API_BASE_URL = oldBase;
    if (oldOrigin === undefined) delete process.env.BROWSER_ORIGIN; else process.env.BROWSER_ORIGIN = oldOrigin;
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

test('review gateway forwards a valid 32-line payload above 4 KiB and retains a bounded limit', async () => {
  const archive = '00000000-0000-4000-8000-000000000001';
  const document = '00000000-0000-4000-8000-000000000002';
  const url = `https://localhost:3000/api/v1/archives/${archive}/documents/${document}/review`;
  const amount = '1234567890123456.12345678';
  const payload = JSON.stringify({
    action: 'draft', expectedVersion: 0, idempotencyKey: crypto.randomUUID(), sourceRunId: null,
    metadata: { documentType: 'invoice', supplier: 'S'.repeat(400), invoiceId: 'I'.repeat(400), accountReference: 'A'.repeat(400), issueDate: '2026-11-11', currency: 'USD', subtotal: amount, tax: null, total: amount, taxLines: Array.from({ length: 32 }, () => ({ amount, currency: 'USD' })) },
    acknowledgedWarnings: [], inputLocale: 'en',
    rawAmounts: { subtotal: amount, tax: null, total: amount, taxLines: Array(32).fill(amount) },
  });
  assert.ok(Buffer.byteLength(payload) > 4096);
  assert.ok(Buffer.byteLength(payload) < 16 * 1024);
  const oldBase = process.env.API_BASE_URL;
  const oldOrigin = process.env.BROWSER_ORIGIN;
  process.env.API_BASE_URL = 'http://127.0.0.1:1';
  process.env.BROWSER_ORIGIN = 'https://localhost:3000';
  try {
    const observed = [];
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async (_target, options) => { observed.push(Buffer.from(options.body).toString()); return Response.json({ ok: true }); };
    try {
      const response = await forwardArchive(new Request(url, { method: 'PUT', body: payload, headers: { 'Content-Type': 'application/json' } }));
      assert.equal(response.status, 200);
      assert.deepEqual(observed, [payload]);
      const oversized = await forwardArchive(new Request(url, { method: 'PUT', body: 'x'.repeat(16 * 1024 + 1), headers: { 'Content-Type': 'application/json' } }));
      assert.equal(oversized.status, 413);
      assert.equal(observed.length, 1);
    } finally { globalThis.fetch = originalFetch; }
  } finally {
    if (oldBase === undefined) delete process.env.API_BASE_URL; else process.env.API_BASE_URL = oldBase;
    if (oldOrigin === undefined) delete process.env.BROWSER_ORIGIN; else process.env.BROWSER_ORIGIN = oldOrigin;
  }
});
