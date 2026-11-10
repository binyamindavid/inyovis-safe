'use client';
import { setAccountLanguage } from '@invoyis/contracts';
import { Select } from '@invoyis/ui';
import { useState } from 'react';
import { useWords, type Locale } from '../lib/catalog';
import { intent } from '../lib/archive-ui';
export function LanguageSelect({ signedIn }: { signedIn: boolean }) {
  const { locale, t } = useWords();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function change(value: Locale) {
    setError(''); setBusy(true);
    try {
      if (signedIn) {
        const { error } = await setAccountLanguage({ body: { language: value }, headers: intent, credentials: 'same-origin' });
        if (error) { setError(t.error); return; }
      } else {
        const response = await fetch('/api/interface-language', { method: 'POST', headers: { 'Content-Type': 'application/json', ...intent }, body: JSON.stringify({ language: value }) });
        if (!response.ok) { setError(t.error); return; }
      }
      window.location.reload();
    } catch { setError(t.error); }
    finally { setBusy(false); }
  }
  return <div><Select label={t.language} selectedKey={locale} isDisabled={busy} onSelectionChange={key => void change(String(key) as Locale)} options={[{id:'en',label:'English'},{id:'fr',label:'Français'},{id:'he',label:'עברית'}]} />{error && <p role="alert" className="ui-error">{error}</p>}</div>;
}
