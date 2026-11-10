'use client';

import { getAuthSession, logoutAuthSession, problemMessage, requestAuthCode, verifyAuthCode, type ProblemCode } from '@invoyis/contracts';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import styles from './sign-in.module.css';

type Locale = 'en' | 'fr' | 'he';
type Step = 'email' | 'code' | 'signed-in';

const copy = {
  en: { title: 'Sign in to Invoyis', email: 'Email address', continue: 'Send code', code: 'Six-digit code', verify: 'Sign in', resend: 'Send a new code', change: 'Change email', sent: 'If this address is eligible, a code is on its way.', resent: 'A new code has been requested.', signedIn: 'Signed in as', logout: 'Sign out', loading: 'Please wait…', language: 'Language', invalidEmail: 'Enter a valid email address.', invalidCode: 'Enter all six digits.' },
  fr: { title: 'Connexion à Invoyis', email: 'Adresse e-mail', continue: 'Envoyer le code', code: 'Code à six chiffres', verify: 'Se connecter', resend: 'Envoyer un nouveau code', change: 'Changer d’adresse', sent: 'Si cette adresse est admissible, un code va arriver.', resent: 'Un nouveau code a été demandé.', signedIn: 'Connecté en tant que', logout: 'Se déconnecter', loading: 'Veuillez patienter…', language: 'Langue', invalidEmail: 'Saisissez une adresse e-mail valide.', invalidCode: 'Saisissez les six chiffres.' },
  he: { title: 'כניסה ל-Invoyis', email: 'כתובת אימייל', continue: 'שליחת קוד', code: 'קוד בן שש ספרות', verify: 'כניסה', resend: 'שליחת קוד חדש', change: 'שינוי כתובת אימייל', sent: 'אם הכתובת זכאית, קוד נשלח אליה.', resent: 'נשלחה בקשה לקוד חדש.', signedIn: 'מחוברים בתור', logout: 'התנתקות', loading: 'נא להמתין…', language: 'שפה', invalidEmail: 'יש להזין כתובת אימייל תקינה.', invalidCode: 'יש להזין שש ספרות.' },
} satisfies Record<Locale, Record<string, string>>;

const mutationHeaders = (locale: Locale) => ({ 'X-Invoyis-Intent': 'browser' as const, 'Accept-Language': locale });

export default function SignInPage() {
  const [locale, setLocale] = useState<Locale>('en');
  const [step, setStep] = useState<Step>('email');
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const emailRef = useRef<HTMLInputElement>(null);
  const codeRef = useRef<HTMLInputElement>(null);
  const text = copy[locale];

  useEffect(() => {
    const language = navigator.language.toLowerCase();
    setLocale(language.startsWith('he') ? 'he' : language.startsWith('fr') ? 'fr' : 'en');
    let active = true;
    getAuthSession({ credentials: 'same-origin' }).then(({ data }) => {
      if (active && data) { setEmail(data.email); setStep('signed-in'); }
    }).catch(() => {});
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (step === 'code') codeRef.current?.focus();
    if (step === 'email') emailRef.current?.focus();
  }, [step]);

  function apiError(value: unknown): string {
    const problem = value as { code?: ProblemCode } | undefined;
    return problemMessage(problem?.code ?? 'INTERNAL_ERROR', locale);
  }

  async function sendCode(event?: FormEvent) {
    event?.preventDefault();
    setError(''); setNotice('');
    if (!/^\S+@\S+\.\S+$/.test(email.trim())) { setError(text.invalidEmail); return; }
    setBusy(true);
    try {
      const { error } = await requestAuthCode({ body: { email: email.trim() }, headers: mutationHeaders(locale), credentials: 'same-origin' });
      if (error) { setError(apiError(error)); return; }
      setCode('');
      setStep('code');
      setNotice(step === 'code' ? text.resent : text.sent);
      codeRef.current?.focus();
    } catch { setError(problemMessage('UPSTREAM_UNAVAILABLE', locale)); }
    finally { setBusy(false); }
  }

  async function verify(event: FormEvent) {
    event.preventDefault();
    setError(''); setNotice('');
    if (!/^[0-9]{6}$/.test(code)) { setError(text.invalidCode); return; }
    setBusy(true);
    try {
      const { data, error } = await verifyAuthCode({ body: { email: email.trim(), code }, headers: mutationHeaders(locale), credentials: 'same-origin' });
      if (error) { setError(apiError(error)); return; }
      if (data) { setCode(''); setStep('signed-in'); }
    } catch { setError(problemMessage('UPSTREAM_UNAVAILABLE', locale)); }
    finally { setBusy(false); }
  }

  async function logout() {
    setError(''); setBusy(true);
    try {
      const { error } = await logoutAuthSession({ headers: mutationHeaders(locale), credentials: 'same-origin' });
      if (error) { setError(apiError(error)); return; }
      setStep('email'); setCode(''); setNotice('');
    } catch { setError(problemMessage('UPSTREAM_UNAVAILABLE', locale)); }
    finally { setBusy(false); }
  }

  return <main className={styles.page} lang={locale} dir={locale === 'he' ? 'rtl' : 'ltr'}>
    <section className={styles.card}>
      <div className={styles.top}>
        <h1>{text.title}</h1>
        <label>{text.language}<select value={locale} onChange={(event) => setLocale(event.target.value as Locale)} aria-label={text.language}>
          <option value="en">English</option><option value="fr">Français</option><option value="he">עברית</option>
        </select></label>
      </div>
      {step === 'email' && <form onSubmit={sendCode}>
        <label htmlFor="email">{text.email}</label>
        <input ref={emailRef} id="email" type="email" autoComplete="email" inputMode="email" dir="ltr" value={email} onChange={(event) => setEmail(event.target.value)} required disabled={busy} />
        <button disabled={busy} type="submit">{busy ? text.loading : text.continue}</button>
      </form>}
      {step === 'code' && <form onSubmit={verify}>
        <p className={styles.address} dir="ltr">{email}</p>
        <label htmlFor="code">{text.code}</label>
        <input ref={codeRef} id="code" type="text" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} dir="ltr" value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, '').slice(0, 6))} required disabled={busy} />
        <button disabled={busy} type="submit">{busy ? text.loading : text.verify}</button>
        <div className={styles.actions}>
          <button type="button" className={styles.link} onClick={() => sendCode()} disabled={busy}>{text.resend}</button>
          <button type="button" className={styles.link} onClick={() => { setStep('email'); setCode(''); setError(''); setNotice(''); }} disabled={busy}>{text.change}</button>
        </div>
      </form>}
      {step === 'signed-in' && <div><p>{text.signedIn} <bdi dir="ltr">{email}</bdi></p><button onClick={logout} disabled={busy}>{busy ? text.loading : text.logout}</button></div>}
      {notice && <p className={styles.notice} role="status">{notice}</p>}
      {error && <p className={styles.error} role="alert">{error}</p>}
    </section>
  </main>;
}
