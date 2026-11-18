'use client';

import { useState, type FormEvent } from 'react';
import { Button, ModalDialog, PopoverButton, TextField } from '@invoyis/ui';
import s from './library.module.css';

const words = {
  en: { actions: 'Document actions', move: 'Move to Trash', title: 'Move this document to Trash?', explain: 'You can restore it for 30 days. It will leave search, review queues, counts and new exports.', confirm: 'Type the document name to confirm', cancel: 'Cancel', error: 'Could not move the document to Trash.' },
  fr: { actions: 'Actions du document', move: 'Déplacer vers la corbeille', title: 'Déplacer ce document vers la corbeille ?', explain: 'Vous pouvez le restaurer pendant 30 jours. Il disparaîtra des recherches, des examens, des totaux et des nouveaux exports.', confirm: 'Saisissez le nom du document pour confirmer', cancel: 'Annuler', error: 'Impossible de déplacer le document.' },
  he: { actions: 'פעולות במסמך', move: 'העברה לאשפה', title: 'להעביר את המסמך לאשפה?', explain: 'אפשר לשחזר אותו במשך 30 יום. הוא יוסר מהחיפוש, מתורי הבדיקה, מהספירות ומייצוא חדש.', confirm: 'יש להקליד את שם המסמך לאישור', cancel: 'ביטול', error: 'לא ניתן להעביר את המסמך לאשפה.' },
} as const;

export function TrashAction({ archiveId, documentId, name, locale, allowed = true, presentation = 'menu' }: {
  archiveId: string; documentId: string; name: string; locale: string; allowed?: boolean; presentation?: 'menu' | 'button';
}) {
  const t = words[locale as keyof typeof words] ?? words.en;
  const [menuOpen, setMenuOpen] = useState(false);
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  if (!allowed) return null;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (typed !== name || busy) return;
    setBusy(true); setError('');
    try {
      const response = await fetch(`/api/v1/archives/${archiveId}/documents/${documentId}/trash`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Invoyis-Intent': 'browser' },
        body: JSON.stringify({ name, reason: 'user_requested' }),
      });
      if (!response.ok) throw new Error(String(response.status));
      window.location.assign(`/archives/${archiveId}/trash`);
    } catch {
      setError(t.error);
      setBusy(false);
    }
  }

  return <>
    {presentation === 'menu' ? <PopoverButton
      label={<span title={t.actions} className={s.moreGlyph}><span className={s.srOnly}>{t.actions}</span><span aria-hidden="true">⋯</span></span>}
      dialogLabel={t.actions} buttonClassName={s.iconButton} popoverClassName={s.documentMenu}
      placement={locale === 'he' ? 'bottom start' : 'bottom end'} isOpen={menuOpen} onOpenChange={setMenuOpen}>
      <button type="button" className={s.documentMenuAction} onClick={() => { setMenuOpen(false); setOpen(true); }}>{t.move}</button>
    </PopoverButton> : <Button variant="secondary" onPress={() => setOpen(true)}>{t.move}</Button>}
    <ModalDialog isOpen={open} onOpenChange={setOpen} title={t.title} className={s.trashDialog}>
      <form onSubmit={submit} dir={locale === 'he' ? 'rtl' : 'ltr'}>
        <p className={s.trashName}><bdi dir="auto">{name}</bdi></p>
        <p className={s.trashExplanation}>{t.explain}</p>
        <TextField label={t.confirm} value={typed} onChange={setTyped} isRequired inputProps={{ autoFocus: true, dir: 'auto', autoComplete: 'off' }} />
        {error && <p role="alert" className="ui-error">{error}</p>}
        <div className="ui-dialog-actions">
          <Button variant="secondary" onPress={() => setOpen(false)}>{t.cancel}</Button>
          <Button variant="danger" type="submit" isDisabled={busy || typed !== name}>{t.move}</Button>
        </div>
      </form>
    </ModalDialog>
  </>;
}
