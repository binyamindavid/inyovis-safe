'use client';
import { acceptArchiveInvitation, getAuthSession, problemMessage, type ProblemCode } from '@invoyis/contracts';
import { Button } from '@invoyis/ui';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { LanguageSelect } from '../../../components/language-select';
import { useWords } from '../../../lib/catalog';
import { intent } from '../../../lib/archive-ui';
import styles from '../../archives/archive.module.css';
export default function AcceptInvitationPage() {
  const {locale,t}=useWords(); const [token,setToken]=useState(''); const [signedIn,setSignedIn]=useState(false);
  const [error,setError]=useState(''); const [busy,setBusy]=useState(false);
  useEffect(()=>{
    const value=new URLSearchParams(window.location.hash.slice(1)).get('token');
    if(value){sessionStorage.setItem('invoyis-invitation',value);history.replaceState(null,'','/invitations/accept');}
    setToken(value??sessionStorage.getItem('invoyis-invitation')??'');
    getAuthSession({credentials:'same-origin'}).then(({data})=>setSignedIn(!!data||sessionStorage.getItem('invoyis-code-verified')==='1'));
  },[]);
  async function accept(){setBusy(true);setError('');const {data,error}=await acceptArchiveInvitation({body:{token},headers:intent,credentials:'same-origin'});setBusy(false);
    if(error||!data){if((error as {code?:string}|undefined)?.code==='UNAUTHORIZED'){sessionStorage.removeItem('invoyis-code-verified');setSignedIn(false);}setError(problemMessage((error as {code?:ProblemCode}|undefined)?.code??'INTERNAL_ERROR',locale));return;}
    sessionStorage.removeItem('invoyis-invitation');sessionStorage.removeItem('invoyis-code-verified');window.location.assign(`/archives/${data.id}`);
  }
  return <main className={styles.page}><div className={styles.wrap}><div className={styles.top}><h1>{t.accept}</h1><LanguageSelect signedIn={signedIn} /></div><section className={styles.card}>
    {!token?<p className={styles.error}>{t.error}</p>:signedIn?<Button onPress={()=>void accept()} isDisabled={busy}>{busy?t.loading:t.accept}</Button>:<Link href="/sign-in?next=/invitations/accept">{t.signInToAccept}</Link>}
    {error&&<p className={styles.error} role="alert">{error}</p>}
  </section></div></main>;
}
