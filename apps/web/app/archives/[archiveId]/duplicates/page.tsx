'use client';
import { listDuplicateCandidates, resolveDuplicateCandidate, type DuplicateCandidate } from '@invoyis/contracts';
import { Button } from '@invoyis/ui';
import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useWords } from '../../../../lib/catalog';
import { intent } from '../../../../lib/archive-ui';
import { canResolveDuplicate, resolveAndRefresh } from '../../../../lib/duplicate-queue.mjs';
import { FileName } from '../../../../components/file-name';
import { ArchiveShell, MenuButton } from '../library/archive-shell';
import { DuplicateIcon } from '../library/icons';
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
    const other = value === candidate.left ? candidate.right : candidate.left;
    // Values that differ between the two documents keep full contrast; shared values recede.
    const differs = (a: unknown, b: unknown) => (String(a ?? '') !== String(b ?? '') ? 'true' : undefined);
    return <section className={styles.side} key={value.documentId} aria-label={value.filename}>
      <h3><FileName>{value.filename}</FileName></h3>
      <dl>
        <div data-differs={differs(value.supplierName, other.supplierName)}><dt>{t.linkedSupplier}</dt><dd><bdi dir="auto">{value.supplierName}</bdi></dd></div>
        <div data-differs={differs(m.supplier, other.metadata.supplier)}><dt>{t.printedSupplier}</dt><dd><bdi dir="auto">{m.supplier}</bdi></dd></div>
        <div data-differs={differs(m.invoiceId, other.metadata.invoiceId)}><dt>{t.invoiceId}</dt><dd><bdi dir="ltr">{m.invoiceId}</bdi></dd></div>
        <div data-differs={differs(m.issueDate, other.metadata.issueDate)}><dt>{t.issueDate}</dt><dd><bdi dir="ltr">{m.issueDate}</bdi></dd></div>
        <div data-differs={differs(`${m.total} ${m.currency}`, `${other.metadata.total} ${other.metadata.currency}`)}><dt>{t.total}</dt><dd><bdi dir="ltr">{m.total} {m.currency}</bdi></dd></div>
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
      <div className={`${styles.content} ${!loading && !error && items.length === 0 ? styles.emptyContent : ''}`}>
        {items.length > 0 && <p className={styles.intro}>{t.duplicateIntro}</p>}
        <p ref={statusRef} tabIndex={-1} role={error ? 'alert' : 'status'} className={error ? styles.error : styles.status}>{error || message}</p>
        {loading ? <p className={styles.loading}>{t.loading}</p> : error && items.length === 0 ? null : items.length === 0
          ? <div className={styles.empty}><DuplicateIcon width={20} height={20} /><p>{t.duplicateEmpty}</p><p className={styles.hint}>{t.duplicateEmptyHint}</p></div> :
          <ol className={styles.list}>{items.map(candidate => <li key={candidate.id} className={styles.card}>
            <h2 className={styles.matchLine}><span>{t.duplicateMatchedOn}</span> {candidate.matchedFields.map(field => fieldLabels[field] ?? field).join(', ')}</h2>
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
