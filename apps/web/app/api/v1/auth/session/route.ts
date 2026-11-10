import { forwardToGo } from '../../../../../lib/api-forward.mjs';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export function GET(request: Request): Promise<Response> {
  return forwardToGo(request, { path: '/v1/auth/session', session: true, cookieNames: ['__Host-invoyis_session'], bufferResponse: true });
}
