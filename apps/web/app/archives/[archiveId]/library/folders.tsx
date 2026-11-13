'use client';
import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react';
import { FOLDER_NONE, folderCountLabel, folderTone, monogram, orderFolders, type FolderItem } from '../../../../lib/document-folders.mjs';
import { useWords } from '../../../../lib/catalog';
import { ChevronIcon } from './icons';
import type { Grouping } from './url-state';
import s from './folders.module.css';

type Words = ReturnType<typeof useWords>['t'];
export type FolderData = { by: Grouping; items: FolderItem[]; unassigned: number; total: number };

/**
 * Company or category folders with archive-wide counts (GET /document-folders). Counts cover every document the member
 * can list, across pages and statuses; the API applies archive membership, so another archive's folder never appears.
 */
export function useFolders(archiveId: string, by: Grouping | null, fast: boolean) {
  const [data, setData] = useState<FolderData | null>(null);
  const [error, setError] = useState(false);
  const generation = useRef(0);
  const load = useCallback(async (initial: boolean) => {
    if (!by) return;
    const mine = ++generation.current;
    if (initial) { setData(prev => prev?.by === by ? prev : null); setError(false); }
    try {
      const response = await fetch(`/api/v1/archives/${archiveId}/document-folders?by=${by}`, { credentials: 'same-origin', cache: 'no-store' });
      if (mine !== generation.current) return;
      if (response.status === 404) { window.location.replace('/archives?access=changed'); return; }
      if (!response.ok) { setError(true); return; }
      setData(await response.json() as FolderData); setError(false);
    } catch { if (mine === generation.current) setError(true); }
  }, [archiveId, by]);
  useEffect(() => { void load(true); }, [load]);
  useEffect(() => {
    if (!by) return;
    const timer = window.setInterval(() => { if (!document.hidden) void load(false); }, fast ? 5000 : 30000);
    return () => window.clearInterval(timer);
  }, [load, by, fast]);
  const current = data?.by === by ? data : null;
  return { data: current, loading: Boolean(by) && !current && !error, error, refresh: useCallback(() => load(false), [load]) };
}

export function folderName(t: Words, by: Grouping, id: string, data: FolderData | null) {
  if (id === FOLDER_NONE) return by === 'company' ? t.folderUnassignedCompany : t.folderUncategorised;
  return data?.items.find(f => f.id === id)?.displayName ?? null;
}
export function folderCount(data: FolderData | null, id: string) {
  if (!data) return null;
  return id === FOLDER_NONE ? data.unassigned : data.items.find(f => f.id === id)?.count ?? null;
}

/** Large folder glyph: a tinted back with a tab, a sheet of paper, and a front carrying a monogram, tag or dash. */
export function FolderIcon({ by, id, name, size = 'large' }: { by: Grouping; id: string; name: string; size?: 'large' | 'small' }) {
  const none = id === FOLDER_NONE;
  const mark = none ? null : by === 'company' ? monogram(name) : null;
  return <span className={s.icon} data-tone={folderTone(id)} data-size={size} aria-hidden="true">
    <svg viewBox="0 0 96 76" className={s.iconSvg}>
      <path className={s.back} d="M6 10a6 6 0 0 1 6-6h22.5a6 6 0 0 1 4.6 2.2L44 12h40a6 6 0 0 1 6 6v46a6 6 0 0 1-6 6H12a6 6 0 0 1-6-6z" />
      <rect className={s.sheet} x="14" y="16" width="62" height="40" rx="3" transform="rotate(-3 45 36)" />
      <rect className={s.sheet2} x="20" y="14" width="62" height="40" rx="3" transform="rotate(2 51 34)" />
      <path className={s.front} d="M4 30a6 6 0 0 1 6-6h76a6 6 0 0 1 6 6v34a6 6 0 0 1-6 6H10a6 6 0 0 1-6-6z" />
      <path className={s.shine} d="M4 30a6 6 0 0 1 6-6h76a6 6 0 0 1 6 6v3H4z" />
    </svg>
    {size === 'large' && <span className={s.mark}>
      {mark ? <bdi className={s.monogram}>{mark}</bdi>
        : none ? <svg viewBox="0 0 16 16" className={s.glyph}><path d="M4 8h8" /></svg>
          : <svg viewBox="0 0 16 16" className={s.glyph}><path d="M2.5 3.5v4.1l6 6 5-5-6-6H3.5z" /><circle cx="5.5" cy="5.5" r="1" /></svg>}
    </span>}
  </span>;
}

/** Plain left click opens in place (history entry, focus kept); modified clicks and middle clicks keep the link's own behaviour. */
function inPlace(e: MouseEvent) { return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey; }

/** Arrow keys move between tiles by row and column, Home and End jump to the ends; Enter follows the focused link. */
function moveFocus(e: KeyboardEvent<HTMLAnchorElement>, rtl: boolean) {
  const tiles = [...(e.currentTarget.closest('ul')?.querySelectorAll<HTMLAnchorElement>('a[data-folder]') ?? [])];
  const index = tiles.indexOf(e.currentTarget);
  const top = e.currentTarget.getBoundingClientRect().top;
  const columns = Math.max(1, tiles.filter(tile => Math.abs(tile.getBoundingClientRect().top - top) < 4).length);
  const forward = rtl ? 'ArrowLeft' : 'ArrowRight', backward = rtl ? 'ArrowRight' : 'ArrowLeft';
  const delta = e.key === forward ? 1 : e.key === backward ? -1 : e.key === 'ArrowDown' ? columns : e.key === 'ArrowUp' ? -columns
    : e.key === 'Home' ? -index : e.key === 'End' ? tiles.length - 1 - index : 0;
  if (!delta) return;
  e.preventDefault();
  tiles[Math.max(0, Math.min(tiles.length - 1, index + delta))]?.focus();
}

export function FolderGrid({ t, locale, by, data, loading, error, archiveName, hrefFor, onOpen }: {
  t: Words; locale: string; by: Grouping; data: FolderData | null; loading: boolean; error: boolean; archiveName: string;
  hrefFor: (id: string) => string; onOpen: (id: string) => void;
}) {
  const label = (t.folderGrid as string).replace('{archive}', archiveName);
  if (error && !data) return <p className={s.alert} role="alert">{t.error}</p>;
  if (loading || !data) return <p className={s.loading} role="status">{t.folderLoading}</p>;
  const folders = orderFolders(data.items, data.unassigned, locale, by === 'company' ? t.folderUnassignedCompany : t.folderUncategorised);
  if (!folders.length) return <div className={s.empty}><FolderIcon by={by} id={FOLDER_NONE} name="" /><p>{data.total ? t.noDocuments : t.libEmpty}</p><p className={s.hint}>{by === 'company' ? t.folderEmptyCompany : t.folderEmptyCategory}</p></div>;
  return <section className={s.gridPane} aria-label={label}>
    <ul className={s.grid}>
      {folders.map(f => <li key={f.id}>
        <a id={`folder-${f.id}`} data-folder={f.id} className={s.tile} href={hrefFor(f.id)} data-empty={f.count === 0 ? '' : undefined}
          onClick={e => { if (inPlace(e)) { e.preventDefault(); onOpen(f.id); } }} onKeyDown={e => moveFocus(e, locale === 'he')}>
          <FolderIcon by={by} id={f.id} name={f.displayName} />
          <span className={s.name} dir="auto">{f.displayName}</span>
          <span className={s.count}>{folderCountLabel(t, f.count, locale)}{f.archived && <> · {t.folderArchived}</>}</span>
        </a>
      </li>)}
    </ul>
  </section>;
}

/** Archive › grouping › folder. Earlier crumbs return to the folder grid; the open folder is the current page. */
export function FolderBreadcrumb({ t, rtl, archiveName, by, folder, gridHref, onGrid }: {
  t: Words; rtl: boolean; archiveName: string; by: Grouping; folder: string; gridHref: string; onGrid: () => void;
}) {
  const go = (e: MouseEvent) => { if (inPlace(e)) { e.preventDefault(); onGrid(); } };
  return <nav aria-label={t.folderBreadcrumb} className={s.crumbs}>
    <ol>
      <li><a href={gridHref} onClick={go}><bdi dir="auto">{archiveName}</bdi></a><ChevronIcon flip={!rtl} width={12} height={12} /></li>
      <li><a href={gridHref} onClick={go}>{by === 'company' ? t.folderByCompany : t.folderByCategory}</a><ChevronIcon flip={!rtl} width={12} height={12} /></li>
      <li aria-current="page"><bdi dir="auto">{folder}</bdi></li>
    </ol>
  </nav>;
}
