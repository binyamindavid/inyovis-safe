const forwardedRequestHeaders = ['accept', 'accept-language'];
const forwardedResponseHeaders = ['content-type', 'content-language', 'cache-control', 'location'];

/**
 * Route-specific proxy policy. Only server route handlers should call this.
 * Session routes must opt in to cookie forwarding and no-store responses.
 * @param {Request} request
 * @param {{ path: string, session?: boolean, cookieNames?: string[], bufferResponse?: boolean, timeoutMs?: number, apiBaseUrl?: string, fetchImpl?: typeof fetch }} options
 */
export async function forwardToGo(request, options) {
  const requestId = crypto.randomUUID();
  const headers = new Headers();
  for (const name of forwardedRequestHeaders) {
    const value = request.headers.get(name);
    if (value !== null) headers.set(name, value);
  }
  if (options.session && options.cookieNames?.length) {
    const allowed = new Set(options.cookieNames);
    const cookies = (request.headers.get('cookie') ?? '').split(';')
      .map((part) => part.trim())
      .filter((part) => allowed.has(part.split('=', 1)[0]));
    if (cookies.length) headers.set('cookie', cookies.join('; '));
  }
  headers.set('X-Request-ID', requestId);

  const baseUrl = options.apiBaseUrl ?? process.env.API_BASE_URL ?? 'http://127.0.0.1:8080';
  let upstream;
  let bufferedBody;
  const deadline = AbortSignal.timeout(options.timeoutMs ?? 5000);
  try {
    const target = new URL(options.path, baseUrl);
    upstream = await (options.fetchImpl ?? fetch)(target, {
      method: request.method,
      headers,
      redirect: 'manual',
      cache: 'no-store',
      signal: deadline,
    });
    // Health is tiny. Finish reading it before committing an upstream 200 to the client.
    if (options.bufferResponse) bufferedBody = await upstream.arrayBuffer();
  } catch (error) {
    const timeout = deadline.aborted || error?.name === 'TimeoutError';
    const status = timeout ? 504 : 502;
    const code = timeout ? 'UPSTREAM_TIMEOUT' : 'UPSTREAM_UNAVAILABLE';
    return Response.json({
      type: 'about:blank',
      title: timeout ? 'Gateway Timeout' : 'Bad Gateway',
      status,
      code,
      requestId,
    }, {
      status,
      headers: {
        'Content-Type': 'application/problem+json',
        'X-Request-ID': requestId,
        'Cache-Control': 'no-store',
      },
    });
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
  if (options.session) responseHeaders.set('Cache-Control', 'no-store');
  return new Response([204, 304].includes(upstream.status) ? null : (options.bufferResponse ? bufferedBody : upstream.body), {
    status: upstream.status,
    headers: responseHeaders,
  });
}
