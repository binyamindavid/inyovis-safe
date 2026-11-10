'use client';
import { getAuthSession, logoutAuthSession, problemMessage, requestAuthCode, verifyAuthCode, type ProblemCode } from '@invoyis/contracts';
import { Button, TextField } from '@invoyis/ui';
import { useEffect, useState, type FormEvent } from 'react';
import { LanguageSelect } from '../../components/language-select';
import { useWords } from '../../lib/catalog';
import { intent } from '../../lib/archive-ui';
import styles from './sign-in.module.css';

type Step = 'email' | 'code' | 'signed-in';
export default function SignInPage() {
  const { locale, t } = useWords();
  const [step, setStep] = useState<Step>('email'); const [email, setEmail] = useState(''); const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false); const [error, setError] = useState(''); const [notice, setNotice] = useState('');
  const next = typeof window === 'undefined' ? '' : new URLSearchParams(window.location.search).get('next');
  const destination = next === '/invitations/accept' || next === '/archives' ? next : '';
  const invitationToken = destination === '/invitations/accept' && typeof window !== 'undefined' ? sessionStorage.getItem('invoyis-invitation') ?? undefined : undefined;
  useEffect(() => {
    let active = true;
    getAuthSession({ credentials: 'same-origin' }).then(({ data }) => {
      if (active && data) { if (destination) window.location.assign(destination); else { setEmail(data.email); setStep('signed-in'); } }
    }).catch(() => {});
    return () => { active = false; };
  }, [destination]);
  const message = (value: unknown) => problemMessage((value as { code?: ProblemCode } | undefined)?.code ?? 'INTERNAL_ERROR', locale);
  async function sendCode(event?: FormEvent) {
    event?.preventDefault(); setError(''); setNotice('');
    if (!/^\S+@\S+\.\S+$/.test(email.trim())) { setError(t.invalidEmail); return; }
    setBusy(true);
    try {
      const { error } = await requestAuthCode({ body: { email: email.trim(), invitationToken }, headers: { ...intent, 'Accept-Language': locale }, credentials: 'same-origin' });
      if (error) { setError(message(error)); return; }
      setCode(''); setStep('code'); setNotice(step === 'code' ? t.resent : t.sent);
    } catch { setError(message({ code:'UPSTREAM_UNAVAILABLE' })); }
    finally { setBusy(false); }
  }
  async function verify(event: FormEvent) {
    event.preventDefault(); setError(''); setNotice('');
    if (!/^[0-9]{6}$/.test(code)) { setError(t.invalidCode); return; }
    setBusy(true);
    try {
      const { data, error } = await verifyAuthCode({ body: { email: email.trim(), code, invitationToken }, headers: { ...intent, 'Accept-Language': locale }, credentials: 'same-origin' });
      if (error) { setError(message(error)); return; }
      if (data) { setCode(''); if (destination === '/invitations/accept') sessionStorage.setItem('invoyis-code-verified', '1'); if (destination) window.location.assign(destination); else window.location.reload(); }
    } catch { setError(message({ code:'UPSTREAM_UNAVAILABLE' })); }
    finally { setBusy(false); }
  }
  async function logout() {
    setError(''); setBusy(true);
    try { const { error } = await logoutAuthSession({ headers: intent, credentials: 'same-origin' }); if (error) { setError(message(error)); return; } setStep('email'); setCode(''); setNotice(''); window.location.reload(); }
    catch { setError(message({ code:'UPSTREAM_UNAVAILABLE' })); }
    finally { setBusy(false); }
  }
  return <main className={styles.page}><section className={styles.card}>
    <div className={styles.top}><h1>{t.signInTitle}</h1><LanguageSelect signedIn={step === 'signed-in'} /></div>
    {step === 'email' && <form onSubmit={sendCode}><TextField label={t.email} type="email" value={email} onChange={setEmail} isRequired error={error === t.invalidEmail ? error : undefined} inputProps={{ autoComplete:'email', inputMode:'email', dir:'ltr', autoFocus:true }} /><Button type="submit" isDisabled={busy}>{busy ? t.loading : t.sendCode}</Button></form>}
    {step === 'code' && <form onSubmit={verify}><p className={styles.address}><bdi dir="ltr">{email}</bdi></p><TextField label={t.code} value={code} onChange={value => setCode(value.replace(/\D/g, '').slice(0, 6))} isRequired error={error === t.invalidCode ? error : undefined} inputProps={{ type:'text', inputMode:'numeric', autoComplete:'one-time-code', pattern:'[0-9]{6}', maxLength:6, dir:'ltr', autoFocus:true }} /><Button type="submit" isDisabled={busy}>{busy ? t.loading : t.signIn}</Button><div className={styles.actions}><Button type="button" variant="quiet" onPress={() => void sendCode()} isDisabled={busy}>{t.resend}</Button><Button type="button" variant="quiet" onPress={() => { setStep('email'); setCode(''); setError(''); setNotice(''); }} isDisabled={busy}>{t.changeEmail}</Button></div></form>}
    {step === 'signed-in' && <div><p>{t.signedIn} <bdi dir="ltr">{email}</bdi></p><p><a href="/archives">{t.archives}</a></p><Button onPress={() => void logout()} isDisabled={busy}>{t.logout}</Button></div>}
    {notice && <p className={styles.notice} role="status">{notice}</p>}{error && error !== t.invalidEmail && error !== t.invalidCode && <p className={styles.error} role="alert">{error}</p>}
  </section></main>;
}
