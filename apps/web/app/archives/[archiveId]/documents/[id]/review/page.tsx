'use client';
import { getDocumentReview, writeDocumentReview, type ReviewMetadata, type ReviewState } from '@invoyis/contracts';
import { Button, Checkbox, Select, TextField } from '@invoyis/ui';
import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useWords } from '../../../../../../lib/catalog';
import { intent } from '../../../../../../lib/archive-ui';
import { displayReviewDecimal, parseReviewDecimal, warningCodes, sumReviewDecimals } from '../../../../../../lib/review-decimal.mjs';
import styles from './review.module.css';
import { PreviewPanel } from './preview-panel';
const empty: ReviewMetadata = { documentType: '', supplier: '', invoiceId: '', accountReference: '', issueDate: '', currency: '', subtotal: null, tax: null, total: null, taxLines: [] };
const fields = ['supplier', 'invoiceId', 'accountReference', 'issueDate', 'currency', 'subtotal', 'tax', 'total'] as const;
type Field = typeof fields[number];
export default function ReviewPage({ params }: {
    params: Promise<{
        archiveId: string;
        id: string;
    }>;
}) {
    const router = useRouter();
    const { locale, t } = useWords();
    const wordsRef = useRef(t);
    wordsRef.current = t;
    const localeRef = useRef(locale);
    localeRef.current = locale;
    const [ids, setIds] = useState<{
        archiveId: string;
        id: string;
    }>();
    const [saved, setSaved] = useState<ReviewState>();
    const [form, setForm] = useState<ReviewMetadata>(empty);
    const [source, setSource] = useState<string | null>(null);
    const [panel, setPanel] = useState<'preview' | 'details'>('details');
    const [error, setError] = useState('');
    const [message, setMessage] = useState('');
    const [conflict, setConflict] = useState<ReviewState>();
    const [ack, setAck] = useState<string[]>([]);
    const [busy, setBusy] = useState(false);
    const [invalid, setInvalid] = useState('');
    const conflictRef = useRef<HTMLDivElement>(null);
    const pendingRef = useRef<{
        fingerprint: string;
        key: string;
    } | null>(null);
    const savedFormRef = useRef(JSON.stringify(empty));
    useEffect(() => { void params.then(setIds); }, [params]);
    useEffect(() => {
        if (!ids)
            return;
        let active = true;
        void getDocumentReview({ path: ids, credentials: 'same-origin' }).then(({ data, error }) => {
            if (!active)
                return;
            if (data) {
                pendingRef.current = null;
                setSaved(data);
                setSource(data.draft ? data.draft.sourceRunId : data.confirmed ? data.confirmed.sourceRunId : data.sourceRunId);
                const base = data.draft?.metadata ?? data.confirmed?.metadata ?? data.candidates;
                const initial = { ...empty, ...base,
                    subtotal: base.subtotal === null ? null : displayReviewDecimal(base.subtotal, localeRef.current),
                    tax: base.tax === null ? null : displayReviewDecimal(base.tax, localeRef.current),
                    total: base.total === null ? null : displayReviewDecimal(base.total, localeRef.current),
                    taxLines: (base.taxLines ?? []).map(line => ({ ...line, amount: displayReviewDecimal(line.amount, localeRef.current) })) };
                savedFormRef.current = JSON.stringify(initial);
                setForm(initial);
            }
            else if ((error as {
                status?: number;
            })?.status === 404) {
                setForm(empty);
                setSaved(undefined);
                setError(wordsRef.current.accessLost);
                setSource(null);
                setAck([]);
                pendingRef.current = null;
                router.replace('/archives?access=changed');
            }
            else
                setError(wordsRef.current.error);
        });
        return () => { active = false; };
    }, [ids, router]);
    const update = (field: Field, value: string) => { setForm(current => ({ ...current, [field]: value })); setInvalid(''); };
    const dirty = !!saved && JSON.stringify(form) !== savedFormRef.current;
    useEffect(() => {
        if (!dirty) return;
        const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ''; };
        window.addEventListener('beforeunload', warn);
        return () => window.removeEventListener('beforeunload', warn);
    }, [dirty]);
    const normalizedForWarnings = { ...form, subtotal: form.subtotal === null ? null : parseReviewDecimal(form.subtotal, locale), tax: form.tax === null ? null : parseReviewDecimal(form.tax, locale), total: form.total === null ? null : parseReviewDecimal(form.total, locale), taxLines: form.taxLines.map(line => ({ ...line, amount: parseReviewDecimal(line.amount, locale) ?? "0" })) };
    const warnings = warningCodes(normalizedForWarnings);
    const warningLabel = (code: string) => code === 'TOTAL_MISMATCH' ? t.warningTotalMismatch : code === 'TAX_LINES_MISMATCH' ? t.warningTaxMismatch : t.warningCurrencyMismatch;
    const provenanceLabel = (code: string) => code === 'extracted' ? t.provenanceExtracted : code === 'corrected' ? t.provenanceCorrected : t.provenanceManual;
    const taxSum = form.taxLines.length && form.taxLines.every(line => !line.currency || line.currency === form.currency) ? sumReviewDecimals(form.taxLines.map(line => parseReviewDecimal(line.amount, locale) ?? '')) : null;
    const accessLost = useCallback(() => { setForm(empty); setSaved(undefined); setSource(null); setAck([]); pendingRef.current = null; setConflict(undefined); router.replace('/archives?access=changed'); }, [router]);
    useEffect(() => { if (conflict)
        conflictRef.current?.focus(); }, [conflict]);
    async function submit(action: 'draft' | 'confirm') {
        if (!ids || !saved || busy)
            return;
        setError('');
        setMessage('');
        setInvalid('');
        if (action === 'confirm') {
            for (const key of ['documentType', 'supplier', 'issueDate', 'currency', 'total'] as const) {
                if (!form[key]) {
                    setInvalid(key);
                    return;
                }
            }
        }
        const next = { ...form, taxLines: form.taxLines.map(line => ({ amount: line.amount, currency: line.currency })) };
        const rawAmounts = { subtotal: form.subtotal, tax: form.tax, total: form.total, taxLines: form.taxLines.map(line => line.amount) };
        for (const key of ['subtotal', 'tax', 'total'] as const) {
            const value = form[key];
            if (value !== null) {
                const parsed = parseReviewDecimal(value, locale);
                if (parsed === undefined) {
                    setInvalid('decimal:' + key);
                    return;
                }
                ;
                next[key] = parsed;
            }
        }
        for (let i = 0; i < next.taxLines.length; i++) {
            const parsed = parseReviewDecimal(next.taxLines[i].amount, locale);
            if (parsed == null) {
                setInvalid('decimal:taxLines');
                return;
            }
            ;
            next.taxLines[i] = { amount: parsed, currency: next.taxLines[i].currency };
        }
        const fingerprint = JSON.stringify({ action, expectedVersion: saved.reviewVersion, source, metadata: next, rawAmounts, locale, ack: [...ack].sort() });
        if (pendingRef.current?.fingerprint !== fingerprint)
            pendingRef.current = { fingerprint, key: crypto.randomUUID() };
        setBusy(true);
        const { data, error, response } = await writeDocumentReview({ path: ids, body: { action, expectedVersion: saved.reviewVersion, idempotencyKey: pendingRef.current.key, sourceRunId: source, metadata: next, acknowledgedWarnings: ack, inputLocale: locale, rawAmounts }, headers: intent, credentials: 'same-origin' });
        setBusy(false);
        if (data) {
            pendingRef.current = null;
            setSaved(data);
            setConflict(undefined);
            setMessage(t.reviewSaved);
            savedFormRef.current = JSON.stringify(form);
            return;
        }
        if (response?.status === 409) {
            pendingRef.current = null;
            const latest = (error as {
                latest?: ReviewState;
            })?.latest;
            if (latest) {
                setConflict(latest);
                setSaved(latest);
                setError(t.conflict);
            }
            else
                setError(t.conflict);
            return;
        }
        if (response?.status === 404) {
            setForm(empty);
            setSaved(undefined);
            setConflict(undefined);
            setError(t.accessLost);
            setSource(null);
            setAck([]);
            pendingRef.current = null;
            router.replace('/archives?access=changed');
            return;
        }
        setError(t.error);
    }
    if (!ids)
        return <main className={styles.page}>{t.loading}</main>;
    return <main className={styles.page}>
<nav>
<Link href={`/archives/${ids.archiveId}`} onClick={event => { if (dirty && !window.confirm(t.unsavedReview)) event.preventDefault(); }}>{t.back}</Link>
</nav>
<h1>{t.reviewTitle}</h1>
 {error && <div tabIndex={-1} ref={conflictRef} role="alert" className={styles.error}>{error}</div>}{message && <p role="status">{message}</p>}
 {invalid && <p role="alert" className={styles.error}>{invalid.startsWith('decimal:') ? t.invalidDecimal : t.requiredForConfirmation}</p>}
 {saved && <>
<div className={styles.mobileTabs}>
<Button variant={panel === 'preview' ? 'primary' : 'secondary'} onPress={() => setPanel('preview')}>{t.preview}</Button>
<Button variant={panel === 'details' ? 'primary' : 'secondary'} onPress={() => setPanel('details')}>{t.details}</Button>
</div>
 <div className={styles.columns}>
<section className={`${styles.preview} ${panel === 'details' ? styles.hiddenMobile : ''}`} aria-label={t.preview}>
<PreviewPanel archiveId={ids.archiveId} id={ids.id} onAccessLost={accessLost}/>
</section>
 <section className={`${styles.details} ${panel === 'preview' ? styles.hiddenMobile : ''}`}>
<h2>{t.details}</h2>
 {!saved.sourceRunId && !saved.draft && !saved.confirmed && <p>{t.noCandidates}</p>}
 {saved.candidateWarnings.length > 0 && <ul role="status">{saved.candidateWarnings.map(code => <li key={code}>{t.candidateWarning.replace('{code}', code)}</li>)}</ul>}
 {saved.state === 'ready' && saved.draft && <p>{t.activeDraftExplanation}</p>}
 <div>
<Select label={t.documentType} selectedKey={form.documentType} onSelectionChange={key => setForm({ ...form, documentType: String(key) as ReviewMetadata['documentType'] })} options={[{ id: '', label: '—' }, { id: 'invoice', label: t.invoice }, { id: 'bill', label: t.bill }, { id: 'credit_note', label: t.creditNote }]}/>{invalid === 'documentType' && <small role="alert">{t.requiredForConfirmation}</small>}</div>
 {fields.map(field => <div key={field}>
<TextField label={field === 'supplier' ? t.supplier : field === 'invoiceId' ? t.invoiceId : field === 'accountReference' ? t.accountReference : field === 'issueDate' ? t.issueDate : field === 'currency' ? t.currency : field === 'subtotal' ? t.subtotal : field === 'tax' ? t.tax : t.total} value={form[field] ?? ''} onChange={value => update(field, value)} error={invalid === field ? t.requiredForConfirmation : invalid === 'decimal:' + field ? t.invalidDecimal : undefined} inputProps={{ dir: field === 'invoiceId' || field === 'accountReference' || field === 'currency' || field === 'issueDate' || field === 'subtotal' || field === 'tax' || field === 'total' ? 'ltr' : 'auto', type: field === 'issueDate' ? 'text' : 'text', placeholder: field === 'issueDate' ? 'YYYY-MM-DD' : undefined }}/>{(field === 'subtotal' || field === 'tax' || field === 'total') && parseReviewDecimal(String(form[field] ?? ''), locale) != null && <small>{t.willSaveAs}: <bdi dir="ltr">{parseReviewDecimal(String(form[field]), locale)} {form.currency}</bdi></small>}{saved.candidates[field] != null && saved.candidates[field] !== '' && <small>{t.extractedCandidate}: <bdi dir="auto">{String(saved.candidates[field])}</bdi></small>}{(saved.draft ?? saved.confirmed)?.provenance[field] && <small> · {t.provenance}: {provenanceLabel((saved.draft ?? saved.confirmed)!.provenance[field])}</small>}</div>)}
 <fieldset>
<legend>{t.taxLines}</legend>{form.taxLines.map((line, i) => <div className={styles.taxLine} key={i}>
<TextField label={`${t.tax} ${i + 1}`} value={line.amount} onChange={value => setForm(current => ({ ...current, taxLines: current.taxLines.map((entry, j) => j === i ? { ...entry, amount: value } : entry) }))} error={invalid === 'decimal:taxLines' ? t.invalidDecimal : undefined} inputProps={{ dir: 'ltr' }}/>
<TextField label={t.currency} value={line.currency} onChange={value => setForm(current => ({ ...current, taxLines: current.taxLines.map((entry, j) => j === i ? { ...entry, currency: value } : entry) }))} inputProps={{ dir: 'ltr' }}/>
<Button variant="quiet" onPress={() => setForm(current => ({ ...current, taxLines: current.taxLines.filter((_, j) => j !== i) }))}>{t.remove}</Button>
</div>)}<Button variant="secondary" onPress={() => setForm(current => ({ ...current, taxLines: [...current.taxLines, { amount: '', currency: current.currency }] }))}>{t.addTaxLine}</Button>
</fieldset>
 {taxSum !== null && <p>{t.taxLineSum.replace('{amount}', displayReviewDecimal(taxSum, locale))}</p>}
 <div aria-live="polite">{warnings.map(code => <Checkbox className={styles.warning} key={code} isSelected={ack.includes(code)} onChange={selected => setAck(current => selected ? [...current, code] : current.filter(item => item !== code))}>{warningLabel(code)} · {t.warningAck}</Checkbox>)}</div>
 <div className={styles.actions}>
<Button isDisabled={busy} onPress={() => void submit('draft')}>{t.saveDraft}</Button>
<Button isDisabled={busy || warnings.some(code => !ack.includes(code))} onPress={() => void submit('confirm')}>{t.confirmReview}</Button>
</div>
 {conflict && <aside>
<h3>{t.latestSaved}</h3>
<dl>{fields.map(field => <div key={field}>
<dt>{field}</dt>
<dd>
<bdi dir="auto">{String((conflict.draft ?? conflict.confirmed)?.metadata[field] ?? '—')}</bdi>
</dd>
</div>)}</dl>
</aside>}
 <section>
<h3>{t.history}</h3>
<ol>{saved.history.map(rev => <li key={rev.id}>
<strong>{rev.kind === 'draft' ? t.draft : t.confirmed}</strong> · <time dateTime={rev.createdAt}>{rev.createdAt}</time> · <bdi dir="ltr">{rev.metadata.invoiceId || '—'}</bdi>
</li>)}</ol>
</section>
 </section>
</div>
</>}
 </main>;
}
