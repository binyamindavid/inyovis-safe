// Library view state lives in the URL so Back, reloads and the review page's return link restore it.
export type View = 'all' | 'review';
export type LibraryState = { view: View; query: string; categoryId: string; propertyId: string; paperLocationId: string; accountReference: string; docId: string | null };
export const emptyState: LibraryState = { view: 'all', query: '', categoryId: '', propertyId: '', paperLocationId: '', accountReference: '', docId: null };

export function parseLibraryState(params: URLSearchParams): LibraryState {
  return {
    view: params.get('view') === 'review' ? 'review' : 'all',
    query: params.get('q') ?? '',
    categoryId: params.get('category') ?? '',
    propertyId: params.get('property') ?? '',
    paperLocationId: params.get('paper') ?? '',
    accountReference: params.get('account') ?? '',
    docId: params.get('doc'),
  };
}
export function toLibrarySearch(st: LibraryState) {
  const q = new URLSearchParams();
  if (st.view === 'review') q.set('view', 'review');
  if (st.query) q.set('q', st.query);
  if (st.categoryId) q.set('category', st.categoryId);
  if (st.propertyId) q.set('property', st.propertyId);
  if (st.paperLocationId) q.set('paper', st.paperLocationId);
  if (st.accountReference) q.set('account', st.accountReference);
  if (st.docId) q.set('doc', st.docId);
  const text = q.toString();
  return text ? `?${text}` : '';
}
export const lastViewKey = (archiveId: string, supplierId?: string) => `invoyis-library:${archiveId}${supplierId ? `:supplier:${supplierId}` : ''}`;
export const stateParams = ['view', 'q', 'category', 'property', 'paper', 'account', 'doc'];
export const lastArchiveKey = 'invoyis-library:last-archive';
