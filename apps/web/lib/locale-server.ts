import 'server-only';
import { cookies, headers } from 'next/headers';
import { resolveLocale } from './locale.mjs';
import type { Locale } from './catalog';

export async function serverLocale(): Promise<Locale> {
  const [jar, requestHeaders] = await Promise.all([cookies(), headers()]);
  const token = jar.get('__Host-invoyis_session')?.value;
  let account: string | undefined;
  if (token && process.env.API_GATEWAY_SECRET) {
    const trustedHeader = process.env.TRUSTED_CLIENT_IP_HEADER;
    const clientIP = trustedHeader ? requestHeaders.get(trustedHeader) : '127.0.0.1';
    if (clientIP) {
      try {
        const response = await fetch(new URL('/v1/account/language', process.env.API_BASE_URL ?? 'http://127.0.0.1:8080'), {
          headers: { Cookie: `__Host-invoyis_session=${token}`, 'X-Invoyis-Gateway-Key': process.env.API_GATEWAY_SECRET,
            'X-Invoyis-Client-IP': clientIP, 'X-Request-ID': crypto.randomUUID() },
          cache: 'no-store', signal: AbortSignal.timeout(1500),
        });
        if (response.ok) account = (await response.json() as { language?: string }).language;
      } catch { /* The signed-out fallback also handles an unavailable API. */ }
    }
  }
  return resolveLocale({ account, choice: jar.get('invoyis_locale')?.value, acceptLanguage: requestHeaders.get('accept-language') }) as Locale;
}
