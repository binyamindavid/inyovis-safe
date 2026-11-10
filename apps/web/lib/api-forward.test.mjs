import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { after, before, test } from 'node:test';
import { forwardToGo } from './api-forward.mjs';

let server;
let baseUrl;
let observed;
const originalBrowserOrigin = process.env.BROWSER_ORIGIN;
const originalTrustedHeader = process.env.TRUSTED_CLIENT_IP_HEADER;

before(async () => {
  process.env.BROWSER_ORIGIN = 'https://localhost:3000';
  delete process.env.TRUSTED_CLIENT_IP_HEADER;
  server = createServer((req, res) => {
    observed = {
      requestId: req.headers['x-request-id'],
      cookie: req.headers.cookie,
      secret: req.headers['x-secret'],
      origin: req.headers.origin,
      intent: req.headers['x-invoyis-intent'],
      clientIP: req.headers['x-invoyis-client-ip'],
    };
    if (req.url === '/auth') {
      const parts = [];
      req.on('data', (chunk) => parts.push(chunk));
      req.on('end', () => {
        observed.body = Buffer.concat(parts).toString();
        res.statusCode = 429;
        res.setHeader('Content-Type', 'application/problem+json');
        res.setHeader('Retry-After', '30');
        res.setHeader('Set-Cookie', ['session=one; Path=/; HttpOnly', 'csrf=two; Path=/; HttpOnly']);
        res.end(JSON.stringify({ type: 'about:blank', title: 'Too Many Requests', status: 429, code: 'RATE_LIMITED', requestId: observed.requestId }));
      });
      return;
    }
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
  if (originalBrowserOrigin === undefined) delete process.env.BROWSER_ORIGIN;
  else process.env.BROWSER_ORIGIN = originalBrowserOrigin;
  if (originalTrustedHeader === undefined) delete process.env.TRUSTED_CLIENT_IP_HEADER;
  else process.env.TRUSTED_CLIENT_IP_HEADER = originalTrustedHeader;
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

test('mutation policy forwards bounded JSON, original Origin, and selected cookie', async () => {
  const body = JSON.stringify({ email: 'pilot@example.test' });
  const response = await forwardToGo(new Request('http://localhost/api/v1/auth/code/request', {
    method: 'POST', body,
    headers: { Origin: 'https://localhost:3001', 'X-Invoyis-Intent': 'browser', 'Content-Type': 'application/json', Cookie: 'session=synthetic; analytics=discard', 'X-Secret': 'discard' },
  }), { path: '/auth', apiBaseUrl: baseUrl, mutation: true, session: true, cookieNames: ['session'], bufferResponse: true });
  assert.equal(observed.body, body);
  assert.equal(observed.origin, 'https://localhost:3001');
  assert.equal(observed.intent, 'browser');
  assert.equal(observed.cookie, 'session=synthetic');
  assert.equal(observed.clientIP, '127.0.0.1');
  assert.equal(observed.secret, undefined);
  assert.equal(response.status, 429);
  assert.equal(response.headers.get('retry-after'), '30');
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.equal(response.headers.getSetCookie().length, 2);
  assert.equal((await response.json()).requestId, response.headers.get('x-request-id'));
});

test('mutation policy rejects an oversized body before forwarding', async () => {
  const response = await forwardToGo(new Request('http://localhost/api/v1/auth/code/request', {
    method: 'POST', body: 'x'.repeat(4097), headers: { 'Content-Type': 'application/json' },
  }), { path: '/auth', apiBaseUrl: baseUrl, mutation: true, session: true });
  assert.equal(response.status, 413);
  assert.equal((await response.json()).code, 'VALIDATION_ERROR');
});

test('nonlocal auth traffic requires a trusted client IP header', async () => {
  const response = await forwardToGo(new Request('https://app.example/api/v1/auth/session'), {
    path: '/session', session: true, apiBaseUrl: baseUrl, browserOrigin: 'https://app.example',
  });
  assert.equal(response.status, 503);
  assert.equal((await response.json()).requestId, response.headers.get('x-request-id'));
});

test('trusted ingress IP is forwarded without a shared fallback', async () => {
  process.env.TRUSTED_CLIENT_IP_HEADER = 'X-Verified-Client-IP';
  try {
    const missing = await forwardToGo(new Request('https://app.example/api/v1/auth/session'), {
      path: '/session', session: true, apiBaseUrl: baseUrl, browserOrigin: 'https://app.example',
    });
    assert.equal(missing.status, 503);
    for (const address of ['198.51.100.1', '198.51.100.2']) {
      const response = await forwardToGo(new Request('https://app.example/api/v1/auth/session', {
        headers: { 'X-Verified-Client-IP': address },
      }), { path: '/session', session: true, apiBaseUrl: baseUrl, browserOrigin: 'https://app.example' });
      assert.equal(response.status, 401);
      assert.equal(observed.clientIP, address);
    }
  } finally { delete process.env.TRUSTED_CLIENT_IP_HEADER; }
});
