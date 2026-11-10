import { NextResponse } from 'next/server';
import { normalizeLocale } from '../../../lib/locale.mjs';
export const runtime = 'nodejs';
export async function POST(request: Request) {
  const browserOrigin = process.env.BROWSER_ORIGIN ?? new URL(request.url).origin;
  if (request.headers.get('origin') !== browserOrigin || request.headers.get('x-invoyis-intent') !== 'browser') {
    return new Response(null, { status: 403 });
  }
  const body = await request.json().catch(() => null) as { language?: string } | null;
  const language = normalizeLocale(body?.language);
  if (!language || body?.language !== language) return new Response(null, { status: 422 });
  const response = NextResponse.json({ language });
  response.cookies.set('invoyis_locale', language, { httpOnly: true, secure: new URL(request.url).protocol === 'https:', sameSite: 'lax', path: '/', maxAge: 365 * 24 * 60 * 60 });
  response.headers.set('Cache-Control', 'no-store');
  return response;
}
