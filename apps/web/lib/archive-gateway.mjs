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
  new RegExp(`^/archives/${uuid}/documents$`),
  new RegExp(`^/archives/${uuid}/documents/${uuid}/status$`),
  new RegExp(`^/archives/${uuid}/documents/${uuid}/review$`),
  new RegExp(`^/archives/${uuid}/documents/${uuid}/preview/[1-9][0-9]*$`),
  new RegExp(`^/archives/${uuid}/documents/${uuid}/original$`),
  new RegExp(`^/archives/${uuid}/exports$`),
  new RegExp(`^/archives/${uuid}/exports/${uuid}$`),
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
    : path.endsWith('/review') ? ['GET', 'PUT']
    : path.endsWith('/exports') ? ['POST']
    : /\/exports\/[0-9a-fA-F-]+$/.test(path) || path.endsWith('/original') ? ['GET']
    : /\/preview\/[0-9]+$/.test(path) ? ['GET']
    : path.endsWith('/documents') || path.endsWith('/status') ? ['GET']
    : ['GET', 'PATCH'];
  if (!allowed.includes(method)) return new Response(null, { status: 405 });
  return forwardToGo(request, {
    path: `/v1${path}`, session: true, cookieNames: ['__Host-invoyis_session'],
    mutation: method !== 'GET', bufferResponse: !path.endsWith('/original') && !/\/exports\/[0-9a-fA-F-]+$/.test(path),
    maxBodyBytes: method === 'PUT' && path.endsWith('/review') ? 16 * 1024 : undefined,
    timeoutMs: path.endsWith('/exports') ? 120000 : /\/preview\/[0-9]+$/.test(path) ? 30000 : /\/exports\/[0-9a-fA-F-]+$/.test(path) ? 120000 : 30000,
  });
}

export function forwardArchiveRoot(request) {
  return forwardToGo(request, {
    path: '/v1/archives', session: true, cookieNames: ['__Host-invoyis_session'],
    mutation: request.method !== 'GET', bufferResponse: true,
  });
}
