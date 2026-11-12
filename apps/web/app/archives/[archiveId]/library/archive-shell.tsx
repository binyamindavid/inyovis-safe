'use client';
import { getArchive, listArchives, problemMessage, updateArchive, type Archive, type ProblemCode } from '@invoyis/contracts';
import { Button, ModalDialog, TextField } from '@invoyis/ui';
import { createContext, useContext, useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { LanguageSelect } from '../../../../components/language-select';
import { useWords } from '../../../../lib/catalog';
import { intent } from '../../../../lib/archive-ui';
import { ExportAction } from '../export-action';
import { BriefcaseIcon, CloseIcon, DocsIcon, GearIcon, MenuIcon, PersonIcon, PlusIcon, ReviewIcon } from './icons';
import { useDocumentCounts, type DocumentCounts } from './use-documents';
import type { View } from './url-state';
import { NewArchiveDialog } from './new-archive-dialog';
import { ArchiveCountryField } from './archive-country-field';
import s from './library.module.css';

type Words = ReturnType<typeof useWords>['t'];
export type Shell = { archiveId: string; archive?: Archive; archives: Archive[]; loadError: string; openMenu: () => void; setArchive: (a: Archive) => void; counts: DocumentCounts | null; refreshCounts: () => Promise<void> };
const ShellContext = createContext<Shell | null>(null);
export function useShell() { const shell = useContext(ShellContext); if (!shell) throw new Error('useShell outside ArchiveShell'); return shell; }

export function useNarrow() {
  const [narrow, setNarrow] = useState(false);
  useEffect(() => { const m = window.matchMedia('(max-width: 63.99rem)'); const on = () => setNarrow(m.matches); on(); m.addEventListener('change', on); return () => m.removeEventListener('change', on); }, []);
  return narrow;
}

/**
 * The frame every archive screen shares: archive sidebar (a sheet on narrow screens), settings dialog and the archive itself.
 * The library passes its own counts and view handler; other screens (review, members, supplier history) let the shell count.
 */
export function ArchiveShell({ archiveId, view, fast = false, onView, children, detailOpen, section }: {
  section?: 'members'; archiveId: string; view?: View | null; fast?: boolean; onView?: (v: View) => void; children: ReactNode | ((shell: Shell) => ReactNode); detailOpen?: boolean;
}) {
  const { locale, t } = useWords();
  const [archive, setArchive] = useState<Archive>();
  const [archives, setArchives] = useState<Archive[]>([]);
  const [loadError, setLoadError] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [newArchiveOpen, setNewArchiveOpen] = useState(false);
  // /archives?new=1 lands here with ?newArchive=1, which opens the New archive dialog over the library.
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    if (params.get('newArchive') !== '1') return;
    setNewArchiveOpen(true); params.delete('newArchive');
    const rest = params.toString(); history.replaceState(history.state, '', `${location.pathname}${rest ? `?${rest}` : ''}`);
  }, []);
  // Archive-wide counts from the API, so the sidebar is right however many documents are loaded in the list.
  const { counts, refresh: refreshCounts } = useDocumentCounts(archiveId, fast);
  const shown = { total: counts?.total ?? null, review: counts?.needsReview ?? 0 };

  useEffect(() => {
    let live = true;
    getArchive({ path: { archiveId }, credentials: 'same-origin' }).then(({ data, error, response }) => {
      if (!live) return;
      if (data) setArchive(data);
      else if (response?.status === 401) window.location.assign(`/sign-in?next=${encodeURIComponent(location.pathname)}`);
      else if (response?.status === 404) window.location.replace('/archives?access=changed');
      else setLoadError(problemMessage((error as { code?: ProblemCode })?.code ?? 'INTERNAL_ERROR', locale));
    }).catch(() => { if (live) setLoadError(problemMessage('UPSTREAM_UNAVAILABLE', locale)); });
    listArchives({ credentials: 'same-origin' }).then(({ data }) => { if (live && data) setArchives(data.items); }).catch(() => undefined);
    return () => { live = false; };
  }, [archiveId, locale]);

  const sidebar = <Sidebar t={t} archives={archives} current={archive} archiveId={archiveId} view={view ?? null} counts={shown}
    section={section} onView={onView ? v => { setMenuOpen(false); onView(v); } : undefined} onSettings={() => { setMenuOpen(false); setSettingsOpen(true); }} onNewArchive={() => { setMenuOpen(false); setNewArchiveOpen(true); }} />;

  const shell: Shell = { archiveId, archive, archives, loadError, openMenu: () => setMenuOpen(true), setArchive, counts, refreshCounts };
  return <ShellContext.Provider value={shell}>
    <div className={s.root} data-detail={detailOpen ? 'open' : 'closed'}>
      <aside className={s.sidebar} aria-label={t.libMenu}>{sidebar}</aside>
      {typeof children === 'function' ? children(shell) : children}
      <ModalDialog isOpen={menuOpen} onOpenChange={setMenuOpen} title={<span className={s.srOnly}>{t.libMenu}</span>} className={s.sheet}>
        <button type="button" className={`${s.iconButton} ${s.sheetClose}`} aria-label={t.libCloseMenu} onClick={() => setMenuOpen(false)}><CloseIcon /></button>
        {sidebar}
      </ModalDialog>
      <NewArchiveDialog isOpen={newArchiveOpen} onOpenChange={setNewArchiveOpen} />
      {archive && <SettingsDialog t={t} locale={locale} archive={archive} isOpen={settingsOpen} onOpenChange={setSettingsOpen} onSaved={setArchive} />}
    </div>
  </ShellContext.Provider>;
}

/** Hamburger shown in each screen's toolbar below 64rem, where the sidebar becomes a sheet. */
export function MenuButton() {
  const { t } = useWords(); const { openMenu } = useShell();
  return <button type="button" className={`${s.iconButton} ${s.menuButton}`} aria-label={t.libOpenMenu} onClick={openMenu}><MenuIcon /></button>;
}

export function ArchiveIcon({ kind }: { kind: 'personal' | 'business' }) {
  return <span className={s.archiveIcon} data-kind={kind}>{kind === 'personal' ? <PersonIcon /> : <BriefcaseIcon />}</span>;
}

function Sidebar({ t, archives, current, archiveId, view, counts, onView, onSettings, onNewArchive, section }: {
  onNewArchive: () => void; section?: 'members'; t: Words; archives: Archive[]; current?: Archive; archiveId: string; view: View | null; counts: { total: number | null; review: number }; onView?: (v: View) => void; onSettings: () => void;
}) {
  const list = archives.length ? archives : current ? [current] : [];
  const viewItem = (v: View, icon: ReactNode, label: string, badge: ReactNode) => {
    const props = { className: s.viewItem, 'aria-current': view === v ? 'page' as const : undefined };
    const body = <>{icon}{label}{badge}</>;
    // Inside the library the views switch in place; elsewhere they link back to the library.
    return onView ? <button type="button" {...props} onClick={() => onView(v)}>{body}</button>
      : <a {...props} href={`/archives/${archiveId}${v === 'review' ? '?view=review' : '?view=all'}`}>{body}</a>;
  };
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
            <li>{viewItem('all', <DocsIcon />, t.libAllDocuments, <span className={s.count}>{counts.total ?? ''}</span>)}</li>
            <li>{viewItem('review', <ReviewIcon />, t.libNeedsReview, counts.review > 0 ? <span className={s.reviewCount}>{counts.review}</span> : <span className={s.count}>0</span>)}</li>
          </ul>}
        </li>;
      })}
    </ul>
    <div className={s.sideFooter}>
      <button type="button" className={s.quietItem} aria-haspopup="dialog" onClick={onNewArchive}><PlusIcon />{t.libNewArchive}</button>
      {current?.role === 'admin' && <button type="button" className={s.quietItem} onClick={onSettings}><GearIcon />{t.libSettings}</button>}
      {current?.role === 'admin' && <a href={`/archives/${current.id}/members`} className={s.quietItem} aria-current={section === 'members' ? 'page' : undefined}><PersonIcon />{t.members}</a>}
      {current?.canBulkExport && <div className={s.exportSlot}><ExportAction archiveId={current.id} /></div>}
      <div className={s.langSelect}><LanguageSelect signedIn /></div>
    </div>
  </div>;
}

function SettingsDialog({ t, locale, archive, isOpen, onOpenChange, onSaved }: { t: Words; locale: ReturnType<typeof useWords>['locale']; archive: Archive; isOpen: boolean; onOpenChange: (open: boolean) => void; onSaved: (a: Archive) => void }) {
  const [name, setName] = useState(archive.name);
  const [jurisdiction, setJurisdiction] = useState(archive.jurisdiction ?? '');
  const [invalid, setInvalid] = useState<'name' | 'jurisdiction' | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (isOpen) { setName(archive.name); setJurisdiction(archive.jurisdiction ?? ''); setInvalid(null); setError(''); } }, [isOpen, archive]);
  async function save(event: FormEvent) {
    event.preventDefault(); setError(''); setInvalid(null);
    if (!name.trim()) { setInvalid('name'); return; }
    if (archive.kind === 'business' && !jurisdiction) { setInvalid('jurisdiction'); return; }
    setBusy(true);
    const { data, error } = await updateArchive({ path: { archiveId: archive.id }, body: { name, jurisdiction: jurisdiction }, headers: intent, credentials: 'same-origin' });
    setBusy(false);
    if (data) { onSaved(data); onOpenChange(false); } else setError(problemMessage((error as { code?: ProblemCode })?.code ?? 'INTERNAL_ERROR', locale));
  }
  return <ModalDialog isOpen={isOpen} onOpenChange={onOpenChange} title={t.libSettings} className={s.settingsDialog}>
    <p className={s.muted}>{archive.kind === 'personal' ? t.personal : t.business} · {t.libRoleLabel}: {t[archive.role]}</p>
    <form className={s.settingsForm} noValidate onSubmit={save}>
      <TextField label={t.name} value={name} onChange={v => { setName(v); setInvalid(null); }} isRequired error={invalid === 'name' ? t.invalidName : undefined} inputProps={{ maxLength: 120 }} />
      <ArchiveCountryField value={jurisdiction} onChange={v => { setJurisdiction(v); setInvalid(null); }} required={archive.kind === 'business'} invalid={invalid === 'jurisdiction'} legacyCode={archive.jurisdiction} />
      {error && <p className="ui-error" role="alert">{error}</p>}
      <div className="ui-dialog-actions">
        <Button variant="secondary" onPress={() => onOpenChange(false)}>{t.cancel}</Button>
        <Button type="submit" isDisabled={busy}>{busy ? t.loading : t.save}</Button>
      </div>
    </form>
  </ModalDialog>;
}
