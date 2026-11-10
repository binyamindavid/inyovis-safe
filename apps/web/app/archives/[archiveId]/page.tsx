'use client';
import { getArchive, getAccountLanguage, problemMessage, updateArchive, type Archive, type ProblemCode } from '@invoyis/contracts';
import Link from 'next/link';
import { useEffect, useState, type FormEvent } from 'react';
import { intent, locale as browserLocale, words, type Locale } from '../../../lib/archive-ui';
import styles from '../archive.module.css';

export default function ArchivePage({ params }: { params: Promise<{ archiveId: string }> }) {
  const [id, setId] = useState(''); const [item, setItem] = useState<Archive>();
  const [language, setLanguage] = useState<Locale>('en'); const [error, setError] = useState('');
  const [name, setName] = useState(''); const [jurisdiction, setJurisdiction] = useState('');
  const t = words[language];
  useEffect(() => { params.then(({ archiveId }) => setId(archiveId)); setLanguage(browserLocale()); getAccountLanguage({ credentials: 'same-origin' }).then(({ data }) => { if (data) setLanguage(data.language); }); }, [params]);
  useEffect(() => { if (!id) return; getArchive({ path: { archiveId: id }, credentials: 'same-origin' }).then(({ data, error }) => { if (data) { setItem(data); setName(data.name); setJurisdiction(data.jurisdiction ?? ''); } else if (error) setError(problemMessage((error as { code?: ProblemCode }).code ?? 'INTERNAL_ERROR', language)); }); }, [id, language]);
  async function save(event: FormEvent) {
    event.preventDefault(); if (!item) return; setError('');
    const { data, error } = await updateArchive({ path: { archiveId: item.id }, body: { name, jurisdiction: jurisdiction.toUpperCase() }, headers: intent, credentials: 'same-origin' });
    if (data) setItem(data); else if (error) setError(t.error);
  }
  return <main className={styles.page} lang={language} dir={language === 'he' ? 'rtl' : 'ltr'}><div className={styles.wrap}>
    <Link href="/archives">{t.back}</Link><div className={styles.card}>{item && <><h1>{item.name}</h1><p>{item.kind === 'personal' ? t.personal : t.business} · {item.jurisdiction ?? '—'} · {t[item.role]}</p>{item.role === 'admin' && <><Link href={`/archives/${item.id}/members`}>{t.members}</Link><form className={styles.form} onSubmit={save}><label className={styles.field}>{t.name}<input value={name} onChange={e => setName(e.target.value)} maxLength={120} required/></label><label className={styles.field}>{t.jurisdiction}<input dir="ltr" value={jurisdiction} onChange={e => setJurisdiction(e.target.value)} required={item.kind === 'business'} pattern="[A-Za-z]{2}" maxLength={2}/></label><button className={styles.button}>{t.save}</button></form></>}</>}</div>
    {error && <p className={styles.error} role="alert">{error}</p>}
  </div></main>;
}
