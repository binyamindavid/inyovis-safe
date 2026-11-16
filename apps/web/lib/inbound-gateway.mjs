const BODY_CAP = 64 * 1024 * 1024;

/** Public, signed SendGrid endpoint. Forward the exact multipart bytes to Go. */
export async function forwardInbound(request) {
  const length = Number(request.headers.get('content-length') ?? '0');
  if (length > BODY_CAP) return new Response('request too large', { status: 413 });
  if (!request.body) return new Response('unauthorised', { status: 401 });
  const reader = request.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > BODY_CAP) { await reader.cancel(); return new Response('request too large', { status: 413 }); }
      chunks.push(value);
    }
  } catch { return new Response('request read failed', { status: 500 }); }
  const body = Buffer.concat(chunks.map(chunk => Buffer.from(chunk)));
  const base = process.env.API_BASE_URL;
  if (!base) return new Response('intake unavailable', { status: 503 });
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 120_000);
    try {
      const upstream = await fetch(new URL('/v1/inbound/sendgrid', base), {
        method: 'POST', body,
        headers: {
          'content-type': request.headers.get('content-type') ?? '',
          'x-twilio-email-event-webhook-timestamp': request.headers.get('x-twilio-email-event-webhook-timestamp') ?? '',
          'x-twilio-email-event-webhook-signature': request.headers.get('x-twilio-email-event-webhook-signature') ?? '',
        },
        signal: controller.signal,
        cache: 'no-store',
      });
      return new Response(upstream.body, { status: upstream.status, headers: { 'content-type': upstream.headers.get('content-type') ?? 'text/plain', 'cache-control': 'no-store' } });
    } finally { clearTimeout(timeout); }
  } catch { return new Response('intake unavailable', { status: 503 }); }
}
