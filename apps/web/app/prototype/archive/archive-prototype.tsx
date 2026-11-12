'use client';
import { Button, I18nProvider, ModalDialog, PopoverButton, Select } from '@invoyis/ui';
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type RefObject } from 'react';
import { formatDateOnly } from '../../../lib/document-format.mjs';
import { archives, filterKeys, initialDocuments, sampleUpload, type DocType, type FilterKey, type ProtoDocument } from './fixtures';
import { BriefcaseIcon, ChevronIcon, CloseIcon, DocsIcon, DownloadIcon, FileGlyph, FilterIcon, GearIcon, MenuIcon, PersonIcon, PlusIcon, ReviewIcon, SearchIcon, Spinner, UploadIcon } from './icons';
import { SamplePage } from './sample-page';
import { fill, words, type ProtoLocale, type Words } from './words';
import s from './prototype.module.css';
import { toSearch, parseUrlState, type UrlState, type View } from './url-state';

const PROCESSING_MS = 4000;
const filterLabel: Record<FilterKey, keyof Words> = { category: 'category', property: 'property', paperLocation: 'paperLocation' };

function typeLabel(t: Words, type: DocType | null) { return type ? t[type] : t.unknownType; }
function useNarrow() {
  const [narrow, setNarrow] = useState(false);
  useEffect(() => { const m = window.matchMedia('(max-width: 63.99rem)'); const on = () => setNarrow(m.matches); on(); m.addEventListener('change', on); return () => m.removeEventListener('change', on); }, []);
  return narrow;
}

export function ArchivePrototype({ initial }: { initial: UrlState }) {
  const [st, setSt] = useState<UrlState>(initial);
  const { locale, archiveId, view, query, filters, docId, step } = st;
  const t = words[locale];
  const dir = locale === 'he' ? 'rtl' : 'ltr';
  const narrow = useNarrow();
  const [docs, setDocs] = useState<ProtoDocument[]>(initialDocuments);
  const [addOpen, setAddOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [toast, setToast] = useState<{ text: string; docId?: string } | null>(null);
  const [announce, setAnnounce] = useState('');
  const [flashId, setFlashId] = useState<string | null>(null);
  const timers = useRef<number[]>([]);
  const localeRef = useRef(locale); localeRef.current = locale;
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const update = useCallback((patch: Partial<UrlState>) => setSt(prev => ({ ...prev, ...patch })), []);

  // Keep <html> in step with the prototype's language so popovers and dialogs (portalled to <body>) mirror correctly.
  useEffect(() => {
    const html = document.documentElement; const prev = { lang: html.lang, dir: html.dir };
    html.lang = locale; html.dir = dir;
    return () => { html.lang = prev.lang; html.dir = prev.dir; };
  }, [locale, dir]);
  useEffect(() => { if ('scrollRestoration' in history) history.scrollRestoration = 'manual'; }, []);
  // URL mirrors state; document/review steps are pushed so the browser Back button behaves like the in-app Back.
  useEffect(() => { const next = toSearch(st); if (next !== location.search) history.replaceState(history.state, '', next); }, [st]);
  useEffect(() => {
    const onPop = () => setSt(parseUrlState(new URLSearchParams(location.search), 'en'));
    window.addEventListener('popstate', onPop); return () => window.removeEventListener('popstate', onPop);
  }, []);
  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  useEffect(() => { if (!toast) return; const id = window.setTimeout(() => setToast(null), 7000); return () => clearTimeout(id); }, [toast]);

  const archive = archives.find(a => a.id === archiveId)!;
  const inArchive = useMemo(() => docs.filter(d => d.archiveId === archiveId), [docs, archiveId]);
  const reviewCount = (id: string) => docs.filter(d => d.archiveId === id && d.status === 'needsReview').length;
  const visible = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return inArchive
      .filter(d => view === 'all' || d.status === 'needsReview')
      .filter(d => filterKeys.every(k => !filters[k] || d[k] === filters[k]))
      .filter(d => !needle || [d.name, d.type ? words.en[d.type] : '', typeLabel(t, d.type)].some(v => v.toLocaleLowerCase().includes(needle)))
      .sort((a, b) => b.date.localeCompare(a.date) || b.added.localeCompare(a.added));
  }, [inArchive, view, filters, query, t]);
  const selected = docs.find(d => d.id === docId && d.archiveId === archiveId) ?? null;
  const detailOpen = Boolean(selected);
  const activeFilters = filterKeys.filter(k => filters[k]);

  // Whenever a document closes (in-app Back, Esc or browser Back), return focus to its row.
  const lastDoc = useRef(docId);
  useEffect(() => { const prev = lastDoc.current; lastDoc.current = docId; if (prev && !docId) focusRow(prev); });
  const focusRow = (id: string | null) => requestAnimationFrame(() => { if (id) document.getElementById(`row-${id}`)?.focus({ preventScroll: !narrow }); });

  function openDoc(id: string, opts: { keepFocus?: boolean } = {}) {
    setToast(prev => prev?.docId === id ? null : prev);
    if (!docId) history.pushState({ proto: 'doc' }, '', toSearch({ ...st, docId: id, step: 'preview' }));
    update({ docId: id, step: 'preview' });
    if (narrow && !opts.keepFocus) requestAnimationFrame(() => detailHeading.current?.focus());
  }
  function closeDoc() {
    if (history.state?.proto === 'doc') history.back(); else update({ docId: null, step: 'preview' });
  }
  function enterReview() {
    history.pushState({ proto: 'review' }, '', toSearch({ ...st, step: 'review' }));
    update({ step: 'review' });
    requestAnimationFrame(() => detailHeading.current?.focus());
  }
  function exitReview() {
    if (history.state?.proto === 'review') history.back(); else update({ step: 'preview' });
    requestAnimationFrame(() => detailHeading.current?.focus());
  }
  function switchArchive(id: string, nextView: View = 'all') {
    update({ archiveId: id, view: nextView, docId: null, step: 'preview', filters: id === archiveId ? filters : {}, query: id === archiveId ? query : '' });
    setMenuOpen(false);
  }

  function simulateUpload(dest: string, fileName: string) {
    const id = `u-${Date.now()}`;
    const today = '2026-11-12';
    const destName = archives.find(a => a.id === dest)!.name;
    setDocs(prev => [...prev, { id, archiveId: dest, name: fileName, type: null, status: 'processing', date: today, added: today, from: '—', pages: 1 }]);
    update({ archiveId: dest, view: 'all', filters: {}, query: '', docId: null, step: 'preview' });
    setFlashId(id);
    const msg = fill(t.uploadedTo, { file: fileName, archive: destName });
    setToast({ text: msg, docId: id }); setAnnounce(msg);
    timers.current.push(window.setTimeout(() => {
      const d = sampleUpload.detected;
      setDocs(prev => prev.map(x => x.id === id ? { ...x, status: 'needsReview', suggestedTitle: d.name, type: d.type, date: d.date, from: d.from, reference: d.reference, category: d.category, property: d.property } : x));
      const done = fill(words[localeRef.current].nowNeedsReview, { file: fileName });
      setToast({ text: done, docId: id }); setAnnounce(done);
    }, PROCESSING_MS));
  }
  function onRowKey(e: KeyboardEvent<HTMLButtonElement>, index: number) {
    const move = e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : e.key === 'Home' ? -index : e.key === 'End' ? visible.length - 1 - index : 0;
    if (move) {
      e.preventDefault();
      const next = visible[Math.max(0, Math.min(visible.length - 1, index + move))];
      document.getElementById(`row-${next.id}`)?.focus();
      if (!narrow && detailOpen) openDoc(next.id, { keepFocus: true }); // Finder-like: preview follows selection on wide screens
    } else if (e.key === 'Escape' && detailOpen) { e.preventDefault(); closeDoc(); }
  }

  const sidebar = <Sidebar t={t} archiveId={archiveId} view={view} locale={locale} reviewCount={reviewCount} countFor={id => docs.filter(d => d.archiveId === id).length}
    onArchive={switchArchive} onView={v => { update({ view: v, docId: null, step: 'preview' }); setMenuOpen(false); }}
    onLocale={l => update({ locale: l })} />;

  const reviewing = selected && step === 'review';

  return <I18nProvider locale={locale}>
    <div className={s.root} dir={dir} lang={locale} data-detail={detailOpen ? 'open' : 'closed'}>
      <aside className={s.sidebar} aria-label={t.menu}>{sidebar}</aside>

      <main className={s.main} inert={narrow && detailOpen}>
        {/* Toolbar */}
        <header className={s.toolbar}>
          <button type="button" className={`${s.iconButton} ${s.menuButton}`} aria-label={t.openMenu} onClick={() => setMenuOpen(true)}><MenuIcon /></button>
          <div className={s.titleBlock}>
            <h1 className={s.title}><ArchiveIcon kind={archive.kind} /><span className={s.titleText} dir="auto">{archive.name}</span></h1>
            <p className={s.subtitle}>{view === 'review' ? t.needsReview : t.allDocuments} · {inArchive.length === 1 ? t.documentsCountOne : fill(t.documentsCount, { n: inArchive.length })}</p>
          </div>
          <div className={s.toolbarActions}>
            <label className={s.search}>
              <SearchIcon className={s.searchIcon} />
              <span className={s.srOnly}>{t.searchDocuments}</span>
              <input type="search" value={query} placeholder={t.searchDocuments} onChange={e => update({ query: e.target.value, docId: null, step: 'preview' })} onKeyDown={e => { if (e.key === 'Escape' && query) { e.preventDefault(); update({ query: '' }); } }} />
            </label>
            <PopoverButton dialogLabel={t.filterDocuments} placement={dir === 'rtl' ? 'bottom start' : 'bottom end'} buttonClassName={`${s.toolButton} ${activeFilters.length ? s.toolButtonOn : ''}`}
              label={<><FilterIcon /><span className={s.hideXs}>{t.filter}</span>{activeFilters.length > 0 && <span className={s.badge}>{activeFilters.length}</span>}</>}>
              <div className={s.filterPanel}>
                <h2 className={s.panelTitle}>{t.filterDocuments}</h2>
                {filterKeys.map(key => {
                  const values = [...new Set(inArchive.map(d => d[key]).filter(Boolean) as string[])].sort();
                  return <Select key={key} label={t[filterLabel[key]] as string} selectedKey={filters[key] ?? '__any'}
                    onSelectionChange={k => update({ filters: { ...filters, [key]: k === '__any' ? undefined : String(k) }, docId: null })}
                    options={[{ id: '__any', label: t.any }, ...values.map(v => ({ id: v, label: v }))]} />;
                })}
                {activeFilters.length > 0 && <Button variant="quiet" onPress={() => update({ filters: {} })}>{t.clearFilters}</Button>}
              </div>
            </PopoverButton>
            <Button className={`${s.addButton} ${s.hideNarrow}`} onPress={() => setAddOpen(true)}><PlusIcon />{t.addDocuments}</Button>
          </div>
        </header>

        {/* Narrow-screen view switch (the sidebar is in the menu sheet there) */}
        <nav className={s.mobileViews} aria-label={t.menu}>
          <button type="button" aria-current={view === 'all' ? 'page' : undefined} onClick={() => update({ view: 'all', docId: null })}>{t.allDocuments}</button>
          <button type="button" aria-current={view === 'review' ? 'page' : undefined} onClick={() => update({ view: 'review', docId: null })}>{t.needsReview}{reviewCount(archiveId) > 0 && <span className={s.reviewCount}>{reviewCount(archiveId)}</span>}</button>
        </nav>

        {(activeFilters.length > 0 || query) && <div className={s.chips} role="group" aria-label={t.filterDocuments}>
          {query && <button type="button" className={s.chip} aria-label={`${fill(t.searchFor, { q: query })} — ${t.clearSearch}`} onClick={() => update({ query: '' })}><span>{fill(t.searchFor, { q: query })}</span><CloseIcon width={12} height={12} /></button>}
          {activeFilters.map(k => { const label = `${t[filterLabel[k]]}: ${filters[k]}`; return <button key={k} type="button" className={s.chip} aria-label={fill(t.removeFilter, { label })} onClick={() => update({ filters: { ...filters, [k]: undefined } })}><span>{t[filterLabel[k]] as string}: <bdi dir="auto">{filters[k]}</bdi></span><CloseIcon width={12} height={12} /></button>; })}
          <button type="button" className={s.clearAll} onClick={() => update({ filters: {}, query: '' })}>{t.clearAll}</button>
        </div>}

        {reviewing && !narrow ? <ReviewEntry t={t} locale={locale} doc={selected} headingRef={detailHeading} onBack={exitReview} /> :
        <div className={s.split} data-preview={selected && !narrow ? 'open' : 'closed'}>
          <section className={s.listPane} aria-label={fill(t.rowsLabel, { archive: archive.name })}>
            <div className={s.listHeader} aria-hidden="true"><span /><span>{t.name}</span><span className={s.colType}>{t.type}</span><span className={s.colDate}>{t.date}</span></div>
            {visible.length ? <ul className={s.rows}>
              {visible.map((d, i) => <li key={d.id}>
                <button id={`row-${d.id}`} type="button" className={`${s.row} ${d.id === flashId ? s.flash : ''}`} aria-current={d.id === docId ? 'true' : undefined} data-status={d.status}
                  onClick={() => d.id === docId && !narrow ? closeDoc() : openDoc(d.id, { keepFocus: !narrow })} onKeyDown={e => onRowKey(e, i)}>
                  <FileGlyph type={d.type} />
                  <span className={s.nameCell}>
                    <span className={s.name} dir="auto">{d.name}</span>
                    <StatusPill t={t} status={d.status} />
                    <span className={s.meta}>{typeLabel(t, d.type)} · <bdi dir="ltr">{formatDateOnly(d.date, locale)}</bdi></span>
                  </span>
                  <span className={s.colType}>{typeLabel(t, d.type)}</span>
                  <span className={s.colDate}><time dateTime={d.date}><bdi dir="ltr">{formatDateOnly(d.date, locale)}</bdi></time></span>
                </button>
              </li>)}
            </ul> : <Empty t={t} view={view} filtered={activeFilters.length > 0 || Boolean(query)} total={inArchive.length} onAdd={() => setAddOpen(true)} />}
          </section>
          {!narrow && selected && <Preview t={t} locale={locale} doc={selected} headingRef={detailHeading} onClose={closeDoc} onReview={enterReview} />}
        </div>}
      </main>

      {/* Narrow screens: the selected document takes the whole screen; the list stays mounted beneath it so scroll position survives. */}
      {narrow && selected && <div className={s.fullscreen} role="region" aria-label={selected.name}>
        {reviewing ? <ReviewEntry t={t} locale={locale} doc={selected} headingRef={detailHeading} onBack={exitReview} />
          : <Preview t={t} locale={locale} doc={selected} headingRef={detailHeading} onClose={closeDoc} onReview={enterReview} narrow />}
      </div>}

      <Button className={s.fab} onPress={() => setAddOpen(true)}><PlusIcon width={18} height={18} /><span>{t.addDocuments}</span></Button>

      <ModalDialog isOpen={menuOpen} onOpenChange={setMenuOpen} title={<span className={s.srOnly}>{t.menu}</span>} className={s.sheet}>
        <button type="button" className={`${s.iconButton} ${s.sheetClose}`} aria-label={t.cancel} onClick={() => setMenuOpen(false)}><CloseIcon /></button>
        {sidebar}
      </ModalDialog>

      {addOpen && <AddDialog t={t} initialArchive={archiveId} onClose={() => setAddOpen(false)} onUpload={(dest, file) => { setAddOpen(false); simulateUpload(dest, file); }} />}

      {toast && <div className={s.toast} role="status">
        <span>{toast.text}</span>
        {toast.docId && <button type="button" onClick={() => { const id = toast.docId!; setToast(null); openDoc(id); }}>{t.view}</button>}
        <button type="button" className={s.iconButton} aria-label={t.cancel} onClick={() => setToast(null)}><CloseIcon width={14} height={14} /></button>
      </div>}
      <p className={s.srOnly} aria-live="polite">{announce}</p>
    </div>
  </I18nProvider>;
}

function ArchiveIcon({ kind }: { kind: 'personal' | 'business' }) {
  return <span className={s.archiveIcon} data-kind={kind}>{kind === 'personal' ? <PersonIcon /> : <BriefcaseIcon />}</span>;
}

function StatusPill({ t, status }: { t: Words; status: ProtoDocument['status'] }) {
  if (status === 'ready') return <span className={s.srOnly}>, {t.ready}</span>;
  return <span className={s.pill} data-status={status}>{status === 'processing' ? <Spinner width={12} height={12} /> : <span className={s.dot} />}{status === 'processing' ? t.processing : t.reviewStatus}</span>;
}

function Sidebar({ t, archiveId, view, locale, reviewCount, countFor, onArchive, onView, onLocale }: {
  t: Words; archiveId: string; view: View; locale: ProtoLocale; reviewCount: (id: string) => number; countFor: (id: string) => number;
  onArchive: (id: string, view?: View) => void; onView: (v: View) => void; onLocale: (l: ProtoLocale) => void;
}) {
  return <div className={s.sidebarInner}>
    <div className={s.brand}><span className={s.logo} aria-hidden="true">I</span>Invoyis<span className={s.protoBadge}>{t.prototypeBadge}</span></div>
    <h2 className={s.sideHeading}>{t.archives}</h2>
    <ul className={s.archiveList}>
      {archives.map(a => {
        const active = a.id === archiveId; const pending = reviewCount(a.id);
        return <li key={a.id}>
          <button type="button" className={s.archiveItem} aria-current={active ? 'true' : undefined} onClick={() => onArchive(a.id)}>
            <ArchiveIcon kind={a.kind} />
            <span className={s.archiveText}><span className={s.archiveName} dir="auto">{a.name}</span><small>{a.kind === 'personal' ? t.personal : t.business}</small></span>
            {!active && pending > 0 && <span className={s.reviewDot} title={`${t.needsReview}: ${pending}`}><span className={s.srOnly}>{t.needsReview}: {pending}</span></span>}
          </button>
          {active && <ul className={s.viewList}>
            <li><button type="button" className={s.viewItem} aria-current={view === 'all' ? 'page' : undefined} onClick={() => onView('all')}><DocsIcon />{t.allDocuments}<span className={s.count}>{countFor(a.id)}</span></button></li>
            <li><button type="button" className={s.viewItem} aria-current={view === 'review' ? 'page' : undefined} onClick={() => onView('review')}><ReviewIcon />{t.needsReview}{pending > 0 ? <span className={s.reviewCount}>{pending}</span> : <span className={s.count}>0</span>}</button></li>
          </ul>}
        </li>;
      })}
    </ul>
    <div className={s.sideFooter}>
      <button type="button" className={s.quietItem} onClick={() => { /* simulated */ }} title={t.simulated}><PlusIcon />{t.newArchive}</button>
      <button type="button" className={s.quietItem} onClick={() => { /* simulated */ }} title={t.simulated}><GearIcon />{t.archiveSettings}</button>
      <div className={s.langSelect}><Select label={t.language} selectedKey={locale} onSelectionChange={k => onLocale(String(k) as ProtoLocale)} options={[{ id: 'en', label: 'English' }, { id: 'fr', label: 'Français' }, { id: 'he', label: 'עברית' }]} /></div>
    </div>
  </div>;
}

function Empty({ t, view, filtered, total, onAdd }: { t: Words; view: View; filtered: boolean; total: number; onAdd: () => void }) {
  return <div className={s.empty}>
    <DocsIcon width={28} height={28} />
    <p>{filtered ? t.noMatches : view === 'review' ? t.noReview : t.emptyArchive}</p>
    {!filtered && view === 'all' && total === 0 && <><p className={s.muted}>{t.emptyHint}</p><Button onPress={onAdd}><PlusIcon />{t.addDocuments}</Button></>}
  </div>;
}

function Preview({ t, locale, doc, headingRef, onClose, onReview, narrow }: { t: Words; locale: ProtoLocale; doc: ProtoDocument; headingRef: RefObject<HTMLHeadingElement | null>; onClose: () => void; onReview: () => void; narrow?: boolean }) {
  const rtl = locale === 'he';
  return <section className={s.preview} aria-labelledby="preview-title" onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); onClose(); } }}>
    <div className={s.previewBar}>
      {narrow ? <button type="button" className={s.backButton} onClick={onClose}><ChevronIcon flip={rtl} />{t.back}</button> : <span />}
      {!narrow && <button type="button" className={s.iconButton} aria-label={t.close} onClick={onClose}><CloseIcon /></button>}
    </div>
    <div className={s.previewHead}>
      <FileGlyph type={doc.type} />
      <div>
        <h2 id="preview-title" ref={headingRef} tabIndex={-1} className={s.previewTitle}><bdi dir="auto">{doc.name}</bdi></h2>
        <p className={s.muted}>{typeLabel(t, doc.type)} · <bdi dir="ltr">{formatDateOnly(doc.date, locale)}</bdi></p>
      </div>
    </div>
    {doc.status === 'needsReview' && <div className={s.callout} data-status="needsReview"><ReviewIcon /><p>{t.reviewNote}</p></div>}
    {doc.status === 'processing' && <div className={s.callout} data-status="processing"><Spinner /><p>{t.processingNote}</p></div>}
    <div className={s.actions}>
      {doc.status === 'needsReview' && <Button onPress={onReview}>{t.review}</Button>}
      {doc.status !== 'processing' && <Button variant="secondary" className={s.secondary}><DownloadIcon />{t.download}</Button>}
    </div>
    <figure className={s.pageFrame}>
      {doc.status === 'processing' ? <div className={s.pageSkeleton} aria-hidden="true" /> : <SamplePage doc={doc} label={fill(t.previewOf, { name: doc.name })} />}
      <figcaption>{t.samplePage} · {fill(t.page, { n: 1, total: doc.pages })}</figcaption>
    </figure>
    {doc.status !== 'processing' && <dl className={s.facts}>
      <div><dt>{t.from}</dt><dd><bdi dir="auto">{doc.from}</bdi></dd></div>
      {doc.reference && <div><dt>{t.reference}</dt><dd><bdi dir="ltr">{doc.reference}</bdi></dd></div>}
      <div><dt>{t.added}</dt><dd><bdi dir="ltr">{formatDateOnly(doc.added, locale)}</bdi></dd></div>
      {doc.category && <div><dt>{t.category}</dt><dd><bdi dir="auto">{doc.category}</bdi></dd></div>}
      {doc.property && <div><dt>{t.property}</dt><dd><bdi dir="auto">{doc.property}</bdi></dd></div>}
      {doc.paperLocation && <div><dt>{t.paperLocation}</dt><dd><bdi dir="auto">{doc.paperLocation}</bdi></dd></div>}
    </dl>}
  </section>;
}

function ReviewEntry({ t, locale, doc, headingRef, onBack }: { t: Words; locale: ProtoLocale; doc: ProtoDocument; headingRef: RefObject<HTMLHeadingElement | null>; onBack: () => void }) {
  const rtl = locale === 'he';
  const fields: [string, string, boolean?][] = [
    [t.type, typeLabel(t, doc.type)], [t.title, doc.suggestedTitle ?? doc.name], [t.from, doc.from],
    [t.documentDate, formatDateOnly(doc.date, locale), true], ...(doc.reference ? [[t.reference, doc.reference, true] as [string, string, boolean]] : []),
  ];
  return <section className={s.review} aria-labelledby="review-title" onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); onBack(); } }}>
    <header className={s.reviewHeader}>
      <button type="button" className={s.backButton} onClick={onBack}><ChevronIcon flip={rtl} />{t.back}</button>
      <div className={s.reviewTitleBlock}>
        <p className={s.eyebrow}>{t.reviewStep}</p>
        <h2 id="review-title" ref={headingRef} tabIndex={-1} className={s.previewTitle}>{t.reviewing}: <bdi dir="auto">{doc.name}</bdi></h2>
      </div>
    </header>
    <div className={s.reviewBody}>
      <figure className={s.pageFrame}><SamplePage doc={doc} label={fill(t.previewOf, { name: doc.name })} /><figcaption>{t.samplePage} · {fill(t.page, { n: 1, total: doc.pages })}</figcaption></figure>
      <div className={s.reviewPanel}>
        <dl className={s.reviewFields}>
          {fields.map(([label, value, ltr]) => <div key={label}><dt>{label}<span className={s.suggested}>{t.suggested}</span></dt><dd><bdi dir={ltr ? 'ltr' : 'auto'}>{value}</bdi></dd></div>)}
        </dl>
        <p className={s.nextPass}>{t.reviewFormNext}</p>
        <div className={s.reviewActions}>
          <Button isDisabled aria-describedby="review-sim">{t.confirmDetails}</Button>
          <span id="review-sim" className={s.muted}>{t.simulated}</span>
        </div>
      </div>
    </div>
  </section>;
}

function AddDialog({ t, initialArchive, onClose, onUpload }: { t: Words; initialArchive: string; onClose: () => void; onUpload: (dest: string, fileName: string) => void }) {
  const [dest, setDest] = useState(initialArchive);
  const [file, setFile] = useState<string | null>(null);
  const [error, setError] = useState('');
  const input = useRef<HTMLInputElement>(null);
  const destArchive = archives.find(a => a.id === dest)!;
  return <ModalDialog isOpen onOpenChange={open => { if (!open) onClose(); }} title={t.addTitle} className={s.addDialog}>
    <div className={s.destination} data-kind={destArchive.kind}>
      <ArchiveIcon kind={destArchive.kind} />
      <div><span className={s.eyebrow}>{t.addingTo}</span><strong><bdi dir="auto">{destArchive.name}</bdi></strong><small>{destArchive.kind === 'personal' ? t.personal : t.business}</small></div>
    </div>
    <Select label={t.changeArchive} selectedKey={dest} onSelectionChange={k => setDest(String(k))} options={archives.map(a => ({ id: a.id, label: `${a.name} · ${a.kind === 'personal' ? t.personal : t.business}` }))} />
    <div className={s.dropZone}>
      <UploadIcon width={22} height={22} />
      <div className={s.dropButtons}>
        <Button variant="secondary" className={s.secondary} onPress={() => input.current?.click()}>{t.chooseFile}</Button>
        <Button data-action="sample" variant="secondary" className={s.secondary} onPress={() => { setFile(sampleUpload.fileName); setError(''); }}>{t.useSample}</Button>
      </div>
      <p className={s.muted}>{t.dropHint}</p>
      <input ref={input} type="file" accept="application/pdf,.pdf" hidden onChange={e => { const f = e.target.files?.[0]; if (f) { setFile(f.name); setError(''); } }} />
      {file && <p className={s.fileChip}><FileGlyph type={null} /><span>{t.selectedFile}: <bdi dir="auto">{file}</bdi></span></p>}
    </div>
    {error && <p className="ui-error" role="alert">{error}</p>}
    <div className="ui-dialog-actions">
      <Button variant="secondary" className={s.secondary} onPress={onClose}>{t.cancel}</Button>
      <Button data-action="upload" onPress={() => { if (!file) { setError(t.noFile); return; } onUpload(dest, file); }}>{fill(t.uploadTo, { archive: destArchive.name })}</Button>
    </div>
  </ModalDialog>;
}
