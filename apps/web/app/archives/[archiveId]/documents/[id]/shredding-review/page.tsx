'use client';
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { useWords } from '../../../../../../lib/catalog';

type Obligation={ruleId:string;ruleVersion:number;source:string;trigger:string;inputs:Record<string,unknown>;keepThrough?:string};
type Evidence={current:{state:string;snapshot?:{id:string;ruleSetVersion:string;inputs:{facts?:Record<string,unknown>};outcome:{keepThrough?:string;obligations:Obligation[]}}};holds:{id:string;reason:string;releasedAt?:string}[];reviewBlocker:string};
const copy={
 en:{heading:'Review for shredding',notice:'This review leaves the document in your archive.',back:'Back to archive',evidence:'Retention evidence',keep:'Keep through',decision:'Decision',rule:'Rule source',trigger:'Trigger event',facts:'Recorded facts',obligations:'Obligations',original:'Download original'},
 fr:{heading:'Examen avant destruction',notice:'Cet examen laisse le document dans vos archives.',back:'Retour aux archives',evidence:'Justificatifs de conservation',keep:"Conserver jusqu’au",decision:'Décision',rule:'Source de la règle',trigger:'Événement déclencheur',facts:'Faits enregistrés',obligations:'Obligations',original:"Télécharger l’original"},
 he:{heading:'בדיקה לקראת גריסה',notice:'בדיקה זו משאירה את המסמך בארכיון שלך.',back:'חזרה לארכיון',evidence:'ראיות שמירה',keep:'לשמור עד',decision:'החלטה',rule:'מקור הכלל',trigger:'אירוע מפעיל',facts:'עובדות שנרשמו',obligations:'חובות',original:'הורדת המקור'}
} as const;
export default function ShreddingReviewPage({params}:{params:Promise<{archiveId:string;id:string}>}){
 const {locale}=useWords();const t=copy[locale as keyof typeof copy]??copy.en;const [ids,setIds]=useState<{archiveId:string;id:string}>();const [evidence,setEvidence]=useState<Evidence>();const [message,setMessage]=useState('');
 useEffect(()=>{void params.then(setIds)},[params]);
 useEffect(()=>{if(!ids)return;let live=true;const base=`/api/v1/archives/${ids.archiveId}/documents/${ids.id}`;
 async function open(){try{const response=await fetch(`${base}/shredding-review`,{cache:'no-store'});if(response.ok){const data=await response.json() as Evidence;if(live)setEvidence(data)}else{const details=await fetch(`${base}/retention`,{cache:'no-store'});const data=details.ok?await details.json() as Evidence:null;if(live)setMessage(data?.reviewBlocker??`Review unavailable (${response.status}).`)}}catch{if(live)setMessage('Review unavailable.')}}void open();return()=>{live=false}},[ids]);
 const snap=evidence?.current.snapshot;
 return <main dir={locale==='he'?'rtl':'ltr'} style={{maxWidth:'50rem',margin:'2rem auto',padding:'1.5rem',color:'#1b2333'}}>
  <header style={{borderBottom:'1px solid #d9dde6',paddingBottom:'1.25rem',marginBottom:'1.5rem'}}><p style={{fontSize:'.8rem',color:'#5d6779',margin:0}}>Invoyis</p><h1 style={{margin:'.45rem 0',fontSize:'1.8rem'}}>{t.heading}</h1><p style={{margin:'.35rem 0 1rem'}}>{t.notice}</p>{ids&&<Link href={`/archives/${ids.archiveId}?doc=${ids.id}`}>{t.back}</Link>}</header>
  {message&&<p role="alert" style={{padding:'1rem',background:'#fff4e2',borderRadius:'.5rem'}}>{message.replaceAll('_',' ')}</p>}
  {snap&&<section aria-label={t.evidence}><h2 style={{fontSize:'1.2rem'}}>{t.evidence}</h2>
   <dl style={{display:'grid',gridTemplateColumns:'minmax(8rem,12rem) 1fr',gap:'.8rem',padding:'1rem',background:'#f5f6f9',borderRadius:'.5rem'}}>
    <dt>{t.keep}</dt><dd style={{margin:0}}><bdi dir="ltr">{snap.outcome.keepThrough}</bdi></dd>
    <dt>{t.decision}</dt><dd style={{margin:0}}><bdi dir="ltr">{snap.id}</bdi> · <bdi dir="auto">{snap.ruleSetVersion}</bdi></dd>
   </dl>
   <h3 style={{fontSize:'1rem'}}>{t.facts}</h3><dl style={{display:'grid',gridTemplateColumns:'minmax(8rem,12rem) 1fr',gap:'.4rem'}}>{Object.entries(snap.inputs.facts??{}).map(([key,value])=><div key={key} style={{display:'contents'}}><dt>{key.replaceAll(/([A-Z])/g,' $1')}</dt><dd style={{margin:0}}><bdi dir="auto">{String(value)}</bdi></dd></div>)}</dl>
   <h3 style={{fontSize:'1rem'}}>{t.obligations}</h3>{snap.outcome.obligations.map((o,i)=><article key={`${o.ruleId}-${i}`} style={{border:'1px solid #e7e9ef',borderRadius:'.5rem',padding:'1rem',marginBottom:'.75rem'}}><p style={{margin:'0 0 .5rem'}}><strong><bdi dir="auto">{o.source}</bdi></strong> · <bdi dir="ltr">{o.ruleId} v{o.ruleVersion}</bdi></p><p style={{margin:'.25rem 0'}}>{t.trigger}: <bdi dir="ltr">{o.trigger}</bdi></p>{Object.entries(o.inputs).map(([key,value])=><p key={key} style={{margin:'.25rem 0'}}>{key}: <bdi dir="auto">{String(value)}</bdi></p>)}<p style={{margin:'.5rem 0 0'}}>{t.keep}: <bdi dir="ltr">{o.keepThrough}</bdi></p></article>)}
   {ids&&<a href={`/api/v1/archives/${ids.archiveId}/documents/${ids.id}/original`}>{t.original}</a>}
  </section>}
 </main>;
}
