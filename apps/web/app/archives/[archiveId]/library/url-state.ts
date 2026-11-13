// Library view state lives in the URL so Back, reloads and the review page's return link restore it.
// company and category show folders (company is the default); a folder is a supplier or category ID, or "none".
export type View = 'company' | 'category' | 'all' | 'review';
export type Grouping = 'company' | 'category';
export const isGrouping = (view: View): view is Grouping => view === 'company' || view === 'category';
const folderPattern = /^(?:none|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
export type LibraryState = { view: View; folder: string | null; query: string; categoryId: string; propertyId: string; paperLocationId: string; accountReference: string; issueDate: string; dateFrom: string; dateTo: string; total: string; currency: string; docId: string | null };
export const emptyState: LibraryState = { view: 'company', folder: null, query: '', categoryId: '', propertyId: '', paperLocationId: '', accountReference: '', issueDate: '', dateFrom: '', dateTo: '', total: '', currency: '', docId: null };

export function parseLibraryState(params: URLSearchParams): LibraryState {
  const raw = params.get('view');
  const view: View = raw === 'review' || raw === 'all' || raw === 'category' ? raw : 'company';
  const folder = params.get('folder');
  return {
    view,
    folder: isGrouping(view) && folder && folderPattern.test(folder) ? folder : null,
    query: params.get('q') ?? '',
    categoryId: params.get('category') ?? '',
    propertyId: params.get('property') ?? '',
    paperLocationId: params.get('paper') ?? '',
    accountReference: params.get('account') ?? '',
    issueDate: params.get('date') ?? '',
    dateFrom: params.get('from') ?? '',
    dateTo: params.get('to') ?? '',
    total: params.get('total') ?? '',
    currency: params.get('currency') ?? '',
    docId: params.get('doc'),
  };
}
// The library always names its view, so a Back to an earlier entry (even across a reload) never looks like a bare
// archive link, which restores the last view instead. Supplier history has no views and keeps bare URLs.
export function toLibrarySearch(st: LibraryState, { bare = false }: { bare?: boolean } = {}) {
  const q = new URLSearchParams();
  if (!bare) q.set('view', st.view);
  if (st.folder && isGrouping(st.view)) q.set('folder', st.folder);
  if (st.query) q.set('q', st.query);
  if (st.categoryId) q.set('category', st.categoryId);
  if (st.propertyId) q.set('property', st.propertyId);
  if (st.paperLocationId) q.set('paper', st.paperLocationId);
  if (st.accountReference) q.set('account', st.accountReference);
  if (st.issueDate) q.set('date', st.issueDate);
  if (st.dateFrom) q.set('from', st.dateFrom);
  if (st.dateTo) q.set('to', st.dateTo);
  if (st.total) q.set('total', st.total);
  if (st.currency) q.set('currency', st.currency);
  if (st.docId) q.set('doc', st.docId);
  const text = q.toString();
  return text ? `?${text}` : '';
}
export const lastViewKey = (archiveId: string, supplierId?: string) => `invoyis-library:${archiveId}${supplierId ? `:supplier:${supplierId}` : ''}`;
export const stateParams = ['view', 'folder', 'q', 'category', 'property', 'paper', 'account', 'date', 'from', 'to', 'total', 'currency', 'doc'];
export const lastArchiveKey = 'invoyis-library:last-archive';
