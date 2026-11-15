import test from 'node:test';
import assert from 'node:assert/strict';
import { forwardInbound } from './inbound-gateway.mjs';

test('inbound proxy preserves exact multipart bytes and only signed headers', async () => {
  const oldBase = process.env.API_BASE_URL;
  const originalFetch = globalThis.fetch;
  process.env.API_BASE_URL = 'http://127.0.0.1:1';
  const raw = Buffer.from([0, 13, 10, 255, 128, 42, 0]);
  let seen;
  globalThis.fetch = async (url, options) => {
    seen = { url: String(url), body: options.body, headers: options.headers };
    return Response.json({ outcome: 'accepted' });
  };
  try {
    const request = new Request('https://localhost:3000/api/inbound/sendgrid', { method: 'POST', body: raw, headers: {
      'content-type': 'multipart/form-data; boundary=exact',
      'x-twilio-email-event-webhook-timestamp': '1767225600',
      'x-twilio-email-event-webhook-signature': 'signature',
      cookie: 'private-session=should-not-forward',
    } });
    const response = await forwardInbound(request);
    assert.equal(response.status, 200);
    assert.equal(seen.url, 'http://127.0.0.1:1/v1/inbound/sendgrid');
    assert.deepEqual(seen.body, raw);
    assert.equal(seen.headers['content-type'], 'multipart/form-data; boundary=exact');
    assert.equal(seen.headers['x-twilio-email-event-webhook-timestamp'], '1767225600');
    assert.equal(seen.headers['x-twilio-email-event-webhook-signature'], 'signature');
    assert.equal(Object.hasOwn(seen.headers, 'cookie'), false);
  } finally {
    globalThis.fetch = originalFetch;
    if (oldBase === undefined) delete process.env.API_BASE_URL; else process.env.API_BASE_URL = oldBase;
  }
});

test('inbound proxy rejects transport size before forwarding', async () => {
  const response = await forwardInbound(new Request('https://localhost:3000/api/inbound/sendgrid', {
    method: 'POST', body: 'x', headers: { 'content-length': String(64 * 1024 * 1024 + 1) },
  }));
  assert.equal(response.status, 413);
});
