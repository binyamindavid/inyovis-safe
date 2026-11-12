'use client';
import { createArchive, listArchives, problemMessage, type Archive, type ArchiveKind, type ProblemCode } from '@invoyis/contracts';
import { Button, Select, TextField } from '@invoyis/ui';
import Link from 'next/link';
import { useEffect, useState, type FormEvent } from 'react';
import { LanguageSelect } from '../../components/language-select';
import { SyntheticDocuments } from '../../components/synthetic-documents';
import { useWords } from '../../lib/catalog';
import { intent } from '../../lib/archive-ui';
import styles from './archive.module.css';
import { lastArchiveKey } from './[archiveId]/library/url-state';
export default function ArchivesPage() {
  const { locale, t } = useWords();
  const [items, setItems] = useState<Archive[]>([]); const [kind, setKind] = useState<ArchiveKind>('personal');
  const [name, setName] = useState(''); const [jurisdiction, setJurisdiction] = useState('');
  const [error, setError] = useState(''); const [accessChanged,setAccessChanged]=useState(false); const [busy, setBusy] = useState(false);
  const [invalid, setInvalid] = useState<'name' | 'jurisdiction' | null>(null);
  const [opening, setOpening] = useState(true);
  useEffect(() => { setAccessChanged(new URLSearchParams(window.location.search).get('access')==='changed'); }, []);
  // With at least one archive, /archives opens the library on the last archive used (or the first one).
  // ?new=1 (sidebar "New archive") and ?access=changed keep this page so its form and notice stay visible.
  useEffect(() => { listArchives({ credentials:'same-origin' }).then(({ data, error }) => { const params = new URLSearchParams(window.location.search);
    if (data && data.items.length && !params.has('new') && params.get('access') !== 'changed') {
      let last: string | null = null; try { last = sessionStorage.getItem(lastArchiveKey); } catch { /* storage unavailable */ }
      const target = data.items.find(item => item.id === last) ?? data.items[0];
      window.location.replace(`/archives/${target.id}`); return;
    }
    setOpening(false);
    if (data) setItems(data.items); else if (error?.status === 401) window.location.assign('/sign-in?next=/archives'); else if (error) setError(problemMessage((error as {code?:ProblemCode}).code ?? 'INTERNAL_ERROR', locale)); }).catch(() => setError(problemMessage('UPSTREAM_UNAVAILABLE', locale))); }, [locale]);
  async function create(event: FormEvent) {
    event.preventDefault(); setError(''); setInvalid(null);
    if (!name.trim()) { setInvalid('name'); return; }
    if (kind === 'business' && !/^[A-Za-z]{2}$/.test(jurisdiction)) { setInvalid('jurisdiction'); return; }
    setBusy(true);
    const { data, error } = await createArchive({ body:{ kind, name, ...(kind === 'business' ? { jurisdiction: jurisdiction.toUpperCase() } : {}) }, headers:intent, credentials:'same-origin' });
    setBusy(false); if (error || !data) { setError(problemMessage((error as {code?:ProblemCode})?.code ?? 'INTERNAL_ERROR',locale)); return; }
    window.location.assign(`/archives/${data.id}`);
  }
  if (opening && !error) return <main className={styles.page}><p className={styles.muted} role="status">{t.loading}</p></main>;
  return <main className={styles.page}><div className={styles.wrap}>
    <div className={styles.top}><h1>{t.archives}</h1><LanguageSelect signedIn /></div>
    <section className={styles.card}><h2>{t.create}</h2><form className={styles.form} noValidate onSubmit={create}>
      <TextField label={t.name} value={name} onChange={value => { setName(value); setInvalid(null); }} isRequired error={invalid === 'name' ? t.invalidName : undefined} inputProps={{ maxLength:120 }} />
      <Select label={t.archives} selectedKey={kind} onSelectionChange={key => setKind(String(key) as ArchiveKind)} options={[{id:'personal',label:t.personal},{id:'business',label:t.business}]} />
      {kind === 'business' && <TextField label={t.jurisdiction} value={jurisdiction} onChange={value => { setJurisdiction(value); setInvalid(null); }} isRequired error={invalid === 'jurisdiction' ? t.invalidJurisdiction : undefined} inputProps={{ dir:'ltr', pattern:'[A-Za-z]{2}', maxLength:2 }} />}
      <Button type="submit" isDisabled={busy}>{busy ? t.loading : t.create}</Button>
    </form></section>
    <section className={styles.card}><h2>{t.archives}</h2>{items.length ? <ul className={styles.list}>{items.map(item => <li key={item.id}><Link href={`/archives/${item.id}`}><bdi dir="auto">{item.name}</bdi> — {item.kind === 'personal' ? t.personal : t.business} ({t[item.role]})</Link></li>)}</ul> : <p className={styles.muted}>{t.empty}</p>}</section>
    <SyntheticDocuments />
    {accessChanged&&<p className={styles.error} role="alert">{t.accessLost}</p>}
    {error && <p className={styles.error} role="alert">{error}</p>}
  </div></main>;
}
