'use client';
import { changeArchiveMember, createArchiveInvitation, getAccountLanguage, listArchiveInvitations, listArchiveMembers, removeArchiveMember, revokeArchiveInvitation, type Member, type Invitation, type ArchiveRole } from '@invoyis/contracts';
import Link from 'next/link';
import { useEffect, useState, type FormEvent } from 'react';
import { intent, locale as browserLocale, words, type Locale } from '../../../../lib/archive-ui';
import styles from '../../archive.module.css';

export default function MembersPage({ params }: { params: Promise<{ archiveId: string }> }) {
  const [id, setId] = useState(''); const [language, setLanguage] = useState<Locale>('en');
  const [members, setMembers] = useState<Member[]>([]); const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [email, setEmail] = useState(''); const [role, setRole] = useState<ArchiveRole>('viewer');
  const [error, setError] = useState(''); const [notice, setNotice] = useState(''); const [busy, setBusy] = useState(false);
  const t = words[language];
  useEffect(() => { params.then(({ archiveId }) => setId(archiveId)); setLanguage(browserLocale()); getAccountLanguage({ credentials: 'same-origin' }).then(({ data }) => { if (data) setLanguage(data.language); }); }, [params]);
  async function refresh() {
    if (!id) return;
    const [m, i] = await Promise.all([listArchiveMembers({ path: { archiveId: id }, credentials: 'same-origin' }), listArchiveInvitations({ path: { archiveId: id }, credentials: 'same-origin' })]);
    if (m.data) setMembers(m.data.items); else setError(t.error);
    if (i.data) setInvitations(i.data.items);
  }
  useEffect(() => { refresh(); }, [id]);
  async function invite(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('');
    const { error } = await createArchiveInvitation({ path: { archiveId: id }, body: { email, role }, headers: { ...intent, 'Accept-Language': language }, credentials: 'same-origin' });
    setBusy(false); if (error) { setError(t.error); return; } setEmail(''); setNotice(t.invited); await refresh();
  }
  async function change(member: Member, nextRole: ArchiveRole, grant: boolean) {
    setError(''); const { error } = await changeArchiveMember({ path: { archiveId: id, accountId: member.accountId }, body: { role: nextRole, canBulkExport: grant }, headers: intent, credentials: 'same-origin' });
    if (error) setError(t.error); await refresh();
  }
  async function remove(member: Member) {
    const { error } = await removeArchiveMember({ path: { archiveId: id, accountId: member.accountId }, headers: intent, credentials: 'same-origin' });
    if (error) setError(t.error); await refresh();
  }
  async function revoke(invitation: Invitation) {
    const { error } = await revokeArchiveInvitation({ path: { archiveId: id, invitationId: invitation.id }, headers: intent, credentials: 'same-origin' });
    if (error) setError(t.error); await refresh();
  }
  return <main className={styles.page} lang={language} dir={language === 'he' ? 'rtl' : 'ltr'}><div className={styles.wrap}>
    <Link href={`/archives/${id}`}>{t.back}</Link><h1>{t.members}</h1>
    <section className={styles.card}><h2>{t.invite}</h2><form className={styles.form} onSubmit={invite}>
      <label className={styles.field}>{t.email}<input dir="ltr" type="email" required value={email} onChange={e => setEmail(e.target.value)}/></label>
      <label className={styles.field}>{t.role}<select value={role} onChange={e => setRole(e.target.value as ArchiveRole)}><option value="viewer">{t.viewer}</option><option value="contributor">{t.contributor}</option><option value="admin">{t.admin}</option></select></label>
      <button className={styles.button} disabled={busy}>{t.invite}</button>
    </form></section>
    <section className={styles.card}><h2>{t.members}</h2><ul className={styles.list}>{members.map(m => <li key={m.accountId} className={styles.row}>
      <bdi dir="ltr">{m.email}</bdi><select aria-label={`${t.role}: ${m.email}`} value={m.role} onChange={e => change(m, e.target.value as ArchiveRole, m.canBulkExport)}><option value="admin">{t.admin}</option><option value="contributor">{t.contributor}</option><option value="viewer">{t.viewer}</option></select>
      <label><input type="checkbox" checked={m.canBulkExport} onChange={e => change(m, m.role, e.target.checked)}/>{t.export}</label>
      <button type="button" onClick={() => remove(m)}>{t.remove}</button>
    </li>)}</ul></section>
    <section className={styles.card}><h2>{t.invite}</h2><ul className={styles.list}>{invitations.map(i => <li key={i.id} className={styles.row}><bdi dir="ltr">{i.email}</bdi> · {t[i.role]} · {i.status} {i.status === 'active' && <button onClick={() => revoke(i)}>{t.revoke}</button>}</li>)}</ul></section>
    {notice && <p role="status">{notice}</p>}{error && <p className={styles.error} role="alert">{error}</p>}
  </div></main>;
}
