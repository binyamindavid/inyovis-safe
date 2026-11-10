'use client';
import { getArchive, problemMessage, updateArchive, type Archive, type ProblemCode } from '@invoyis/contracts';
import { Button, TextField } from '@invoyis/ui';
import Link from 'next/link';
import { useEffect, useState, type FormEvent } from 'react';
import { LanguageSelect } from '../../../components/language-select';
import { useWords } from '../../../lib/catalog';
import { intent } from '../../../lib/archive-ui';
import styles from '../archive.module.css';
export default function ArchivePage({ params }: { params: Promise<{ archiveId:string }> }) {
  const { locale, t } = useWords();
  const [id,setId]=useState(''); const [item,setItem]=useState<Archive>(); const [error,setError]=useState('');
  const [name,setName]=useState(''); const [jurisdiction,setJurisdiction]=useState('');
  useEffect(() => { params.then(({archiveId}) => setId(archiveId)); },[params]);
  useEffect(() => { if (!id) return; getArchive({ path:{archiveId:id}, credentials:'same-origin' }).then(({data,error}) => { if (data) {setItem(data);setName(data.name);setJurisdiction(data.jurisdiction ?? '');} else if(error) setError(problemMessage((error as {code?:ProblemCode}).code ?? 'INTERNAL_ERROR',locale)); }); },[id,locale]);
  async function save(event:FormEvent) { event.preventDefault(); if(!item) return; setError(''); const {data,error}=await updateArchive({path:{archiveId:item.id},body:{name,jurisdiction:jurisdiction.toUpperCase()},headers:intent,credentials:'same-origin'}); if(data) setItem(data); else if(error) setError(t.error); }
  return <main className={styles.page}><div className={styles.wrap}>
    <div className={styles.top}><Link href="/archives">{t.back}</Link><LanguageSelect signedIn /></div>
    <section className={styles.card}>{item && <><h1><bdi dir="auto">{item.name}</bdi></h1><p>{item.kind==='personal'?t.personal:t.business} · <bdi dir="ltr">{item.jurisdiction ?? '—'}</bdi> · {t[item.role]}</p>
      {item.role==='admin' && <><Link href={`/archives/${item.id}/members`}>{t.members}</Link><form className={styles.form} onSubmit={save}>
        <TextField label={t.name} value={name} onChange={setName} isRequired inputProps={{maxLength:120}} />
        <TextField label={t.jurisdiction} value={jurisdiction} onChange={setJurisdiction} isRequired={item.kind==='business'} inputProps={{dir:'ltr',pattern:'[A-Za-z]{2}',maxLength:2}} />
        <Button type="submit">{t.save}</Button></form></>}
    </>}</section>{error && <p className={styles.error} role="alert">{error}</p>}
  </div></main>;
}
