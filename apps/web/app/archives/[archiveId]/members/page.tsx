'use client';
import { changeArchiveMember, createArchiveInvitation, listArchiveInvitations, listArchiveMembers, removeArchiveMember, revokeArchiveInvitation, type Member, type Invitation, type ArchiveRole } from '@invoyis/contracts';
import { Button, Checkbox, ConfirmDialog, Select, TextField } from '@invoyis/ui';
import Link from 'next/link';
import { useEffect, useState, type FormEvent } from 'react';
import { LanguageSelect } from '../../../../components/language-select';
import { useWords } from '../../../../lib/catalog';
import { intent } from '../../../../lib/archive-ui';
import styles from '../../archive.module.css';
export default function MembersPage({ params }: { params: Promise<{ archiveId:string }> }) {
  const { locale, t } = useWords();
  const [id,setId]=useState(''); const [members,setMembers]=useState<Member[]>([]); const [invitations,setInvitations]=useState<Invitation[]>([]);
  const [email,setEmail]=useState(''); const [role,setRole]=useState<ArchiveRole>('viewer');
  const [error,setError]=useState(''); const [notice,setNotice]=useState(''); const [busy,setBusy]=useState(false);
  useEffect(() => { params.then(({archiveId}) => setId(archiveId)); },[params]);
  async function refresh() { if(!id)return; const [m,i]=await Promise.all([listArchiveMembers({path:{archiveId:id},credentials:'same-origin'}),listArchiveInvitations({path:{archiveId:id},credentials:'same-origin'})]); if(m.data)setMembers(m.data.items);else setError(t.error); if(i.data)setInvitations(i.data.items); }
  useEffect(() => { void refresh(); },[id]);
  async function invite(event:FormEvent) { event.preventDefault(); setBusy(true);setError(''); const {error}=await createArchiveInvitation({path:{archiveId:id},body:{email,role},headers:{...intent,'Accept-Language':locale},credentials:'same-origin'});setBusy(false);if(error){setError(t.error);return;}setEmail('');setNotice(t.invited);await refresh(); }
  async function change(member:Member,nextRole:ArchiveRole,grant:boolean){setError('');const {error}=await changeArchiveMember({path:{archiveId:id,accountId:member.accountId},body:{role:nextRole,canBulkExport:grant},headers:intent,credentials:'same-origin'});if(error)setError(t.error);await refresh();}
  async function remove(member:Member){const {error}=await removeArchiveMember({path:{archiveId:id,accountId:member.accountId},headers:intent,credentials:'same-origin'});if(error)setError(t.error);await refresh();}
  async function revoke(invitation:Invitation){const {error}=await revokeArchiveInvitation({path:{archiveId:id,invitationId:invitation.id},headers:intent,credentials:'same-origin'});if(error)setError(t.error);await refresh();}
  const roleOptions=[{id:'viewer',label:t.viewer},{id:'contributor',label:t.contributor},{id:'admin',label:t.admin}];
  return <main className={styles.page}><div className={styles.wrap}>
    <div className={styles.top}><Link href={`/archives/${id}`}>{t.back}</Link><LanguageSelect signedIn /></div><h1>{t.members}</h1>
    <section className={styles.card}><h2>{t.invite}</h2><form className={styles.form} onSubmit={invite}>
      <TextField label={t.email} type="email" value={email} onChange={setEmail} isRequired inputProps={{dir:'ltr',autoComplete:'email'}} />
      <Select label={t.role} selectedKey={role} onSelectionChange={key=>setRole(String(key) as ArchiveRole)} options={roleOptions} />
      <Button type="submit" isDisabled={busy}>{busy?t.loading:t.invite}</Button>
    </form></section>
    <section className={styles.card}><h2>{t.members}</h2><ul className={styles.list}>{members.map(m=><li key={m.accountId} className={styles.row}>
      <bdi dir="ltr">{m.email}</bdi><Select label={`${t.role}: ${m.email}`} selectedKey={m.role} onSelectionChange={key=>void change(m,String(key) as ArchiveRole,m.canBulkExport)} options={roleOptions} />
      <Checkbox isSelected={m.canBulkExport} onChange={grant=>void change(m,m.role,grant)}>{t.export}</Checkbox>
      <ConfirmDialog trigger={t.remove} title={t.confirmRemove} closeLabel={t.cancel} action={<Button slot="close" variant="danger" onPress={()=>void remove(m)}>{t.remove}</Button>}><bdi dir="ltr">{m.email}</bdi></ConfirmDialog>
    </li>)}</ul></section>
    <section className={styles.card}><h2>{t.invite}</h2><ul className={styles.list}>{invitations.map(i=><li key={i.id} className={styles.row}><bdi dir="ltr">{i.email}</bdi> · {t[i.role]} · {t[i.status]}
      {i.status==='active'&&<ConfirmDialog trigger={t.revoke} title={t.confirmRevoke} closeLabel={t.cancel} action={<Button slot="close" variant="danger" onPress={()=>void revoke(i)}>{t.revoke}</Button>}><bdi dir="ltr">{i.email}</bdi></ConfirmDialog>}
    </li>)}</ul></section>
    {notice&&<p role="status">{notice}</p>}{error&&<p className={styles.error} role="alert">{error}</p>}
  </div></main>;
}
