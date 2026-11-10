'use client';
import { createArchive, getAccountLanguage, listArchives, setAccountLanguage, type Archive, type ArchiveKind } from '@invoyis/contracts';
import Link from 'next/link';
import { useEffect, useState, type FormEvent } from 'react';
import { intent, locale as browserLocale, words, type Locale } from '../../lib/archive-ui';
import styles from './archive.module.css';

export default function ArchivesPage() {
  const [language, setLanguage] = useState<Locale>('en');
  const [items, setItems] = useState<Archive[]>([]);
  const [kind, setKind] = useState<ArchiveKind>('personal');
  const [name, setName] = useState('');
  const [jurisdiction, setJurisdiction] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const t = words[language];
  useEffect(() => {
    setLanguage(browserLocale());
    getAccountLanguage({ credentials: 'same-origin' }).then(({ data }) => { if (data) setLanguage(data.language); });
    listArchives({ credentials: 'same-origin' }).then(({ data, error }) => {
      if (data) setItems(data.items);
      else if (error) window.location.assign('/sign-in?next=/archives');
    });
  }, []);
  async function create(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('');
    const { data, error } = await createArchive({ body: { kind, name, ...(kind === 'business' ? { jurisdiction: jurisdiction.toUpperCase() } : {}) }, headers: intent, credentials: 'same-origin' });
    setBusy(false);
    if (error || !data) { setError(t.error); return; }
    window.location.assign(`/archives/${data.id}`);
  }
  async function changeLanguage(value: Locale) {
    setLanguage(value);
    const { error } = await setAccountLanguage({ body: { language: value }, headers: intent, credentials: 'same-origin' });
    if (error) setError(t.error);
  }
  return <main className={styles.page} lang={language} dir={language === 'he' ? 'rtl' : 'ltr'}><div className={styles.wrap}>
    <div className={styles.top}><h1>{t.archives}</h1><label>{t.language} <select value={language} onChange={e => changeLanguage(e.target.value as Locale)}><option value="en">English</option><option value="fr">Français</option><option value="he">עברית</option></select></label></div>
    <section className={styles.card}><h2>{t.create}</h2><form className={styles.form} onSubmit={create}>
      <label className={styles.field}>{t.name}<input value={name} onChange={e => setName(e.target.value)} required maxLength={120}/></label>
      <label className={styles.field}>{t.archives}<select value={kind} onChange={e => setKind(e.target.value as ArchiveKind)}><option value="personal">{t.personal}</option><option value="business">{t.business}</option></select></label>
      {kind === 'business' && <label className={styles.field}>{t.jurisdiction}<input dir="ltr" value={jurisdiction} onChange={e => setJurisdiction(e.target.value)} required pattern="[A-Za-z]{2}" maxLength={2}/></label>}
      <button className={styles.button} disabled={busy}>{busy ? t.loading : t.create}</button>
    </form></section>
    <section className={styles.card}><h2>{t.archives}</h2>{items.length ? <ul className={styles.list}>{items.map(item => <li key={item.id}><Link href={`/archives/${item.id}`}>{item.name} — {item.kind === 'personal' ? t.personal : t.business} ({t[item.role]})</Link></li>)}</ul> : <p className={styles.muted}>{t.empty}</p>}</section>
    {error && <p className={styles.error} role="alert">{error}</p>}
  </div></main>;
}
