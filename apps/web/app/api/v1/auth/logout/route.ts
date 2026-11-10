import { forwardToGo } from '../../../../../lib/api-forward.mjs';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export function POST(request: Request): Promise<Response> {
  return forwardToGo(request, { path: '/v1/auth/logout', mutation: true, session: true, cookieNames: ['__Host-invoyis_session'], bufferResponse: true });
}
