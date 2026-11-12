'use client';
import { listDuplicateCandidates, resolveDuplicateCandidate, type DuplicateCandidate } from '@invoyis/contracts';
import { Button } from '@invoyis/ui';
import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useWords } from '../../../../lib/catalog';
import { intent } from '../../../../lib/archive-ui';
import { canResolveDuplicate, resolveAndRefresh } from '../../../../lib/duplicate-queue.mjs';
import { ArchiveShell, MenuButton } from '../library/archive-shell';
import styles from './queue.module.css';

export default function DuplicateQueuePage({ params }: { params: Promise<{ archiveId: string }> }) {
  const { t } = useWords();
  const [archiveId, setArchiveId] = useState('');
  const [items, setItems] = useState<DuplicateCandidate[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const statusRef = useRef<HTMLParagraphElement>(null);
  useEffect(() => { void params.then(v => setArchiveId(v.archiveId)); }, [params]);
  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const { data, response } = await listDuplicateCandidates({ path: { archiveId }, credentials: 'same-origin' });
      if (data) { setItems(data.items); setLoading(false); return true; }
      if (response?.status === 404) location.replace('/archives?access=changed');
    } catch { /* The queue remains unavailable until a successful reload. */ }
    setItems([]);
    setError(t.error);
    setLoading(false);
    return false;
  }, [archiveId, t.error]);
  useEffect(() => { if (archiveId) void refresh(); }, [archiveId, refresh]);

  async function resolve(candidate: DuplicateCandidate, decision: 'duplicate' | 'keep_both') {
    setBusy(candidate.id); setError(''); setMessage('');
    try {
      const { data, response } = await resolveAndRefresh(() => resolveDuplicateCandidate({
        path: { archiveId, id: candidate.id }, headers: intent, credentials: 'same-origin',
        body: { decision, leftRevisionId: candidate.left.revisionId, rightRevisionId: candidate.right.revisionId }
      }), refresh);
      if (data) setMessage(t.duplicateResolved);
      else if (response?.status === 404) location.replace('/archives?access=changed');
      else setError(response?.status === 409 ? t.duplicateConflict : t.error);
    } catch { setItems([]); setError(t.error); }
    setBusy('');
    requestAnimationFrame(() => statusRef.current?.focus());
  }

  const fieldLabels: Record<string, string> = { linkedSupplier: t.linkedSupplier, printedNumber: t.invoiceId, issueDate: t.issueDate, total: t.total, currency: t.currency };
  const side = (candidate: DuplicateCandidate, value: DuplicateCandidate['left']) => {
    const m = value.metadata;
    return <section className={styles.side} key={value.documentId} aria-label={value.filename}>
      <h3><bdi dir="auto">{value.filename}</bdi></h3>
      <dl>
        <div><dt>{t.linkedSupplier}</dt><dd><bdi dir="auto">{value.supplierName}</bdi></dd></div>
        <div><dt>{t.printedSupplier}</dt><dd><bdi dir="auto">{m.supplier}</bdi></dd></div>
        <div><dt>{t.invoiceId}</dt><dd><bdi dir="ltr">{m.invoiceId}</bdi></dd></div>
        <div><dt>{t.issueDate}</dt><dd><bdi dir="ltr">{m.issueDate}</bdi></dd></div>
        <div><dt>{t.total}</dt><dd><bdi dir="ltr">{m.total} {m.currency}</bdi></dd></div>
      </dl>
      <div className={styles.links}>
        <Link href={`/archives/${archiveId}/documents/${value.documentId}/review`}>{t.review}</Link>
        <a href={`/api/v1/archives/${archiveId}/documents/${value.documentId}/original`} target="_blank" rel="noopener noreferrer">{t.downloadOriginal}</a>
      </div>
    </section>;
  };
  if (!archiveId) return null;
  return <ArchiveShell archiveId={archiveId} section="duplicates">{shell =>
    <main className={styles.main}>
      <header className={styles.header}><MenuButton /><h1>{t.duplicateQueue}</h1></header>
      <div className={styles.content}>
        <p>{t.duplicateIntro}</p>
        <p ref={statusRef} tabIndex={-1} role={error ? 'alert' : 'status'} className={error ? styles.error : styles.status}>{error || message}</p>
        {loading ? <p>{t.loading}</p> : error && items.length === 0 ? null : items.length === 0 ? <p>{t.duplicateEmpty}</p> :
          <ol className={styles.list}>{items.map(candidate => <li key={candidate.id} className={styles.card}>
            <h2>{t.duplicateMatched}</h2>
            <ul className={styles.matches}>{candidate.matchedFields.map(field => <li key={field}>{fieldLabels[field] ?? field}</li>)}</ul>
            <div className={styles.sides}>{side(candidate, candidate.left)}{side(candidate, candidate.right)}</div>
            {canResolveDuplicate(shell.archive?.role) && <div className={styles.actions}>
              <Button onPress={() => void resolve(candidate, 'duplicate')} isDisabled={!!busy}>{t.duplicateMark}</Button>
              <Button variant="secondary" onPress={() => void resolve(candidate, 'keep_both')} isDisabled={!!busy}>{t.duplicateKeepBoth}</Button>
            </div>}
          </li>)}</ol>}
      </div>
    </main>
  }</ArchiveShell>;
}
