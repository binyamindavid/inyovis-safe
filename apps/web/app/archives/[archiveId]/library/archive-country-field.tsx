'use client';

import { useId } from 'react';
import { useWords } from '../../../../lib/catalog';

const countryCodes = ['AT', 'BE', 'DE', 'ES', 'FR', 'IE', 'IL', 'IT', 'LU', 'NL', 'PT'];

export function ArchiveCountryField({ value, onChange, required, invalid, legacyCode }: {
  value: string;
  onChange: (code: string) => void;
  required: boolean;
  invalid: boolean;
  legacyCode?: string | null;
}) {
  const { locale, t } = useWords();
  const errorId = useId();
  const names = new Intl.DisplayNames(locale, { type: 'region' });
  const countries = countryCodes.map(code => ({ code, name: names.of(code) ?? code }))
    .sort((a, b) => a.name.localeCompare(b.name, locale));

  return <div className="ui-field" data-invalid={invalid || undefined}>
    <label className="ui-field">
      <span className="react-aria-Label">{t.countryCode}</span>
      <select className="ui-input" value={value} onChange={event => onChange(event.target.value)}
        required={required} aria-invalid={invalid || undefined} aria-describedby={invalid ? errorId : undefined}>
        <option value="" disabled={required}>{required ? t.chooseCountry : t.noCountry}</option>
        {legacyCode && !countryCodes.includes(legacyCode) &&
          <option value={legacyCode} disabled>{t.legacyCountryCode.replace('{code}', legacyCode)}</option>}
        {countries.map(({ code, name }) => <option key={code} value={code}>{name} ({code})</option>)}
      </select>
    </label>
    {invalid && <span id={errorId} className="ui-error" role="alert">{t.chooseCountry}</span>}
  </div>;
}
