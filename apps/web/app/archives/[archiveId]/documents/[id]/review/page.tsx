'use client';
import { getDocumentReview, writeDocumentReview, type ReviewMetadata, type ReviewState } from '@invoyis/contracts';
import { Button, Checkbox, Select, TextField } from '@invoyis/ui';
import Link from 'next/link';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useWords } from '../../../../../../lib/catalog';
import { intent } from '../../../../../../lib/archive-ui';
import { displayReviewDecimal, parseReviewDecimal, warningCodes, sumReviewDecimals, applicableAcknowledgements } from '../../../../../../lib/review-decimal.mjs';
import styles from './review.module.css';
import { PreviewPanel } from './preview-panel';
import { candidateWarningsFor, confirmRequirements, discardedByTypeSwitch, fieldsFor, groupOf, metadataForType, switchType } from '../../../../../../lib/review-groups.mjs';
type Form = Omit<ReviewMetadata, 'supplier' | 'invoiceId' | 'accountReference' | 'currency' | 'subtotal' | 'tax' | 'total' | 'taxLines' | 'title' | 'sender' | 'reference' | 'notes'> & { supplier: string; invoiceId: string; accountReference: string; currency: string; subtotal: string | null; tax: string | null; total: string | null; taxLines: { amount: string; currency: string; provenance?: string }[]; title: string; sender: string; reference: string; notes: string };
const empty: Form = { documentType: '', supplier: '', invoiceId: '', accountReference: '', issueDate: '', currency: '', subtotal: null, tax: null, total: null, taxLines: [], title: '', sender: '', reference: '', notes: '' };
type Field = 'supplier' | 'invoiceId' | 'accountReference' | 'issueDate' | 'currency' | 'subtotal' | 'tax' | 'total';
type DocumentField = 'title' | 'sender' | 'issueDate' | 'reference' | 'notes';
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
    const [form, setForm] = useState<Form>(empty);
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
                const initial: Form = { ...empty, ...base,
                    subtotal: base.subtotal == null ? null : displayReviewDecimal(base.subtotal, localeRef.current),
                    tax: base.tax == null ? null : displayReviewDecimal(base.tax, localeRef.current),
                    total: base.total == null ? null : displayReviewDecimal(base.total, localeRef.current),
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
    const update = (field: Field | DocumentField, value: string) => { setForm(current => ({ ...current, [field]: value })); if (['subtotal', 'tax', 'total', 'currency'].includes(field)) setAck([]); setInvalid(''); };
    const dirty = !!saved && JSON.stringify(form) !== savedFormRef.current;
    const group = groupOf(form.documentType);
    const isDocument = group === 'document';
    const shownCandidateWarnings = saved ? candidateWarningsFor(form.documentType, saved.candidateWarnings) as string[] : [];
    const visibleFields = fieldsFor(form.documentType) as readonly (Field | DocumentField)[];
    const fieldLabel = (field: string) => ({ supplier: t.supplier, invoiceId: t.invoiceId, accountReference: t.accountReference, issueDate: isDocument ? t.docDate : t.issueDate, currency: t.currency, subtotal: t.subtotal, tax: t.tax, total: t.total, taxLines: t.taxLines, title: t.docTitle, sender: t.docSender, reference: t.docReference, notes: t.docNotes } as Record<string, string>)[field] ?? field;
    const typeLabel = (type: string) => ({ invoice: t.invoice, bill: t.bill, credit_note: t.creditNote, lease: t.typeLease, court_correspondence: t.typeCourtCorrespondence, other: t.typeOther } as Record<string, string>)[type] ?? '—';
    const changeType = (key: string) => {
        const nextType = key as ReviewMetadata['documentType'];
        const savedForm = JSON.parse(savedFormRef.current) as Partial<Form>;
        const lost = discardedByTypeSwitch(form, savedForm, nextType) as string[];
        if (lost.length && !window.confirm(t.switchTypeDiscard.replace('{fields}', lost.map(fieldLabel).join(', '))))
            return;
        setForm(switchType(form, nextType) as Form);
        setAck([]);
        setInvalid('');
    };
    useEffect(() => {
        if (!dirty) return;
        const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ''; };
        window.addEventListener('beforeunload', warn);
        return () => window.removeEventListener('beforeunload', warn);
    }, [dirty]);
    const normalizedForWarnings = { ...form, subtotal: form.subtotal === null ? null : parseReviewDecimal(form.subtotal, locale), tax: form.tax === null ? null : parseReviewDecimal(form.tax, locale), total: form.total === null ? null : parseReviewDecimal(form.total, locale), taxLines: form.taxLines.map(line => ({ ...line, amount: parseReviewDecimal(line.amount, locale) ?? "0" })) };
    const warnings: string[] = isDocument ? [] : warningCodes(normalizedForWarnings);
    const applicableAck = applicableAcknowledgements(ack, warnings);
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
            for (const key of confirmRequirements(form.documentType) as (keyof Form)[]) {
                if (!form[key]) {
                    setInvalid(key);
                    return;
                }
            }
        }
        // A non-invoice submission omits every invoice field; the API rejects conflicting values.
        const documentMetadata = metadataForType(form) as ReviewMetadata | null;
        const next: ReviewMetadata = documentMetadata ?? { documentType: form.documentType, supplier: form.supplier, invoiceId: form.invoiceId, accountReference: form.accountReference, issueDate: form.issueDate, currency: form.currency, subtotal: form.subtotal, tax: form.tax, total: form.total, taxLines: form.taxLines.map(line => ({ amount: line.amount, currency: line.currency })) };
        const rawAmounts = documentMetadata ? { subtotal: null, tax: null, total: null, taxLines: [] } : { subtotal: form.subtotal, tax: form.tax, total: form.total, taxLines: form.taxLines.map(line => line.amount) };
        for (const key of documentMetadata ? [] as const : ['subtotal', 'tax', 'total'] as const) {
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
        for (let i = 0; i < (next.taxLines?.length ?? 0); i++) {
            const parsed = parseReviewDecimal(next.taxLines![i].amount, locale);
            if (parsed == null) {
                setInvalid('decimal:taxLines');
                return;
            }
            ;
            next.taxLines![i] = { amount: parsed, currency: next.taxLines![i].currency };
        }
        const fingerprint = JSON.stringify({ action, expectedVersion: saved.reviewVersion, source, metadata: next, rawAmounts, locale, ack: [...applicableAck].sort() });
        if (pendingRef.current?.fingerprint !== fingerprint)
            pendingRef.current = { fingerprint, key: crypto.randomUUID() };
        setBusy(true);
        const { data, error, response } = await writeDocumentReview({ path: ids, body: { action, expectedVersion: saved.reviewVersion, idempotencyKey: pendingRef.current.key, sourceRunId: source, metadata: next, acknowledgedWarnings: applicableAck, inputLocale: locale, rawAmounts }, headers: intent, credentials: 'same-origin' });
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
{saved && <p><a href={`/api/v1/archives/${ids.archiveId}/documents/${ids.id}/original`}>{t.downloadOriginal}</a></p>}
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
 {shownCandidateWarnings.length > 0 && <ul role="status">{shownCandidateWarnings.map(code => <li key={code}>{t.candidateWarning.replace('{code}', code)}</li>)}</ul>}
 {saved.state === 'ready' && saved.draft && <p>{t.activeDraftExplanation}</p>}
 <div>
<Select label={t.documentType} selectedKey={form.documentType} onSelectionChange={key => changeType(String(key))} options={[{ id: '', label: '—' }, { id: 'invoice', label: t.invoice }, { id: 'bill', label: t.bill }, { id: 'credit_note', label: t.creditNote }, { id: 'lease', label: t.typeLease }, { id: 'court_correspondence', label: t.typeCourtCorrespondence }, { id: 'other', label: t.typeOther }]}/>{invalid === 'documentType' && <small role="alert">{t.requiredForConfirmation}</small>}</div>
 {isDocument && <p>{t.nonInvoiceHelp}</p>}
 {visibleFields.map(field => <div key={field}>
<TextField label={isDocument && field !== 'title' ? `${fieldLabel(field)} (${t.optionalField})` : fieldLabel(field)} description={isDocument ? ({ sender: t.docSenderHint, reference: t.docReferenceHint, notes: t.docNotesHint } as Record<string, string>)[field] : undefined} value={(form as unknown as Record<string, string | null>)[field] ?? ''} onChange={value => update(field, value)} error={invalid === field ? t.requiredForConfirmation : invalid === 'decimal:' + field ? t.invalidDecimal : undefined} inputProps={{ dir: field === 'invoiceId' || field === 'accountReference' || field === 'currency' || field === 'issueDate' || field === 'subtotal' || field === 'tax' || field === 'total' || field === 'reference' ? 'ltr' : 'auto', type: 'text', placeholder: field === 'issueDate' ? 'YYYY-MM-DD' : undefined }}/>{!isDocument && (field === 'subtotal' || field === 'tax' || field === 'total') && parseReviewDecimal(String((form as unknown as Record<string, string | null>)[field] ?? ''), locale) != null && <small>{t.willSaveAs}: <bdi dir="ltr">{parseReviewDecimal(String((form as unknown as Record<string, string | null>)[field]), locale)} {form.currency}</bdi></small>}{!isDocument && (saved.candidates as Record<string, unknown>)[field] != null && (saved.candidates as Record<string, unknown>)[field] !== '' && <small>{t.extractedCandidate}: <bdi dir="auto">{String((saved.candidates as Record<string, unknown>)[field])}</bdi></small>}{(saved.draft ?? saved.confirmed)?.provenance[field] && <small> · {t.provenance}: {provenanceLabel((saved.draft ?? saved.confirmed)!.provenance[field])}</small>}</div>)}
 {!isDocument && <fieldset>
<legend>{t.taxLines}</legend>{form.taxLines.map((line, i) => <div className={styles.taxLine} key={i}>
<TextField label={`${t.tax} ${i + 1}`} value={line.amount} onChange={value => { setAck([]); setForm(current => ({ ...current, taxLines: current.taxLines.map((entry, j) => j === i ? { ...entry, amount: value } : entry) })); }} error={invalid === 'decimal:taxLines' ? t.invalidDecimal : undefined} inputProps={{ dir: 'ltr' }}/>
<TextField label={t.currency} value={line.currency} onChange={value => { setAck([]); setForm(current => ({ ...current, taxLines: current.taxLines.map((entry, j) => j === i ? { ...entry, currency: value } : entry) })); }} inputProps={{ dir: 'ltr' }}/>
<Button variant="quiet" onPress={() => { setAck([]); setForm(current => ({ ...current, taxLines: current.taxLines.filter((_, j) => j !== i) })); }}>{t.remove}</Button>
</div>)}<Button variant="secondary" onPress={() => { setAck([]); setForm(current => ({ ...current, taxLines: [...current.taxLines, { amount: '', currency: current.currency }] })); }}>{t.addTaxLine}</Button>
</fieldset>}
 {!isDocument && taxSum !== null && <p>{t.taxLineSum.replace('{amount}', displayReviewDecimal(taxSum, locale))}</p>}
 <div aria-live="polite">{warnings.map(code => <Checkbox className={styles.warning} key={code} isSelected={applicableAck.includes(code)} onChange={selected => setAck(current => selected ? [...current, code] : current.filter(item => item !== code))}>{warningLabel(code)} · {t.warningAck}</Checkbox>)}</div>
 <div className={styles.actions}>
<Button isDisabled={busy} onPress={() => void submit('draft')}>{t.saveDraft}</Button>
<Button isDisabled={busy || warnings.some(code => !applicableAck.includes(code))} onPress={() => void submit('confirm')}>{t.confirmReview}</Button>
</div>
 {conflict && <aside>
<h3>{t.latestSaved}</h3>
<dl>{(fieldsFor((conflict.draft ?? conflict.confirmed)?.metadata.documentType ?? '') as readonly string[]).map(field => <div key={field}>
<dt>{fieldLabel(field)}</dt>
<dd>
<bdi dir="auto">{String((((conflict.draft ?? conflict.confirmed)?.metadata ?? {}) as Record<string, unknown>)[field] ?? '—')}</bdi>
</dd>
</div>)}</dl>
</aside>}
 <section>
<h3>{t.history}</h3>
<ol>{saved.history.map(rev => <li key={rev.id}>
<strong>{rev.kind === 'draft' ? t.draft : t.confirmed}</strong> · <time dateTime={rev.createdAt}>{rev.createdAt}</time> · {typeLabel(rev.metadata.documentType)} · {rev.metadata.title ? <bdi dir="auto">{rev.metadata.title}</bdi> : <bdi dir="ltr">{rev.metadata.invoiceId || '—'}</bdi>}
</li>)}</ol>
</section>
 </section>
</div>
</>}
 </main>;
}
