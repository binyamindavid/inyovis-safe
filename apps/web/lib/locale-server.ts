import 'server-only';
import { cookies, headers } from 'next/headers';
import { resolveLocale } from './locale.mjs';
import { forwardToGo } from './api-forward.mjs';
import type { Locale } from './catalog';

export async function serverLocale(): Promise<Locale> {
  const [jar, requestHeaders] = await Promise.all([cookies(), headers()]);
  const token = jar.get('__Host-invoyis_session')?.value;
  let account: string | undefined;
  if (token) {
      try {
        const browserOrigin = process.env.BROWSER_ORIGIN;
        const forwardHeaders = new Headers({ Cookie: `__Host-invoyis_session=${token}` });
        const trustedHeader = process.env.TRUSTED_CLIENT_IP_HEADER;
        if (trustedHeader) { const value = requestHeaders.get(trustedHeader); if (value) forwardHeaders.set(trustedHeader, value); }
        const response = await forwardToGo(new Request('http://localhost/api/v1/account/language', { headers: forwardHeaders }), {
          path: '/v1/account/language', session: true, cookieNames: ['__Host-invoyis_session'],
          bufferResponse: true, browserOrigin, timeoutMs: 1500,
        });
        if (response.ok) account = (await response.json() as { language?: string }).language;
      } catch { /* The signed-out fallback also handles an unavailable API. */ }
  }
  return resolveLocale({ account, choice: jar.get('invoyis_locale')?.value, acceptLanguage: requestHeaders.get('accept-language') }) as Locale;
}
