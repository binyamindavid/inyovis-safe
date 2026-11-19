'use client';

import type { ShredStatus, TrashItem, TrashList, TrashShredEvent, TrashShredPreview } from '@invoyis/contracts';
import { Button, ModalDialog } from '@invoyis/ui';
import { use, useCallback, useEffect, useRef, useState } from 'react';
import { FileName } from '../../../../components/file-name';
import { useWords } from '../../../../lib/catalog';
import { ArchiveShell, MenuButton } from '../library/archive-shell';
import { FileGlyph, TrashIcon } from '../library/icons';
import { shredCopy } from './shred-copy';
import s from './trash.module.css';

const shredBatchSize = 10;
type ShredDialog = {
  document?: TrashItem;
  preview?: TrashShredPreview;
  results?: TrashShredEvent[];
  uncertain?: string[];
  interrupted?: boolean;
  error?: boolean;
};
const copy = {
  en: { title: 'Trash', intro: 'Restore documents during the 30-day recovery period. Moving a document to Trash does not erase stored copies.', name: 'Name', deadline: 'Restore until', restore: 'Restore', restoring: 'Restoring…', expired: 'Recovery period ended', empty: 'Trash is empty', emptyHint: 'Documents you move to Trash will appear here during their recovery period.', loading: 'Loading Trash…', error: 'Could not load Trash.', restoreError: 'Could not restore this document.', retry: 'Try again' },
  fr: { title: 'Corbeille', intro: 'Restaurez les documents pendant la période de récupération de 30 jours. Le déplacement vers la corbeille n’efface pas les copies stockées.', name: 'Nom', deadline: 'Restaurer jusqu’au', restore: 'Restaurer', restoring: 'Restauration…', expired: 'Période de récupération terminée', empty: 'La corbeille est vide', emptyHint: 'Les documents déplacés vers la corbeille apparaîtront ici pendant leur période de récupération.', loading: 'Chargement de la corbeille…', error: 'Impossible de charger la corbeille.', restoreError: 'Impossible de restaurer ce document.', retry: 'Réessayer' },
  he: { title: 'אשפה', intro: 'אפשר לשחזר מסמכים במהלך תקופת השחזור של 30 יום. העברה לאשפה אינה מוחקת עותקים שמורים.', name: 'שם', deadline: 'ניתן לשחזר עד', restore: 'שחזור', restoring: 'משחזרים…', expired: 'תקופת השחזור הסתיימה', empty: 'האשפה ריקה', emptyHint: 'מסמכים שיועברו לאשפה יופיעו כאן במהלך תקופת השחזור.', loading: 'טוענים את האשפה…', error: 'לא ניתן לטעון את האשפה.', restoreError: 'לא ניתן לשחזר את המסמך.', retry: 'לנסות שוב' },
} as const;

export default function TrashPage({ params }: { params: Promise<{ archiveId: string }> }) {
  const { archiveId } = use(params);
  const { locale } = useWords();
  const t = copy[locale as keyof typeof copy] ?? copy.en;
  const sh = shredCopy[locale as keyof typeof shredCopy] ?? shredCopy.en;
  const [list, setList] = useState<TrashList>({ items: [], canShred: false, shredding: [] });
  const [dialog, setDialog] = useState<ShredDialog>();
  const [submitting, setSubmitting] = useState(false);
  const activeRequest = useRef(false);
  const shredRequest = useRef<AbortController>(null);
  const loadSequence = useRef(0);
  const resultHeading = useRef<HTMLHeadingElement>(null);
  const historyHeading = useRef<HTMLHeadingElement>(null);
  const dir = locale === 'he' ? 'rtl' : 'ltr';
  const base = `/api/v1/archives/${archiveId}/trash`;
  const number = new Intl.NumberFormat(locale);
  const items = list.items;
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const [restoreError, setRestoreError] = useState('');

  const load = useCallback(async (signal?: AbortSignal, refresh = false) => {
    const sequence = ++loadSequence.current;
    const isCurrent = () => !signal?.aborted && sequence === loadSequence.current;
    if (!refresh) setLoading(true);
    setError('');
    try {
      const response = await fetch(base, { credentials: 'same-origin', cache: 'no-store', signal });
      if (!isCurrent()) return;
      if (response.status === 404) { window.location.replace('/archives?access=changed'); return; }
      if (!response.ok) throw new Error(String(response.status));
      const data = await response.json() as TrashList;
      if (isCurrent()) {
        setList(data);
        return data;
      }
    } catch {
      if (isCurrent() && !refresh) setError(t.error);
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [base, t.error]);
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort(); }, [load]);

  const hasCleanup = list.shredding.some(item => item.status === 'pending' || item.status === 'failed');
  useEffect(() => () => shredRequest.current?.abort(), [base]);

  useEffect(() => {
    if (!hasCleanup || busy) return;
    const controller = new AbortController();
    const timer = window.setInterval(() => {
      if (!activeRequest.current) void load(controller.signal, true);
    }, 5000);
    return () => { window.clearInterval(timer); controller.abort(); };
  }, [busy, hasCleanup, load]);

  useEffect(() => {
    if (!submitting && dialog?.results) resultHeading.current?.focus();
  }, [dialog?.results, submitting]);

  async function openShred(document?: TrashItem) {
    if (activeRequest.current) return;
    shredRequest.current?.abort();
    const controller = new AbortController();
    shredRequest.current = controller;
    setDialog({ document });
    try {
      const preview: TrashShredPreview = { items: [], eligible: 0, blocked: 0 };
      const seen = new Set<string>();
      let cursor: string | undefined;
      do {
        const query = new URLSearchParams();
        if (document) query.set('documentId', document.id);
        if (cursor) query.set('cursor', cursor);
        const response = await fetch(`${base}/shred?${query}`, { cache: 'no-store', signal: controller.signal });
        if (!response.ok) throw new Error(String(response.status));
        const page = await response.json() as TrashShredPreview;
        for (const item of page.items) {
          if (seen.has(item.id)) continue;
          seen.add(item.id);
          preview.items.push(item);
          if (item.status === 'eligible') preview.eligible++;
          else preview.blocked++;
        }
        cursor = page.nextCursor;
      } while (cursor && !controller.signal.aborted);
      if (!controller.signal.aborted) setDialog({ document, preview });
    } catch {
      if (!controller.signal.aborted) setDialog({ document, error: true });
    }
  }

  async function confirmShred() {
    if (activeRequest.current || !dialog?.preview?.eligible) return;
    const targets = dialog.preview.items.filter(item => item.status === 'eligible').map(({ id, name, trashedAt, revision }) => ({ id, name, trashedAt, revision }));
    activeRequest.current = true;
    const controller = new AbortController();
    shredRequest.current = controller;
    setSubmitting(true);
    let batch: typeof targets = [];
    const results: TrashShredEvent[] = [];
    setDialog({ ...dialog, results: [] });
    try {
      for (let offset = 0; offset < targets.length; offset += shredBatchSize) {
        batch = targets.slice(offset, offset + shredBatchSize);
        const response = await fetch(`${base}/shred`, {
          method: 'POST',
          signal: controller.signal,
          headers: { 'Content-Type': 'application/json', 'X-Invoyis-Intent': 'browser' },
          body: JSON.stringify({ confirmedCount: batch.length, targets: batch }),
        });
        if (!response.ok) throw new Error(String(response.status));
        const data = await response.json() as { items: TrashShredEvent[] };
        if (controller.signal.aborted) return;
        results.push(...data.items);
        const authorized = data.items.filter(item => item.status !== 'blocked');
        const authorizedIDs = new Set(authorized.map(item => item.id));
        setList(previous => ({
          ...previous,
          items: previous.items.filter(item => !authorizedIDs.has(item.id)),
          shredding: [...authorized, ...previous.shredding.filter(item => !authorizedIDs.has(item.id))],
        }));
        setDialog({ ...dialog, results: [...results] });
        if (data.items.length !== batch.length) throw new Error('Incomplete batch');
      }
      await load(controller.signal, true);
    } catch {
      if (controller.signal.aborted) return;
      const known = new Set(results.map(item => item.id));
      const uncertain = batch.filter(item => !known.has(item.id)).map(item => item.id);
      setDialog({ ...dialog, results: [...results], uncertain, interrupted: true });
      await reconcileShred(uncertain, controller.signal);
    } finally {
      setSubmitting(false);
      activeRequest.current = false;
    }
  }

  async function reconcileShred(uncertain: string[], signal?: AbortSignal) {
    const current = await load(signal, true);
    if (!current) return;
    const ids = new Set(uncertain);
    const recovered = current.shredding.filter(item => ids.has(item.id));
    if (!recovered.length) return;
    const recoveredIDs = new Set(recovered.map(item => item.id));
    setDialog(previous => previous?.uncertain === uncertain
      ? { ...previous, results: [...(previous.results ?? []), ...recovered], uncertain: uncertain.filter(id => !recoveredIDs.has(id)) }
      : previous);
  }

  function closeShred() {
    if (activeRequest.current) return;
    shredRequest.current?.abort();
    const completed = dialog?.results?.some(item => item.status !== 'blocked');
    setDialog(undefined);
    if (completed) window.requestAnimationFrame(() => historyHeading.current?.focus());
  }

  function outcome(item: ShredStatus) {
    const reason = sh.blockers[item.blocker as keyof typeof sh.blockers];
    return reason ? `${sh.states.blocked} · ${reason}` : sh.states[item.status];
  }

  async function restore(id: string) {
    setBusy(id); setRestoreError('');
    try {
      const response = await fetch(`/api/v1/archives/${archiveId}/documents/${id}/restore`, { method: 'POST', credentials: 'same-origin', headers: { 'X-Invoyis-Intent': 'browser' } });
      if (response.status === 404) { window.location.replace('/archives?access=changed'); return; }
      if (!response.ok) throw new Error(String(response.status));
      setList(previous => ({ ...previous, items: previous.items.filter(item => item.id !== id) }));
    } catch {
      setRestoreError(id);
    } finally {
      setBusy('');
    }
  }

  const resultsByID = new Map(dialog?.results?.map(item => [item.id, item]));
  const uncertainIDs = new Set(dialog?.uncertain);
  const deadline = (value: string) => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
  return <ArchiveShell archiveId={archiveId} section="trash">
    <main className={s.main} dir={dir}>
      <header className={s.header}><MenuButton /><div><h1>{t.title}</h1><p>{t.intro}</p></div></header>
      <div className={s.content} aria-busy={loading}>
        {!loading && !error && list.canShred && items.length > 0 && <div className={s.pageActions}>
          <Button variant="secondary" isDisabled={busy !== ''} onPress={() => void openShred()}>{sh.empty}</Button>
        </div>}
        {error && <div className={s.error} role="alert"><p>{error}</p><Button variant="secondary" onPress={() => void load()}>{t.retry}</Button></div>}
        {loading ? <p className={s.loading} role="status">{t.loading}</p> : !error && items.length === 0 ? <div className={s.empty}><TrashIcon width={24} height={24} /><h2>{t.empty}</h2><p>{t.emptyHint}</p></div> : !error && <section aria-label={t.title}>
          <div className={s.columnHead} aria-hidden="true"><span /> <span>{t.name}</span><span>{t.deadline}</span><span /></div>
          <ul className={s.rows}>{items.map(item => {
            const expired = new Date(item.recoveryUntil) <= new Date();
            return <li key={item.id} className={s.row}>
              <span className={s.fileIcon}><FileGlyph /></span>
              <FileName className={s.filename} title={item.name}>{item.name}</FileName>
              <div className={s.date}><span className={s.mobileLabel}>{t.deadline}: </span><time dateTime={item.recoveryUntil}>{deadline(item.recoveryUntil)}</time></div>
              <div className={s.action}>{expired ? <span className={s.expired}>{t.expired}</span> : <Button variant="secondary" isDisabled={busy !== ''} onPress={() => void restore(item.id)}>{busy === item.id ? t.restoring : t.restore}</Button>}
                {list.canShred && <Button variant="quiet" isDisabled={busy !== ''} onPress={() => void openShred(item)}>{sh.shred}</Button>}
              </div>
              {restoreError === item.id && <p className={s.rowError} role="alert">{t.restoreError}</p>}
            </li>;
          })}</ul>
        </section>}
        {list.shredding.length > 0 && <section className={s.history} aria-labelledby="trash-shred-history">
          <h2 id="trash-shred-history" ref={historyHeading} tabIndex={-1}>{sh.history}</h2>
          <ul>{list.shredding.map(item => <li key={item.id}>
            <span>{outcome(item)}</span>
            <span>{sh.reference} <bdi>{item.id}</bdi></span>
            {item.requestedAt && <time dateTime={item.requestedAt}>{deadline(item.requestedAt)}</time>}
          </li>)}</ul>
        </section>}
      </div>
      <ModalDialog isOpen={!!dialog} onOpenChange={open => { if (!open) closeShred(); }}
        isDismissable={!submitting}
        className={s.dialog} title={<span dir={dir}>{dialog?.document ? sh.shred : sh.empty}</span>}>
        <div dir={dir}>
          {dialog?.document && <p><FileName>{dialog.document.name}</FileName></p>}
          {dialog?.error && <p role="alert">{sh.error}</p>}
          {dialog?.interrupted && <p role="alert">{sh.interrupted}</p>}
          {dialog?.results && dialog.preview && <p role="status">
            {sh.progress(number.format(dialog.results.length), number.format(dialog.preview.eligible))}
          </p>}
          {!dialog?.preview && !dialog?.error && <p role="status">{sh.checking}</p>}
          {dialog?.preview && <>
            {dialog.results ? <h3 tabIndex={-1} ref={resultHeading}>{submitting && !dialog.interrupted ? sh.submitting : sh.results}</h3> : <>
              <p>{sh.count(number.format(dialog.preview.eligible), number.format(dialog.preview.blocked))}</p>
              {dialog.preview.eligible > 0 && <p>{sh.warning(number.format(dialog.preview.eligible), dialog.preview.eligible === 1)}</p>}
            </>}
            <ul className={s.preview}>{dialog.preview.items.map(item => {
              const result = resultsByID.get(item.id);
              const status = result ? outcome(result) : item.status !== 'eligible' || !dialog.results
                ? outcome(item) : uncertainIDs.has(item.id) ? sh.uncertain : submitting && !dialog.interrupted ? sh.waiting : sh.notSent;
              return <li key={item.id}>
                <FileName>{item.name}</FileName>
                <span>{status}</span>
                {result && result.status !== 'blocked' && <bdi className={s.reference}>{result.id}</bdi>}
              </li>;
            })}</ul>
          </>}
          <div className="ui-dialog-actions">
            <Button variant="secondary" autoFocus isDisabled={submitting} onPress={closeShred}>
              {dialog?.results || dialog?.error || dialog?.preview?.eligible === 0 ? sh.close : sh.cancel}
            </Button>
            {dialog?.uncertain && dialog.uncertain.length > 0 && !submitting &&
              <Button variant="secondary" onPress={() => void reconcileShred(dialog.uncertain ?? [])}>{sh.refreshOutcomes}</Button>}
            {!!dialog?.preview?.eligible && !dialog.results && !dialog.error &&
              <Button variant="danger" isDisabled={submitting} onPress={() => void confirmShred()}>{sh.confirm}</Button>}
          </div>
        </div>
      </ModalDialog>
    </main>
  </ArchiveShell>;
}
