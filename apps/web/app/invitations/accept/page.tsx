'use client';
import { acceptArchiveInvitation, getAuthSession } from '@invoyis/contracts';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { intent, locale as browserLocale, words, type Locale } from '../../../lib/archive-ui';
import styles from '../../archives/archive.module.css';

export default function AcceptInvitationPage() {
  const [language, setLanguage] = useState<Locale>('en'); const [token, setToken] = useState('');
  const [signedIn, setSignedIn] = useState(false); const [error, setError] = useState(''); const [busy, setBusy] = useState(false);
  const t = words[language];
  useEffect(() => {
    setLanguage(browserLocale());
    const value = new URLSearchParams(window.location.hash.slice(1)).get('token');
    if (value) { sessionStorage.setItem('invoyis-invitation', value); history.replaceState(null, '', '/invitations/accept'); }
    setToken(value ?? sessionStorage.getItem('invoyis-invitation') ?? '');
    getAuthSession({ credentials: 'same-origin' }).then(({ data }) => setSignedIn(!!data || sessionStorage.getItem('invoyis-code-verified') === '1'));
  }, []);
  async function accept() {
    setBusy(true); setError('');
    const { data, error } = await acceptArchiveInvitation({ body: { token }, headers: intent, credentials: 'same-origin' });
    setBusy(false); if (error || !data) { if ((error as { code?: string } | undefined)?.code === 'UNAUTHORIZED') { sessionStorage.removeItem('invoyis-code-verified'); setSignedIn(false); } setError(t.error); return; }
    sessionStorage.removeItem('invoyis-invitation'); sessionStorage.removeItem('invoyis-code-verified'); window.location.assign(`/archives/${data.id}`);
  }
  return <main className={styles.page} lang={language} dir={language === 'he' ? 'rtl' : 'ltr'}><div className={styles.wrap}><section className={styles.card}>
    <h1>{t.accept}</h1>{!token ? <p className={styles.error}>{t.error}</p> : signedIn ? <button className={styles.button} onClick={accept} disabled={busy}>{busy ? t.loading : t.accept}</button> : <Link href="/sign-in?next=/invitations/accept">{t.signIn}</Link>}
    {error && <p className={styles.error} role="alert">{error}</p>}
  </section></div></main>;
}
