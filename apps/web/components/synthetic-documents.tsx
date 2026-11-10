'use client';
import { useState } from 'react';
import { ConfirmDialog, DateField } from '@invoyis/ui';
import { useWords } from '../lib/catalog';
import { formatDateOnly, formatExactMoney } from '../lib/document-format.mjs';
import styles from '../app/archives/archive.module.css';
const rows = [
  { supplier:'חשמל Alpha (2026)', id:'000042-A/26', date:'2026-01-01', amount:'9007199254740993.01', currency:'EUR' },
  { supplier:'Boulangerie Pilote', id:'000007-B/26', date:'2026-03-15', amount:'0.00', currency:'ILS' },
  { supplier:'Alpha חשמל', id:'INV-26/0003', date:'2026-11-10', amount:'125.40', currency:'ILS' },
] as const;
export function SyntheticDocuments() {
  const { locale, t } = useWords();
  const [demoDate, setDemoDate] = useState('2026-01-01');
  return <section className={styles.card} aria-label={t.sampleTitle}>
    <div className={styles.top}><div><h2>{t.sampleTitle}</h2><p className={styles.muted}>{t.sampleNote}</p></div>
      <ConfirmDialog trigger={t.openDateDemo} title={t.dateDemo} closeLabel={t.cancel}>
        <DateField label={t.issueDate} value={demoDate} onChange={setDemoDate} description={t.dateHelp} />
        <p><bdi dir="ltr">{demoDate}</bdi></p>
      </ConfirmDialog></div>
    <div className={styles.tableScroll}><table className={styles.table}><caption className={styles.srOnly}>{t.sampleTitle}</caption><thead><tr><th scope="col">{t.supplier}</th><th scope="col">{t.invoiceId}</th><th scope="col">{t.issueDate}</th><th scope="col">{t.total}</th></tr></thead><tbody>{rows.map(row => <tr key={row.id}>
      <td><bdi className="bidi-value" dir="ltr">{row.supplier}</bdi></td>
      <td><bdi className="bidi-value" dir="ltr" title={row.id}>{row.id}</bdi></td>
      <td><time dateTime={row.date} title={row.date}><bdi dir="ltr">{formatDateOnly(row.date, locale)}</bdi></time></td>
      <td><bdi dir="ltr" title={`${row.amount} ${row.currency}`}>{formatExactMoney(row.amount, row.currency, locale)}</bdi></td>
    </tr>)}</tbody></table></div>
  </section>;
}
