'use client';
import {use,useEffect,useState} from 'react';
import Link from 'next/link';
import {DocumentStatuses} from '../../document-statuses';
import {useWords} from '../../../../../lib/catalog';
import styles from '../../../archive.module.css';
export default function SupplierPage({params}:{params:Promise<{archiveId:string;id:string}>}){
 const {archiveId,id}=use(params);const {t}=useWords();const [name,setName]=useState('');
 useEffect(()=>{let active=true;fetch(`/api/v1/archives/${archiveId}/organisation/suppliers/${id}`,{credentials:'same-origin'}).then(r=>r.ok?r.json():null).then(v=>{if(active&&v)setName(v.displayName)});return()=>{active=false}},[archiveId,id]);
 return <main className={styles.page}><div className={styles.wrap}><Link href={`/archives/${archiveId}`}>{t.back}</Link><h1><bdi dir="auto">{name}</bdi></h1><DocumentStatuses archiveId={archiveId} supplierId={id}/></div></main>;
}
