'use client';
import {listDocumentStatuses,type DocumentStatus} from '@invoyis/contracts';
import {useEffect,useState} from 'react';
import {useWords} from '../../../lib/catalog';
import styles from '../archive.module.css';

export function DocumentStatuses({archiveId}:{archiveId:string}){
 const {t}=useWords();const [items,setItems]=useState<DocumentStatus[]>([]);
 useEffect(()=>{
  let active=true;
  async function refresh(){const response=await listDocumentStatuses({path:{archiveId},credentials:'same-origin'});if(active&&response.data)setItems(response.data.items);}
  void refresh();const timer=setInterval(()=>void refresh(),5000);
  return ()=>{active=false;clearInterval(timer)};
 },[archiveId]);
 const labels={pending:t.statusPending,processing:t.statusProcessing,needs_review:t.statusNeedsReview,file_blocked:t.statusFileBlocked,processing_failed:t.statusProcessingFailed};
 return <section className={styles.card} aria-label={t.documentStatuses}><h2>{t.documentStatuses}</h2><ul>{items.map(item=><li key={item.id}><bdi dir="auto">{item.originalFilename}</bdi>{' · '}<span role="status">{labels[item.status]}</span></li>)}</ul></section>;
}
