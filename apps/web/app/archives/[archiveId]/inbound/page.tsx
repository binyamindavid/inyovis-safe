'use client';
import { use, useEffect, useState } from 'react';
import { useWords } from '../../../../lib/catalog';
import { ArchiveShell, MenuButton } from '../library/archive-shell';

type Outcome = { id: string; outcome: string; acceptedCount: number; rejectedCount: number; receivedAt: string };
const words = {
  en: { title: 'Forwarded email', empty: 'No forwarded emails yet.', loading: 'Loading…', error: 'Forwarded email history is unavailable.', accepted: 'PDFs accepted', rejected: 'Files rejected', upload: 'This email had no PDF. Upload the document directly from Add documents.', states: { accepted: 'Accepted', accepted_with_rejections: 'Accepted with rejected files', attachment_rejected: 'Files rejected', upload_guidance: 'No PDF attached', no_pdf: 'No PDF attached', disabled_address: 'Address disabled', retired_address: 'Address rotated', address_quota: 'Address limit reached', archive_quota: 'Archive limit reached', pending: 'Processing' } },
  fr: { title: 'E-mails transférés', empty: 'Aucun e-mail transféré.', loading: 'Chargement…', error: 'L’historique des e-mails est indisponible.', accepted: 'PDF acceptés', rejected: 'Fichiers refusés', upload: 'Cet e-mail ne contenait aucun PDF. Importez le document directement avec Ajouter des documents.', states: { accepted: 'Accepté', accepted_with_rejections: 'Accepté avec des fichiers refusés', attachment_rejected: 'Fichiers refusés', upload_guidance: 'Aucun PDF joint', no_pdf: 'Aucun PDF joint', disabled_address: 'Adresse désactivée', retired_address: 'Adresse remplacée', address_quota: 'Limite de l’adresse atteinte', archive_quota: 'Limite de l’archive atteinte', pending: 'Traitement' } },
  he: { title: 'דוא״ל מועבר', empty: 'אין עדיין הודעות מועברות.', loading: 'טוענים…', error: 'היסטוריית הדוא״ל אינה זמינה.', accepted: 'קובצי PDF התקבלו', rejected: 'קבצים נדחו', upload: 'להודעה לא צורף PDF. אפשר להעלות את המסמך ישירות דרך הוספת מסמכים.', states: { accepted: 'התקבל', accepted_with_rejections: 'התקבל עם קבצים שנדחו', attachment_rejected: 'קבצים נדחו', upload_guidance: 'לא צורף PDF', no_pdf: 'לא צורף PDF', disabled_address: 'הכתובת מושבתת', retired_address: 'הכתובת הוחלפה', address_quota: 'מגבלת הכתובת הושגה', archive_quota: 'מגבלת הארכיון הושגה', pending: 'בעיבוד' } },
} as const;

export default function InboundPage({ params }: { params: Promise<{ archiveId: string }> }) {
  const { archiveId } = use(params);
  const { locale } = useWords();
  const t = words[locale];
  const [items, setItems] = useState<Outcome[] | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => {
    let live = true;
    fetch(`/api/v1/archives/${archiveId}/inbound-deliveries`, { credentials: 'same-origin', cache: 'no-store' })
      .then(async response => { if (!response.ok) throw new Error(); return response.json() as Promise<{ items: Outcome[] }>; })
      .then(value => { if (live) setItems(value.items); })
      .catch(() => { if (live) setError(true); });
    return () => { live = false; };
  }, [archiveId]);
  return <ArchiveShell archiveId={archiveId} section="inbound"><main dir={locale === 'he' ? 'rtl' : 'ltr'} style={{ maxWidth: 760, padding: '2rem' }}>
    <header style={{ display: 'flex', alignItems: 'center', gap: 16 }}><MenuButton /><h1>{t.title}</h1></header>
    {error ? <p role="alert">{t.error}</p> : !items ? <p>{t.loading}</p> : items.length === 0 ? <p>{t.empty}</p> :
      <ol>{items.map(item => <li key={item.id} style={{ marginBlock: '1rem' }}>
        <strong>{(t.states as Record<string,string>)[item.outcome] ?? item.outcome}</strong>{' · '}
        <time dateTime={item.receivedAt}>{new Date(item.receivedAt).toLocaleString(locale)}</time>
        <div>{t.accepted}: {item.acceptedCount} · {t.rejected}: {item.rejectedCount}</div>
        {item.outcome === 'upload_guidance' && <p>{t.upload}</p>}
      </li>)}</ol>}
  </main></ArchiveShell>;
}
