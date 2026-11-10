'use client';
import { completeUploadSession, createUploadSession, problemMessage, type ProblemCode, type UploadReceipt, type UploadSession } from '@invoyis/contracts';
import { Button } from '@invoyis/ui';
import { useRef, useState, type FormEvent } from 'react';
import { useWords } from '../../../lib/catalog';
import { intent } from '../../../lib/archive-ui';
import styles from '../archive.module.css';

type Attempt = { file: File; key: string; digest: string; session?: UploadSession; uploaded: boolean };
function hex(bytes: ArrayBuffer) { return Array.from(new Uint8Array(bytes), b=>b.toString(16).padStart(2,'0')).join(''); }
function send(file:File, session:UploadSession, progress:(value:number)=>void):Promise<void> {
  return new Promise((resolve,reject)=>{
    const xhr=new XMLHttpRequest(); xhr.open(session.grant.method,session.grant.url);
    for(const [name,value] of Object.entries(session.grant.headers)) xhr.setRequestHeader(name,value);
    xhr.upload.onprogress=event=>{ if(event.lengthComputable) progress(Math.round(event.loaded/event.total*100)); };
    xhr.onerror=()=>reject(new Error('network'));
    xhr.onload=()=>xhr.status>=200&&xhr.status<300?resolve():reject(new Error(String(xhr.status)));
    if(session.grant.method==='POST') { const body=new FormData();for(const [name,value] of Object.entries(session.grant.fields))body.append(name,value);body.append('file',file,file.name);xhr.send(body); }
    else xhr.send(file);
  });
}
export function UploadForm({archiveId,archiveName}:{archiveId:string;archiveName:string}) {
  const {locale,t}=useWords();const [file,setFile]=useState<File>();const [busy,setBusy]=useState(false);const [status,setStatus]=useState('');const [error,setError]=useState('');const [receipt,setReceipt]=useState<UploadReceipt>();const attempt=useRef<Attempt | undefined>(undefined);
  async function submit(event:FormEvent){event.preventDefault();if(!file||busy)return;setBusy(true);setError('');setReceipt(undefined);
    try {
      if(file.size<1||file.size>25*1024*1024||file.type&&file.type!=='application/pdf')throw new Error(t.uploadInvalid);
      let current=attempt.current;
      if(!current||current.file!==file){setStatus(t.uploadHashing);const digest=hex(await crypto.subtle.digest('SHA-256',await file.arrayBuffer()));current={file,key:crypto.randomUUID(),digest,uploaded:false};attempt.current=current;}
      if(!current.session){setStatus(t.uploadPreparing);const result=await createUploadSession({path:{archiveId},body:{idempotencyKey:current.key,filename:file.name,mediaType:'application/pdf',byteCount:file.size,sha256:current.digest},headers:intent,credentials:'same-origin'});if(!result.data)throw new Error(problemMessage((result.error as {code?:ProblemCode})?.code??'INTERNAL_ERROR',locale));current.session=result.data;}
      if(!current.uploaded){
        // A lost upload response may still have staged the file. Completion is
        // authoritative; the user can retry this same session and grant.
        setStatus(t.uploadSending.replace('{percent}','0'));
        await send(file,current.session,value=>setStatus(t.uploadSending.replace('{percent}',String(value))));current.uploaded=true;
      }
      setStatus(t.uploadVerifying);
      const result=await completeUploadSession({path:{archiveId,id:current.session.id},headers:intent,credentials:'same-origin'});
      if(!result.data)throw new Error(problemMessage((result.error as {code?:ProblemCode})?.code??'INTERNAL_ERROR',locale));
      setReceipt(result.data);setStatus('');
    }catch(cause){setError(cause instanceof Error?cause.message:t.error);setStatus(t.uploadRetry);}
    finally{setBusy(false)}
  }
  return <section className={styles.card}><h2>{t.uploadTitle}</h2><p>{t.uploadDestination} <bdi dir="auto">{archiveName}</bdi></p>
    <form onSubmit={submit}><label htmlFor="archive-upload">{t.uploadChoose}</label>{' '}<input id="archive-upload" type="file" accept="application/pdf,.pdf" onChange={event=>{const next=event.target.files?.[0];setFile(next);attempt.current=undefined;setReceipt(undefined);setError('');setStatus('');}} />
      {file&&<p><bdi dir="auto">{file.name}</bdi></p>}<Button type="submit" isDisabled={busy||!file}>{busy?t.loading:t.uploadStart}</Button></form>
    <p role="status" aria-live="polite">{status}</p>{error&&<p role="alert" className={styles.error}>{error}</p>}
    {receipt&&<div role="status" aria-live="polite"><strong>{t.uploadReceived}</strong><p>{receipt.byteCount.toLocaleString(locale)} {t.uploadBytes}</p><p>SHA-256: <bdi dir="ltr">{receipt.sha256}</bdi></p></div>}
  </section>;
}
