import { forwardToGo } from '../../../../../lib/api-forward.mjs';
export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';
export function POST(request: Request): Promise<Response> {
  return forwardToGo(request, { path: '/v1/invitations/accept', session: true, cookieNames: ['__Host-invoyis_session'], mutation: true, bufferResponse: true });
}
