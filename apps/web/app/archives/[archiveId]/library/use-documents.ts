'use client';
import { useCallback, useEffect, useRef, useState } from 'react';

export type DocumentRow = {
  id: string; status: string; originalFilename: string; confirmedType?: string | null; confirmedTitle?: string | null; issueDate?: string | null;
  printedSupplier?: string | null; documentNumber?: string | null; accountReference?: string | null; total?: string | null; currency?: string | null;
  supplierId?: string | null; supplierName?: string | null; categoryId?: string | null; categoryName?: string | null; propertyId?: string | null; propertyName?: string | null;
  paperLocationId?: string | null; paperLocationName?: string | null; referenceScan: boolean;
};
/** Server-side filters. Search (q/types) and status run in the API so they cover the whole archive, not just loaded pages. */
export type ServerFilters = {
  /** A filing ID, or "none" for documents without one (the Unassigned company and Uncategorised folders). */
  supplierId?: string; categoryId?: string; propertyId?: string; paperLocationId?: string; accountReference?: string; issueDate?: string; dateFrom?: string; dateTo?: string; total?: string; currency?: string;
  status?: 'ready' | 'attention' | 'needs_review'; q?: string; types?: string;
};

/** Statuses that will change on their own, so lists keep polling quickly while any are present. */
export const IN_FLIGHT = new Set(['pending', 'processing']);

function query(filters: ServerFilters, cursor: string) {
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) if (value) q.set(key, value);
  if (cursor) q.set('cursor', cursor);
  return q;
}

/**
 * One cursor-paginated document list. `loadMore` appends the next page; polling re-fetches the pages already shown
 * so status changes appear in place. Pass an empty archiveId to disable.
 */
export function usePagedDocuments(archiveId: string, filters: ServerFilters, { fast, supplierId, enabled = true, initialPages = 1 }: { fast: boolean; supplierId?: string; enabled?: boolean; initialPages?: number }) {
  const [items, setItems] = useState<DocumentRow[]>([]);
  const [next, setNext] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState(false);
  const generation = useRef(0);
  const busy = useRef(false);
  // Pages to load on the very first fetch (restoring a list position); later filter changes start from one page.
  const firstPages = useRef(Math.min(Math.max(1, Math.floor(initialPages)), 25));
  const [pages, setPages] = useState(firstPages.current);
  const base = supplierId ? `/api/v1/archives/${archiveId}/suppliers/${supplierId}/documents` : `/api/v1/archives/${archiveId}/documents`;
  const key = `${base}?${query(filters, '')}`;
  const initialKey = useRef(key);
  const active = Boolean(archiveId) && enabled;

  // Fetches `count` pages from the start. Used for the first load and for polling.
  const load = useCallback(async (count: number, initial: boolean) => {
    if (!active || (busy.current && !initial)) return;
    const mine = ++generation.current; busy.current = true;
    if (initial) setLoading(true);
    try {
      const rows: DocumentRow[] = []; let cursor = ''; let fetched = 0;
      do {
        const response = await fetch(`${base}?${query(filters, cursor)}`, { credentials: 'same-origin', cache: 'no-store' });
        if (mine !== generation.current) return;
        if (!response.ok) { setError(true); return; }
        const data = await response.json() as { items: DocumentRow[]; nextCursor?: string };
        rows.push(...data.items); cursor = data.nextCursor ?? ''; fetched++;
      } while (cursor && fetched < count);
      if (mine !== generation.current) return;
      setItems(rows); setNext(cursor); setPages(Math.max(1, fetched)); setError(false);
    } catch { if (mine === generation.current) setError(true); }
    finally { if (mine === generation.current) { busy.current = false; setLoading(false); } }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, active]);

  useEffect(() => {
    if (!active) { setItems([]); setNext(''); setLoading(false); return; }
    // Effects can run twice in development, so derive the page count instead of consuming it.
    const count = key === initialKey.current ? firstPages.current : 1;
    setPages(count); void load(count, true);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, active]);

  const loadMore = useCallback(async () => {
    if (!next || loadingMore) return;
    const mine = ++generation.current; busy.current = true; setLoadingMore(true);
    try {
      const response = await fetch(`${base}?${query(filters, next)}`, { credentials: 'same-origin', cache: 'no-store' });
      if (mine !== generation.current) return;
      if (!response.ok) { setError(true); return; }
      const data = await response.json() as { items: DocumentRow[]; nextCursor?: string };
      if (mine !== generation.current) return;
      setItems(prev => [...prev, ...data.items.filter(row => !prev.some(p => p.id === row.id))]);
      setNext(data.nextCursor ?? ''); setPages(p => p + 1); setError(false);
    } catch { if (mine === generation.current) setError(true); }
    finally { if (mine === generation.current) busy.current = false; setLoadingMore(false); }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, next, loadingMore]);

  const inFlight = items.some(item => IN_FLIGHT.has(item.status));
  useEffect(() => {
    if (!active) return;
    const every = inFlight || fast ? 5000 : 30000;
    const timer = window.setInterval(() => { if (!document.hidden) void load(pages, false); }, every);
    return () => window.clearInterval(timer);
  }, [load, inFlight, fast, pages, active]);

  return { items, pages, loading: active && loading, loadingMore, error, hasMore: Boolean(next), loadMore, refresh: useCallback(() => load(pages, false), [load, pages]) };
}

export type DocumentCounts = { total: number; needsReview: number; attention: number };
/** Archive-wide counts for the sidebar and badges, independent of the list's filters and paging. */
export function useDocumentCounts(archiveId: string, fast: boolean) {
  const [counts, setCounts] = useState<DocumentCounts | null>(null);
  const load = useCallback(async () => {
    if (!archiveId) return;
    try {
      const response = await fetch(`/api/v1/archives/${archiveId}/document-counts`, { credentials: 'same-origin', cache: 'no-store' });
      if (response.ok) setCounts(await response.json() as DocumentCounts);
    } catch { /* counts are advisory; keep the last known values */ }
  }, [archiveId]);
  useEffect(() => { setCounts(null); void load(); }, [load]);
  const busy = fast || Boolean(counts && counts.attention > counts.needsReview);
  useEffect(() => {
    const timer = window.setInterval(() => { if (!document.hidden) void load(); }, busy ? 5000 : 30000);
    return () => window.clearInterval(timer);
  }, [load, busy]);
  return { counts, refresh: load };
}

export type Choice = { id: string; displayName: string };
export type ChoiceKind = 'categories' | 'properties' | 'paper-locations';
export function useChoices(archiveId: string) {
  const [choices, setChoices] = useState<Record<ChoiceKind, Choice[]>>({ categories: [], properties: [], 'paper-locations': [] });
  useEffect(() => {
    if (!archiveId) return; let live = true;
    Promise.all((['categories', 'properties', 'paper-locations'] as ChoiceKind[]).map(async kind => {
      const r = await fetch(`/api/v1/archives/${archiveId}/organisation/${kind}`, { credentials: 'same-origin' }).catch(() => null);
      return [kind, r?.ok ? ((await r.json()).items as Choice[]) : []] as const;
    })).then(rows => { if (live) setChoices(Object.fromEntries(rows) as Record<ChoiceKind, Choice[]>); });
    return () => { live = false; };
  }, [archiveId]);
  return choices;
}
