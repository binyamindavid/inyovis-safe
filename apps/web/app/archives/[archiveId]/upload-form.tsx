'use client';
import { completeUploadSession, createUploadSession, getDocumentStatus, problemMessage, type ProblemCode, type UploadReceipt, type UploadSession } from '@invoyis/contracts';
import { Button } from '@invoyis/ui';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useWords } from '../../../lib/catalog';
import { intent } from '../../../lib/archive-ui';
import { ACTIVE_STATES, MAX_FILES, MAX_TOTAL_BYTES, TERMINAL_PROCESSING, classifyFailure, queueReducer, validateSelection, type QueueState } from '../../../lib/upload-queue.mjs';
import styles from '../archive.module.css';

type Row = { id:string; file:File; key:string; state:QueueState; digest?:string; session?:UploadSession; uploaded?:boolean; uncertain?:boolean; resume?:'hashing'|'session_created'|'uploading'|'completing'; receipt?:UploadReceipt; processing?:string; progress?:number; error?:string };
type ApiError = { status:number; code:string; retryAfter?:string };
function hex(bytes:ArrayBuffer) { return Array.from(new Uint8Array(bytes), b=>b.toString(16).padStart(2,'0')).join(''); }
function failure(result:{response?:Response;error?:unknown}):ApiError { const error=result.error as {code?:string}|undefined; return {status:result.response?.status??0,code:error?.code??'INTERNAL_ERROR',retryAfter:result.response?.headers.get('retry-after')??undefined}; }
function send(file:File, session:UploadSession, progress:(value:number)=>void):Promise<void> {
  return new Promise((resolve,reject)=>{ const xhr=new XMLHttpRequest(); xhr.open(session.grant.method,session.grant.url);
    for(const [name,value] of Object.entries(session.grant.headers)) xhr.setRequestHeader(name,value);
    xhr.upload.onprogress=event=>{if(event.lengthComputable)progress(Math.round(event.loaded/event.total*100));};
    xhr.onerror=()=>reject({status:0,code:'INTERNAL_ERROR'}); xhr.ontimeout=()=>reject({status:0,code:'INTERNAL_ERROR'});
    xhr.onload=()=>xhr.status>=200&&xhr.status<300?resolve():reject({status:xhr.status,code:''});
    xhr.timeout=120000;
    if(session.grant.method==='POST'){const body=new FormData();for(const [name,value] of Object.entries(session.grant.fields))body.append(name,value);body.append('file',file,file.name);xhr.send(body);}else xhr.send(file);
  });
}
export function UploadForm({archiveId,archiveName,maxBytes}:{archiveId:string;archiveName:string;maxBytes:number}) {
  const {locale,t}=useWords(); const [rows,setRows]=useState<Row[]>([]); const rowsRef=useRef<Row[]>([]); const working=useRef(false); const [paused,setPaused]=useState(false); const pausedRef=useRef(false); const [announcement,setAnnouncement]=useState(''); const [backoffUntil,setBackoffUntil]=useState(0); const backoffRef=useRef(0); const rateCount=useRef(0);
  const update=useCallback((action:Parameters<typeof queueReducer<Row>>[1])=>{rowsRef.current=queueReducer(rowsRef.current,action);setRows(rowsRef.current);},[]);
  function change(id:string,state:QueueState,patch:Partial<Row>={}) {update({type:'transition',id,state,patch}); if(['received','rejected','failed_recoverable','failed_terminal'].includes(state)) {const row=rowsRef.current.find(r=>r.id===id); if(row)setAnnouncement(`${row.file.name}: ${state==='received'?t.uploadReceived:patch.error??state}`);} }
  function select(files:File[]) {if(!files.length)return; const reasons=validateSelection(files,rowsRef.current,maxBytes); const added=files.map((file,index):Row=>({id:crypto.randomUUID(),key:crypto.randomUUID(),file,state:reasons[index]?'rejected':'queued',error:reasons[index]==='count'?t.uploadCountCap.replace('{count}',String(MAX_FILES)):reasons[index]==='total'?t.uploadTotalCap.replace('{size}',String(Math.round(MAX_TOTAL_BYTES/1048576))):reasons[index]?t.uploadInvalid.replace('{max}',String(Math.round(maxBytes/1048576*10)/10)):undefined}));update({type:'add',rows:added});if(added.some(r=>r.state==='rejected'))setAnnouncement(t.uploadRejectedAnnouncement);}
  async function sessionFor(row:Row,renew=false) {if(row.session&&!renew)return row.session;const result=await createUploadSession({path:{archiveId},body:{idempotencyKey:row.key,filename:row.file.name,mediaType:'application/pdf',byteCount:row.file.size,sha256:row.digest!},headers:intent,credentials:'same-origin'});if(!result.data)throw {...failure(result),step:'session'}; update({type:'patch',id:row.id,patch:{session:result.data}});return result.data;}
  async function complete(row:Row) {change(row.id,'completing');const session=rowsRef.current.find(r=>r.id===row.id)?.session;if(!session)throw {status:0,code:'INTERNAL_ERROR',step:'complete'};const result=await completeUploadSession({path:{archiveId,id:session.id},headers:intent,credentials:'same-origin'});if(result.data){change(row.id,'received',{receipt:result.data,uncertain:false,error:undefined});return true;}const error=failure(result);if(error.code==='UPLOAD_STAGED_OBJECT_MISSING')return false;throw {...error,step:'complete'};}
  async function sendAndRecover(row:Row, allowGrantRenewal=true, allowResend=true):Promise<boolean> {
    const current=rowsRef.current.find(r=>r.id===row.id)!;
    change(row.id,'uploading',{progress:0});
    try {
      await send(row.file,current.session!,progress=>update({type:'progress',id:row.id,progress}));
      update({type:'patch',id:row.id,patch:{uploaded:true,uncertain:false}});
      return false;
    } catch(cause) {
      const error=cause as ApiError;
      if(error.status===429)throw {...error,step:'transfer'};
      if(error.status===401||error.status===403) {
        if(!allowGrantRenewal)throw {...error,step:'transfer'};
        change(row.id,'session_created');
        await sessionFor(rowsRef.current.find(r=>r.id===row.id)!,true);
        return sendAndRecover(row,false,allowResend);
      }
      // A dropped response may follow a committed storage write. Preserve the
      // session and ask completion before any further transfer, including after renewal.
      update({type:'patch',id:row.id,patch:{uncertain:true}});
      if(await complete(row))return true;
      if(!allowResend)throw {status:0,code:'INTERNAL_ERROR',step:'transfer'};
      let retryRow=rowsRef.current.find(r=>r.id===row.id)!;
      if(Date.parse(retryRow.session!.grant.expiresAt)<=Date.now()+5000) {
        change(row.id,'session_created');
        await sessionFor(retryRow,true);
        retryRow=rowsRef.current.find(r=>r.id===row.id)!;
      }
      return sendAndRecover(retryRow,true,false);
    }
  }
  async function run(row:Row) {
    let step=row.resume??'hashing';
    try {
      if(!row.digest){change(row.id,'validating');change(row.id,'hashing');const digest=hex(await crypto.subtle.digest('SHA-256',await row.file.arrayBuffer()));update({type:'patch',id:row.id,patch:{digest}});step='session_created';}
      if(!rowsRef.current.find(r=>r.id===row.id)?.session){change(row.id,'session_created');await sessionFor(rowsRef.current.find(r=>r.id===row.id)!);step='uploading';}
      let current=rowsRef.current.find(r=>r.id===row.id)!;
      if(current.uncertain||current.uploaded||step==='completing') {const done=await complete(current);if(done)return;step='uploading';}
      current=rowsRef.current.find(r=>r.id===row.id)!;
      if(step==='uploading'||!current.uploaded){
        if(Date.parse(current.session!.grant.expiresAt)<=Date.now()+5000){
          if(current.state!=='session_created')change(row.id,'session_created');
          await sessionFor(current,true);
        }
        if(await sendAndRecover(row))return;
      }
      if(!await complete(row))throw {status:409,code:'UPLOAD_STAGED_OBJECT_MISSING',step:'complete'};
    } catch(cause) {
      const e=cause as ApiError & {step?:string};const stepName=e.step??(rowsRef.current.find(r=>r.id===row.id)?.state==='uploading'?'transfer':'complete');const kind=classifyFailure(e.status??0,e.code??'',stepName);
      if(kind==='pause'){pausedRef.current=true;setPaused(true);setAnnouncement(t.uploadPaused);change(row.id,'failed_recoverable',{resume:step,error:t.uploadPaused});return;}
      if(kind==='rate'){const seconds=Number(e.retryAfter);const dateDelay=e.retryAfter?Date.parse(e.retryAfter)-Date.now():NaN;rateCount.current++;const delay=Number.isFinite(seconds)&&seconds>0?seconds*1000:Number.isFinite(dateDelay)&&dateDelay>0?dateDelay:Math.min(15000,1000*2**rateCount.current);backoffRef.current=Date.now()+delay;setBackoffUntil(backoffRef.current);setTimeout(()=>setBackoffUntil(0),delay);change(row.id,'failed_recoverable',{resume:step,error:problemMessage('RATE_LIMITED',locale)});return;}
      const message=kind==='grant'?t.uploadRetry:problemMessage((e.code||'INTERNAL_ERROR') as ProblemCode,locale);
      change(row.id,kind==='rejected'?'rejected':kind==='terminal'?'failed_terminal':'failed_recoverable',{resume:step,error:message});
    }
  }
  useEffect(()=>{if(paused||working.current)return;const queued=rows.find(r=>r.state==='queued');if(!queued)return;const delay=Math.max(0,backoffUntil-Date.now());const timer=setTimeout(()=>{if(working.current||pausedRef.current)return;working.current=true;void run(queued).finally(()=>{working.current=false;setRows([...rowsRef.current]);});},delay);return()=>clearTimeout(timer);},[rows,paused,backoffUntil]);
  useEffect(()=>{if(!rows.some(r=>ACTIVE_STATES.has(r.state)||r.state==='queued'))return;const warning=(event:BeforeUnloadEvent)=>{event.preventDefault();event.returnValue='';};window.addEventListener('beforeunload',warning);return()=>window.removeEventListener('beforeunload',warning);},[rows]);
  useEffect(()=>{let alive=true;let timer:ReturnType<typeof setTimeout>;let delay=3000;async function poll(){const pending=rowsRef.current.filter(r=>r.receipt&&!TERMINAL_PROCESSING.has(r.processing??''));if(!pending.length)return;let changed=false;for(const row of pending){const result=await getDocumentStatus({path:{archiveId,id:row.receipt!.documentId},credentials:'same-origin'});if(!alive)return;if(result.data){changed ||= result.data.status!==row.processing;update({type:'processing',id:row.id,status:result.data.status});}}delay=changed?3000:Math.min(15000,delay*1.5);if(alive)timer=setTimeout(poll,delay);}timer=setTimeout(poll,3000);return()=>{alive=false;clearTimeout(timer);};},[archiveId,rows.filter(r=>r.receipt&&!TERMINAL_PROCESSING.has(r.processing??'')).length,update]);
  function retry(row:Row){if(row.state!=='failed_recoverable'||working.current||pausedRef.current||Date.now()<backoffRef.current)return;document.getElementById(`upload-status-${row.id}`)?.focus();setAnnouncement(`${t.uploadRetryAction} ${row.file.name}`);working.current=true;void run(row).finally(()=>{working.current=false;setRows([...rowsRef.current]);});}
  const receivedCount=rows.filter(r=>r.receipt).length; const plural=new Intl.PluralRules(locale).select(receivedCount); const countCopy=plural==='one'?t.uploadCountOne:plural==='two'?t.uploadCountTwo:t.uploadCount;
  const batchComplete = rows.length > 0 && rows.every(row =>
    row.state === 'received' && (row.processing === 'needs_review' || row.processing === 'ready')
  );
  const batchSummary = countCopy.replace('{done}', new Intl.NumberFormat(locale).format(receivedCount))
    .replace('{total}', new Intl.NumberFormat(locale).format(rows.length));
  const statusLabels:Record<QueueState,string>={queued:t.uploadQueued,validating:t.uploadValidating,hashing:t.uploadHashing,session_created:t.uploadPreparing,uploading:t.uploadSending.replace('{percent}',''),completing:t.uploadVerifying,received:t.uploadReceived,rejected:t.uploadRejected,failed_recoverable:t.uploadFailed,failed_terminal:t.uploadFailedTerminal};
  const processingLabels:Record<string,string>={pending:t.statusPending,processing:t.statusProcessing,needs_review:t.statusNeedsReview,ready:t.statusReady,file_blocked:t.statusFileBlocked,processing_failed:t.statusProcessingFailed};
  return <section className={styles.card}><h2>{t.uploadTitle}</h2><p>{t.uploadDestination} <bdi dir="auto">{archiveName}</bdi></p><label htmlFor="archive-upload">{t.uploadChoose}</label>{' '}<input id="archive-upload" type="file" multiple accept="application/pdf,.pdf" onChange={event=>{select(Array.from(event.target.files??[]));event.target.value='';}} />
    {paused&&<p role="alert" className={styles.error}>{t.uploadPaused}</p>}<p aria-live="polite" className={styles.srOnly}>{announcement}</p>
    {rows.length>0&&<>
      {!batchComplete && <p>{batchSummary}</p>}
      <details open={!batchComplete}>
        <summary hidden={!batchComplete}>{batchSummary}</summary>
        <ol className={styles.uploadQueue}>{rows.map(row=><li key={row.id}><bdi dir="auto" className={styles.uploadFilename} title={row.file.name}>{row.file.name}</bdi>{' · '}<span id={`upload-status-${row.id}`} tabIndex={-1}>{statusLabels[row.state]}{row.state==='uploading'?` ${row.progress??0}%`:''}</span>{row.error&&<p className={styles.error}>{row.error}</p>}{row.receipt&&<p>{t.uploadReceipt} <bdi dir="ltr">{row.receipt.id}</bdi> · {processingLabels[row.processing??'pending']}</p>}{row.state==='failed_recoverable'&&!paused&&<Button variant="secondary" aria-label={`${t.uploadRetryAction} ${row.file.name}`} onPress={()=>retry(row)} isDisabled={working.current||Date.now()<backoffUntil}>{t.uploadRetryAction}</Button>}</li>)}</ol>
      </details>
      <Button variant="quiet" onPress={()=>{update({type:'dismiss'});setAnnouncement('');}} isDisabled={rows.some(r=>ACTIVE_STATES.has(r.state))}>{t.uploadDismiss}</Button></>}
  </section>;
}
