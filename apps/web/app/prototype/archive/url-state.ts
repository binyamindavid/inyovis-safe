import { archives, filterKeys, type FilterKey, type Filters } from './fixtures';
import type { ProtoLocale } from './words';

export type View = 'all' | 'review';
export type Step = 'preview' | 'review';
export type UrlState = { locale: ProtoLocale; archiveId: string; view: View; query: string; filters: Filters; docId: string | null; step: Step };

const paramFor: Record<FilterKey, string> = { category: 'category', property: 'property', paperLocation: 'paper' };

export function parseUrlState(params: URLSearchParams, fallbackLocale: ProtoLocale): UrlState {
  const lang = params.get('lang');
  const archiveId = archives.some(a => a.id === params.get('archive')) ? params.get('archive')! : archives[0].id;
  const filters: Filters = {};
  for (const key of filterKeys) { const v = params.get(paramFor[key]); if (v) filters[key] = v; }
  return {
    locale: lang === 'en' || lang === 'fr' || lang === 'he' ? lang : fallbackLocale,
    archiveId, view: params.get('view') === 'review' ? 'review' : 'all', query: params.get('q') ?? '', filters,
    docId: params.get('doc'), step: params.get('step') === 'review' ? 'review' : 'preview',
  };
}
export function toSearch(st: UrlState) {
  const q = new URLSearchParams();
  q.set('lang', st.locale); q.set('archive', st.archiveId);
  if (st.view === 'review') q.set('view', 'review');
  if (st.query) q.set('q', st.query);
  for (const key of filterKeys) if (st.filters[key]) q.set(paramFor[key], st.filters[key]!);
  if (st.docId) { q.set('doc', st.docId); if (st.step === 'review') q.set('step', 'review'); }
  return `?${q}`;
}
