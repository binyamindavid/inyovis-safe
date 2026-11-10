import { forwardToGo } from '../../../../lib/api-forward.mjs';

export const dynamic = 'force-dynamic';

export function GET(request: Request): Promise<Response> {
  return forwardToGo(request, { path: '/healthz', bufferResponse: true });
}
