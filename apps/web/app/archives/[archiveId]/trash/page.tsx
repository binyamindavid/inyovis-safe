'use client';
import { useEffect,useState } from 'react';
import { ArchiveShell } from '../library/archive-shell';
import { useWords } from '../../../../lib/catalog';

type Item={id:string;name:string;trashedAt:string;recoveryUntil:string};
const copy={en:{title:'Trash',intro:'Documents can be restored during the 30-day recovery period. Trash does not erase stored copies.',restore:'Restore',deadline:'Restore by',error:'Could not load Trash.',empty:'Trash is empty.'},fr:{title:'Corbeille',intro:'Les documents peuvent être restaurés pendant 30 jours. La corbeille n’efface pas les copies stockées.',restore:'Restaurer',deadline:'Restaurer avant le',error:'Impossible de charger la corbeille.',empty:'La corbeille est vide.'},he:{title:'אשפה',intro:'אפשר לשחזר מסמכים במשך 30 יום. העברה לאשפה אינה מוחקת עותקים שמורים.',restore:'שחזור',deadline:'ניתן לשחזר עד',error:'לא ניתן לטעון את האשפה.',empty:'האשפה ריקה.'}} as const;
export default function TrashPage({params}:{params:Promise<{archiveId:string}>}){
 const {locale}=useWords(),t=copy[locale as keyof typeof copy]??copy.en;
 const [archiveId,setArchiveId]=useState(''),[items,setItems]=useState<Item[]>([]),[error,setError]=useState(''),[busy,setBusy]=useState('');
 useEffect(()=>{void params.then(v=>setArchiveId(v.archiveId))},[params]);
 useEffect(()=>{if(!archiveId)return;fetch(`/api/v1/archives/${archiveId}/trash`,{cache:'no-store'}).then(async r=>{if(!r.ok)throw Error();return r.json() as Promise<{items:Item[]}>}).then(v=>setItems(v.items)).catch(()=>setError(t.error))},[archiveId,t.error]);
 async function restore(id:string){setBusy(id);try{const r=await fetch(`/api/v1/archives/${archiveId}/documents/${id}/restore`,{method:'POST',headers:{'X-Invoyis-Intent':'browser'}});if(!r.ok)throw Error();setItems(v=>v.filter(x=>x.id!==id))}catch{setError(t.error)}finally{setBusy('')}}
 if(!archiveId)return null;
 return <ArchiveShell archiveId={archiveId} view={null} section="trash"><main dir={locale==='he'?'rtl':'ltr'} style={{padding:'2rem',maxWidth:'50rem'}}><h1>{t.title}</h1><p>{t.intro}</p>{error&&<p role="alert">{error}</p>}{items.length===0&&!error&&<p>{t.empty}</p>}<ul>{items.map(item=><li key={item.id} style={{padding:'1rem',borderBottom:'1px solid #ddd'}}><strong dir="auto">{item.name}</strong><p>{t.deadline}: <time dateTime={item.recoveryUntil}>{new Date(item.recoveryUntil).toLocaleDateString(locale)}</time></p><button type="button" disabled={busy===item.id||new Date(item.recoveryUntil)<=new Date()} onClick={()=>restore(item.id)}>{t.restore}</button></li>)}</ul></main></ArchiveShell>;
}
