'use client';
import { getArchive, listArchives, problemMessage, updateArchive, type Archive, type ProblemCode } from '@invoyis/contracts';
import { Button, ModalDialog, PopoverButton, Select, TextField } from '@invoyis/ui';
import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import { LanguageSelect } from '../../../../components/language-select';
import { formatDateOnly } from '../../../../lib/document-format.mjs';
import { useWords } from '../../../../lib/catalog';
import { intent } from '../../../../lib/archive-ui';
import { UploadForm } from '../upload-form';
import { ExportAction } from '../export-action';
import { PreviewPanel } from '../documents/[id]/review/preview-panel';
import { BriefcaseIcon, ChevronIcon, CloseIcon, DocsIcon, DownloadIcon, FileGlyph, FilterIcon, GearIcon, MenuIcon, PersonIcon, PlusIcon, ReviewIcon, SearchIcon, Spinner } from './icons';
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
function useNarrow() {
  const [narrow, setNarrow] = useState(false);
  useEffect(() => { const m = window.matchMedia('(max-width: 63.99rem)'); const on = () => setNarrow(m.matches); on(); m.addEventListener('change', on); return () => m.removeEventListener('change', on); }, []);
  return narrow;
}

export function ArchiveLibrary({ archiveId, initial, hasQuery, openAdd }: { archiveId: string; initial: LibraryState; hasQuery: boolean; openAdd: boolean }) {
  const { locale, t } = useWords();
  const rtl = locale === 'he';
  const narrow = useNarrow();
  const [st, setSt] = useState<LibraryState>(initial);
  const { view, query, docId } = st;
  const [archive, setArchive] = useState<Archive>();
  const [archives, setArchives] = useState<Archive[]>([]);
  const [loadError, setLoadError] = useState('');
  const [uploadOpen, setUploadOpen] = useState(openAdd);
  const [menuOpen, setMenuOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const update = (patch: Partial<LibraryState>) => setSt(prev => ({ ...prev, ...patch }));

  const serverFilters = { categoryId: st.categoryId || undefined, propertyId: st.propertyId || undefined, paperLocationId: st.paperLocationId || undefined };
  const { items, loading, error, truncated, refresh } = useDocuments(archiveId, serverFilters, uploadOpen);
  const choices = useChoices(archiveId);

  useEffect(() => {
    let live = true;
    getArchive({ path: { archiveId }, credentials: 'same-origin' }).then(({ data, error, response }) => {
      if (!live) return;
      if (data) setArchive(data);
      else if (response?.status === 401) window.location.assign(`/sign-in?next=/archives/${archiveId}`);
      else if (response?.status === 404) window.location.replace('/archives?access=changed');
      else setLoadError(problemMessage((error as { code?: ProblemCode })?.code ?? 'INTERNAL_ERROR', locale));
    }).catch(() => { if (live) setLoadError(problemMessage('UPSTREAM_UNAVAILABLE', locale)); });
    listArchives({ credentials: 'same-origin' }).then(({ data }) => { if (live && data) setArchives(data.items); }).catch(() => undefined);
    return () => { live = false; };
  }, [archiveId, locale]);

  // Coming back without a query (sidebar link, review page "Back") restores this archive's last view.
  useEffect(() => {
    storage(store => store.setItem(lastArchiveKey, archiveId));
    if (hasQuery) return;
    storage(store => { const saved = store.getItem(lastViewKey(archiveId)); if (saved) setSt(parseLibraryState(new URLSearchParams(saved))); });
  }, [archiveId, hasQuery]);
  useEffect(() => {
    const search = toLibrarySearch(st);
    if (search !== location.search) history.replaceState(history.state, '', `${location.pathname}${search}`);
    storage(store => store.setItem(lastViewKey(archiveId), search));
  }, [st, archiveId]);
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
  const activeFilters = filterDefs.filter(f => st[f.key]);
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
  function setView(next: View) { update({ view: next, docId: null }); setMenuOpen(false); }
  function onRowKey(e: KeyboardEvent<HTMLButtonElement>, index: number) {
    const move = e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : e.key === 'Home' ? -index : e.key === 'End' ? visible.length - 1 - index : 0;
    if (move) {
      e.preventDefault();
      const next = visible[Math.max(0, Math.min(visible.length - 1, index + move))];
      document.getElementById(`row-${next.id}`)?.focus();
      if (!narrow && selected) openDoc(next.id, true);
    } else if (e.key === 'Escape' && selected) { e.preventDefault(); closeDoc(); }
  }

  const canUpload = archive && archive.role !== 'viewer';
  const sidebar = <Sidebar t={t} archives={archives} current={archive} archiveId={archiveId} view={view} reviewCount={reviewCount} total={items.length}
    onView={setView} onSettings={() => { setMenuOpen(false); setSettingsOpen(true); }} />;

  return <div className={s.root} data-detail={narrow && selected ? 'open' : 'closed'}>
    <aside className={s.sidebar} aria-label={t.libMenu}>{sidebar}</aside>

    <main className={s.main} inert={narrow && Boolean(selected)}>
      <header className={s.toolbar}>
        <button type="button" className={`${s.iconButton} ${s.menuButton}`} aria-label={t.libOpenMenu} onClick={() => setMenuOpen(true)}><MenuIcon /></button>
        <div className={s.titleBlock}>
          <h1 className={s.title}>{archive && <ArchiveIcon kind={archive.kind} />}<span className={s.titleText} dir="auto">{archive?.name ?? ' '}</span></h1>
          <p className={s.subtitle}>{view === 'review' ? t.libNeedsReview : t.libAllDocuments}{!loading && <> · {items.length === 1 ? t.libDocCountOne : fill(t.libDocCount, { n: items.length })}</>}</p>
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
              {filterDefs.map(f => <Select key={f.key} label={t[f.label]} selectedKey={st[f.key] || '__any'}
                onSelectionChange={k => update({ [f.key]: k === '__any' ? '' : String(k), docId: null } as Partial<LibraryState>)}
                options={[{ id: '__any', label: t.libAny }, ...choices[f.kind].map(c => ({ id: c.id, label: c.displayName }))]} />)}
              {activeFilters.length > 0 && <Button variant="quiet" onPress={() => update({ categoryId: '', propertyId: '', paperLocationId: '' })}>{t.clearFilters}</Button>}
            </div>
          </PopoverButton>
          {canUpload && <Button className={`${s.addButton} ${s.hideNarrow}`} onPress={() => setUploadOpen(true)}><PlusIcon />{t.libAddDocuments}</Button>}
        </div>
      </header>

      <nav className={s.mobileViews} aria-label={t.libMenu}>
        <button type="button" aria-current={view === 'all' ? 'page' : undefined} onClick={() => setView('all')}>{t.libAllDocuments}</button>
        <button type="button" aria-current={view === 'review' ? 'page' : undefined} onClick={() => setView('review')}>{t.libNeedsReview}{reviewCount > 0 && <span className={s.reviewCount}>{reviewCount}</span>}</button>
      </nav>

      {(activeFilters.length > 0 || query) && <div className={s.chips} role="group" aria-label={t.filterDocuments}>
        {query && <button type="button" className={s.chip} aria-label={`${fill(t.libSearchFor, { q: query })} — ${t.libClearSearch}`} onClick={() => update({ query: '' })}><span>{fill(t.libSearchFor, { q: query })}</span><CloseIcon width={12} height={12} /></button>}
        {activeFilters.map(f => { const name = choiceName(f.kind, st[f.key] as string); return <button key={f.key} type="button" className={s.chip} aria-label={fill(t.libRemoveFilter, { label: `${t[f.label]}: ${name}` })} onClick={() => update({ [f.key]: '' } as Partial<LibraryState>)}><span>{t[f.label]}: <bdi dir="auto">{name}</bdi></span><CloseIcon width={12} height={12} /></button>; })}
        <button type="button" className={s.clearAll} onClick={() => update({ categoryId: '', propertyId: '', paperLocationId: '', query: '' })}>{t.libClearAll}</button>
      </div>}

      {/* Kept mounted while closed so uploads in progress continue. */}
      {canUpload && <section className={s.uploadPanel} hidden={!uploadOpen} aria-labelledby="upload-heading">
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
        <section className={s.listPane} aria-label={fill(t.libRows, { archive: archive?.name ?? '' })} aria-busy={loading}>
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
          </ul> : !error && <Empty t={t} view={view} filtered={activeFilters.length > 0 || Boolean(query)} total={items.length} onAdd={canUpload ? () => setUploadOpen(true) : undefined} />}
          {truncated && <p className={s.muted} role="note">{fill(t.libTruncated, { n: MAX_PAGES * 20 })}</p>}
        </section>
        {!narrow && selected && <Detail t={t} locale={locale} archiveId={archiveId} doc={selected} headingRef={detailHeading} onClose={closeDoc} />}
      </div>
    </main>

    {narrow && selected && <div className={s.fullscreen} role="region" aria-label={displayName(selected)}>
      <Detail t={t} locale={locale} archiveId={archiveId} doc={selected} headingRef={detailHeading} onClose={closeDoc} narrow />
    </div>}

    {canUpload && <Button className={s.fab} onPress={() => { setUploadOpen(true); window.scrollTo({ top: 0 }); }}><PlusIcon width={18} height={18} /><span>{t.libAddDocuments}</span></Button>}

    <ModalDialog isOpen={menuOpen} onOpenChange={setMenuOpen} title={<span className={s.srOnly}>{t.libMenu}</span>} className={s.sheet}>
      <button type="button" className={`${s.iconButton} ${s.sheetClose}`} aria-label={t.libCloseMenu} onClick={() => setMenuOpen(false)}><CloseIcon /></button>
      {sidebar}
    </ModalDialog>
    {archive && <SettingsDialog t={t} locale={locale} archive={archive} isOpen={settingsOpen} onOpenChange={setSettingsOpen} onSaved={setArchive} />}
  </div>;
}

function ArchiveIcon({ kind }: { kind: 'personal' | 'business' }) {
  return <span className={s.archiveIcon} data-kind={kind}>{kind === 'personal' ? <PersonIcon /> : <BriefcaseIcon />}</span>;
}
function statusText(t: Words, status: string) {
  return ({ pending: t.statusPending, processing: t.statusProcessing, needs_review: t.statusNeedsReview, file_blocked: t.statusFileBlocked, processing_failed: t.statusProcessingFailed, ready: t.statusReady } as Record<string, string>)[status] ?? status;
}
function StatusPill({ t, status }: { t: Words; status: string }) {
  if (status === 'ready') return <span className={s.srOnly}>, {t.statusReady}</span>;
  const tone = IN_FLIGHT.has(status) ? 'processing' : status === 'needs_review' ? 'needsReview' : 'problem';
  return <span className={s.pill} data-status={tone}>{tone === 'processing' ? <Spinner width={12} height={12} /> : <span className={s.dot} />}{statusText(t, status)}</span>;
}

function Sidebar({ t, archives, current, archiveId, view, reviewCount, total, onView, onSettings }: {
  t: Words; archives: Archive[]; current?: Archive; archiveId: string; view: View; reviewCount: number; total: number; onView: (v: View) => void; onSettings: () => void;
}) {
  const list = archives.length ? archives : current ? [current] : [];
  return <div className={s.sidebarInner}>
    <div className={s.brand}><span className={s.logo} aria-hidden="true">I</span>Invoyis</div>
    <h2 className={s.sideHeading}>{t.archives}</h2>
    <ul className={s.archiveList}>
      {list.map(a => {
        const active = a.id === archiveId;
        // Full navigation (not client routing) so an upload in progress can warn before leaving.
        return <li key={a.id}>
          <a href={`/archives/${a.id}`} className={s.archiveItem} aria-current={active ? 'true' : undefined}>
            <ArchiveIcon kind={a.kind} />
            <span className={s.archiveText}><span className={s.archiveName} dir="auto">{a.name}</span><small>{a.kind === 'personal' ? t.personal : t.business} · {t[a.role]}</small></span>
          </a>
          {active && <ul className={s.viewList}>
            <li><button type="button" className={s.viewItem} aria-current={view === 'all' ? 'page' : undefined} onClick={() => onView('all')}><DocsIcon />{t.libAllDocuments}<span className={s.count}>{total}</span></button></li>
            <li><button type="button" className={s.viewItem} aria-current={view === 'review' ? 'page' : undefined} onClick={() => onView('review')}><ReviewIcon />{t.libNeedsReview}{reviewCount > 0 ? <span className={s.reviewCount}>{reviewCount}</span> : <span className={s.count}>0</span>}</button></li>
          </ul>}
        </li>;
      })}
    </ul>
    <div className={s.sideFooter}>
      <a href="/archives?new=1" className={s.quietItem}><PlusIcon />{t.libNewArchive}</a>
      {current?.role === 'admin' && <button type="button" className={s.quietItem} onClick={onSettings}><GearIcon />{t.libSettings}</button>}
      {current?.role === 'admin' && <a href={`/archives/${current.id}/members`} className={s.quietItem}><PersonIcon />{t.members}</a>}
      {current?.canBulkExport && <div className={s.exportSlot}><ExportAction archiveId={current.id} /></div>}
      <div className={s.langSelect}><LanguageSelect signedIn /></div>
    </div>
  </div>;
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

function SettingsDialog({ t, locale, archive, isOpen, onOpenChange, onSaved }: { t: Words; locale: ReturnType<typeof useWords>["locale"]; archive: Archive; isOpen: boolean; onOpenChange: (open: boolean) => void; onSaved: (a: Archive) => void }) {
  const [name, setName] = useState(archive.name);
  const [jurisdiction, setJurisdiction] = useState(archive.jurisdiction ?? '');
  const [invalid, setInvalid] = useState<'name' | 'jurisdiction' | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (isOpen) { setName(archive.name); setJurisdiction(archive.jurisdiction ?? ''); setInvalid(null); setError(''); } }, [isOpen, archive]);
  async function save(event: FormEvent) {
    event.preventDefault(); setError(''); setInvalid(null);
    if (!name.trim()) { setInvalid('name'); return; }
    if (archive.kind === 'business' && !/^[A-Za-z]{2}$/.test(jurisdiction)) { setInvalid('jurisdiction'); return; }
    setBusy(true);
    const { data, error } = await updateArchive({ path: { archiveId: archive.id }, body: { name, jurisdiction: jurisdiction.toUpperCase() }, headers: intent, credentials: 'same-origin' });
    setBusy(false);
    if (data) { onSaved(data); onOpenChange(false); } else setError(problemMessage((error as { code?: ProblemCode })?.code ?? 'INTERNAL_ERROR', locale));
  }
  return <ModalDialog isOpen={isOpen} onOpenChange={onOpenChange} title={t.libSettings} className={s.settingsDialog}>
    <p className={s.muted}>{archive.kind === 'personal' ? t.personal : t.business} · {t.libRoleLabel}: {t[archive.role]}</p>
    <form className={s.settingsForm} noValidate onSubmit={save}>
      <TextField label={t.name} value={name} onChange={v => { setName(v); setInvalid(null); }} isRequired error={invalid === 'name' ? t.invalidName : undefined} inputProps={{ maxLength: 120 }} />
      <TextField label={t.jurisdiction} value={jurisdiction} onChange={v => { setJurisdiction(v); setInvalid(null); }} isRequired={archive.kind === 'business'} error={invalid === 'jurisdiction' ? t.invalidJurisdiction : undefined} inputProps={{ dir: 'ltr', pattern: '[A-Za-z]{2}', maxLength: 2 }} />
      {error && <p className="ui-error" role="alert">{error}</p>}
      <div className="ui-dialog-actions">
        <Button variant="secondary" onPress={() => onOpenChange(false)}>{t.cancel}</Button>
        <Button type="submit" isDisabled={busy}>{busy ? t.loading : t.save}</Button>
      </div>
    </form>
  </ModalDialog>;
}

