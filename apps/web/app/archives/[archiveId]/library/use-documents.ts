'use client';
import { useCallback, useEffect, useRef, useState } from 'react';

export type DocumentRow = {
  id: string; status: string; originalFilename: string; confirmedType?: string | null; confirmedTitle?: string | null; issueDate?: string | null;
  printedSupplier?: string | null; documentNumber?: string | null; accountReference?: string | null; total?: string | null; currency?: string | null;
  supplierId?: string | null; supplierName?: string | null; categoryId?: string | null; categoryName?: string | null; propertyId?: string | null; propertyName?: string | null;
  paperLocationId?: string | null; paperLocationName?: string | null; referenceScan: boolean;
};
export type ServerFilters = { categoryId?: string; propertyId?: string; paperLocationId?: string; accountReference?: string };

/** Statuses that will change on their own, so the list keeps polling quickly while any are present. */
export const IN_FLIGHT = new Set(['pending', 'processing']);
/** The library loads up to this many pages (20 rows each) so Needs review, counts and search cover the whole archive. */
export const MAX_PAGES = 10;

export function useDocuments(archiveId: string, filters: ServerFilters, fast: boolean, supplierId?: string) {
  const [items, setItems] = useState<DocumentRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [truncated, setTruncated] = useState(false);
  const generation = useRef(0);
  const busy = useRef(false);
  const key = `${archiveId}|${supplierId ?? ''}|${filters.categoryId ?? ''}|${filters.propertyId ?? ''}|${filters.paperLocationId ?? ''}|${filters.accountReference ?? ''}`;

  const load = useCallback(async (initial: boolean) => {
    if (!archiveId || (busy.current && !initial)) return;
    const mine = ++generation.current; busy.current = true;
    if (initial) setLoading(true);
    try {
      const rows: DocumentRow[] = []; let cursor = ''; let pages = 0;
      do {
        const q = new URLSearchParams();
        if (filters.categoryId) q.set('categoryId', filters.categoryId);
        if (filters.propertyId) q.set('propertyId', filters.propertyId);
        if (filters.paperLocationId) q.set('paperLocationId', filters.paperLocationId);
        if (filters.accountReference) q.set('accountReference', filters.accountReference);
        if (cursor) q.set('cursor', cursor);
        const base = supplierId ? `/api/v1/archives/${archiveId}/suppliers/${supplierId}/documents` : `/api/v1/archives/${archiveId}/documents`;
        const response = await fetch(`${base}?${q}`, { credentials: 'same-origin', cache: 'no-store' });
        if (mine !== generation.current) return;
        if (!response.ok) { setError(true); return; }
        const data = await response.json() as { items: DocumentRow[]; nextCursor?: string };
        rows.push(...data.items); cursor = data.nextCursor ?? ''; pages++;
      } while (cursor && pages < MAX_PAGES);
      if (mine !== generation.current) return;
      setItems(rows); setTruncated(Boolean(cursor)); setError(false);
    } catch { if (mine === generation.current) setError(true); }
    finally { if (mine === generation.current) { busy.current = false; setLoading(false); } }
  }, [key]);

  useEffect(() => { void load(true); }, [load]);
  const inFlight = items.some(item => IN_FLIGHT.has(item.status));
  useEffect(() => {
    const every = inFlight || fast ? 5000 : 30000;
    const timer = window.setInterval(() => { if (!document.hidden) void load(false); }, every);
    return () => window.clearInterval(timer);
  }, [load, inFlight, fast]);

  return { items, loading, error, truncated, refresh: () => load(false) };
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
