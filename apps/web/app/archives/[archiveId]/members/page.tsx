'use client';
import { changeArchiveMember, createArchiveInvitation, listArchiveInvitations, listArchiveMembers, removeArchiveMember, revokeArchiveInvitation, problemMessage, type ProblemCode, type Member, type Invitation, type ArchiveRole } from '@invoyis/contracts';
import { Button, Checkbox, ConfirmDialog, Select, TextField } from '@invoyis/ui';
import { use, useEffect, useRef, useState, type FormEvent } from 'react';
import { ArchiveShell, MenuButton } from '../library/archive-shell';
import { ChevronIcon } from '../library/icons';
import lib from '../library/library.module.css';
import screen from '../library/screens.module.css';
import { useWords } from '../../../../lib/catalog';
import { intent } from '../../../../lib/archive-ui';
import styles from '../../archive.module.css';
export default function MembersPage({ params }: { params: Promise<{ archiveId:string }> }) {
  const { locale, t } = useWords();
  const { archiveId: id } = use(params); const [members,setMembers]=useState<Member[]>([]); const [invitations,setInvitations]=useState<Invitation[]>([]);
  const [email,setEmail]=useState(''); const [role,setRole]=useState<ArchiveRole>('viewer');
  const [error,setError]=useState(''); const [notice,setNotice]=useState(''); const [busy,setBusy]=useState(false);
  const [invalidEmail,setInvalidEmail]=useState(false);
  const membersHeading=useRef<HTMLHeadingElement>(null); const invitationsHeading=useRef<HTMLHeadingElement>(null);
  const message=(value:unknown)=>problemMessage((value as {code?:ProblemCode}|undefined)?.code??'INTERNAL_ERROR',locale);
  async function refresh() { if(!id)return; const [m,i]=await Promise.all([listArchiveMembers({path:{archiveId:id},credentials:'same-origin'}),listArchiveInvitations({path:{archiveId:id},credentials:'same-origin'})]); if(m.data)setMembers(m.data.items);else if(m.error)setError(message(m.error)); if(i.data)setInvitations(i.data.items);else if(i.error)setError(message(i.error)); }
  useEffect(() => { void refresh(); },[id]);
  async function invite(event:FormEvent) { event.preventDefault();setError('');setInvalidEmail(false);if(!/^\S+@\S+\.\S+$/.test(email.trim())){setInvalidEmail(true);return;} setBusy(true); const {error}=await createArchiveInvitation({path:{archiveId:id},body:{email,role},headers:{...intent,'Accept-Language':locale},credentials:'same-origin'});setBusy(false);if(error){setError(message(error));return;}setEmail('');setNotice(t.invited);await refresh(); }
  async function change(member:Member,nextRole:ArchiveRole,grant:boolean){setError('');const {error}=await changeArchiveMember({path:{archiveId:id,accountId:member.accountId},body:{role:nextRole,canBulkExport:grant},headers:intent,credentials:'same-origin'});if(error)setError(message(error));await refresh();}
  async function remove(member:Member){const {error}=await removeArchiveMember({path:{archiveId:id,accountId:member.accountId},headers:intent,credentials:'same-origin'});if(error)setError(message(error));await refresh();if(!error)requestAnimationFrame(()=>membersHeading.current?.focus());}
  async function revoke(invitation:Invitation){const {error}=await revokeArchiveInvitation({path:{archiveId:id,invitationId:invitation.id},headers:intent,credentials:'same-origin'});if(error)setError(message(error));await refresh();if(!error)requestAnimationFrame(()=>invitationsHeading.current?.focus());}
  const roleOptions=[{id:'viewer',label:t.viewer},{id:'contributor',label:t.contributor},{id:'admin',label:t.admin}];
  return <ArchiveShell archiveId={id} section="members">{({ archive }) => <main className={lib.main}>
    <header className={lib.toolbar}>
      <MenuButton />
      <a href={`/archives/${id}`} className={lib.backButton}><ChevronIcon flip={locale === 'he'} />{t.libBack}</a>
      <div className={lib.titleBlock}><h1 className={lib.title}><span className={lib.titleText}>{t.members}</span></h1>{archive && <p className={screen.subtitle}><bdi dir="auto">{archive.name}</bdi> · {members.length}</p>}</div>
    </header>
    <div className={screen.content}><div className={screen.column}>
    <section className={screen.panel} aria-labelledby="invite-heading"><h2 id="invite-heading" className={screen.panelTitle}>{t.invite}</h2><form className={screen.inviteForm} noValidate onSubmit={invite}>
      <TextField label={t.email} type="email" value={email} onChange={value=>{setEmail(value);setInvalidEmail(false);}} isRequired error={invalidEmail?t.invalidInviteEmail:undefined} inputProps={{dir:'ltr',autoComplete:'email'}} />
      <Select label={t.role} selectedKey={role} onSelectionChange={key=>setRole(String(key) as ArchiveRole)} options={roleOptions} />
      <Button type="submit" isDisabled={busy}>{busy?t.loading:t.invite}</Button>
    </form></section>
    {notice&&<p className={screen.notice} role="status">{notice}</p>}{error&&<p className={styles.error} role="alert">{error}</p>}
    <section className={screen.panel}><h2 ref={membersHeading} tabIndex={-1} className={screen.panelTitle}>{t.members}</h2><ul className={screen.rows}>{members.map(m=><li key={m.accountId} className={screen.memberRow}>
      <span className={screen.avatar} aria-hidden="true">{m.email.slice(0,1).toUpperCase()}</span><bdi dir="ltr" className={screen.email}>{m.email}</bdi>
      <div className={screen.roleSelect}><Select label={`${t.role}: ${m.email}`} selectedKey={m.role} onSelectionChange={key=>void change(m,String(key) as ArchiveRole,m.canBulkExport)} options={roleOptions} /></div>
      <Checkbox isSelected={m.canBulkExport} onChange={grant=>void change(m,m.role,grant)}>{t.export}</Checkbox>
      <ConfirmDialog trigger={t.remove} title={t.confirmRemove} closeLabel={t.cancel} action={<Button slot="close" variant="danger" onPress={()=>void remove(m)}>{t.remove}</Button>}><bdi dir="ltr">{m.email}</bdi></ConfirmDialog>
    </li>)}</ul></section>
    <section className={screen.panel}><h2 ref={invitationsHeading} tabIndex={-1} className={screen.panelTitle}>{t.libInvitations}</h2>{invitations.length ? <ul className={screen.rows}>{invitations.map(i=><li key={i.id} className={screen.memberRow}><span className={screen.avatar} aria-hidden="true">@</span><bdi dir="ltr" className={screen.email}>{i.email}</bdi><span className={screen.meta}>{t[i.role]} · <span className={screen.state} data-state={i.status}>{t[i.status]}</span></span>
      {i.status==='active'?<ConfirmDialog trigger={t.revoke} title={t.confirmRevoke} closeLabel={t.cancel} action={<Button slot="close" variant="danger" onPress={()=>void revoke(i)}>{t.revoke}</Button>}><bdi dir="ltr">{i.email}</bdi></ConfirmDialog>:<span />}
    </li>)}</ul> : <p className={lib.muted}>—</p>}</section>
    </div></div>
  </main>}</ArchiveShell>;
}
