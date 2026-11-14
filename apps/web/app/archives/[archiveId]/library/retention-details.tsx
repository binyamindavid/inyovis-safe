'use client';
import { Button, TextField } from '@invoyis/ui';
import { useCallback, useEffect, useState } from 'react';

import styles from './library.module.css';

type Obligation = { ruleId: string; ruleVersion: number; source: string; trigger: string; inputs: Record<string, unknown>; keepThrough?: string; pending?: string };
type Snapshot = { id: string; ruleSetVersion: string; inputs: { facts?: Record<string, unknown> }; outcome: { status: string; keepThrough?: string; lowerBound?: string; reasons?: string[]; obligations: Obligation[] }; };
type Hold = { id: string; reason: string; placedBy: number; placedAt: string; releasedBy?: number; releasedAt?: string; releaseReason?: string };
type Details = { archiveKind: 'personal'|'business'; current: { state: string; staleReasons?: string[]; snapshot?: Snapshot }; history: Snapshot[]; holds: Hold[]; reminder?: string; reviewBlocker: string; canManage: boolean; canEdit: boolean };
const labels = {
 en: { title:'Retention', reminder:'Reminder', save:'Save reminder', clear:'Clear reminder', reassess:'Reassess', hold:'Place hold', reason:'Reason for hold', releaseReason:'Reason for release', release:'Release', history:'Decision history', keep:'Keep through', lower:'Keep at least through', pending:'Pending rule validation', noRule:'No validated rule for this document', factsNeeded:'More facts needed', stale:'Reassessment needed', ready:'Ready for shredding review', review:'Review for shredding', unavailable:'No decision yet', blocker: { reviewer_permission_required:'Only an archive admin can review.', cleared_confirmed_document_required:'A cleared, confirmed document is required.', retention_missing:'A retention decision is needed.', retention_stale:'Reassessment is needed.', retention_unresolved:'Obligations or rule validation remain unresolved.', active_hold:'An active hold blocks review.', keep_through_date:'Review starts the day after the keep-through date.', business_document_required:'Only Business documents have statutory retention.' } },
 fr: { title:'Conservation', reminder:'Rappel', save:'Enregistrer le rappel', clear:'Effacer le rappel', reassess:'Réévaluer', hold:'Placer une suspension', reason:'Motif de la suspension', releaseReason:'Motif de la levée', release:'Lever', history:'Historique des décisions', keep:"Conserver jusqu’au", lower:'Conserver au moins jusqu’au', pending:'Validation de la règle en attente', noRule:'Aucune règle validée pour ce document', factsNeeded:'Faits supplémentaires requis', stale:'Réévaluation nécessaire', ready:'Prêt pour examen de destruction', review:'Examiner pour destruction', unavailable:'Aucune décision', blocker: { reviewer_permission_required:'Seul un administrateur peut examiner.', cleared_confirmed_document_required:'Un document confirmé et autorisé est requis.', retention_missing:'Une décision de conservation est requise.', retention_stale:'Une réévaluation est nécessaire.', retention_unresolved:'Des obligations ou règles restent à résoudre.', active_hold:'Une suspension active bloque l’examen.', keep_through_date:'L’examen commence le lendemain de la date de conservation.', business_document_required:'Réservé aux documents professionnels.' } },
 he: { title:'שמירה', reminder:'תזכורת', save:'שמירת תזכורת', clear:'מחיקת תזכורת', reassess:'הערכה מחדש', hold:'הוספת עיכוב', reason:'סיבת העיכוב', releaseReason:'סיבת השחרור', release:'שחרור', history:'היסטוריית החלטות', keep:'לשמור עד', lower:'לשמור לפחות עד', pending:'ממתין לאימות כלל', noRule:'אין כלל מאומת למסמך זה', factsNeeded:'נדרשות עובדות נוספות', stale:'נדרשת הערכה מחדש', ready:'מוכן לבדיקת גריסה', review:'בדיקה לקראת גריסה', unavailable:'אין החלטה עדיין', blocker: { reviewer_permission_required:'רק מנהל ארכיון יכול לבדוק.', cleared_confirmed_document_required:'נדרש מסמך מאושר ומאומת.', retention_missing:'נדרשת החלטת שמירה.', retention_stale:'נדרשת הערכה מחדש.', retention_unresolved:'יש חובות או כללים שטרם הוכרעו.', active_hold:'עיכוב פעיל מונע בדיקה.', keep_through_date:'הבדיקה מתחילה ביום שלאחר תאריך השמירה.', business_document_required:'שמירה חוקית חלה על מסמכים עסקיים בלבד.' } }
} as const;
const reasonCopy: Record<string,Record<string,string>>={
 en:{new_confirmed_revision:'New confirmed revision',published_rule_version:'Published rule version',corrected_filing_date:'Corrected filing date',corrected_facts_or_events:'Corrected facts or events',coverage_missing:'No validated rule scope',coverage_gap:'Rule coverage gap',coverage_facts:'Coverage facts needed',filing_date:'Filing date needed',financial_year_end:'Financial year end needed',issue_date:'Issue date needed',document_type:'Document type needed',entity:'Entity needed',tax_year:'Tax year needed',jurisdiction:'Archive country needed'},
 fr:{new_confirmed_revision:'Nouvelle version confirmée',published_rule_version:'Nouvelle version de règle',corrected_filing_date:'Date de dépôt corrigée',corrected_facts_or_events:'Faits ou événements corrigés',coverage_missing:'Aucune règle validée',coverage_gap:'Couverture des règles incomplète',coverage_facts:'Faits de couverture requis',filing_date:'Date de dépôt requise',financial_year_end:'Fin d’exercice requise',issue_date:'Date du document requise',document_type:'Type de document requis',entity:'Entité requise',tax_year:'Année fiscale requise',jurisdiction:'Pays de l’archive requis'},
 he:{new_confirmed_revision:'גרסה מאושרת חדשה',published_rule_version:'פורסמה גרסת כלל חדשה',corrected_filing_date:'תאריך ההגשה תוקן',corrected_facts_or_events:'עובדות או אירועים תוקנו',coverage_missing:'אין כלל מאומת לתחום',coverage_gap:'כיסוי הכללים חסר',coverage_facts:'נדרשות עובדות לכיסוי',filing_date:'נדרש תאריך הגשה',financial_year_end:'נדרש סוף שנת כספים',issue_date:'נדרש תאריך מסמך',document_type:'נדרש סוג מסמך',entity:'נדרשת ישות',tax_year:'נדרשת שנת מס',jurisdiction:'נדרשת מדינת ארכיון'}
};
function reasonLabel(locale:string,reason:string){return reasonCopy[locale]?.[reason]??reasonCopy.en[reason]??reason.replaceAll('_',' ')}
export function RetentionDetails({ archiveId, documentId, locale }: { archiveId:string; documentId:string; locale:string }) {
 const l=labels[locale as keyof typeof labels] ?? labels.en;
 const [busy, setBusy] = useState(false);
 const [data,setData]=useState<Details|null>(null);const [error,setError]=useState('');const [reason,setReason]=useState('');const [date,setDate]=useState('');const [releaseReasons,setReleaseReasons]=useState<Record<string,string>>({});
 const base=`/api/v1/archives/${archiveId}/documents/${documentId}`;
 const load=useCallback(async()=>{try{const r=await fetch(`${base}/retention`,{cache:'no-store'});if(!r.ok)throw Error(String(r.status));const d=await r.json() as Details;setData(d);setDate(d.reminder??'');setError('')}catch{setError('Could not load retention details.')}},[base]);
 useEffect(()=>{void load()},[load]);
 async function act(path: string, method: string, body?: object) {
   setBusy(true);
   setError('');
   try {
     const response = await fetch(`${base}/${path}`, {
       method,
       headers: { 'X-Invoyis-Intent': 'browser', ...(body ? { 'Content-Type': 'application/json' } : {}) },
       body: body ? JSON.stringify(body) : undefined,
     });
     if (!response.ok) throw new Error(String(response.status));
     await load();
     return true;
   } catch {
     setError(locale === 'fr' ? 'Impossible d’enregistrer la modification.' : locale === 'he' ? 'לא ניתן לשמור את השינוי.' : 'Could not save the change.');
     return false;
   } finally {
     setBusy(false);
   }
 }
 if (!data) return <section className={styles.retentionPanel} aria-label={l.title}>{error || '…'}</section>;

 const current = data.current.snapshot;
 const personal = data.archiveKind === 'personal';
 const reasons = [...(data.current.staleReasons ?? []), ...(current?.outcome.reasons ?? [])];
 const blocker = (l.blocker as Record<string, string>)[data.reviewBlocker] ?? data.reviewBlocker;
 let status: string = l.unavailable;
 if (data.current.state === 'stale') {
   status = l.stale;
 } else if (current?.outcome.status === 'pending') {
   if (reasons.includes('coverage_missing')) status = l.noRule;
   else if (reasons.some(reason => reason.startsWith('coverage_'))) status = l.pending;
   else status = l.factsNeeded;
 } else if (data.reviewBlocker === '') {
   status = l.ready;
 } else if (current) {
   status = current.outcome.keepThrough ? l.keep : blocker || l.unavailable;
 }
 const showBlocker = blocker && blocker !== status && (!reasons.length || !['retention_unresolved', 'retention_stale'].includes(data.reviewBlocker));
 return <section className={styles.retentionPanel} aria-label={l.title} aria-busy={busy}>
   <h3 className={styles.previewTitle}>{l.title}</h3>
   {error && <p role="alert">{error}</p>}
   <div className={styles.retentionSummary} role="status">
     <p>{status}{status === l.keep && current?.outcome.keepThrough && <> · <bdi dir="ltr">{current.outcome.keepThrough}</bdi></>}</p>
     {reasons.filter(reason => reason !== 'coverage_missing' || status !== l.noRule).map(reason =>
       <p className={styles.muted} key={reason}>{reasonLabel(locale, reason)}</p>
     )}
     {showBlocker && <p className={styles.muted}>{blocker}</p>}
   </div>
   {current && <>
     {current.outcome.keepThrough && status !== l.keep && <p>{l.keep} · <bdi dir="ltr">{current.outcome.keepThrough}</bdi></p>}
     {!current.outcome.keepThrough && current.outcome.lowerBound && <p>{l.lower} · <bdi dir="ltr">{current.outcome.lowerBound}</bdi></p>}
     {current.outcome.obligations.map((o, i) => <div key={`${o.ruleId}-${i}`}>
       <bdi dir="auto">{o.source}</bdi> · <bdi dir="ltr">{o.ruleId} v{o.ruleVersion}</bdi> · {o.trigger}
       <ul>{Object.entries(o.inputs).map(([key, value]) => <li key={key}>{key}: <bdi dir="auto">{String(value)}</bdi></li>)}</ul>
       {o.keepThrough ? <>{l.keep} · <bdi dir="ltr">{o.keepThrough}</bdi></> : o.pending && reasonLabel(locale, o.pending)}
     </div>)}
   </>}
   <div className={styles.retentionActions}>
     {data.reviewBlocker === '' && <a className="ui-button ui-button-primary" href={`/archives/${archiveId}/documents/${documentId}/shredding-review`}>{l.review}</a>}
     {data.canManage && <Button variant="secondary" isDisabled={busy} onPress={() => void act('retention/reassess', 'POST')}>{l.reassess}</Button>}
   </div>
   {data.canManage && <details className={styles.retentionDisclosure}>
     <summary>{l.hold}</summary>
     <div className={styles.retentionForm}>
       <TextField label={l.reason} value={reason} onChange={setReason} isDisabled={busy} inputProps={{ maxLength: 1000 }} />
       <div className={styles.retentionActions}>
         <Button variant="secondary" isDisabled={busy || !reason.trim()} onPress={async () => {
           if (await act('holds', 'POST', { reason })) setReason('');
         }}>{l.hold}</Button>
       </div>
     </div>
   </details>}
   {data.holds.map(h => <div className={styles.retentionForm} key={h.id}>
     <div><bdi dir="auto">{h.reason}</bdi> · <bdi dir="ltr">{h.placedAt.slice(0, 10)} · {h.placedBy}</bdi></div>
     {h.releasedAt ? <div className={styles.muted}><bdi dir="ltr">{h.releasedAt.slice(0, 10)} · {h.releasedBy}</bdi> · <bdi dir="auto">{h.releaseReason}</bdi></div> : data.canManage && <>
       <TextField label={l.releaseReason} value={releaseReasons[h.id] ?? ''} onChange={value => setReleaseReasons(previous => ({ ...previous, [h.id]: value }))} isDisabled={busy} inputProps={{ maxLength: 1000 }} />
       <div className={styles.retentionActions}>
         <Button variant="secondary" isDisabled={busy || !releaseReasons[h.id]?.trim()} onPress={() => void act(`holds/${h.id}/release`, 'POST', { reason: releaseReasons[h.id] })}>{l.release}</Button>
       </div>
     </>}
   </div>)}
   {data.history.length>0&&<details className={styles.retentionDisclosure}><summary>{l.history} ({data.history.length})</summary>{data.history.map(h=><div key={h.id}><bdi dir="ltr">{h.id}</bdi> · {h.ruleSetVersion} · {h.outcome.status} {h.outcome.keepThrough||h.outcome.lowerBound}</div>)}</details>}
   {personal && (data.canEdit || data.reminder) && <details className={styles.retentionDisclosure}>
     <summary>{l.reminder}{data.reminder && <> · <bdi dir="ltr">{data.reminder}</bdi></>}</summary>
     {data.canEdit && <div className={styles.retentionForm}>
       <TextField label={l.reminder} value={date} onChange={setDate} isDisabled={busy} inputProps={{ type: 'date', lang: locale, dir: 'ltr' }} />
       <div className={styles.retentionActions}>
         <Button variant="secondary" isDisabled={busy || !date || date === data.reminder} onPress={() => void act('reminder', 'PUT', { date })}>{l.save}</Button>
         {data.reminder && <Button variant="quiet" isDisabled={busy} onPress={() => void act('reminder', 'PUT', { date: null })}>{l.clear}</Button>}
       </div>
     </div>}
   </details>}
 </section>;
}
