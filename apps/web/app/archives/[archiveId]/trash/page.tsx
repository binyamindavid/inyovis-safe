'use client';

import { Button } from '@invoyis/ui';
import { use, useCallback, useEffect, useState } from 'react';
import { FileName } from '../../../../components/file-name';
import { useWords } from '../../../../lib/catalog';
import { ArchiveShell, MenuButton } from '../library/archive-shell';
import { FileGlyph, TrashIcon } from '../library/icons';
import s from './trash.module.css';

type Item = { id: string; name: string; trashedAt: string; recoveryUntil: string };
const copy = {
  en: { title: 'Trash', intro: 'Restore documents during the 30-day recovery period. Moving a document to Trash does not erase stored copies.', name: 'Name', deadline: 'Restore until', restore: 'Restore', restoring: 'Restoring…', expired: 'Recovery period ended', empty: 'Trash is empty', emptyHint: 'Documents you move to Trash will appear here during their recovery period.', loading: 'Loading Trash…', error: 'Could not load Trash.', restoreError: 'Could not restore this document.', retry: 'Try again' },
  fr: { title: 'Corbeille', intro: 'Restaurez les documents pendant la période de récupération de 30 jours. Le déplacement vers la corbeille n’efface pas les copies stockées.', name: 'Nom', deadline: 'Restaurer jusqu’au', restore: 'Restaurer', restoring: 'Restauration…', expired: 'Période de récupération terminée', empty: 'La corbeille est vide', emptyHint: 'Les documents déplacés vers la corbeille apparaîtront ici pendant leur période de récupération.', loading: 'Chargement de la corbeille…', error: 'Impossible de charger la corbeille.', restoreError: 'Impossible de restaurer ce document.', retry: 'Réessayer' },
  he: { title: 'אשפה', intro: 'אפשר לשחזר מסמכים במהלך תקופת השחזור של 30 יום. העברה לאשפה אינה מוחקת עותקים שמורים.', name: 'שם', deadline: 'ניתן לשחזר עד', restore: 'שחזור', restoring: 'משחזרים…', expired: 'תקופת השחזור הסתיימה', empty: 'האשפה ריקה', emptyHint: 'מסמכים שיועברו לאשפה יופיעו כאן במהלך תקופת השחזור.', loading: 'טוענים את האשפה…', error: 'לא ניתן לטעון את האשפה.', restoreError: 'לא ניתן לשחזר את המסמך.', retry: 'לנסות שוב' },
} as const;

export default function TrashPage({ params }: { params: Promise<{ archiveId: string }> }) {
  const { archiveId } = use(params);
  const { locale } = useWords();
  const t = copy[locale as keyof typeof copy] ?? copy.en;
  const [items, setItems] = useState<Item[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const [restoreError, setRestoreError] = useState('');

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('');
    try {
      const response = await fetch(`/api/v1/archives/${archiveId}/trash`, { credentials: 'same-origin', cache: 'no-store', signal });
      if (response.status === 404) { window.location.replace('/archives?access=changed'); return; }
      if (!response.ok) throw new Error(String(response.status));
      const data = await response.json() as { items: Item[] };
      if (!signal?.aborted) setItems(data.items);
    } catch {
      if (!signal?.aborted) setError(t.error);
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, [archiveId, t.error]);
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort(); }, [load]);

  async function restore(id: string) {
    setBusy(id); setRestoreError('');
    try {
      const response = await fetch(`/api/v1/archives/${archiveId}/documents/${id}/restore`, { method: 'POST', credentials: 'same-origin', headers: { 'X-Invoyis-Intent': 'browser' } });
      if (response.status === 404) { window.location.replace('/archives?access=changed'); return; }
      if (!response.ok) throw new Error(String(response.status));
      setItems(previous => previous.filter(item => item.id !== id));
    } catch {
      setRestoreError(id);
    } finally {
      setBusy('');
    }
  }

  const deadline = (value: string) => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
  return <ArchiveShell archiveId={archiveId} section="trash">
    <main className={s.main} dir={locale === 'he' ? 'rtl' : 'ltr'}>
      <header className={s.header}><MenuButton /><div><h1>{t.title}</h1><p>{t.intro}</p></div></header>
      <div className={s.content} aria-busy={loading}>
        {error && <div className={s.error} role="alert"><p>{error}</p><Button variant="secondary" onPress={() => void load()}>{t.retry}</Button></div>}
        {loading ? <p className={s.loading} role="status">{t.loading}</p> : !error && items.length === 0 ? <div className={s.empty}><TrashIcon width={24} height={24} /><h2>{t.empty}</h2><p>{t.emptyHint}</p></div> : !error && <section aria-label={t.title}>
          <div className={s.columnHead} aria-hidden="true"><span /> <span>{t.name}</span><span>{t.deadline}</span><span /></div>
          <ul className={s.rows}>{items.map(item => {
            const expired = new Date(item.recoveryUntil) <= new Date();
            return <li key={item.id} className={s.row}>
              <span className={s.fileIcon}><FileGlyph /></span>
              <FileName className={s.filename} title={item.name}>{item.name}</FileName>
              <div className={s.date}><span className={s.mobileLabel}>{t.deadline}: </span><time dateTime={item.recoveryUntil}>{deadline(item.recoveryUntil)}</time></div>
              <div className={s.action}>{expired ? <span className={s.expired}>{t.expired}</span> : <Button variant="secondary" isDisabled={busy !== ''} onPress={() => void restore(item.id)}>{busy === item.id ? t.restoring : t.restore}</Button>}</div>
              {restoreError === item.id && <p className={s.rowError} role="alert">{t.restoreError}</p>}
            </li>;
          })}</ul>
        </section>}
      </div>
    </main>
  </ArchiveShell>;
}
