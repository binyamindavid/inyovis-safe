import { isIP } from 'node:net';

const forwardedRequestHeaders = ['accept', 'accept-language'];
const forwardedResponseHeaders = ['content-type', 'content-language', 'cache-control', 'content-security-policy', 'location', 'retry-after', 'x-content-type-options', 'x-preview-page-cap', 'x-preview-page-count'];

function localBrowserOrigin(value) {
  try {
    return ['localhost', '127.0.0.1', '[::1]'].includes(new URL(value).hostname);
  } catch { return false; }
}

function gatewayError(status, code, title, requestId) {
  return Response.json({ type: 'about:blank', title, status, code, requestId }, {
    status,
    headers: { 'Content-Type': 'application/problem+json', 'X-Request-ID': requestId, 'Cache-Control': 'no-store' },
  });
}

async function boundedBody(request, maxBytes) {
  if (!request.body) return undefined;
  const reader = request.body.getReader();
  const chunks = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > maxBytes) { await reader.cancel(); throw new RangeError('request too large'); }
    chunks.push(value);
  }
  const body = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) { body.set(chunk, offset); offset += chunk.byteLength; }
  return body;
}

/**
 * Route-specific proxy policy. Only server route handlers should call this.
 * Session routes must opt in to cookie forwarding and no-store responses.
 * @param {Request} request
 * @param {{ path: string, session?: boolean, cookieNames?: string[], mutation?: boolean, maxBodyBytes?: number, bufferResponse?: boolean, timeoutMs?: number, apiBaseUrl?: string, browserOrigin?: string, fetchImpl?: typeof fetch }} options
 */
export async function forwardToGo(request, options) {
  const requestId = crypto.randomUUID();
  const headers = new Headers();
  for (const name of forwardedRequestHeaders) {
    const value = request.headers.get(name);
    if (value !== null) headers.set(name, value);
  }
  if (options.mutation) {
    for (const name of ['origin', 'content-type', 'x-invoyis-intent']) {
      const value = request.headers.get(name);
      if (value !== null) headers.set(name, value);
    }
  }
  if (options.session && options.cookieNames?.length) {
    const allowed = new Set(options.cookieNames);
    const cookies = (request.headers.get('cookie') ?? '').split(';')
      .map((part) => part.trim())
      .filter((part) => allowed.has(part.split('=', 1)[0]));
    if (cookies.length) headers.set('cookie', cookies.join('; '));
  }
  headers.set('X-Request-ID', requestId);
  if (options.session) {
    const gatewayKey = process.env.API_GATEWAY_SECRET;
    if (gatewayKey) headers.set('X-Invoyis-Gateway-Key', gatewayKey);
    const trustedHeader = process.env.TRUSTED_CLIENT_IP_HEADER;
    const candidate = trustedHeader ? request.headers.get(trustedHeader) : null;
    if (trustedHeader) {
      if (!candidate || !isIP(candidate)) {
        console.error('Auth gateway: trusted client IP header is missing or invalid');
        return gatewayError(503, 'UPSTREAM_UNAVAILABLE', 'Service Unavailable', requestId);
      }
      headers.set('X-Invoyis-Client-IP', candidate);
    } else if (localBrowserOrigin(options.browserOrigin ?? process.env.BROWSER_ORIGIN)) {
      headers.set('X-Invoyis-Client-IP', '127.0.0.1');
    } else {
      console.error('Auth gateway: TRUSTED_CLIENT_IP_HEADER is required outside local development');
      return gatewayError(503, 'UPSTREAM_UNAVAILABLE', 'Service Unavailable', requestId);
    }
  }

  const baseUrl = options.apiBaseUrl ?? process.env.API_BASE_URL ?? 'http://127.0.0.1:8080';
  let upstream;
  let bufferedBody;
  const deadline = AbortSignal.timeout(options.timeoutMs ?? 5000);
  try {
    const body = options.mutation ? await boundedBody(request, options.maxBodyBytes ?? 4096) : undefined;
    const target = new URL(options.path, baseUrl);
    upstream = await (options.fetchImpl ?? fetch)(target, {
      method: request.method,
      headers,
      body,
      redirect: 'manual',
      cache: 'no-store',
      signal: deadline,
    });
    // Health is tiny. Finish reading it before committing an upstream 200 to the client.
    if (options.bufferResponse) bufferedBody = await upstream.arrayBuffer();
  } catch (error) {
    if (error instanceof RangeError) {
      return gatewayError(413, 'VALIDATION_ERROR', 'Payload Too Large', requestId);
    }
    const timeout = deadline.aborted || error?.name === 'TimeoutError';
    const status = timeout ? 504 : 502;
    const code = timeout ? 'UPSTREAM_TIMEOUT' : 'UPSTREAM_UNAVAILABLE';
    return gatewayError(status, code, timeout ? 'Gateway Timeout' : 'Bad Gateway', requestId);
  }

  const responseHeaders = new Headers();
  for (const name of forwardedResponseHeaders) {
    const value = upstream.headers.get(name);
    if (value !== null) responseHeaders.set(name, value);
  }
  for (const cookie of upstream.headers.getSetCookie()) {
    responseHeaders.append('Set-Cookie', cookie);
  }
  responseHeaders.set('X-Request-ID', requestId);
  if (options.session) responseHeaders.set('Cache-Control', upstream.headers.get('content-type') === 'image/png' ? 'private, no-store' : 'no-store');
  return new Response([204, 304].includes(upstream.status) ? null : (options.bufferResponse ? bufferedBody : upstream.body), {
    status: upstream.status,
    headers: responseHeaders,
  });
}
