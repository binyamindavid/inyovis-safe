import { forwardToGo } from './api-forward.mjs';

const uuid = '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}';
const paths = [
  new RegExp(`^/archives/${uuid}$`),
  new RegExp(`^/archives/${uuid}/members$`),
  new RegExp(`^/archives/${uuid}/members/[0-9]+$`),
  new RegExp(`^/archives/${uuid}/invitations$`),
  new RegExp(`^/archives/${uuid}/invitations/${uuid}$`),
  new RegExp(`^/archives/${uuid}/upload-sessions$`),
  new RegExp(`^/archives/${uuid}/upload-sessions/${uuid}/complete$`),
];

export function forwardArchive(request) {
  const { pathname } = new URL(request.url);
  const path = pathname.replace(/^\/api\/v1/, '');
  if (!paths.some((allowed) => allowed.test(path))) return new Response(null, { status: 404 });
  const method = request.method;
  const allowed = path.endsWith('/members') ? ['GET']
    : /\/members\/[0-9]+$/.test(path) ? ['PATCH', 'DELETE']
    : path.endsWith('/invitations') ? ['GET', 'POST']
    : /\/invitations\//.test(path) ? ['DELETE']
    : path.endsWith('/upload-sessions') || path.endsWith('/complete') ? ['POST']
    : ['GET', 'PATCH'];
  if (!allowed.includes(method)) return new Response(null, { status: 405 });
  return forwardToGo(request, {
    path: `/v1${path}`, session: true, cookieNames: ['__Host-invoyis_session'],
    mutation: method !== 'GET', bufferResponse: true,
  });
}

export function forwardArchiveRoot(request) {
  return forwardToGo(request, {
    path: '/v1/archives', session: true, cookieNames: ['__Host-invoyis_session'],
    mutation: request.method !== 'GET', bufferResponse: true,
  });
}
