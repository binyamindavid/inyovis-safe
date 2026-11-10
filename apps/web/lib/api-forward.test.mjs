import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { after, before, test } from 'node:test';
import { forwardToGo } from './api-forward.mjs';

let server;
let baseUrl;
let observed;

before(async () => {
  server = createServer((req, res) => {
    observed = {
      requestId: req.headers['x-request-id'],
      cookie: req.headers.cookie,
      secret: req.headers['x-secret'],
    };
    if (req.url === '/slow') {
      setTimeout(() => { if (!res.destroyed) res.end('late'); }, 100);
      return;
    }
    if (req.url === '/partial') {
      res.setHeader('Content-Type', 'application/json');
      res.write('{"status":"');
      setTimeout(() => { if (!res.destroyed) res.end('ok"}'); }, 100);
      return;
    }
    res.setHeader('X-Request-ID', observed.requestId);
    if (req.url === '/session') {
      res.statusCode = 401;
      res.setHeader('Content-Type', 'application/problem+json');
      res.setHeader('Set-Cookie', ['session=deleted; Path=/; HttpOnly', 'csrf=deleted; Path=/; HttpOnly']);
      res.end(JSON.stringify({ type: 'about:blank', title: 'Unauthorized', status: 401, code: 'UNAUTHORIZED', requestId: observed.requestId }));
      return;
    }
    res.setHeader('Content-Type', 'application/json');
    res.end('{"status":"ok"}\n');
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  baseUrl = `http://127.0.0.1:${server.address().port}`;
});

after(async () => {
  await new Promise((resolve) => server.close(resolve));
});

test('health forwards a generated request ID and keeps the upstream response', async () => {
  const response = await forwardToGo(new Request('http://localhost/api/v1/health', {
    headers: { 'X-Secret': 'should-not-forward' },
  }), { path: '/healthz', apiBaseUrl: baseUrl, bufferResponse: true });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { status: 'ok' });
  assert.match(response.headers.get('x-request-id'), /^[0-9a-f-]{36}$/);
  assert.equal(response.headers.get('x-request-id'), observed.requestId);
  assert.equal(observed.secret, undefined);
  assert.equal(observed.cookie, undefined);
});

test('session policy forwards cookies, preserves problem and separate Set-Cookie headers', async () => {
  const response = await forwardToGo(new Request('http://localhost/api/v1/session', {
    headers: { Cookie: 'session=synthetic; analytics=discard', 'X-Secret': 'should-not-forward' },
  }), { path: '/session', session: true, cookieNames: ['session'], apiBaseUrl: baseUrl });
  assert.equal(observed.cookie, 'session=synthetic');
  assert.equal(observed.secret, undefined);
  assert.equal(response.status, 401);
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.equal(response.headers.get('content-type'), 'application/problem+json');
  assert.equal(response.headers.getSetCookie().length, 2);
  const problem = await response.json();
  assert.equal(problem.code, 'UNAUTHORIZED');
  assert.equal(problem.requestId, response.headers.get('x-request-id'));
});

test('timeout returns a correlated RFC 9457 problem', async () => {
  const response = await forwardToGo(new Request('http://localhost/api/v1/health'), {
    path: '/slow', apiBaseUrl: baseUrl, timeoutMs: 10, bufferResponse: true,
  });
  assert.equal(response.status, 504);
  assert.equal(response.headers.get('content-type'), 'application/problem+json');
  const problem = await response.json();
  assert.equal(problem.code, 'UPSTREAM_TIMEOUT');
  assert.equal(problem.requestId, response.headers.get('x-request-id'));
});

test('a stalled body after 200 headers becomes a complete 504 problem', async () => {
  const response = await forwardToGo(new Request('http://localhost/api/v1/health'), {
    path: '/partial', apiBaseUrl: baseUrl, timeoutMs: 10, bufferResponse: true,
  });
  assert.equal(response.status, 504);
  assert.equal(response.headers.get('content-type'), 'application/problem+json');
  const problem = await response.json();
  assert.equal(problem.status, 504);
  assert.equal(problem.code, 'UPSTREAM_TIMEOUT');
  assert.equal(problem.requestId, response.headers.get('x-request-id'));
});

test('connection failure returns a correlated RFC 9457 problem', async () => {
  const response = await forwardToGo(new Request('http://localhost/api/v1/health'), {
    path: '/healthz', apiBaseUrl: 'http://127.0.0.1:1',
  });
  assert.equal(response.status, 502);
  const problem = await response.json();
  assert.equal(problem.code, 'UPSTREAM_UNAVAILABLE');
  assert.equal(problem.requestId, response.headers.get('x-request-id'));
});
