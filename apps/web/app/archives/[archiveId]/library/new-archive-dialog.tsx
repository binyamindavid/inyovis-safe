'use client';
import { createArchive, problemMessage, type ArchiveKind, type ProblemCode } from '@invoyis/contracts';
import { Button, ModalDialog, TextField } from '@invoyis/ui';
import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { useWords } from '../../../../lib/catalog';
import { intent } from '../../../../lib/archive-ui';
import { BriefcaseIcon, PersonIcon } from './icons';
import s from './library.module.css';

/** Name, Personal or Business, and (for Business) a two-letter jurisdiction. Opens the new archive when created. */
export function NewArchiveForm({ onCancel, autoFocus = true }: { onCancel?: () => void; autoFocus?: boolean }) {
  const { locale, t } = useWords();
  const [kind, setKind] = useState<ArchiveKind>('personal');
  const [name, setName] = useState('');
  const [jurisdiction, setJurisdiction] = useState('');
  const [invalid, setInvalid] = useState<'name' | 'jurisdiction' | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const group = useId();
  const nameInput = useRef<HTMLInputElement>(null);
  useEffect(() => { if (autoFocus) nameInput.current?.focus(); }, [autoFocus]);

  async function create(event: FormEvent) {
    event.preventDefault(); setError(''); setInvalid(null);
    if (!name.trim()) { setInvalid('name'); return; }
    if (kind === 'business' && !/^[A-Za-z]{2}$/.test(jurisdiction)) { setInvalid('jurisdiction'); return; }
    setBusy(true);
    try {
      const { data, error } = await createArchive({ body: { kind, name, ...(kind === 'business' ? { jurisdiction: jurisdiction.toUpperCase() } : {}) }, headers: intent, credentials: 'same-origin' });
      if (error || !data) { setError(problemMessage((error as { code?: ProblemCode })?.code ?? 'INTERNAL_ERROR', locale)); return; }
      window.location.assign(`/archives/${data.id}`);
    } catch { setError(problemMessage('UPSTREAM_UNAVAILABLE', locale)); }
    finally { setBusy(false); }
  }

  const kinds: { id: ArchiveKind; label: string; hint: string; icon: ReactNode }[] = [
    { id: 'personal', label: t.personal, hint: t.libKindPersonalHint, icon: <PersonIcon width={18} height={18} /> },
    { id: 'business', label: t.business, hint: t.libKindBusinessHint, icon: <BriefcaseIcon width={18} height={18} /> },
  ];
  return <form className={s.newArchiveForm} noValidate onSubmit={create}>
    <TextField label={t.name} value={name} onChange={v => { setName(v); setInvalid(null); }} isRequired error={invalid === 'name' ? t.invalidName : undefined} inputProps={{ maxLength: 120, ref: nameInput, dir: 'auto' }} />
    <fieldset className={s.kindGroup}>
      <legend>{t.libArchiveType}</legend>
      <div className={s.kindOptions}>
        {kinds.map(k => <label key={k.id} className={s.kindOption} data-kind={k.id} data-selected={kind === k.id || undefined}>
          <input type="radio" name={group} value={k.id} checked={kind === k.id} onChange={() => { setKind(k.id); setInvalid(null); }} />
          <span className={s.archiveIcon} data-kind={k.id}>{k.icon}</span>
          <span className={s.kindText}><strong>{k.label}</strong><small>{k.hint}</small></span>
        </label>)}
      </div>
    </fieldset>
    {kind === 'business' && <TextField label={t.jurisdiction} value={jurisdiction} onChange={v => { setJurisdiction(v); setInvalid(null); }} isRequired error={invalid === 'jurisdiction' ? t.invalidJurisdiction : undefined} inputProps={{ dir: 'ltr', pattern: '[A-Za-z]{2}', maxLength: 2, autoComplete: 'off' }} />}
    {error && <p className="ui-error" role="alert">{error}</p>}
    <div className="ui-dialog-actions">
      {onCancel && <Button variant="secondary" className={s.secondary} onPress={onCancel}>{t.cancel}</Button>}
      <Button type="submit" isDisabled={busy}>{busy ? t.loading : t.create}</Button>
    </div>
  </form>;
}

export function NewArchiveDialog({ isOpen, onOpenChange }: { isOpen: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useWords();
  return <ModalDialog isOpen={isOpen} onOpenChange={onOpenChange} title={t.libNewArchive} className={s.newArchiveDialog}>
    <p className={s.muted}>{t.libFirstArchiveHint}</p>
    <NewArchiveForm onCancel={() => onOpenChange(false)} />
  </ModalDialog>;
}
