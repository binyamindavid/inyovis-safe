export function canResolveDuplicate(role) {
  return role === 'admin' || role === 'contributor';
}

// A successful decision exposes the next server page. A conflict means the
// compared revisions or another reviewer's decision changed this row.
export async function resolveAndRefresh(send, refresh) {
  const result = await send();
  if (result.data || result.response?.status === 409) await refresh();
  return result;
}
