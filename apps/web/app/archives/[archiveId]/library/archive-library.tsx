'use client';
import { Button, PopoverButton, Select, TextField } from '@invoyis/ui';
import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent, type ReactNode, type RefObject } from 'react';
import { documentTypeLabel, identifierIsInvoiceId, typesMatching } from '../../../../lib/document-types.mjs';
import { formatDateOnly } from '../../../../lib/document-format.mjs';
import { canonicalSearchAmount } from '../../../../lib/review-decimal.mjs';
import { useWords } from '../../../../lib/catalog';
import { FileName } from '../../../../components/file-name';
import { UploadForm, type UploadControl } from '../upload-form';
import { AddDocumentsDialog } from './add-documents-dialog';
import { PreviewPanel } from '../documents/[id]/review/preview-panel';
import { ChevronIcon, CloseIcon, DocsIcon, DownloadIcon, FileGlyph, FilterIcon, PlusIcon, SearchIcon, Spinner } from './icons';
import { ArchiveIcon, ArchiveShell, MenuButton, useNarrow } from './archive-shell';
import { useChoices, usePagedDocuments, IN_FLIGHT, type ChoiceKind, type DocumentRow } from './use-documents';
import { lastArchiveKey, lastViewKey, parseLibraryState, toLibrarySearch, type LibraryState, type View } from './url-state';
import s from './library.module.css';

type Words = ReturnType<typeof useWords>['t'];
const filterDefs = [
  { key: 'categoryId', kind: 'categories', label: 'category' },
  { key: 'propertyId', kind: 'properties', label: 'property' },
  { key: 'paperLocationId', kind: 'paper-locations', label: 'paperLocation' },
] as const satisfies readonly { key: keyof LibraryState; kind: ChoiceKind; label: keyof Words }[];

function fill(template: string, values: Record<string, string | number>) { return template.replace(/\{(\w+)\}/g, (_, k) => String(values[k] ?? '')); }
function typeLabel(t: Words, type?: string | null) { return documentTypeLabel(t, type) as string; }
function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value);
  useEffect(() => { const id = window.setTimeout(() => setV(value), ms); return () => window.clearTimeout(id); }, [value, ms]);
  return v;
}
type SavedPosition = { search: string; scroll: number; pages: number; attentionPages: number };
function readPosition(key: string): SavedPosition | null {
  if (typeof window === 'undefined') return null;
  try { const raw = sessionStorage.getItem(key); return raw ? JSON.parse(raw) as SavedPosition : null; } catch { return null; }
}
function displayName(d: DocumentRow) { return d.confirmedTitle || d.originalFilename; }
function safeDate(value: string | null | undefined, locale: string) { try { return value ? formatDateOnly(value, locale) : ''; } catch { return value ?? ''; } }
function storage(action: (store: Storage) => void) { try { action(sessionStorage); } catch { /* storage may be unavailable */ } }

/**
 * The archive document library. With `supplier`, the same layout shows that supplier's confirmed documents
 * (supplier history), filtered by property and customer account reference.
 */
export function ArchiveLibrary({ archiveId, initial, hasQuery, openAdd = false, supplier }: { archiveId: string; initial: LibraryState; hasQuery: boolean; openAdd?: boolean; supplier?: { id: string } }) {
  const { locale, t } = useWords();
  const rtl = locale === 'he';
  const narrow = useNarrow();
  const splitRef = useRef<HTMLDivElement>(null);
  const [previewWidth, setPreviewWidth] = useState(416);
  const [splitWidth, setSplitWidth] = useState(0);
  const clampPreview = (width: number, total: number) => Math.max(360, Math.min(width, Math.max(360, total - 320 - 12)));
  useEffect(() => {
    if (!splitRef.current) return;
    const observer = new ResizeObserver(([entry]) => {
      const width = entry.contentRect.width;
      setSplitWidth(width);
      setPreviewWidth(value => clampPreview(value, width));
    });
    observer.observe(splitRef.current);
    return () => observer.disconnect();
  }, []);
  function resizeFromPointer(e: PointerEvent<HTMLDivElement>) {
    const bounds = splitRef.current?.getBoundingClientRect();
    if (!bounds) return;
    setPreviewWidth(clampPreview(rtl ? e.clientX - bounds.left : bounds.right - e.clientX, bounds.width));
  }
  function resizeWithKeys(e: KeyboardEvent<HTMLDivElement>) {
    const direction = e.key === 'ArrowLeft' ? (rtl ? -1 : 1) : e.key === 'ArrowRight' ? (rtl ? 1 : -1) : 0;
    if (direction) { e.preventDefault(); setPreviewWidth(value => clampPreview(value + direction * (e.shiftKey ? 50 : 20), splitWidth)); }
    if (e.key === 'Home') { e.preventDefault(); setPreviewWidth(360); }
    if (e.key === 'End') { e.preventDefault(); setPreviewWidth(clampPreview(Infinity, splitWidth)); }
  }
  const [st, setSt] = useState<LibraryState>(initial);
  const { view, query, docId } = st;
  const [uploadOpen, setUploadOpen] = useState(openAdd);
  const [uploading, setUploading] = useState(false);
  const uploadControls = useRef<Record<string, UploadControl | null>>({});
  const [supplierName, setSupplierName] = useState('');
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const update = (patch: Partial<LibraryState>) => setSt(prev => ({ ...prev, ...patch }));

  // Search, status and filters all run in the API, so every view covers the whole archive; pages load on demand.
  const searchText = useDebounced(query.trim(), 250);
  const types = typesMatching(t, searchText, locale).join(',');
  const amount = canonicalSearchAmount(st.total, locale);
  const amountReady = typeof amount === 'string' && ['EUR', 'ILS'].includes(st.currency);
  const dateReady = !st.issueDate || !st.dateFrom && !st.dateTo;
  const rangeReady = !st.dateFrom || !st.dateTo || st.dateFrom <= st.dateTo;
  const base = supplier
    ? { propertyId: st.propertyId || undefined }
    : { categoryId: st.categoryId || undefined, propertyId: st.propertyId || undefined, paperLocationId: st.paperLocationId || undefined };
  const common = { accountReference: st.accountReference.trim() || undefined,
    issueDate: dateReady ? st.issueDate || undefined : undefined,
    dateFrom: dateReady && rangeReady ? st.dateFrom || undefined : undefined,
    dateTo: dateReady && rangeReady ? st.dateTo || undefined : undefined,
    total: amountReady ? amount : undefined, currency: amountReady ? st.currency : undefined };
  const search = { q: searchText || undefined, types: searchText && types ? types : undefined };
  const posKey = `${lastViewKey(archiveId, supplier?.id)}:pos`;
  const saved = useRef(readPosition(posKey)).current;
  // In All documents, anything needing attention (in progress, awaiting review, failed) is listed first, then confirmed documents.
  const attention = usePagedDocuments(archiveId, { ...base, ...common, ...search, status: 'attention' }, { fast: uploading, enabled: !supplier && view === 'all', initialPages: saved?.attentionPages });
  const main = usePagedDocuments(archiveId, { ...base, ...common, ...search, status: supplier ? undefined : view === 'review' ? 'needs_review' : 'ready' }, { fast: uploading, supplierId: supplier?.id, initialPages: saved?.pages });
  const loading = main.loading || attention.loading;
  const error = main.error || attention.error;
  const refresh = () => { void main.refresh(); void attention.refresh(); };
  const choices = useChoices(archiveId);
  const filters = supplier ? filterDefs.filter(f => f.key === 'propertyId') : filterDefs;

  useEffect(() => {
    if (!supplier) return; let live = true;
    fetch(`/api/v1/archives/${archiveId}/organisation/suppliers/${supplier.id}`, { credentials: 'same-origin' }).then(r => r.ok ? r.json() : null).then(v => { if (live && v) setSupplierName(v.displayName); }).catch(() => undefined);
    return () => { live = false; };
  }, [archiveId, supplier]);

  // Coming back without a query (sidebar link, review page "Back") restores this archive's last view.
  useEffect(() => {
    storage(store => store.setItem(lastArchiveKey, archiveId));
    if (hasQuery) return;
    storage(store => { const saved = store.getItem(lastViewKey(archiveId, supplier?.id)); if (saved) setSt(parseLibraryState(new URLSearchParams(saved))); });
  }, [archiveId, hasQuery, supplier?.id]);
  useEffect(() => {
    const search = toLibrarySearch(st);
    if (search !== location.search) history.replaceState(history.state, '', `${location.pathname}${search}`);
    storage(store => store.setItem(lastViewKey(archiveId, supplier?.id), search));
  }, [st, archiveId, supplier?.id]);
  useEffect(() => {
    if ('scrollRestoration' in history) history.scrollRestoration = 'manual';
    const onPop = () => setSt(parseLibraryState(new URLSearchParams(location.search)));
    window.addEventListener('popstate', onPop); return () => window.removeEventListener('popstate', onPop);
  }, []);

  const visible = useMemo(() => {
    const rows = [...(!supplier && view === 'all' ? attention.items : []), ...main.items];
    return rows.filter((d, i) => rows.findIndex(r => r.id === d.id) === i);
  }, [attention.items, main.items, view, supplier]);
  const hasMore = (!supplier && view === 'all' && attention.hasMore) || main.hasMore;
  const loadingMore = attention.loadingMore || main.loadingMore;
  const loadMore = () => { if (!supplier && view === 'all' && attention.hasMore) void attention.loadMore(); else void main.loadMore(); };
  // A document opened from a link or restored after Review may sit on a page not loaded yet; fetch it on its own.
  const [linked, setLinked] = useState<DocumentRow | null>(null);
  const listed = visible.find(d => d.id === docId) ?? null;
  useEffect(() => {
    if (!docId || listed || loading) { setLinked(null); return; }
    let live = true;
    fetch(`/api/v1/archives/${archiveId}/documents/${docId}/status`, { credentials: 'same-origin', cache: 'no-store' })
      .then(r => r.ok ? r.json() : null).then(v => { if (live) setLinked(v ? { referenceScan: false, ...v } : null); }).catch(() => undefined);
    return () => { live = false; };
  }, [archiveId, docId, listed, loading]);
  const selected = listed ?? (linked?.id === docId ? linked : null);

  // Keep the list position (scroll and pages loaded) so returning from Review lands where you left.
  const listRef = useRef<HTMLElement>(null);
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current || loading) return;
    restored.current = true;
    if (!saved) return;
    requestAnimationFrame(() => {
      if (narrow) window.scrollTo({ top: saved.scroll }); else if (listRef.current) listRef.current.scrollTop = saved.scroll;
      if (docId) document.getElementById(`row-${docId}`)?.focus({ preventScroll: true });
    });
  }, [loading, saved, narrow, docId]);
  useEffect(() => {
    // Nothing is saved until the previous position has been restored, so a remount cannot overwrite it with page one.
    const save = () => restored.current && storage(store => store.setItem(posKey, JSON.stringify({ search: toLibrarySearch(st), scroll: narrow ? window.scrollY : listRef.current?.scrollTop ?? 0, pages: main.pages, attentionPages: attention.pages } satisfies SavedPosition)));
    const target: HTMLElement | Window | null = narrow ? window : listRef.current;
    let pending = 0;
    const onScroll = () => { window.clearTimeout(pending); pending = window.setTimeout(save, 150); };
    target?.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('pagehide', save);
    save();
    return () => { target?.removeEventListener('scroll', onScroll); window.removeEventListener('pagehide', save); window.clearTimeout(pending); };
  }, [posKey, st, narrow, main.pages, attention.pages]);
  const activeFilters = filters.filter(f => st[f.key]);
  const clearFilters = { categoryId: '', propertyId: '', paperLocationId: '', accountReference: '', issueDate: '', dateFrom: '', dateTo: '', total: '', currency: '' };
  const extraFilters = Number(Boolean(st.accountReference)) + Number(Boolean(st.issueDate || st.dateFrom || st.dateTo)) + Number(Boolean(st.total || st.currency));
  const anyFilters = activeFilters.length + extraFilters > 0;
  const choiceName = (kind: ChoiceKind, id: string) => choices[kind].find(c => c.id === id)?.displayName ?? id;

  // Return focus to a row whenever its document closes (in-app Back, Esc or the browser's Back).
  const lastDoc = useRef(docId);
  useEffect(() => { const prev = lastDoc.current; lastDoc.current = docId; if (prev && !docId) focusRow(prev); });
  function focusRow(id: string) { requestAnimationFrame(() => document.getElementById(`row-${id}`)?.focus({ preventScroll: !narrow })); }
  function openDoc(id: string, keepFocus = !narrow) {
    if (!docId) history.pushState({ library: 'doc' }, '', `${location.pathname}${toLibrarySearch({ ...st, docId: id })}`);
    update({ docId: id });
    if (!keepFocus) requestAnimationFrame(() => detailHeading.current?.focus());
  }
  function closeDoc() { if (history.state?.library === 'doc') history.back(); else update({ docId: null }); }
  function setView(next: View) { update({ view: next, docId: null }); }
  function onRowKey(e: KeyboardEvent<HTMLButtonElement>, index: number) {
    const move = e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : e.key === 'Home' ? -index : e.key === 'End' ? visible.length - 1 - index : 0;
    if (move) {
      e.preventDefault();
      const next = visible[Math.max(0, Math.min(visible.length - 1, index + move))];
      document.getElementById(`row-${next.id}`)?.focus();
      if (!narrow && selected) openDoc(next.id, true);
    } else if (e.key === 'Escape' && selected) { e.preventDefault(); closeDoc(); }
  }

  const onView = supplier ? undefined : setView;
  const title = supplier ? supplierName : undefined;

  return <ArchiveShell archiveId={archiveId} view={supplier ? null : view} fast={uploading} onView={onView} detailOpen={narrow && Boolean(selected)}>{({ archive, archives, loadError, counts, refreshCounts }) => {
    const canUpload = Boolean(!supplier && archive && archive.role !== 'viewer');
    // The open archive's queue is always mounted when it accepts uploads, even before (or without) the archive list.
    const uploadable = [...(canUpload && archive ? [archive] : []), ...archives.filter(a => a.role !== 'viewer' && a.id !== archive?.id)];
    const reviewCount = counts?.needsReview ?? 0;
    return <>
    <main className={s.main} inert={narrow && Boolean(selected)}>
      <header className={s.toolbar}>
        <MenuButton />
        {supplier && <a href={`/archives/${archiveId}`} className={s.backButton}><ChevronIcon flip={rtl} />{t.libBack}</a>}
        <div className={s.titleBlock}>
          <h1 className={s.title}>{archive && !supplier && <ArchiveIcon kind={archive.kind} />}<span className={s.titleText} dir="auto">{title || archive?.name || '\u00a0'}</span></h1>
          {/* The sidebar already names the view and its count; only supplier history needs a second line. */}
          {supplier && <p className={s.subtitle}>{t.supplierHistory}{archive && <> · <bdi dir="auto">{archive.name}</bdi></>}</p>}
        </div>
        <div className={s.toolbarActions}>
          <label className={s.search}>
            <SearchIcon className={s.searchIcon} />
            <span className={s.srOnly}>{t.libSearch}</span>
            <input type="search" value={query} placeholder={t.libSearch} onChange={e => update({ query: e.target.value, docId: null })} onKeyDown={e => { if (e.key === 'Escape' && query) { e.preventDefault(); update({ query: '' }); } }} />
          </label>
          <PopoverButton dialogLabel={t.filterDocuments} placement={rtl ? 'bottom start' : 'bottom end'} buttonClassName={`${s.toolButton} ${anyFilters ? s.toolButtonOn : ''}`}
            label={<><FilterIcon /><span className={s.hideXs}>{t.libFilter}</span>{anyFilters && <span className={s.badge}>{activeFilters.length + extraFilters}</span>}</>}>
            <div className={s.filterPanel}>
              <h2 className={s.panelTitle}>{t.filterDocuments}</h2>
              {filters.map(f => <Select key={f.key} label={t[f.label]} selectedKey={st[f.key] || '__any'}
                onSelectionChange={k => update({ [f.key]: k === '__any' ? '' : String(k), docId: null } as Partial<LibraryState>)}
                options={[{ id: '__any', label: t.libAny }, ...choices[f.kind].map(c => ({ id: c.id, label: c.displayName }))]} />)}
              <TextField label={t.accountReference} value={st.accountReference} onChange={v => update({ accountReference: v, docId: null })} inputProps={{ dir: 'auto' }} />
              <TextField label={t.searchExactDate} value={st.issueDate} onChange={v => update({ issueDate: v, dateFrom: '', dateTo: '', docId: null })} inputProps={{ type: 'date', lang: locale, dir: 'ltr' }} />
              <TextField label={t.searchDateFrom} value={st.dateFrom} onChange={v => update({ dateFrom: v, issueDate: '', docId: null })} inputProps={{ type: 'date', lang: locale, dir: 'ltr' }} />
              <TextField label={t.searchDateTo} value={st.dateTo} onChange={v => update({ dateTo: v, issueDate: '', docId: null })} error={!rangeReady ? t.searchDateOrder : undefined} inputProps={{ type: 'date', lang: locale, dir: 'ltr' }} />
              <TextField label={t.searchExactTotal} value={st.total} onChange={v => update({ total: v, docId: null })} error={st.total && amount === undefined ? t.invalidDecimal : undefined} inputProps={{ inputMode: 'decimal', dir: 'ltr' }} />
              <Select label={t.currency} selectedKey={st.currency || '__any'} onSelectionChange={v => update({ currency: v === '__any' ? '' : String(v), docId: null })} options={[{ id: '__any', label: t.libAny }, { id: 'EUR', label: 'EUR' }, { id: 'ILS', label: 'ILS' }]} />
              {st.total && !st.currency && <small role="alert">{t.searchCurrencyRequired}</small>}
              {anyFilters && <Button variant="quiet" onPress={() => update(clearFilters)}>{t.clearFilters}</Button>}
            </div>
          </PopoverButton>
          {canUpload && <Button className={`${s.addButton} ${s.hideNarrow}`} onPress={() => setUploadOpen(true)}><PlusIcon />{t.libAddDocuments}</Button>}
        </div>
      </header>

      {!supplier && <nav className={s.mobileViews} aria-label={t.libMenu}>
        <button type="button" aria-current={view === 'all' ? 'page' : undefined} onClick={() => setView('all')}>{t.libAllDocuments}</button>
        <button type="button" aria-current={view === 'review' ? 'page' : undefined} onClick={() => setView('review')}>{t.libNeedsReview}{reviewCount > 0 && <span className={s.reviewCount}>{reviewCount}</span>}</button>
      </nav>}

      {(anyFilters || query) && <div className={s.chips} role="group" aria-label={t.filterDocuments}>
        {query && <button type="button" className={s.chip} aria-label={`${fill(t.libSearchFor, { q: query })} — ${t.libClearSearch}`} onClick={() => update({ query: '' })}><span>{fill(t.libSearchFor, { q: query })}</span><CloseIcon width={12} height={12} /></button>}
        {activeFilters.map(f => { const name = choiceName(f.kind, st[f.key] as string); return <button key={f.key} type="button" className={s.chip} aria-label={fill(t.libRemoveFilter, { label: `${t[f.label]}: ${name}` })} onClick={() => update({ [f.key]: '' } as Partial<LibraryState>)}><span>{t[f.label]}: <bdi dir="auto">{name}</bdi></span><CloseIcon width={12} height={12} /></button>; })}
        {st.accountReference && <button type="button" className={s.chip} aria-label={fill(t.libRemoveFilter, { label: `${t.accountReference}: ${st.accountReference}` })} onClick={() => update({ accountReference: '' })}><span>{t.accountReference}: <bdi dir="auto">{st.accountReference}</bdi></span><CloseIcon width={12} height={12} /></button>}
        {(st.issueDate || st.dateFrom || st.dateTo) && <button type="button" className={s.chip} onClick={() => update({ issueDate: '', dateFrom: '', dateTo: '' })}><span>{t.issueDate}: <bdi dir="ltr">{st.issueDate ? safeDate(st.issueDate, locale) : `${safeDate(st.dateFrom, locale)} – ${safeDate(st.dateTo, locale)}`}</bdi></span><CloseIcon width={12} height={12} /></button>}
        {(st.total || st.currency) && <button type="button" className={s.chip} onClick={() => update({ total: '', currency: '' })}><span>{t.total}: <bdi dir="ltr">{st.total} {st.currency}</bdi></span><CloseIcon width={12} height={12} /></button>}
        <button type="button" className={s.clearAll} onClick={() => update({ ...clearFilters, query: '' })}>{t.libClearAll}</button>
      </div>}

      {/* Upload queues stay mounted for every archive you can add to, so uploads continue while you browse; each shows only once it has files. */}
      {!supplier && <div className={s.uploads}>{uploadable.map(a => <UploadForm key={a.id} compact archiveId={a.id} archiveName={a.name}
        maxBytes={a.uploadMaxBytes ?? 25 * 1024 * 1024}
        controlRef={control => { uploadControls.current[a.id] = control; }} onActivityChange={a.id === archiveId ? setUploading : undefined} onReceived={() => { if (a.id === archiveId) { refresh(); void refreshCounts(); } }} />)}</div>}
      {canUpload && archive && <AddDocumentsDialog isOpen={uploadOpen} onOpenChange={setUploadOpen} current={archive} archives={uploadable}
        onUpload={(dest, files) => {
          // Close only once the destination queue has taken the files; otherwise the dialog keeps them and explains.
          const control = uploadControls.current[dest];
          if (!control) return false;
          control.add(files); setUploadOpen(false);
          if (dest === archiveId) update({ view: 'all', docId: null });
          return true;
        }} />}

      <div ref={splitRef} className={s.split} data-preview={selected && !narrow ? 'open' : 'closed'} style={{ '--preview-width': `${previewWidth}px` } as CSSProperties}>
        <section ref={listRef} className={s.listPane} aria-label={fill(t.libRows, { archive: title ?? archive?.name ?? '' })} aria-busy={loading}>
          {(loading || visible.length > 0) && <div className={s.listHeader} aria-hidden="true"><span /><span>{t.libName}</span><span className={s.colType}>{t.libType}</span><span className={s.colDate}>{t.libDate}</span></div>}
          {(error || loadError) && <p className={s.alert} role="alert">{loadError || t.error}</p>}
          {loading ? <p className={s.loadingRow} role="status">{t.loading}</p> : visible.length ? <ul className={s.rows}>
            {visible.map((d, i) => { const type = typeLabel(t, d.confirmedType); const date = safeDate(d.issueDate, locale); const facts = [type, date].filter(Boolean).join(' · '); const attention = d.status !== 'ready'; return <li key={d.id}>
              <button id={`row-${d.id}`} type="button" className={s.row} aria-current={d.id === docId ? 'true' : undefined} data-status={d.status}
                onClick={() => d.id === docId && !narrow ? closeDoc() : openDoc(d.id)} onKeyDown={e => onRowKey(e, i)}>
                <FileGlyph type={d.confirmedType} />
                <span className={s.nameCell}>
                  <FileName className={s.name}>{displayName(d)}</FileName>
                  {!attention && <span className={s.srOnly}>, {t.statusReady}</span>}
                  {!attention && facts && <span className={s.meta}>{facts}</span>}
                </span>
                {/* Unconfirmed documents have no type or date yet, so their status fills those columns instead of a pill beside the name
                    (on narrow lists it moves under the name). */}
                {attention ? <span className={s.statusCell}><StatusLabel t={t} status={d.status} />{facts && <span className={s.statusExtra}>{facts}</span>}</span> : <>
                  <span className={s.colType}>{type || <span className={s.none} aria-hidden="true">–</span>}</span>
                  <span className={s.colDate}>{date ? <time dateTime={d.issueDate!}><bdi dir="ltr">{date}</bdi></time> : <span className={s.none} aria-hidden="true">–</span>}</span>
                </>}
              </button>
            </li>; })}
          </ul> : !error && <Empty t={t} view={view} filtered={anyFilters || Boolean(query)} total={supplier ? visible.length : counts?.total ?? visible.length} canAdd={canUpload} />}
          {!loading && hasMore && <div className={s.moreRow}><Button variant="secondary" className={s.secondary} isDisabled={loadingMore} onPress={loadMore}>{loadingMore ? t.loading : t.moreDocuments}</Button></div>}
        </section>
        {!narrow && selected && <><div className={s.divider} role="separator" aria-orientation="vertical" aria-label={t.resizePreview} aria-valuemin={360} aria-valuemax={Math.max(360, splitWidth - 332)} aria-valuenow={previewWidth} tabIndex={0} onKeyDown={resizeWithKeys} onPointerDown={e => { e.currentTarget.setPointerCapture(e.pointerId); resizeFromPointer(e); }} onPointerMove={e => { if (e.currentTarget.hasPointerCapture(e.pointerId)) resizeFromPointer(e); }} onPointerUp={e => e.currentTarget.releasePointerCapture(e.pointerId)} onPointerCancel={e => e.currentTarget.releasePointerCapture(e.pointerId)}><span /></div><Detail t={t} locale={locale} archiveId={archiveId} doc={selected} headingRef={detailHeading} onClose={closeDoc} /></>}
      </div>
    </main>

    {narrow && selected && <div className={s.fullscreen} role="region" aria-label={displayName(selected)}>
      <Detail t={t} locale={locale} archiveId={archiveId} doc={selected} headingRef={detailHeading} onClose={closeDoc} narrow />
    </div>}

    {canUpload && <Button className={s.fab} onPress={() => setUploadOpen(true)}><PlusIcon width={18} height={18} /><span>{t.libAddDocuments}</span></Button>}
  </>; }}</ArchiveShell>;
}

function statusText(t: Words, status: string) {
  return ({ pending: t.statusPending, processing: t.statusProcessing, needs_review: t.statusNeedsReview, file_blocked: t.statusFileBlocked, processing_failed: t.statusProcessingFailed, ready: t.statusReady } as Record<string, string>)[status] ?? status;
}
function statusTone(status: string) { return IN_FLIGHT.has(status) ? 'processing' : status === 'needs_review' ? 'needsReview' : 'problem'; }
/** Quiet dot-and-text status, aligned in the list's columns rather than a pill beside the filename. */
function StatusLabel({ t, status }: { t: Words; status: string }) {
  const tone = statusTone(status);
  return <span className={s.status} data-tone={tone}>{tone === 'processing' ? <Spinner width={12} height={12} /> : <span className={s.dot} />}{statusText(t, status)}</span>;
}

/** The toolbar (or the mobile button) already offers Add documents, so the empty list only explains itself. */
function Empty({ t, view, filtered, total, canAdd }: { t: Words; view: View; filtered: boolean; total: number; canAdd: boolean }) {
  return <div className={s.empty}>
    <DocsIcon width={20} height={20} />
    <p>{filtered ? t.noDocuments : view === 'review' ? t.libNoReview : t.libEmpty}</p>
    {!filtered && view === 'all' && total === 0 && canAdd && <p className={s.emptyHint}>{t.libEmptyHint}</p>}
  </div>;
}

function Detail({ t, locale, archiveId, doc, headingRef, onClose, narrow }: { t: Words; locale: string; archiveId: string; doc: DocumentRow; headingRef: RefObject<HTMLHeadingElement | null>; onClose: () => void; narrow?: boolean }) {
  const type = typeLabel(t, doc.confirmedType);
  const date = safeDate(doc.issueDate, locale);
  const openable = doc.status === 'needs_review' || doc.status === 'ready';
  const reviewHref = `/archives/${archiveId}/documents/${doc.id}/review`;
  const numberLabel = identifierIsInvoiceId(doc.confirmedType) ? t.invoiceId : t.docReference;
  const facts: [string, ReactNode][] = [];
  if (doc.supplierName || doc.printedSupplier) facts.push([t.libSupplier, doc.supplierId ? <a href={`/archives/${archiveId}/suppliers/${doc.supplierId}`}><bdi dir="auto">{doc.supplierName ?? doc.printedSupplier}</bdi></a> : <bdi dir="auto">{doc.printedSupplier}</bdi>]);
  if (doc.supplierId && doc.printedSupplier && doc.printedSupplier !== doc.supplierName) facts.push([t.printedSupplier, <bdi dir="auto" key="printed">{doc.printedSupplier}</bdi>]);
  if (doc.documentNumber) facts.push([numberLabel, <bdi dir="ltr" key="n">{doc.documentNumber}</bdi>]);
  if (doc.accountReference) facts.push([t.accountReference, <bdi dir="auto" key="a">{doc.accountReference}</bdi>]);
  if (doc.total) facts.push([t.total, <bdi dir="ltr" key="t">{doc.total} {doc.currency}</bdi>]);
  if (doc.categoryName) facts.push([t.category, <bdi dir="auto" key="c">{doc.categoryName}</bdi>]);
  if (doc.propertyName) facts.push([t.property, <bdi dir="auto" key="p">{doc.propertyName}</bdi>]);
  if (doc.paperLocationName || doc.referenceScan) facts.push([t.paperLocation, <bdi dir="auto" key="l">{doc.paperLocationName || t.unknownLocation}</bdi>]);
  if (doc.referenceScan) facts.push([t.referenceScan, '✓']);
  if (doc.confirmedTitle && doc.confirmedTitle !== doc.originalFilename) facts.push(['PDF', <FileName key="f">{doc.originalFilename}</FileName>]);
  const note = doc.status === 'needs_review' ? t.libReviewNote : IN_FLIGHT.has(doc.status) ? t.libProcessingNote : doc.status === 'file_blocked' ? t.libBlockedNote : doc.status === 'processing_failed' ? t.libFailedNote : '';
  const meta = [type || (doc.status === 'ready' ? '' : t.libNotIdentified), date].filter(Boolean).join(' · ');

  return <section className={s.preview} aria-labelledby="detail-title" onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); onClose(); } }}>
    {narrow && <div className={s.previewBar}><button type="button" className={s.backButton} onClick={onClose}><ChevronIcon flip={locale === 'he'} />{t.libBack}</button></div>}
    <div className={s.previewHead}>
      <div className={s.previewHeadText}>
        <h2 id="detail-title" ref={headingRef} tabIndex={-1} className={s.previewTitle}><FileName>{displayName(doc)}</FileName></h2>
        {meta && <p className={s.muted}>{meta}</p>}
      </div>
      {!narrow && <button type="button" className={s.iconButton} aria-label={t.libClose} onClick={onClose}><CloseIcon /></button>}
    </div>
    {/* Status and the next step as quiet helper text; the Review button below carries the emphasis. */}
    {note && <div className={s.note} role={IN_FLIGHT.has(doc.status) ? 'status' : undefined}><StatusLabel t={t} status={doc.status} /><p>{note}</p></div>}
    {openable && <div className={s.actions}>
      {doc.status === 'needs_review'
        ? <a className="ui-button ui-button-primary" href={reviewHref}>{t.libReview}</a>
        : <a className={`ui-button ui-button-secondary ${s.secondary}`} href={reviewHref}>{t.libEditDetails}</a>}
      <a className={`ui-button ui-button-secondary ${s.secondary}`} href={`/api/v1/archives/${archiveId}/documents/${doc.id}/original`}><DownloadIcon />{t.downloadOriginal}</a>
    </div>}
    {openable ? <div className={s.pageFrame}><PreviewPanel key={doc.id} archiveId={archiveId} id={doc.id} onAccessLost={() => window.location.replace('/archives?access=changed')} /></div>
      : IN_FLIGHT.has(doc.status) && <div className={s.pageFrame}><div className={s.pageSkeleton} aria-hidden="true" /></div>}
    {facts.length > 0 && <dl className={s.facts}>{facts.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>}
  </section>;
}
