'use client';
import { Button, PopoverButton, Select, TextField } from '@invoyis/ui';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import { formatDateOnly } from '../../../../lib/document-format.mjs';
import { useWords } from '../../../../lib/catalog';
import { UploadForm } from '../upload-form';
import { PreviewPanel } from '../documents/[id]/review/preview-panel';
import { ChevronIcon, CloseIcon, DocsIcon, DownloadIcon, FileGlyph, FilterIcon, PlusIcon, ReviewIcon, SearchIcon, Spinner } from './icons';
import { ArchiveIcon, ArchiveShell, MenuButton, useNarrow } from './archive-shell';
import { useChoices, useDocuments, IN_FLIGHT, MAX_PAGES, type ChoiceKind, type DocumentRow } from './use-documents';
import { lastArchiveKey, lastViewKey, parseLibraryState, toLibrarySearch, type LibraryState, type View } from './url-state';
import s from './library.module.css';

type Words = ReturnType<typeof useWords>['t'];
const filterDefs = [
  { key: 'categoryId', kind: 'categories', label: 'category' },
  { key: 'propertyId', kind: 'properties', label: 'property' },
  { key: 'paperLocationId', kind: 'paper-locations', label: 'paperLocation' },
] as const satisfies readonly { key: keyof LibraryState; kind: ChoiceKind; label: keyof Words }[];
const ATTENTION = new Set(['needs_review', 'pending', 'processing', 'file_blocked', 'processing_failed']);

function fill(template: string, values: Record<string, string | number>) { return template.replace(/\{(\w+)\}/g, (_, k) => String(values[k] ?? '')); }
function typeLabel(t: Words, type?: string | null) {
  return type === 'invoice' ? t.invoice : type === 'lease' ? t.typeLease : type === 'court_correspondence' ? t.typeCourtCorrespondence : type === 'other' ? t.typeOther : '';
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
  const [st, setSt] = useState<LibraryState>(initial);
  const { view, query, docId } = st;
  const [uploadOpen, setUploadOpen] = useState(openAdd);
  const [supplierName, setSupplierName] = useState('');
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const update = (patch: Partial<LibraryState>) => setSt(prev => ({ ...prev, ...patch }));

  const serverFilters = supplier
    ? { propertyId: st.propertyId || undefined, accountReference: st.accountReference.trim() || undefined }
    : { categoryId: st.categoryId || undefined, propertyId: st.propertyId || undefined, paperLocationId: st.paperLocationId || undefined };
  const { items, loading, error, truncated, refresh } = useDocuments(archiveId, serverFilters, uploadOpen, supplier?.id);
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

  const reviewCount = items.filter(d => d.status === 'needs_review').length;
  const visible = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase(locale);
    const rows = items
      .filter(d => view === 'all' || d.status === 'needs_review')
      .filter(d => !needle || [displayName(d), d.originalFilename, typeLabel(t, d.confirmedType), d.printedSupplier, d.supplierName, d.documentNumber]
        .some(v => v && v.toLocaleLowerCase(locale).includes(needle)));
    // Anything that needs attention comes first; the rest keep the server's newest-first order.
    return [...rows.filter(d => ATTENTION.has(d.status)), ...rows.filter(d => !ATTENTION.has(d.status))];
  }, [items, view, query, t, locale]);
  const selected = items.find(d => d.id === docId) ?? null;
  const activeFilters = filters.filter(f => st[f.key]);
  const clearFilters = { categoryId: '', propertyId: '', paperLocationId: '', accountReference: '' };
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

  // Supplier history keeps the library's own counts out of the sidebar (they would cover only this supplier).
  const counts = supplier ? undefined : { total: items.length, review: reviewCount };
  const onView = supplier ? undefined : setView;
  const title = supplier ? supplierName : undefined;

  return <ArchiveShell archiveId={archiveId} view={supplier ? null : view} counts={counts} onView={onView} detailOpen={narrow && Boolean(selected)}>{({ archive, archives, loadError }) => { const canUpload = !supplier && archive && archive.role !== 'viewer'; return <>
    <main className={s.main} inert={narrow && Boolean(selected)}>
      <header className={s.toolbar}>
        <MenuButton />
        {supplier && <a href={`/archives/${archiveId}`} className={s.backButton}><ChevronIcon flip={rtl} />{t.libBack}</a>}
        <div className={s.titleBlock}>
          <h1 className={s.title}>{archive && !supplier && <ArchiveIcon kind={archive.kind} />}<span className={s.titleText} dir="auto">{title || archive?.name || '\u00a0'}</span></h1>
          <p className={s.subtitle}>{supplier ? <>{t.supplierHistory}{archive && <> · <bdi dir="auto">{archive.name}</bdi></>}</> : view === 'review' ? t.libNeedsReview : t.libAllDocuments}{!loading && <> · {items.length === 1 ? t.libDocCountOne : fill(t.libDocCount, { n: items.length })}</>}</p>
        </div>
        <div className={s.toolbarActions}>
          <label className={s.search}>
            <SearchIcon className={s.searchIcon} />
            <span className={s.srOnly}>{t.libSearch}</span>
            <input type="search" value={query} placeholder={t.libSearch} onChange={e => update({ query: e.target.value, docId: null })} onKeyDown={e => { if (e.key === 'Escape' && query) { e.preventDefault(); update({ query: '' }); } }} />
          </label>
          <PopoverButton dialogLabel={t.filterDocuments} placement={rtl ? 'bottom start' : 'bottom end'} buttonClassName={`${s.toolButton} ${activeFilters.length ? s.toolButtonOn : ''}`}
            label={<><FilterIcon /><span className={s.hideXs}>{t.libFilter}</span>{activeFilters.length > 0 && <span className={s.badge}>{activeFilters.length}</span>}</>}>
            <div className={s.filterPanel}>
              <h2 className={s.panelTitle}>{t.filterDocuments}</h2>
              {filters.map(f => <Select key={f.key} label={t[f.label]} selectedKey={st[f.key] || '__any'}
                onSelectionChange={k => update({ [f.key]: k === '__any' ? '' : String(k), docId: null } as Partial<LibraryState>)}
                options={[{ id: '__any', label: t.libAny }, ...choices[f.kind].map(c => ({ id: c.id, label: c.displayName }))]} />)}
              {supplier && <TextField label={t.accountReference} value={st.accountReference} onChange={v => update({ accountReference: v, docId: null })} inputProps={{ dir: 'auto' }} />}
              {(activeFilters.length > 0 || st.accountReference) && <Button variant="quiet" onPress={() => update(clearFilters)}>{t.clearFilters}</Button>}
            </div>
          </PopoverButton>
          {canUpload && <Button className={`${s.addButton} ${s.hideNarrow}`} onPress={() => setUploadOpen(true)}><PlusIcon />{t.libAddDocuments}</Button>}
        </div>
      </header>

      {!supplier && <nav className={s.mobileViews} aria-label={t.libMenu}>
        <button type="button" aria-current={view === 'all' ? 'page' : undefined} onClick={() => setView('all')}>{t.libAllDocuments}</button>
        <button type="button" aria-current={view === 'review' ? 'page' : undefined} onClick={() => setView('review')}>{t.libNeedsReview}{reviewCount > 0 && <span className={s.reviewCount}>{reviewCount}</span>}</button>
      </nav>}

      {(activeFilters.length > 0 || query || st.accountReference) && <div className={s.chips} role="group" aria-label={t.filterDocuments}>
        {query && <button type="button" className={s.chip} aria-label={`${fill(t.libSearchFor, { q: query })} — ${t.libClearSearch}`} onClick={() => update({ query: '' })}><span>{fill(t.libSearchFor, { q: query })}</span><CloseIcon width={12} height={12} /></button>}
        {activeFilters.map(f => { const name = choiceName(f.kind, st[f.key] as string); return <button key={f.key} type="button" className={s.chip} aria-label={fill(t.libRemoveFilter, { label: `${t[f.label]}: ${name}` })} onClick={() => update({ [f.key]: '' } as Partial<LibraryState>)}><span>{t[f.label]}: <bdi dir="auto">{name}</bdi></span><CloseIcon width={12} height={12} /></button>; })}
        {st.accountReference && <button type="button" className={s.chip} aria-label={fill(t.libRemoveFilter, { label: `${t.accountReference}: ${st.accountReference}` })} onClick={() => update({ accountReference: '' })}><span>{t.accountReference}: <bdi dir="auto">{st.accountReference}</bdi></span><CloseIcon width={12} height={12} /></button>}
        <button type="button" className={s.clearAll} onClick={() => update({ ...clearFilters, query: '' })}>{t.libClearAll}</button>
      </div>}

      {/* Kept mounted while closed so uploads in progress continue. */}
      {canUpload && archive && <section className={s.uploadPanel} hidden={!uploadOpen} aria-labelledby="upload-heading">
        <div className={s.uploadHead}>
          <h2 id="upload-heading" className={s.panelTitle}>{t.libAddDocuments}</h2>
          <button type="button" className={s.iconButton} aria-label={t.libCloseUpload} onClick={() => setUploadOpen(false)}><CloseIcon /></button>
        </div>
        <div className={s.destination}>
          <ArchiveIcon kind={archive.kind} />
          <div><span className={s.eyebrow}>{t.libAddingTo}</span><strong dir="auto">{archive.name}</strong><small>{archive.kind === 'personal' ? t.personal : t.business}</small></div>
          {archives.filter(a => a.role !== 'viewer').length > 1 && <div className={s.changeArchive}><Select label={t.libChangeArchive} selectedKey={archiveId}
            onSelectionChange={k => { const id = String(k); if (id !== archiveId) window.location.assign(`/archives/${id}?add=1`); }}
            options={archives.filter(a => a.role !== 'viewer').map(a => ({ id: a.id, label: `${a.name} · ${a.kind === 'personal' ? t.personal : t.business}` }))} /></div>}
        </div>
        <div className={s.uploadBody}><UploadForm archiveId={archive.id} archiveName={archive.name} maxBytes={archive.uploadMaxBytes ?? 25 * 1024 * 1024} onReceived={() => void refresh()} /></div>
      </section>}

      <div className={s.split} data-preview={selected && !narrow ? 'open' : 'closed'}>
        <section className={s.listPane} aria-label={fill(t.libRows, { archive: title ?? archive?.name ?? '' })} aria-busy={loading}>
          <div className={s.listHeader} aria-hidden="true"><span /><span>{t.libName}</span><span className={s.colType}>{t.libType}</span><span className={s.colDate}>{t.libDate}</span></div>
          {(error || loadError) && <p className={s.alert} role="alert">{loadError || t.error}</p>}
          {loading ? <p className={s.loadingRow} role="status">{t.loading}</p> : visible.length ? <ul className={s.rows}>
            {visible.map((d, i) => { const type = typeLabel(t, d.confirmedType); const date = safeDate(d.issueDate, locale); return <li key={d.id}>
              <button id={`row-${d.id}`} type="button" className={s.row} aria-current={d.id === docId ? 'true' : undefined} data-status={d.status}
                onClick={() => d.id === docId && !narrow ? closeDoc() : openDoc(d.id)} onKeyDown={e => onRowKey(e, i)}>
                <FileGlyph type={d.confirmedType} />
                <span className={s.nameCell}>
                  <span className={s.name} dir="auto">{displayName(d)}</span>
                  <StatusPill t={t} status={d.status} />
                  {(type || date) && <span className={s.meta}>{[type, date].filter(Boolean).join(' · ')}</span>}
                </span>
                <span className={s.colType}>{type || '—'}</span>
                <span className={s.colDate}>{date ? <time dateTime={d.issueDate!}><bdi dir="ltr">{date}</bdi></time> : '—'}</span>
              </button>
            </li>; })}
          </ul> : !error && <Empty t={t} view={view} filtered={activeFilters.length > 0 || Boolean(query) || Boolean(st.accountReference)} total={items.length} onAdd={canUpload ? () => setUploadOpen(true) : undefined} />}
          {truncated && <p className={s.muted} role="note">{fill(t.libTruncated, { n: MAX_PAGES * 20 })}</p>}
        </section>
        {!narrow && selected && <Detail t={t} locale={locale} archiveId={archiveId} doc={selected} headingRef={detailHeading} onClose={closeDoc} />}
      </div>
    </main>

    {narrow && selected && <div className={s.fullscreen} role="region" aria-label={displayName(selected)}>
      <Detail t={t} locale={locale} archiveId={archiveId} doc={selected} headingRef={detailHeading} onClose={closeDoc} narrow />
    </div>}

    {canUpload && <Button className={s.fab} onPress={() => { setUploadOpen(true); window.scrollTo({ top: 0 }); }}><PlusIcon width={18} height={18} /><span>{t.libAddDocuments}</span></Button>}
  </>; }}</ArchiveShell>;
}

function statusText(t: Words, status: string) {
  return ({ pending: t.statusPending, processing: t.statusProcessing, needs_review: t.statusNeedsReview, file_blocked: t.statusFileBlocked, processing_failed: t.statusProcessingFailed, ready: t.statusReady } as Record<string, string>)[status] ?? status;
}
function StatusPill({ t, status }: { t: Words; status: string }) {
  if (status === 'ready') return <span className={s.srOnly}>, {t.statusReady}</span>;
  const tone = IN_FLIGHT.has(status) ? 'processing' : status === 'needs_review' ? 'needsReview' : 'problem';
  return <span className={s.pill} data-status={tone}>{tone === 'processing' ? <Spinner width={12} height={12} /> : <span className={s.dot} />}{statusText(t, status)}</span>;
}

function Empty({ t, view, filtered, total, onAdd }: { t: Words; view: View; filtered: boolean; total: number; onAdd?: () => void }) {
  return <div className={s.empty}>
    <DocsIcon width={28} height={28} />
    <p>{filtered ? t.noDocuments : view === 'review' ? t.libNoReview : t.libEmpty}</p>
    {!filtered && view === 'all' && total === 0 && onAdd && <><p className={s.muted}>{t.libEmptyHint}</p><Button onPress={onAdd}><PlusIcon />{t.libAddDocuments}</Button></>}
  </div>;
}

function Detail({ t, locale, archiveId, doc, headingRef, onClose, narrow }: { t: Words; locale: string; archiveId: string; doc: DocumentRow; headingRef: RefObject<HTMLHeadingElement | null>; onClose: () => void; narrow?: boolean }) {
  const type = typeLabel(t, doc.confirmedType);
  const date = safeDate(doc.issueDate, locale);
  const openable = doc.status === 'needs_review' || doc.status === 'ready';
  const reviewHref = `/archives/${archiveId}/documents/${doc.id}/review`;
  const numberLabel = doc.confirmedType && doc.confirmedType !== 'invoice' ? t.docReference : t.invoiceId;
  const facts: [string, ReactNode][] = [];
  if (doc.supplierName || doc.printedSupplier) facts.push([t.libSupplier, doc.supplierId ? <a href={`/archives/${archiveId}/suppliers/${doc.supplierId}`}><bdi dir="auto">{doc.supplierName ?? doc.printedSupplier}</bdi></a> : <bdi dir="auto">{doc.printedSupplier}</bdi>]);
  if (doc.documentNumber) facts.push([numberLabel, <bdi dir="ltr" key="n">{doc.documentNumber}</bdi>]);
  if (doc.accountReference) facts.push([t.accountReference, <bdi dir="auto" key="a">{doc.accountReference}</bdi>]);
  if (doc.total) facts.push([t.total, <bdi dir="ltr" key="t">{doc.total} {doc.currency}</bdi>]);
  if (doc.categoryName) facts.push([t.category, <bdi dir="auto" key="c">{doc.categoryName}</bdi>]);
  if (doc.propertyName) facts.push([t.property, <bdi dir="auto" key="p">{doc.propertyName}</bdi>]);
  if (doc.paperLocationName || doc.referenceScan) facts.push([t.paperLocation, <bdi dir="auto" key="l">{doc.paperLocationName || t.unknownLocation}</bdi>]);
  if (doc.referenceScan) facts.push([t.referenceScan, '✓']);
  if (doc.confirmedTitle && doc.confirmedTitle !== doc.originalFilename) facts.push(['PDF', <bdi dir="auto" key="f">{doc.originalFilename}</bdi>]);
  const note = doc.status === 'needs_review' ? t.libReviewNote : IN_FLIGHT.has(doc.status) ? t.libProcessingNote : doc.status === 'file_blocked' ? t.libBlockedNote : doc.status === 'processing_failed' ? t.libFailedNote : '';
  const tone = doc.status === 'needs_review' ? 'needsReview' : IN_FLIGHT.has(doc.status) ? 'processing' : 'problem';

  return <section className={s.preview} aria-labelledby="detail-title" onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); onClose(); } }}>
    <div className={s.previewBar}>
      {narrow ? <button type="button" className={s.backButton} onClick={onClose}><ChevronIcon flip={locale === 'he'} />{t.libBack}</button> : <span />}
      {!narrow && <button type="button" className={s.iconButton} aria-label={t.libClose} onClick={onClose}><CloseIcon /></button>}
    </div>
    <div className={s.previewHead}>
      <FileGlyph type={doc.confirmedType} />
      <div>
        <h2 id="detail-title" ref={headingRef} tabIndex={-1} className={s.previewTitle} dir="auto">{displayName(doc)}</h2>
        <p className={s.muted}>{[type || (doc.status === 'ready' ? '' : t.libNotIdentified), date].filter(Boolean).join(' · ')}{doc.status !== 'ready' && <> · {statusText(t, doc.status)}</>}</p>
      </div>
    </div>
    {note && <div className={s.callout} data-status={tone} role={IN_FLIGHT.has(doc.status) ? 'status' : undefined}>{tone === 'processing' ? <Spinner /> : <ReviewIcon />}<p>{note}</p></div>}
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
